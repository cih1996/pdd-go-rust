package task

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"unified-server/internal/account"
	"unified-server/internal/config"
	"unified-server/internal/device"
	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
	"unified-server/internal/upstream"
	"unified-server/internal/vision"
	"unified-server/internal/ws"
)

const (
	externalClaimTimeout  = 120 * time.Second
	externalBufferTimeout = 3 * time.Minute
	groupTaskTimeout      = 3 * time.Minute
)

type Service struct {
	cfg      config.Config
	hub      *ws.Hub
	tpl      *template.Store
	vision   *vision.Engine
	devices  *device.Service
	upstream *upstream.Store
	accounts *account.Store
	runtime  *rt.Store
	client   *http.Client

	mu               sync.Mutex
	workers          map[string]context.CancelFunc
	prefetchCancel   context.CancelFunc
	externalSweepRun bool
	candidateCursor  int
	noCandidateWarn  bool
	pending          []pendingTask
	active           map[string]runningTask
	groups           map[string]*groupedTask
	externalBuffered map[string]externalBufferedTask
	externalBufferQ  []string
	externalClaims   map[string]externalClaim
	externalBusiness map[string]string
	externalSources  map[string]string
	urlTemplateState map[string]deviceURLTemplateState

	emitMu      sync.Mutex
	emitTimer   *time.Timer
	emitPending bool
}

const emitStateDebounce = 250 * time.Millisecond

type startRequest struct {
	DeviceIDs []string `json:"device_ids"`
	Mode      string   `json:"mode"`
}

type stopRequest struct {
	DeviceIDs []string `json:"device_ids"`
}

type clientTaskItem struct {
	GoodsID   string   `json:"goods_id"`
	GoodsName string   `json:"goods_name,omitempty"`
	SKUName   []string `json:"sku_name,omitempty"`
	SKUID     string   `json:"sku_id"`
	SourceURL string   `json:"source_url"`
	StepIndex int      `json:"step_index"`
}

type clientTask struct {
	TaskID          string           `json:"task_id"`
	UpstreamTaskRef string           `json:"upstream_task_ref"`
	SourceCode      string           `json:"source_code"`
	SourceName      string           `json:"source_name"`
	AccountID       string           `json:"account_id"`
	AccountName     string           `json:"account_name"`
	TaskItems       []clientTaskItem `json:"task_items"`
}

type clientSubmitTaskItem struct {
	GoodsID     string   `json:"goods_id,omitempty"`
	SKUID       string   `json:"sku_id"`
	Recognition string   `json:"recognition,omitempty"`
	Message     string   `json:"message,omitempty"`
	CaptureIDs  []string `json:"capture_ids"`
	CaptureURLs []string `json:"capture_urls"`
}

type clientSubmitRequest struct {
	TaskID    string                 `json:"task_id"`
	Type      string                 `json:"type"`
	DeviceID  string                 `json:"device_id"`
	Message   string                 `json:"message"`
	TaskItems []clientSubmitTaskItem `json:"task_items"`
}

type uploadCaptureResponse struct {
	CaptureID  string `json:"capture_id"`
	CaptureURL string `json:"capture_url"`
}

type adapterRequestError struct {
	StatusCode int
	Message    string
}

func (e *adapterRequestError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("adapter status %d", e.StatusCode)
	}
	return fmt.Sprintf("adapter status %d: %s", e.StatusCode, e.Message)
}

type sourceCandidate struct {
	Upstream upstream.Record
	Account  *account.Record
	Token    string
	Key      string
}

type pendingTask struct {
	Task        clientTask
	Source      sourceCandidate
	BusinessKey string
	ParentKey   string
	ChildKey    string
}

type runningTask struct {
	Task        clientTask
	Source      sourceCandidate
	BusinessKey string
	ParentKey   string
	ChildKey    string
}

type skuExecutionResult struct {
	GoodsID           string
	SKUID             string
	Recognition       string
	Message           string
	CaptureBytes      [][]byte
	TemplateID        string
	TemplateLabel     string
	RecognitionEngine string
}

type matchedTemplateMeta struct {
	TemplateID        string
	TemplateLabel     string
	TemplateType      string
	RecognitionEngine string
}

type deviceURLTemplateState struct {
	CurrentIndex int
	RiskedIDs    map[string]struct{}
}

type groupedTask struct {
	Task         clientTask
	Source       sourceCandidate
	BusinessKey  string
	TotalCount   int
	PrefetchedAt string
	Pending      map[string]pendingTask
	Active       map[string]runningTask
	Completed    map[string]clientSubmitTaskItem
	Completion   []string
	FinalStatus  string
	FinalMessage string
	Released     bool
}

type PlatformAccountTestResult struct {
	Success         bool   `json:"success"`
	Fetched         bool   `json:"fetched"`
	Released        bool   `json:"released"`
	UpstreamCode    string `json:"upstream_code"`
	UpstreamType    string `json:"upstream_type"`
	AccountID       string `json:"account_id"`
	AccountName     string `json:"account_name"`
	TaskID          string `json:"task_id,omitempty"`
	UpstreamTaskRef string `json:"upstream_task_ref,omitempty"`
	ItemCount       int    `json:"item_count,omitempty"`
	Message         string `json:"message"`
}

type ExternalFetchRequest struct {
	WorkerID   string `json:"worker_id"`
	WorkerName string `json:"worker_name"`
	SourceCode string `json:"source_code"`
}

type ExternalTaskItem struct {
	GoodsID   string   `json:"goods_id"`
	GoodsName string   `json:"goods_name,omitempty"`
	SKUName   []string `json:"sku_name,omitempty"`
	SKUID     string   `json:"sku_id"`
	SourceURL string   `json:"source_url"`
	StepIndex int      `json:"step_index"`
}

type ExternalTask struct {
	TaskID          string             `json:"task_id"`
	UpstreamTaskRef string             `json:"upstream_task_ref"`
	SourceCode      string             `json:"source_code"`
	SourceName      string             `json:"source_name"`
	AccountID       string             `json:"account_id,omitempty"`
	AccountName     string             `json:"account_name,omitempty"`
	TaskItems       []ExternalTaskItem `json:"task_items"`
}

type ExternalFetchResponse struct {
	Success bool          `json:"success"`
	HasTask bool          `json:"has_task"`
	Message string        `json:"message"`
	Task    *ExternalTask `json:"task"`
}

type ExternalURLTemplate struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Template     string `json:"template"`
	TriggerCount int    `json:"trigger_count"`
	SuccessCount int    `json:"success_count"`
	RiskCount    int    `json:"risk_count"`
}

type ExternalURLTemplatesResponse struct {
	Success         bool                  `json:"success"`
	UseURLTemplates bool                  `json:"use_url_templates"`
	Templates       []ExternalURLTemplate `json:"templates"`
}

type ExternalSubmitCapture struct {
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
}

type ExternalSubmitTaskItem struct {
	GoodsID     string                  `json:"goods_id,omitempty"`
	SKUID       string                  `json:"sku_id,omitempty"`
	Recognition string                  `json:"recognition,omitempty"`
	Message     string                  `json:"message,omitempty"`
	Captures    []ExternalSubmitCapture `json:"captures,omitempty"`
}

type ExternalSubmitRequest struct {
	TaskID     string                   `json:"task_id"`
	WorkerID   string                   `json:"worker_id"`
	DeviceID   string                   `json:"device_id,omitempty"`
	TemplateID string                   `json:"template_id,omitempty"`
	Result     string                   `json:"result"`
	Message    string                   `json:"message,omitempty"`
	TaskItems  []ExternalSubmitTaskItem `json:"task_items"`
}

type ExternalSubmitResponse struct {
	Success     bool     `json:"success"`
	Message     string   `json:"message"`
	TaskID      string   `json:"task_id"`
	DeviceID    string   `json:"device_id"`
	DetailIDs   []string `json:"detail_ids,omitempty"`
	CaptureURLs []string `json:"capture_urls,omitempty"`
}

type externalClaim struct {
	Task        clientTask
	Source      sourceCandidate
	BusinessKey string
	WorkerID    string
	WorkerName  string
	ClaimedAt   string
}

type externalBufferedTask struct {
	Task         clientTask
	Source       sourceCandidate
	BusinessKey  string
	PrefetchedAt string
}

type externalFetchCandidateResult struct {
	Index     int
	Candidate sourceCandidate
	Task      *clientTask
	Err       error
}

func (s *Service) Runtime() *rt.Store {
	return s.runtime
}

func NewService(cfg config.Config, hub *ws.Hub, tpl *template.Store, visionEngine *vision.Engine, devices *device.Service, ups *upstream.Store, accounts *account.Store, runtimeStore *rt.Store) *Service {
	service := &Service{
		cfg:              cfg,
		hub:              hub,
		tpl:              tpl,
		vision:           visionEngine,
		devices:          devices,
		upstream:         ups,
		accounts:         accounts,
		runtime:          runtimeStore,
		client:           &http.Client{Timeout: 30 * time.Second},
		workers:          map[string]context.CancelFunc{},
		active:           map[string]runningTask{},
		groups:           map[string]*groupedTask{},
		externalBuffered: map[string]externalBufferedTask{},
		externalBufferQ:  []string{},
		externalClaims:   map[string]externalClaim{},
		externalBusiness: map[string]string{},
		externalSources:  map[string]string{},
		urlTemplateState: map[string]deviceURLTemplateState{},
	}
	service.ensureExternalClaimSweeper()
	return service
}

func (s *Service) RuntimePlan() map[string]any {
	return map[string]any{
		"adapter_mode":                   "standalone rust service",
		"ws_push":                        true,
		"vision":                         s.vision.Plan(),
		"template_total":                 s.tpl.Count(),
		"device_total":                   len(s.devices.List()),
		"worker_total":                   s.workerCount(),
		"external_claim_timeout_seconds": int(externalClaimTimeout / time.Second),
	}
}

func (s *Service) ensureExternalClaimSweeper() {
	s.mu.Lock()
	if s.externalSweepRun {
		s.mu.Unlock()
		return
	}
	s.externalSweepRun = true
	s.mu.Unlock()

	go s.externalClaimSweepLoop()
}

func (s *Service) externalClaimSweepLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.releaseExpiredExternalClaims()
	}
}

func (s *Service) releaseExpiredExternalClaims() {
	now := time.Now().UTC()
	expired := make([]externalClaim, 0)

	s.mu.Lock()
	for _, claim := range s.externalClaims {
		claimedAt, err := time.Parse(time.RFC3339, claim.ClaimedAt)
		if err != nil {
			claimedAt = now.Add(-externalClaimTimeout - time.Second)
		}
		if now.Sub(claimedAt) >= externalClaimTimeout {
			expired = append(expired, claim)
		}
	}
	s.mu.Unlock()

	for _, claim := range expired {
		s.releaseExpiredExternalClaim(claim)
	}
}

func (s *Service) releaseExpiredExternalClaim(claim externalClaim) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	message := fmt.Sprintf("外部设备超时 %d 秒未提交，系统自动释放", int(externalClaimTimeout/time.Second))
	err := s.submitTaskWithMessage(ctx, externalDeviceID(claim.WorkerID, ""), claim.Task, "cancelled", nil, message)
	if err != nil {
		s.emitEvent("warning", "外部任务超时自动释放失败", externalDeviceID(claim.WorkerID, ""), map[string]any{
			"worker_id":         claim.WorkerID,
			"worker_name":       claim.WorkerName,
			"task_id":           claim.Task.TaskID,
			"upstream_task_ref": claim.Task.UpstreamTaskRef,
			"source_code":       claim.Task.SourceCode,
			"error":             err.Error(),
		})
		return
	}

	s.releaseExternalClaim(claim.Task.TaskID)
	detail := s.buildDetail(
		claim.Task,
		nil,
		externalDeviceID(claim.WorkerID, ""),
		"external",
		"",
		"cancelled",
		"external_timeout_release",
		nil,
		"",
		message,
		nil,
	)
	stored := s.runtime.AddDetail(detail)
	s.hub.Broadcast(ws.Event{Type: "detail", Data: map[string]any(detailToMap(stored))})
	s.emitEvent("warning", "外部任务超时自动释放", externalDeviceID(claim.WorkerID, ""), map[string]any{
		"worker_id":         claim.WorkerID,
		"worker_name":       claim.WorkerName,
		"task_id":           claim.Task.TaskID,
		"upstream_task_ref": claim.Task.UpstreamTaskRef,
		"source_code":       claim.Task.SourceCode,
		"timeout_seconds":   int(externalClaimTimeout / time.Second),
	})
	s.emitStateImmediate()
}

func (s *Service) releaseExpiredExternalBufferedTask(buffered externalBufferedTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	message := fmt.Sprintf("外部接口缓存任务超过 %d 秒未领取，系统自动释放", int(externalBufferTimeout/time.Second))
	err := s.submitTaskWithMessage(ctx, "", buffered.Task, "cancelled", nil, message)
	if err != nil {
		s.emitEvent("warning", "外部缓存任务自动释放失败", "", map[string]any{
			"task_id":           buffered.Task.TaskID,
			"upstream_task_ref": buffered.Task.UpstreamTaskRef,
			"source_code":       buffered.Task.SourceCode,
			"error":             err.Error(),
		})
		return
	}

	s.releaseExternalBuffered(buffered.Task.TaskID)
	s.emitEvent("warning", "外部缓存任务已自动释放", "", map[string]any{
		"task_id":           buffered.Task.TaskID,
		"upstream_task_ref": buffered.Task.UpstreamTaskRef,
		"source_code":       buffered.Task.SourceCode,
		"timeout_seconds":   int(externalBufferTimeout / time.Second),
	})
}

func (s *Service) FetchExternalTask(ctx context.Context, req ExternalFetchRequest) (ExternalFetchResponse, error) {
	workerID := strings.TrimSpace(req.WorkerID)
	workerName := strings.TrimSpace(req.WorkerName)
	sourceCode := strings.TrimSpace(req.SourceCode)
	if workerID == "" {
		return ExternalFetchResponse{}, errors.New("worker_id 不能为空")
	}

	candidates := s.fetchCandidatesBySource(sourceCode)
	if len(candidates) == 0 {
		if sourceCode != "" {
			return ExternalFetchResponse{}, fmt.Errorf("未找到可用的上游账号: %s", sourceCode)
		}
		return ExternalFetchResponse{}, errors.New("当前没有启用的上游账号")
	}

	var firstErr error
	start := s.nextCandidateCursor(len(candidates))
	for offset := 0; offset < len(candidates); offset++ {
		candidateIndex := (start + offset) % len(candidates)
		candidate := candidates[candidateIndex]
		if s.externalSourceLocked(candidate.Key) || s.sourceLocked(candidate.Key) {
			continue
		}

		taskItem, err := s.fetchTaskForCandidateWithOptions(ctx, candidate, true)
		s.setCandidateCursor((candidateIndex + 1) % len(candidates))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.emitEvent("warning", "外部接口轮询取任务失败", "", map[string]any{
				"source_key":   candidate.Key,
				"source_code":  candidate.Upstream.Code,
				"worker_id":    workerID,
				"worker_name":  workerName,
				"account_id":   accountIDOfCandidate(candidate),
				"account_name": accountNameOfCandidate(candidate),
				"error":        err.Error(),
			})
			continue
		}
		if taskItem == nil {
			continue
		}
		if taskItem.AccountID == "" && candidate.Account != nil {
			taskItem.AccountID = candidate.Account.ID
		}
		if taskItem.AccountName == "" && candidate.Account != nil {
			taskItem.AccountName = candidate.Account.Name
		}
		if !s.reserveExternalClaim(*taskItem, candidate, workerID, workerName) {
			_ = s.submitTaskWithMessage(ctx, "", *taskItem, "cancelled", nil, "duplicate business key released by external fetch")
			continue
		}

		s.emitEvent("info", "外部设备领取任务", externalDeviceID(workerID, ""), map[string]any{
			"worker_id":         workerID,
			"worker_name":       workerName,
			"task_id":           taskItem.TaskID,
			"upstream_task_ref": taskItem.UpstreamTaskRef,
			"source_code":       taskItem.SourceCode,
			"account_id":        accountIDOfCandidate(candidate),
			"account_name":      accountNameOfCandidate(candidate),
		})
		return ExternalFetchResponse{
			Success: true,
			HasTask: true,
			Message: "ok",
			Task:    buildExternalTaskPayload(*taskItem),
		}, nil
	}

	if firstErr != nil {
		return ExternalFetchResponse{}, firstErr
	}
	return ExternalFetchResponse{
		Success: true,
		HasTask: false,
		Message: "当前暂无可领取任务",
		Task:    nil,
	}, nil
}

func (s *Service) ListExternalURLTemplates() ExternalURLTemplatesResponse {
	cfg := s.runtime.SystemConfig()
	items := configuredURLTemplates(cfg)
	templates := make([]ExternalURLTemplate, 0, len(items))
	for _, item := range items {
		templates = append(templates, ExternalURLTemplate{
			ID:           item.ID,
			Name:         strings.TrimSpace(item.Name),
			Template:     item.Template,
			TriggerCount: item.TriggerCount,
			SuccessCount: item.SuccessCount,
			RiskCount:    item.RiskCount,
		})
	}
	return ExternalURLTemplatesResponse{
		Success:         true,
		UseURLTemplates: cfg.UseURLTemplates,
		Templates:       templates,
	}
}

func (s *Service) SubmitExternalTask(ctx context.Context, req ExternalSubmitRequest) (ExternalSubmitResponse, error) {
	taskID := strings.TrimSpace(req.TaskID)
	workerID := strings.TrimSpace(req.WorkerID)
	templateID := strings.TrimSpace(req.TemplateID)
	if taskID == "" {
		return ExternalSubmitResponse{}, errors.New("task_id 不能为空")
	}
	if workerID == "" {
		return ExternalSubmitResponse{}, errors.New("worker_id 不能为空")
	}

	claim, ok := s.getExternalClaim(taskID)
	if !ok {
		return ExternalSubmitResponse{}, errors.New("未找到对应的外部任务认领记录")
	}
	if claim.WorkerID != workerID {
		return ExternalSubmitResponse{}, errors.New("当前任务不属于该 worker")
	}

	submitType, detailStatus, err := normalizeExternalSubmitResult(req.Result)
	if err != nil {
		return ExternalSubmitResponse{}, err
	}

	deviceID := externalDeviceID(workerID, req.DeviceID)
	systemConfig := s.runtime.SystemConfig()
	templateRecord, templateFound := findURLTemplateByID(systemConfig, templateID)
	templateMeta := urlTemplateMetaFromRecord(templateID, templateRecord, templateFound)
	submitItems := make([]clientSubmitTaskItem, 0, len(req.TaskItems))
	detailRecords := make([]rt.DetailRecord, 0, max(1, len(req.TaskItems)))
	allCaptureURLs := make([]string, 0)
	riskTriggered := false

	if templateID != "" {
		s.runtime.RecordURLTemplateTrigger(templateID)
	}

	for index, item := range req.TaskItems {
		taskItem := matchExternalTaskItem(claim.Task, item, index)
		if isExternalTemplateRiskItem(item) {
			riskTriggered = true
		}
		captureIDs := make([]string, 0, len(item.Captures))
		captureURLs := make([]string, 0, len(item.Captures))
		for captureIndex, capture := range item.Captures {
			data, contentType, err := decodeExternalCapture(capture)
			if err != nil {
				return ExternalSubmitResponse{}, fmt.Errorf("解析第 %d 张图片失败: %w", captureIndex+1, err)
			}
			fileName := buildExternalCaptureFileName(claim.Task.TaskID, taskItem, captureIndex, contentType)
			uploaded, err := s.uploadCaptureNamed(ctx, claim.Task, deviceID, taskItem, data, fileName)
			if err != nil {
				return ExternalSubmitResponse{}, fmt.Errorf("上传第 %d 张图片失败: %w", captureIndex+1, err)
			}
			captureIDs = append(captureIDs, uploaded.CaptureID)
			captureURLs = append(captureURLs, uploaded.CaptureURL)
			allCaptureURLs = append(allCaptureURLs, uploaded.CaptureURL)
		}

		submitItems = append(submitItems, clientSubmitTaskItem{
			GoodsID:     firstNonEmpty(strings.TrimSpace(item.GoodsID), taskItem.GoodsID),
			SKUID:       firstNonEmpty(strings.TrimSpace(item.SKUID), taskItem.SKUID),
			Recognition: strings.TrimSpace(item.Recognition),
			Message:     strings.TrimSpace(item.Message),
			CaptureIDs:  captureIDs,
			CaptureURLs: captureURLs,
		})

		currentItem := taskItem
		detailRecords = append(detailRecords, s.buildDetail(
			claim.Task,
			&currentItem,
			deviceID,
			"external",
			resolveExternalDetailURL(taskItem, templateRecord, templateFound),
			detailStatus,
			firstNonEmpty(strings.TrimSpace(item.Recognition), submitType),
			captureURLs,
			"",
			firstNonEmpty(strings.TrimSpace(item.Message), strings.TrimSpace(req.Message)),
			templateMeta,
		))
	}

	if len(detailRecords) == 0 {
		detailRecords = append(detailRecords, s.buildDetail(
			claim.Task,
			nil,
			deviceID,
			"external",
			resolveExternalFallbackURL(claim.Task, templateRecord, templateFound),
			detailStatus,
			submitType,
			nil,
			"",
			strings.TrimSpace(req.Message),
			templateMeta,
		))
	}

	submitErr := s.submitTaskWithMessage(ctx, deviceID, claim.Task, submitType, submitItems, strings.TrimSpace(req.Message))
	for index := range detailRecords {
		if submitErr != nil {
			detailRecords[index].Status = "failure"
			detailRecords[index].SubmitStatusCode, detailRecords[index].SubmitError = parseAdapterSubmitFailure(submitErr)
			detailRecords[index].Message = appendDetailMessage(detailRecords[index].Message, submitErr.Error())
		}
		stored := s.runtime.AddDetail(detailRecords[index])
		detailRecords[index] = stored
		s.hub.Broadcast(ws.Event{Type: "detail", Data: map[string]any(detailToMap(stored))})
	}
	s.emitStateImmediate()

	if submitErr != nil {
		return ExternalSubmitResponse{
			Success:     false,
			Message:     submitErr.Error(),
			TaskID:      claim.Task.TaskID,
			DeviceID:    deviceID,
			DetailIDs:   collectDetailIDs(detailRecords),
			CaptureURLs: allCaptureURLs,
		}, submitErr
	}

	if templateID != "" {
		if riskTriggered {
			s.runtime.RecordURLTemplateRisk(templateID)
		} else if submitType == "success" {
			s.runtime.RecordURLTemplateSuccess(templateID)
		}
	}

	s.releaseExternalClaim(claim.Task.TaskID)
	s.emitEvent(deviceID, "info", "外部设备提交任务", map[string]any{
		"worker_id":         workerID,
		"worker_name":       claim.WorkerName,
		"task_id":           claim.Task.TaskID,
		"upstream_task_ref": claim.Task.UpstreamTaskRef,
		"source_code":       claim.Task.SourceCode,
		"result":            submitType,
	})

	return ExternalSubmitResponse{
		Success:     true,
		Message:     "任务已提交",
		TaskID:      claim.Task.TaskID,
		DeviceID:    deviceID,
		DetailIDs:   collectDetailIDs(detailRecords),
		CaptureURLs: allCaptureURLs,
	}, nil
}

func (s *Service) ResetDeviceURLTemplateState(deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urlTemplateState[deviceID] = newDeviceURLTemplateState()
}

func (s *Service) Start(deviceIDs []string, mode string) ([]string, []string) {
	started := make([]string, 0, len(deviceIDs))
	skipped := make([]string, 0, len(deviceIDs))

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.runtime.SubmitCount() >= 1000 {
		s.emitEvent("warning", "提交次数已达上限(1000)，已自动停止任务", "", map[string]any{"submit_count": s.runtime.SubmitCount()})
		skipped = append(skipped, deviceIDs...)
		return started, skipped
	}

	for _, deviceID := range deviceIDs {
		if _, exists := s.workers[deviceID]; exists {
			skipped = append(skipped, deviceID)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.workers[deviceID] = cancel
		s.urlTemplateState[deviceID] = newDeviceURLTemplateState()
		started = append(started, deviceID)
		go s.runWorker(ctx, deviceID, mode)
	}
	s.ensurePrefetchLoopLocked()
	return started, skipped
}

func (s *Service) Stop(deviceIDs []string) ([]string, []string) {
	stopped := make([]string, 0, len(deviceIDs))
	missing := make([]string, 0, len(deviceIDs))
	shouldReleaseAll := false

	s.mu.Lock()
	for _, deviceID := range deviceIDs {
		cancel, exists := s.workers[deviceID]
		if !exists {
			missing = append(missing, deviceID)
			continue
		}
		cancel()
		delete(s.workers, deviceID)
		delete(s.urlTemplateState, deviceID)
		stopped = append(stopped, deviceID)
	}
	if len(s.workers) == 0 && s.prefetchCancel != nil {
		s.prefetchCancel()
		s.prefetchCancel = nil
		shouldReleaseAll = true
	}
	s.mu.Unlock()
	if shouldReleaseAll {
		s.releaseAllGroupedTasks(context.Background(), "cancelled", "全部设备已停止，已主动释放已领取任务")
	}
	return stopped, missing
}

func (s *Service) runWorker(ctx context.Context, deviceID string, mode string) {
	s.emitEvent("info", "设备任务循环已启动", deviceID, map[string]any{"mode": mode})
	s.devices.SetCurrentTask(deviceID, &device.CurrentTask{
		TaskMode:       mode,
		StartedAt:      nowString(),
		CurrentStage:   "idle",
		CurrentMessage: "等待上游任务",
	})
	s.applyCurrentURLTemplateStatus(deviceID)
	s.emitState()

	defer func() {
		shouldReleaseAll := s.cleanupWorkerOnExit(deviceID)
		if shouldKeepCurrentTaskSnapshot(s.devices.CurrentTask(deviceID)) {
			s.devices.SetTaskRunning(deviceID, false)
		} else {
			s.devices.SetCurrentTask(deviceID, nil)
		}
		if shouldReleaseAll {
			s.releaseAllGroupedTasks(context.Background(), "cancelled", "全部设备已停止，已主动释放已领取任务")
		}
		s.emitEvent("info", "设备任务循环已停止", deviceID, nil)
		s.emitStateImmediate()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		taskItem, source, ok := s.takePendingTask()
		if !ok {
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.CurrentStage = "idle"
				current.CurrentMessage = "暂无任务"
			})
			s.applyCurrentURLTemplateStatus(deviceID)
			s.emitState()
			time.Sleep(2 * time.Second)
			continue
		}

		success, shouldStop, skuResults, detail := s.executeTask(ctx, deviceID, mode, taskItem.Task)
		if ctx.Err() != nil {
			if s.workerCount() > 0 {
				s.requeueInterruptedTask(taskItem)
				s.emitEvent("warning", "设备停止，子任务已回到候选区", deviceID, map[string]any{
					"task_id": taskItem.Task.TaskID,
					"sku_id":  firstSKUID(taskItem.Task),
				})
			}
			return
		}
		submitItems := []clientSubmitTaskItem{}
		if success {
			items, captureURLs, err := s.uploadSuccessCaptures(ctx, taskItem.Task, deviceID, skuResults)
			if err != nil {
				success = false
				detail.Status = "failure"
				detail.Recognition = "upload_capture_failed"
				detail.Message = "上传截图失败: " + err.Error()
			} else {
				submitItems = items
				detail.CaptureURLs = captureURLs
				if len(captureURLs) > 0 {
					detail.CaptureURL = captureURLs[len(captureURLs)-1]
					detail.ImageCount = len(captureURLs)
				}
			}
		} else {
			item, captureURLs, err := s.uploadFailureEvidence(ctx, taskItem.Task, deviceID, mode, detail.Recognition, detail.Message)
			if err == nil && item.SKUID != "" {
				submitItems = []clientSubmitTaskItem{item}
				detail.CaptureURLs = captureURLs
				if len(captureURLs) > 0 {
					detail.CaptureURL = captureURLs[len(captureURLs)-1]
					detail.ImageCount = len(captureURLs)
				}
			}
		}

		s.devices.RecordResult(deviceID, success)
		if source.Account != nil {
			s.accounts.UnbindDevice(source.Account.ID, deviceID)
		}

		var submission *groupSubmission
		if success {
			submission = s.finalizeChildSuccess(taskItem, firstSubmitItem(submitItems))
		} else {
			submission = s.finalizeChildFailure(taskItem, detail, submitItems)
		}
		if submission != nil {
			if err := s.submitTaskWithMessage(ctx, deviceID, submission.Task, submission.SubmitType, submission.TaskItems, submission.Message); err != nil {
				statusCode, submitError := parseAdapterSubmitFailure(err)
				s.emitEvent("error", "提交任务失败", deviceID, map[string]any{"task_id": submission.Task.TaskID, "error": err.Error()})
				detail.Status = "failure"
				detail.Recognition = "submit_failed"
				detail.SubmitStatusCode = statusCode
				detail.SubmitError = submitError
				detail.Message = strings.TrimSpace(detail.Message + "; submit failed: " + err.Error())
				if shouldAutoStopDeviceOnSubmitError(source, err) {
					autoStopMessage := buildAutoStopMessageForSubmitError(err)
					detail.Message = strings.TrimSpace(detail.Message + "; " + autoStopMessage)
					s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
						current.CurrentStage = "submit_limit_stop"
						current.CurrentMessage = autoStopMessage
					})
					s.runtime.AddDetail(detail)
					s.hub.Broadcast(ws.Event{Type: "detail", Data: map[string]any(detailToMap(detail))})
					s.emitEvent("error", "老钱提交达到 IP 限额，当前设备已自动停止", deviceID, map[string]any{
						"task_id":            submission.Task.TaskID,
						"submit_status_code": detail.SubmitStatusCode,
						"submit_error":       detail.SubmitError,
					})
					s.emitStateImmediate()
					return
				}
			} else {
				reportSuccess := submission.SubmitType == "success"
				if reportSuccess && detail.TemplateID != "" {
					s.runtime.RecordURLTemplateSuccess(detail.TemplateID)
				}
				if source.Account != nil {
					s.accounts.RecordSubmit(source.Account.ID, reportSuccess)
				}
				s.upstream.RecordReport(source.Upstream.Code, reportSuccess)
			}
		}

		s.runtime.AddDetail(detail)
		s.hub.Broadcast(ws.Event{Type: "detail", Data: map[string]any(detailToMap(detail))})

		s.devices.SetCurrentTask(deviceID, &device.CurrentTask{
			TaskID:         taskItem.Task.TaskID,
			TaskMode:       mode,
			StartedAt:      nowString(),
			CurrentStage:   "completed",
			CurrentMessage: detail.Message,
		})
		s.applyCurrentURLTemplateStatus(deviceID)
		s.emitState()
		if shouldStop {
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.CurrentStage = "account_risk_stop"
				current.CurrentMessage = "URL 模板已全部触发风控，设备自动停止"
			})
			s.emitState()
			s.emitEvent("warning", "URL 模板已全部触发风控，设备停止后续任务循环", deviceID, map[string]any{"task_id": taskItem.Task.TaskID})
			return
		}
		if detail.Recognition == "account_risk" {
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.CurrentStage = "account_risk"
				current.CurrentMessage = "账号风控，已自动切换到下一个 URL 模板"
			})
			s.applyCurrentURLTemplateStatus(deviceID)
			s.emitState()
		}
		time.Sleep(time.Second)
	}
}

func (s *Service) executeTask(ctx context.Context, deviceID string, mode string, taskItem clientTask) (bool, bool, []skuExecutionResult, rt.DetailRecord) {
	systemConfig := s.runtime.SystemConfig()
	results := make([]skuExecutionResult, 0, len(taskItem.TaskItems))
	clickTriggered := false
	lastURL := ""
	adbCommands := make([]string, 0, len(taskItem.TaskItems))

	for index, item := range taskItem.TaskItems {
		taskURLSelection := s.selectTaskURLForDevice(deviceID, systemConfig, item)
		taskURL := taskURLSelection.URL
		lastURL = taskURL
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.TaskID = taskItem.TaskID
			current.TaskMode = mode
			current.CurrentStage = "open_url"
			current.CurrentMessage = fmt.Sprintf("处理 SKU %d/%d", index+1, len(taskItem.TaskItems))
			current.LoopCount = index + 1
			current.URLTemplateID = taskURLSelection.TemplateID
			current.URLTemplateIndex = taskURLSelection.TemplateIndex
			current.URLTemplateTotal = taskURLSelection.TemplateTotal
		})
		s.emitState()

		if strings.TrimSpace(taskURL) != "" {
			adbCommand := device.BuildOpenURLADBCommand(taskURL)
			if adbCommand != "" {
				adbCommands = append(adbCommands, adbCommand)
			}
			if taskURLSelection.TemplateID != "" {
				s.runtime.RecordURLTemplateTrigger(taskURLSelection.TemplateID)
			}
			if err := s.devices.OpenURL(ctx, deviceID, taskURL); err != nil {
				return false, false, nil, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, "failure", "open_url_failed", nil, adbCommand, "打开链接失败: "+err.Error(), nil)
			}
			sleepWithContext(ctx, durationFromSeconds(systemConfig.OpenURLDelaySeconds))
		}

		modeEx := s.devices.SelectedTaskModeEx(deviceID)
		var skuResult skuExecutionResult
		var shouldStop bool
		var matchedClick bool
		var matchedMeta matchedTemplateMeta
		var err error
		switch strings.TrimSpace(modeEx) {
		case "detail":
			skuResult, shouldStop, matchedClick, matchedMeta, err = s.runDetailMode(ctx, deviceID, mode, item, systemConfig)
		default:
			skuResult, shouldStop, matchedClick, matchedMeta, err = s.runStealthMode(ctx, deviceID, mode, item, systemConfig)
		}
		if err != nil {
			status := "failure"
			recognition := "loop_failed"
			message := err.Error()
			if strings.HasPrefix(message, "account_risk:") {
				recognition = "account_risk"
				message = strings.TrimPrefix(message, "account_risk:")
				if taskURLSelection.TemplateID != "" {
					s.runtime.RecordURLTemplateRisk(taskURLSelection.TemplateID)
					if advanced, exhausted := s.advanceDeviceURLTemplateAfterRisk(deviceID, systemConfig, taskURLSelection.TemplateID); advanced {
						s.applyCurrentURLTemplateStatus(deviceID)
						shouldStop = exhausted
					}
				}
				message = detailMessageWithTemplate(message, matchedMeta)
				return false, shouldStop, results, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, status, recognition, nil, strings.TrimSpace(strings.Join(adbCommands, "\n")), message, &matchedMeta)
			}
			if strings.HasPrefix(message, "fail_release:") {
				recognition = "fail_release"
				message = strings.TrimPrefix(message, "fail_release:")
			}
			if strings.HasPrefix(message, "condition_mismatch:") {
				recognition = "condition_mismatch"
				message = strings.TrimPrefix(message, "condition_mismatch:")
			}
			if strings.HasPrefix(message, "coupon_detail:") {
				recognition = "coupon_detail_missing"
				message = strings.TrimPrefix(message, "coupon_detail:")
			}
			if strings.HasPrefix(message, "sku_name_mismatch:") {
				recognition = "sku_name_mismatch"
				message = strings.TrimPrefix(message, "sku_name_mismatch:")
			}
			if strings.HasPrefix(message, "goods_confirm:") {
				recognition = "goods_confirm_timeout"
				message = strings.TrimPrefix(message, "goods_confirm:")
			}
			message = detailMessageWithTemplate(message, matchedMeta)
			return false, shouldStop, results, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, status, recognition, nil, strings.TrimSpace(strings.Join(adbCommands, "\n")), message, &matchedMeta)
		}
		clickTriggered = clickTriggered || matchedClick
		results = append(results, skuResult)
	}

	message := "全部 SKU 识别完成"
	if clickTriggered {
		message = "全部 SKU 识别完成，包含点击图链路"
	}
	var meta *matchedTemplateMeta
	if len(results) > 0 {
		meta = &matchedTemplateMeta{
			TemplateID:        results[len(results)-1].TemplateID,
			TemplateLabel:     results[len(results)-1].TemplateLabel,
			RecognitionEngine: results[len(results)-1].RecognitionEngine,
		}
	}
	return true, false, results, s.buildDetail(taskItem, nil, deviceID, mode, lastURL, "success", "success_image", nil, strings.TrimSpace(strings.Join(adbCommands, "\n")), message, meta)
}

func (s *Service) submitTask(ctx context.Context, deviceID string, taskItem clientTask, submitType string, items []clientSubmitTaskItem) error {
	return s.submitTaskWithMessage(ctx, deviceID, taskItem, submitType, items, "")
}

func (s *Service) submitTaskWithMessage(ctx context.Context, deviceID string, taskItem clientTask, submitType string, items []clientSubmitTaskItem, message string) error {
	if strings.TrimSpace(message) == "" {
		switch submitType {
		case "success":
			message = "任务成功"
		case "cancelled":
			message = "任务已取消"
		default:
			message = "任务失败"
		}
	}
	if items == nil {
		items = []clientSubmitTaskItem{}
	}
	requestPayload := clientSubmitRequest{
		TaskID:    taskItem.TaskID,
		Type:      submitType,
		DeviceID:  deviceID,
		Message:   message,
		TaskItems: items,
	}
	body, _ := json.Marshal(requestPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AdapterBaseURL+"/api/client/submit-task", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	logRecord := rt.AdapterSubmitLogRecord{
		Action:          "submit-task",
		RequestMethod:   http.MethodPost,
		Endpoint:        s.cfg.AdapterBaseURL + "/api/client/submit-task",
		TaskID:          taskItem.TaskID,
		UpstreamTaskRef: taskItem.UpstreamTaskRef,
		SourceCode:      taskItem.SourceCode,
		DeviceID:        deviceID,
		SubmitType:      submitType,
		RequestPayload:  requestPayload,
	}
	if err != nil {
		logRecord.Error = err.Error()
		s.runtime.AddAdapterSubmitLog(logRecord)
		return err
	}
	defer resp.Body.Close()

	responseBody, responseText := decodeAdapterResponse(resp.Body)
	logRecord.ResponseStatus = resp.StatusCode
	logRecord.ResponsePayload = responseBody
	if resp.StatusCode >= http.StatusBadRequest {
		logRecord.Error = (&adapterRequestError{
			StatusCode: resp.StatusCode,
			Message:    buildAdapterErrorMessage(responseBody, responseText),
		}).Error()
	}
	s.runtime.AddAdapterSubmitLog(logRecord)
	if resp.StatusCode >= http.StatusBadRequest {
		return &adapterRequestError{
			StatusCode: resp.StatusCode,
			Message:    buildAdapterErrorMessage(responseBody, responseText),
		}
	}
	if submitType == "success" {
		s.runtime.IncrementSubmitCount()
		if s.runtime.SubmitCount() >= 1000 {
			s.emitEvent("warning", "submit limit reached", "", map[string]any{"submit_count": s.runtime.SubmitCount()})
			deviceIDs := make([]string, 0)
			s.mu.Lock()
			for id := range s.workers {
				deviceIDs = append(deviceIDs, id)
			}
			s.mu.Unlock()
			if len(deviceIDs) > 0 {
				s.Stop(deviceIDs)
			}
		}
	}
	return nil
}

func (s *Service) uploadCapture(ctx context.Context, taskItem clientTask, deviceID string, item clientTaskItem, capture []byte) (uploadCaptureResponse, error) {
	return s.uploadCaptureNamed(ctx, taskItem, deviceID, item, capture, buildDefaultCaptureFileName(taskItem.TaskID, item, "png"))
}

func (s *Service) uploadCaptureNamed(ctx context.Context, taskItem clientTask, deviceID string, item clientTaskItem, capture []byte, fileName string) (uploadCaptureResponse, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(strings.TrimSpace(fileName)))
	if err != nil {
		return uploadCaptureResponse{}, err
	}
	if _, err := part.Write(capture); err != nil {
		return uploadCaptureResponse{}, err
	}
	_ = writer.WriteField("task_id", taskItem.TaskID)
	_ = writer.WriteField("device_id", deviceID)
	_ = writer.WriteField("goods_id", item.GoodsID)
	_ = writer.WriteField("sku_id", item.SKUID)
	if err := writer.Close(); err != nil {
		return uploadCaptureResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AdapterBaseURL+"/api/client/upload-capture", body)
	if err != nil {
		return uploadCaptureResponse{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := s.client.Do(req)
	logRecord := rt.AdapterSubmitLogRecord{
		Action:          "upload-capture",
		RequestMethod:   http.MethodPost,
		Endpoint:        s.cfg.AdapterBaseURL + "/api/client/upload-capture",
		TaskID:          taskItem.TaskID,
		UpstreamTaskRef: taskItem.UpstreamTaskRef,
		SourceCode:      taskItem.SourceCode,
		DeviceID:        deviceID,
		RequestPayload: map[string]any{
			"goods_id":  item.GoodsID,
			"sku_id":    item.SKUID,
			"size":      len(capture),
			"file_name": filepath.Base(strings.TrimSpace(fileName)),
		},
	}
	if err != nil {
		logRecord.Error = err.Error()
		s.runtime.AddAdapterSubmitLog(logRecord)
		return uploadCaptureResponse{}, err
	}
	defer resp.Body.Close()

	var payload uploadCaptureResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		logRecord.Error = err.Error()
		s.runtime.AddAdapterSubmitLog(logRecord)
		return uploadCaptureResponse{}, err
	}
	logRecord.ResponseStatus = resp.StatusCode
	logRecord.ResponsePayload = payload
	s.runtime.AddAdapterSubmitLog(logRecord)
	if resp.StatusCode >= http.StatusBadRequest {
		return uploadCaptureResponse{}, fmt.Errorf("adapter status %d", resp.StatusCode)
	}
	return payload, nil
}

func (s *Service) captureForMode(ctx context.Context, deviceID string, mode string) ([]byte, error) {
	if s.vision.Mode() == "mock" {
		return []byte{}, nil
	}
	return s.devices.Capture(ctx, deviceID)
}

func (s *Service) buildDetail(taskItem clientTask, currentItem *clientTaskItem, deviceID string, mode string, rawURL string, status string, recognition string, captureURLs []string, adbCommand string, message string, meta *matchedTemplateMeta) rt.DetailRecord {
	imageCount := 0
	captureURL := ""
	if len(captureURLs) > 0 {
		imageCount = len(captureURLs)
		captureURL = captureURLs[len(captureURLs)-1]
	}
	goodsID, skuID := detailItemIDs(taskItem, currentItem)
	detail := rt.DetailRecord{
		TaskID:          taskItem.TaskID,
		UpstreamTaskRef: taskItem.UpstreamTaskRef,
		TaskMode:        mode,
		DeviceID:        deviceID,
		GoodsID:         goodsID,
		SKUID:           skuID,
		URL:             rawURL,
		Status:          status,
		Recognition:     recognition,
		ImageCount:      imageCount,
		CaptureURL:      captureURL,
		CaptureURLs:     captureURLs,
		ADBCommand:      strings.TrimSpace(adbCommand),
		Message:         message,
	}
	if meta != nil {
		detail.TemplateID = meta.TemplateID
		detail.TemplateLabel = meta.TemplateLabel
		detail.RecognitionEngine = meta.RecognitionEngine
	}
	return detail
}

func buildExternalTaskPayload(task clientTask) *ExternalTask {
	items := make([]ExternalTaskItem, 0, len(task.TaskItems))
	for _, item := range task.TaskItems {
		items = append(items, ExternalTaskItem{
			GoodsID:   item.GoodsID,
			GoodsName: item.GoodsName,
			SKUName:   append([]string(nil), item.SKUName...),
			SKUID:     item.SKUID,
			SourceURL: item.SourceURL,
			StepIndex: item.StepIndex,
		})
	}
	return &ExternalTask{
		TaskID:          task.TaskID,
		UpstreamTaskRef: task.UpstreamTaskRef,
		SourceCode:      task.SourceCode,
		SourceName:      task.SourceName,
		AccountID:       task.AccountID,
		AccountName:     task.AccountName,
		TaskItems:       items,
	}
}

func findURLTemplateByID(cfg rt.SystemConfig, templateID string) (rt.URLTemplateRecord, bool) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return rt.URLTemplateRecord{}, false
	}
	for _, item := range configuredURLTemplates(cfg) {
		if item.ID == templateID {
			return item, true
		}
	}
	return rt.URLTemplateRecord{}, false
}

func urlTemplateMetaFromRecord(templateID string, record rt.URLTemplateRecord, found bool) *matchedTemplateMeta {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return nil
	}
	label := ""
	if found {
		label = strings.TrimSpace(record.Name)
	}
	return &matchedTemplateMeta{
		TemplateID:    templateID,
		TemplateLabel: label,
	}
}

func resolveExternalDetailURL(item clientTaskItem, templateRecord rt.URLTemplateRecord, templateFound bool) string {
	if templateFound && strings.TrimSpace(templateRecord.Template) != "" {
		if rewritten := rewriteTemplateURL(templateRecord.Template, item.GoodsID, item.SKUID); strings.TrimSpace(rewritten) != "" {
			return rewritten
		}
	}
	return strings.TrimSpace(item.SourceURL)
}

func resolveExternalFallbackURL(task clientTask, templateRecord rt.URLTemplateRecord, templateFound bool) string {
	if len(task.TaskItems) > 0 {
		return resolveExternalDetailURL(task.TaskItems[0], templateRecord, templateFound)
	}
	if templateFound {
		return strings.TrimSpace(templateRecord.Template)
	}
	return ""
}

func normalizeExternalSubmitResult(result string) (submitType string, detailStatus string, err error) {
	switch strings.TrimSpace(strings.ToLower(result)) {
	case "success":
		return "success", "success", nil
	case "failure":
		return "failure", "failure", nil
	case "cancelled":
		return "cancelled", "cancelled", nil
	default:
		return "", "", errors.New("result 仅支持 success、failure、cancelled")
	}
}

func isExternalTemplateRiskItem(item ExternalSubmitTaskItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.Recognition), "account_risk")
}

func matchExternalTaskItem(task clientTask, item ExternalSubmitTaskItem, index int) clientTaskItem {
	goodsID := strings.TrimSpace(item.GoodsID)
	skuID := strings.TrimSpace(item.SKUID)
	for _, taskItem := range task.TaskItems {
		if goodsID != "" && skuID != "" && taskItem.GoodsID == goodsID && taskItem.SKUID == skuID {
			return taskItem
		}
		if skuID != "" && taskItem.SKUID == skuID {
			return taskItem
		}
	}
	if index >= 0 && index < len(task.TaskItems) {
		return task.TaskItems[index]
	}
	return clientTaskItem{
		GoodsID: goodsID,
		SKUID:   skuID,
	}
}

func decodeExternalCapture(capture ExternalSubmitCapture) ([]byte, string, error) {
	raw := strings.TrimSpace(capture.ContentBase64)
	contentType := strings.TrimSpace(capture.ContentType)
	if raw == "" {
		return nil, "", errors.New("content_base64 不能为空")
	}
	if strings.HasPrefix(raw, "data:") {
		parts := strings.SplitN(raw, ",", 2)
		if len(parts) != 2 {
			return nil, "", errors.New("data url 格式无效")
		}
		if contentType == "" {
			header := parts[0]
			if strings.HasPrefix(header, "data:") {
				contentType = strings.TrimPrefix(strings.SplitN(header, ";", 2)[0], "data:")
			}
		}
		raw = parts[1]
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, "", err
		}
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func buildDefaultCaptureFileName(taskID string, item clientTaskItem, extension string) string {
	ext := strings.TrimPrefix(strings.TrimSpace(extension), ".")
	if ext == "" {
		ext = "png"
	}
	return fmt.Sprintf("%s_%s_%s_%d.%s",
		safeCaptureNamePart(taskID),
		safeCaptureNamePart(item.GoodsID),
		safeCaptureNamePart(item.SKUID),
		time.Now().UnixNano(),
		ext,
	)
}

func buildExternalCaptureFileName(taskID string, item clientTaskItem, captureIndex int, contentType string) string {
	fileName := buildDefaultCaptureFileName(taskID, item, contentTypeExtension(contentType, "png"))
	if captureIndex <= 0 {
		return fileName
	}
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	return fmt.Sprintf("%s_%d%s", base, captureIndex+1, ext)
}

func contentTypeExtension(contentType string, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	case "image/png":
		return "png"
	default:
		return fallback
	}
}

func safeCaptureNamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "capture"
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			continue
		}
		switch r {
		case '-', '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "capture"
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendDetailMessage(base string, suffix string) string {
	base = strings.TrimSpace(base)
	suffix = strings.TrimSpace(suffix)
	switch {
	case base == "":
		return suffix
	case suffix == "":
		return base
	default:
		return base + "; submit failed: " + suffix
	}
}

func collectDetailIDs(records []rt.DetailRecord) []string {
	result := make([]string, 0, len(records))
	for _, record := range records {
		if record.ID != "" {
			result = append(result, record.ID)
		}
	}
	return result
}

func externalDeviceID(workerID string, deviceID string) string {
	if strings.TrimSpace(deviceID) != "" {
		return strings.TrimSpace(deviceID)
	}
	return "ext:" + strings.TrimSpace(workerID)
}

func accountIDOfCandidate(candidate sourceCandidate) string {
	if candidate.Account == nil {
		return ""
	}
	return candidate.Account.ID
}

func accountNameOfCandidate(candidate sourceCandidate) string {
	if candidate.Account == nil {
		return ""
	}
	return candidate.Account.Name
}

func removeTaskIDFromQueue(queue []string, taskID string) []string {
	if len(queue) == 0 {
		return queue
	}
	result := queue[:0]
	for _, item := range queue {
		if item != taskID {
			result = append(result, item)
		}
	}
	return result
}

func detailItemIDs(taskItem clientTask, currentItem *clientTaskItem) (string, string) {
	if currentItem != nil {
		return strings.TrimSpace(currentItem.GoodsID), strings.TrimSpace(currentItem.SKUID)
	}
	goods := make([]string, 0, len(taskItem.TaskItems))
	skus := make([]string, 0, len(taskItem.TaskItems))
	seenGoods := map[string]struct{}{}
	seenSKUs := map[string]struct{}{}
	for _, item := range taskItem.TaskItems {
		if value := strings.TrimSpace(item.GoodsID); value != "" {
			if _, exists := seenGoods[value]; !exists {
				seenGoods[value] = struct{}{}
				goods = append(goods, value)
			}
		}
		if value := strings.TrimSpace(item.SKUID); value != "" {
			if _, exists := seenSKUs[value]; !exists {
				seenSKUs[value] = struct{}{}
				skus = append(skus, value)
			}
		}
	}
	return strings.Join(goods, ", "), strings.Join(skus, ", ")
}

func parseAdapterSubmitFailure(err error) (int, string) {
	var adapterErr *adapterRequestError
	if errors.As(err, &adapterErr) {
		return adapterErr.StatusCode, strings.TrimSpace(adapterErr.Message)
	}
	if err == nil {
		return 0, ""
	}
	return 0, strings.TrimSpace(err.Error())
}

func shouldAutoStopDeviceOnSubmitError(source sourceCandidate, err error) bool {
	statusCode, _ := parseAdapterSubmitFailure(err)
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	return strings.TrimSpace(source.Upstream.UpstreamType) == "laoqian_worker" || strings.TrimSpace(source.Upstream.Code) == "laoqian_worker"
}

func buildAutoStopMessageForSubmitError(err error) string {
	statusCode, submitError := parseAdapterSubmitFailure(err)
	if strings.TrimSpace(submitError) == "" {
		return fmt.Sprintf("老钱上游提交返回 %d，当前设备已自动停止", statusCode)
	}
	return fmt.Sprintf("老钱上游提交返回 %d: %s；当前设备已自动停止", statusCode, strings.TrimSpace(submitError))
}

func shouldKeepCurrentTaskSnapshot(task *device.CurrentTask) bool {
	if task == nil {
		return false
	}
	switch strings.TrimSpace(task.CurrentStage) {
	case "submit_limit_stop", "account_risk_stop":
		return true
	default:
		return false
	}
}

func decodeAdapterResponse(reader io.Reader) (any, string) {
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, ""
	}
	text := strings.TrimSpace(string(raw))
	if len(raw) == 0 {
		return nil, text
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err == nil {
		return payload, text
	}
	return text, text
}

func buildAdapterErrorMessage(responseBody any, responseText string) string {
	switch payload := responseBody.(type) {
	case map[string]any:
		for _, key := range []string{"detail", "message", "error"} {
			value, ok := payload[key].(string)
			if ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	case string:
		if strings.TrimSpace(payload) != "" {
			return strings.TrimSpace(payload)
		}
	}
	if strings.TrimSpace(responseText) != "" {
		return strings.TrimSpace(responseText)
	}
	return ""
}

func (s *Service) emitEvent(level string, message string, deviceID string, payload map[string]any) {
	record := s.runtime.AddEvent(rt.EventRecord{
		Level:    level,
		Message:  message,
		DeviceID: deviceID,
		Payload:  payload,
	})
	s.hub.Broadcast(ws.Event{Type: "event", Data: map[string]any(eventToMap(record))})
}

func (s *Service) emitState() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.emitTimer == nil {
		s.emitTimer = time.AfterFunc(emitStateDebounce, s.emitStateNow)
	}
	if !s.emitPending {
		s.emitPending = true
		s.emitTimer.Reset(emitStateDebounce)
	}
}

func (s *Service) emitStateNow() {
	s.emitMu.Lock()
	s.emitPending = false
	s.emitMu.Unlock()

	summary, events, pending, adapterLogs, systemConfig := s.runtime.SnapshotWithoutDetails()
	s.hub.Broadcast(ws.Event{
		Type: "state",
		Data: map[string]any{
			"devices":             s.devices.List(),
			"templates":           s.tpl.List(),
			"summary":             summary,
			"event_log":           events,
			"pending_tasks":       pending,
			"adapter_submit_logs": adapterLogs,
			"system_config":       systemConfig,
			"upstream_configs":    s.upstream.List(),
			"platform_accounts":   s.accounts.List(),
			"upstream_options":    buildUpstreamOptions(s.upstream.List()),
			"submit_count":        s.runtime.SubmitCount(),
		},
	})
}

func (s *Service) emitStateImmediate() {
	s.emitMu.Lock()
	if s.emitTimer != nil {
		s.emitTimer.Stop()
	}
	s.emitPending = false
	s.emitMu.Unlock()
	s.emitStateNow()
}

func (s *Service) workerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}

func (s *Service) ensurePrefetchLoopLocked() {
	if s.prefetchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.prefetchCancel = cancel
	go s.prefetchLoop(ctx)
}

func (s *Service) prefetchLoop(ctx context.Context) {
	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.releaseExpiredGroupedTasks(ctx)
			s.fillPending(ctx)
		}
	}
}

type expiredGroupedTaskRelease struct {
	TaskID          string
	UpstreamTaskRef string
	SourceCode      string
	Source          sourceCandidate
	Task            clientTask
	PendingCount    int
	ActiveCount     int
	CompletedCount  int
	Message         string
}

func (s *Service) releaseExpiredGroupedTasks(ctx context.Context) {
	now := time.Now().UTC()
	releases := make([]expiredGroupedTaskRelease, 0)

	s.mu.Lock()
	for parentKey, group := range s.groups {
		// Only release tasks that are still sitting in the candidate queue and have
		// never started execution. Once any child item is active or completed, the
		// upstream task must stay claimed until the running worker finishes.
		if len(group.Active) > 0 || len(group.Completed) > 0 {
			continue
		}
		prefetchedAt, err := time.Parse(time.RFC3339, group.PrefetchedAt)
		if err != nil {
			prefetchedAt = now.Add(-groupTaskTimeout - time.Second)
		}
		if now.Sub(prefetchedAt) < groupTaskTimeout {
			continue
		}
		message := fmt.Sprintf("任务进入候选区超过 %d 秒仍未开始执行，系统自动释放", int(groupTaskTimeout/time.Second))
		releases = append(releases, expiredGroupedTaskRelease{
			TaskID:          group.Task.TaskID,
			UpstreamTaskRef: group.Task.UpstreamTaskRef,
			SourceCode:      group.Task.SourceCode,
			Source:          group.Source,
			Task:            group.Task,
			PendingCount:    len(group.Pending),
			ActiveCount:     len(group.Active),
			CompletedCount:  len(group.Completed),
			Message:         message,
		})
		for childKey := range group.Pending {
			s.pending = removePendingByChildKey(s.pending, childKey)
		}
		for childKey := range group.Active {
			delete(s.active, childKey)
		}
		delete(s.groups, parentKey)
	}
	s.mu.Unlock()

	if len(releases) == 0 {
		return
	}
	for _, release := range releases {
		s.runtime.RemovePendingTask(release.TaskID)
		if err := s.submitTaskWithMessage(ctx, "", release.Task, "cancelled", nil, release.Message); err != nil {
			s.emitEvent("error", "候选区超时自动释放失败", "", map[string]any{
				"task_id":           release.TaskID,
				"upstream_task_ref": release.UpstreamTaskRef,
				"source_code":       release.SourceCode,
				"pending_count":     release.PendingCount,
				"active_count":      release.ActiveCount,
				"completed_count":   release.CompletedCount,
				"error":             err.Error(),
			})
			continue
		}
		s.emitEvent("warning", "候选区任务超时自动释放", "", map[string]any{
			"task_id":           release.TaskID,
			"upstream_task_ref": release.UpstreamTaskRef,
			"source_code":       release.SourceCode,
			"pending_count":     release.PendingCount,
			"active_count":      release.ActiveCount,
			"completed_count":   release.CompletedCount,
			"timeout_seconds":   int(groupTaskTimeout / time.Second),
		})
	}
	s.emitStateImmediate()
}

func (s *Service) fillPending(ctx context.Context) {
	target := max(1, max(s.workerCount()*2, len(s.fetchCandidates())))
	for s.pendingCount() < target {
		candidates := s.fetchCandidates()
		if len(candidates) == 0 {
			if s.markNoCandidateWarn() {
				s.emitEvent("warning", "没有可用的平台账号可用于拉取任务", "", map[string]any{
					"enabled_upstreams": s.upstream.List(),
					"platform_accounts": s.accounts.List(),
					"reason":            "请检查是否已创建并启用上游配置，且已给对应上游实例导入并启用平台账号",
				})
			}
			return
		}
		s.clearNoCandidateWarn()
		start := s.nextCandidateCursor(len(candidates))
		fetched := false
		for i := 0; i < len(candidates); i++ {
			candidate := candidates[(start+i)%len(candidates)]
			if s.isSourceLocked(candidate.Key) {
				continue
			}
			taskItem, err := s.fetchTaskForCandidate(ctx, candidate)
			if err != nil {
				s.emitEvent("warning", "预取任务失败", "", map[string]any{"source_key": candidate.Key, "error": err.Error()})
				continue
			}
			if taskItem == nil {
				continue
			}
			businessKey := buildBusinessKey(*taskItem)
			if s.hasBusinessKey(businessKey) {
				_ = s.submitTask(ctx, "", *taskItem, "cancelled", nil)
				continue
			}
			cfg := s.runtime.SystemConfig()
			if cfg.MaxTaskSKUCount > 0 && len(taskItem.TaskItems) > cfg.MaxTaskSKUCount {
				_ = s.submitTask(ctx, "", *taskItem, "cancelled", nil)
				continue
			}
			s.enqueuePendingGroup(*taskItem, candidate, businessKey)
			s.setCandidateCursor((start + i + 1) % len(candidates))
			fetched = true
			break
		}
		if !fetched {
			return
		}
	}
}

func (s *Service) markNoCandidateWarn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noCandidateWarn {
		return false
	}
	s.noCandidateWarn = true
	return true
}

func (s *Service) clearNoCandidateWarn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noCandidateWarn = false
}

func (s *Service) fetchCandidates() []sourceCandidate {
	items := s.upstream.List()
	accounts := s.accounts.List()
	result := make([]sourceCandidate, 0)
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		matchedAccounts := make([]account.Record, 0)
		for _, acct := range accounts {
			if acct.Enabled && acct.UpstreamCode == item.Code {
				matchedAccounts = append(matchedAccounts, acct)
			}
		}
		if len(matchedAccounts) > 0 {
			for _, acct := range matchedAccounts {
				accountItem := acct
				result = append(result, sourceCandidate{
					Upstream: item,
					Account:  &accountItem,
					Token:    accountItem.Token,
					Key:      "acct:" + accountItem.ID,
				})
			}
			continue
		}
	}
	return result
}

func (s *Service) fetchCandidatesBySource(sourceCode string) []sourceCandidate {
	sourceCode = strings.TrimSpace(sourceCode)
	if sourceCode == "" {
		return s.fetchCandidates()
	}
	candidates := s.fetchCandidates()
	result := make([]sourceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Upstream.Code == sourceCode {
			result = append(result, candidate)
		}
	}
	return result
}

func (s *Service) fetchExternalTasksConcurrently(ctx context.Context, candidates []sourceCandidate) []externalFetchCandidateResult {
	start := s.nextCandidateCursor(len(candidates))
	ordered := make([]sourceCandidate, 0, len(candidates))
	for offset := 0; offset < len(candidates); offset++ {
		candidate := candidates[(start+offset)%len(candidates)]
		if s.externalSourceLocked(candidate.Key) || s.sourceLocked(candidate.Key) {
			continue
		}
		ordered = append(ordered, candidate)
	}
	if len(ordered) == 0 {
		return nil
	}

	results := make([]externalFetchCandidateResult, len(ordered))
	var wg sync.WaitGroup
	for index, candidate := range ordered {
		wg.Add(1)
		go func(index int, candidate sourceCandidate) {
			defer wg.Done()
			taskItem, err := s.fetchTaskForCandidateWithOptions(ctx, candidate, true)
			results[index] = externalFetchCandidateResult{
				Index:     index,
				Candidate: candidate,
				Task:      taskItem,
				Err:       err,
			}
		}(index, candidate)
	}
	wg.Wait()
	s.setCandidateCursor((start + len(ordered)) % len(candidates))
	return results
}

func (s *Service) fetchTaskForCandidate(ctx context.Context, candidate sourceCandidate) (*clientTask, error) {
	return s.fetchTaskForCandidateWithOptions(ctx, candidate, true)
}

func (s *Service) fetchTaskForCandidateWithOptions(ctx context.Context, candidate sourceCandidate, recordFetchStats bool) (*clientTask, error) {
	requestBody := map[string]any{
		"device_id":   nil,
		"source_code": candidate.Upstream.Code,
		"token":       candidate.Token,
	}
	body, _ := json.Marshal(requestBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.AdapterBaseURL+"/api/client/fetch-task", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	logRecord := rt.AdapterSubmitLogRecord{
		Action:         "fetch-task",
		RequestMethod:  http.MethodPost,
		Endpoint:       s.cfg.AdapterBaseURL + "/api/client/fetch-task",
		SourceCode:     candidate.Upstream.Code,
		RequestPayload: requestBody,
	}
	if candidate.Account != nil {
		logRecord.DeviceID = strings.Join(candidate.Account.BoundDeviceIDs, ",")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		logRecord.Error = err.Error()
		s.runtime.AddAdapterSubmitLog(logRecord)
		return nil, err
	}
	defer resp.Body.Close()
	logRecord.ResponseStatus = resp.StatusCode
	if resp.StatusCode == http.StatusNoContent {
		s.runtime.AddAdapterSubmitLog(logRecord)
		return nil, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		payload := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		logRecord.ResponsePayload = payload
		s.runtime.AddAdapterSubmitLog(logRecord)
		return nil, fmt.Errorf("%v", payload)
	}
	var taskItem clientTask
	if err := json.NewDecoder(resp.Body).Decode(&taskItem); err != nil {
		logRecord.Error = err.Error()
		s.runtime.AddAdapterSubmitLog(logRecord)
		return nil, err
	}
	logRecord.TaskID = taskItem.TaskID
	logRecord.UpstreamTaskRef = taskItem.UpstreamTaskRef
	logRecord.ResponsePayload = taskItem
	s.runtime.AddAdapterSubmitLog(logRecord)
	if recordFetchStats && candidate.Account != nil {
		s.accounts.RecordFetch(candidate.Account.ID)
	}
	if recordFetchStats {
		s.upstream.RecordFetch(candidate.Upstream.Code)
	}
	return &taskItem, nil
}

func (s *Service) TestPlatformAccountFetch(ctx context.Context, upstreamItem upstream.Record, accountItem account.Record) (PlatformAccountTestResult, error) {
	result := PlatformAccountTestResult{
		Success:      true,
		Fetched:      false,
		Released:     false,
		UpstreamCode: upstreamItem.Code,
		UpstreamType: upstreamItem.UpstreamType,
		AccountID:    accountItem.ID,
		AccountName:  accountItem.Name,
		Message:      "当前没有领取到任务",
	}
	candidate := sourceCandidate{
		Upstream: upstreamItem,
		Account:  &accountItem,
		Token:    accountItem.Token,
		Key:      "acct:" + accountItem.ID,
	}
	taskItem, err := s.fetchTaskForCandidateWithOptions(ctx, candidate, false)
	if err != nil {
		return PlatformAccountTestResult{}, err
	}
	if taskItem == nil {
		return result, nil
	}
	result.Fetched = true
	result.TaskID = taskItem.TaskID
	result.UpstreamTaskRef = taskItem.UpstreamTaskRef
	result.ItemCount = len(taskItem.TaskItems)
	if err := s.submitTaskWithMessage(ctx, "", *taskItem, "cancelled", nil, "账号测试领取成功，已立即释放"); err != nil {
		return PlatformAccountTestResult{}, err
	}
	result.Released = true
	result.Message = fmt.Sprintf("测试领取成功，已立即释放，领取到 %d 个 SKU", len(taskItem.TaskItems))
	return result, nil
}

func (s *Service) enqueuePendingGroup(task clientTask, source sourceCandidate, businessKey string) {
	if len(task.TaskItems) == 0 {
		return
	}
	group := &groupedTask{
		Task:         task,
		Source:       source,
		BusinessKey:  businessKey,
		TotalCount:   len(task.TaskItems),
		PrefetchedAt: nowString(),
		Pending:      map[string]pendingTask{},
		Active:       map[string]runningTask{},
		Completed:    map[string]clientSubmitTaskItem{},
		Completion:   []string{},
	}
	items := make([]pendingTask, 0, len(task.TaskItems))
	for _, item := range task.TaskItems {
		childTask := task
		childTask.TaskItems = []clientTaskItem{item}
		childKey := buildChildKey(task.TaskID, item)
		pendingItem := pendingTask{
			Task:        childTask,
			Source:      source,
			BusinessKey: childKey,
			ParentKey:   businessKey,
			ChildKey:    childKey,
		}
		group.Pending[childKey] = pendingItem
		items = append(items, pendingItem)
	}

	s.mu.Lock()
	s.groups[businessKey] = group
	s.pending = append(s.pending, items...)
	s.mu.Unlock()

	s.syncPendingGroupRecord(businessKey)
	s.emitEvent("info", "任务已进入候选区", "", map[string]any{
		"task_id":           task.TaskID,
		"source_code":       task.SourceCode,
		"task_item_count":   len(task.TaskItems),
		"upstream_task_ref": task.UpstreamTaskRef,
	})
	s.emitStateImmediate()
}

func (s *Service) takePendingTask() (pendingTask, sourceCandidate, bool) {
	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return pendingTask{}, sourceCandidate{}, false
	}
	item := s.pending[0]
	s.pending = append([]pendingTask{}, s.pending[1:]...)
	running := runningTask{
		Task:        item.Task,
		Source:      item.Source,
		BusinessKey: item.BusinessKey,
		ParentKey:   item.ParentKey,
		ChildKey:    item.ChildKey,
	}
	s.active[item.BusinessKey] = running
	if group := s.groups[item.ParentKey]; group != nil {
		delete(group.Pending, item.ChildKey)
		group.Active[item.ChildKey] = running
	}
	s.mu.Unlock()
	s.syncPendingGroupRecord(item.ParentKey)
	return item, item.Source, true
}

func (s *Service) finishTask(item pendingTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, item.BusinessKey)
}

func (s *Service) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func (s *Service) isSourceLocked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isSourceLockedLocked(key)
}

func (s *Service) sourceLocked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isSourceLockedLocked(key)
}

func (s *Service) externalSourceLocked(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.externalSources[key]
	return ok
}

func (s *Service) isSourceLockedLocked(key string) bool {
	for _, item := range s.pending {
		if item.Source.Key == key {
			return true
		}
	}
	for _, item := range s.active {
		if item.Source.Key == key {
			return true
		}
	}
	if _, ok := s.externalSources[key]; ok {
		return true
	}
	return false
}

func (s *Service) hasBusinessKey(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasBusinessKeyLocked(key)
}

func (s *Service) hasBusinessKeyLocked(key string) bool {
	if _, ok := s.groups[key]; ok {
		return true
	}
	_, ok := s.externalBusiness[key]
	return ok
}

func (s *Service) reserveExternalBuffered(task clientTask, source sourceCandidate) bool {
	businessKey := buildBusinessKey(task)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasBusinessKeyLocked(businessKey) || s.isSourceLockedLocked(source.Key) {
		return false
	}
	s.externalBuffered[task.TaskID] = externalBufferedTask{
		Task:         task,
		Source:       source,
		BusinessKey:  businessKey,
		PrefetchedAt: nowString(),
	}
	s.externalBufferQ = append(s.externalBufferQ, task.TaskID)
	s.externalBusiness[businessKey] = task.TaskID
	s.externalSources[source.Key] = task.TaskID
	return true
}

func (s *Service) reserveExternalClaim(task clientTask, source sourceCandidate, workerID string, workerName string) bool {
	businessKey := buildBusinessKey(task)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasBusinessKeyLocked(businessKey) || s.isSourceLockedLocked(source.Key) {
		return false
	}
	s.externalClaims[task.TaskID] = externalClaim{
		Task:        task,
		Source:      source,
		BusinessKey: businessKey,
		WorkerID:    workerID,
		WorkerName:  workerName,
		ClaimedAt:   nowString(),
	}
	s.externalBusiness[businessKey] = task.TaskID
	s.externalSources[source.Key] = task.TaskID
	return true
}

func (s *Service) claimBufferedExternalTask(sourceCode string, workerID string, workerName string) (clientTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, taskID := range s.externalBufferQ {
		buffered, ok := s.externalBuffered[taskID]
		if !ok {
			continue
		}
		if sourceCode != "" && buffered.Task.SourceCode != sourceCode {
			continue
		}
		delete(s.externalBuffered, taskID)
		s.externalClaims[taskID] = externalClaim{
			Task:        buffered.Task,
			Source:      buffered.Source,
			BusinessKey: buffered.BusinessKey,
			WorkerID:    workerID,
			WorkerName:  workerName,
			ClaimedAt:   nowString(),
		}
		s.externalBufferQ = removeTaskIDFromQueue(s.externalBufferQ, taskID)
		return buffered.Task, true
	}
	return clientTask{}, false
}

func (s *Service) getExternalClaim(taskID string) (externalClaim, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.externalClaims[taskID]
	return claim, ok
}

func (s *Service) releaseExternalBuffered(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buffered, ok := s.externalBuffered[taskID]
	if !ok {
		return
	}
	delete(s.externalBuffered, taskID)
	s.externalBufferQ = removeTaskIDFromQueue(s.externalBufferQ, taskID)
	delete(s.externalBusiness, buffered.BusinessKey)
	delete(s.externalSources, buffered.Source.Key)
}

func (s *Service) releaseExternalClaim(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claim, ok := s.externalClaims[taskID]
	if !ok {
		return
	}
	delete(s.externalClaims, taskID)
	delete(s.externalBusiness, claim.BusinessKey)
	delete(s.externalSources, claim.Source.Key)
}

func (s *Service) nextCandidateCursor(size int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if size == 0 {
		return 0
	}
	return s.candidateCursor % size
}

func (s *Service) setCandidateCursor(next int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.candidateCursor = next
}

func (s *Service) syncPendingGroupRecord(parentKey string) {
	s.mu.Lock()
	group := s.groups[parentKey]
	if group == nil {
		s.mu.Unlock()
		return
	}
	record := rt.PendingTaskRecord{
		TaskID:          group.Task.TaskID,
		UpstreamTaskRef: group.Task.UpstreamTaskRef,
		SourceCode:      group.Task.SourceCode,
		SourceName:      group.Task.SourceName,
		TaskItems:       pendingTaskItemsForRecord(group.Task.TaskItems),
		ItemCount:       len(group.Pending),
		TotalItemCount:  group.TotalCount,
		PendingCount:    len(group.Pending),
		ActiveCount:     len(group.Active),
		CompletedCount:  len(group.Completed),
		Status:          s.groupStatus(group),
		PrefetchedAt:    group.PrefetchedAt,
	}
	if group.Source.Account != nil {
		record.AccountID = group.Source.Account.ID
		record.AccountName = group.Source.Account.Name
	}
	shouldRemove := len(group.Pending) == 0
	taskID := group.Task.TaskID
	s.mu.Unlock()

	if shouldRemove {
		s.runtime.RemovePendingTask(taskID)
		return
	}
	s.runtime.SetPendingTask(record)
}

func pendingTaskItemsForRecord(items []clientTaskItem) []rt.PendingTaskItemRecord {
	result := make([]rt.PendingTaskItemRecord, 0, len(items))
	for _, item := range items {
		result = append(result, rt.PendingTaskItemRecord{
			GoodsID:   item.GoodsID,
			SKUID:     item.SKUID,
			StepIndex: item.StepIndex,
		})
	}
	return result
}

func (s *Service) groupStatus(group *groupedTask) string {
	if group == nil {
		return ""
	}
	if group.Released {
		return "released"
	}
	if len(group.Completed) == group.TotalCount {
		return "completed"
	}
	if len(group.Active) > 0 {
		return "active"
	}
	return "pending"
}

func buildChildKey(taskID string, item clientTaskItem) string {
	return fmt.Sprintf("%s:%s:%s:%d", taskID, item.GoodsID, item.SKUID, item.StepIndex)
}

type groupSubmission struct {
	Task       clientTask
	Source     sourceCandidate
	SubmitType string
	Message    string
	TaskItems  []clientSubmitTaskItem
}

func (s *Service) releaseAllGroupedTasks(ctx context.Context, submitType string, message string) {
	submissions := make([]groupSubmission, 0)
	s.mu.Lock()
	taskIDs := make([]string, 0, len(s.groups))
	for parentKey, group := range s.groups {
		_ = parentKey
		taskIDs = append(taskIDs, group.Task.TaskID)
		submissions = append(submissions, groupSubmission{
			Task:       group.Task,
			Source:     group.Source,
			SubmitType: submitType,
			Message:    message,
		})
	}
	s.pending = nil
	s.active = map[string]runningTask{}
	s.groups = map[string]*groupedTask{}
	s.mu.Unlock()

	for _, taskID := range taskIDs {
		s.runtime.RemovePendingTask(taskID)
	}
	for _, submission := range submissions {
		if err := s.submitTaskWithMessage(ctx, "", submission.Task, submission.SubmitType, nil, submission.Message); err != nil {
			s.emitEvent("error", "主动释放任务失败", "", map[string]any{"task_id": submission.Task.TaskID, "error": err.Error()})
		}
	}
	s.emitStateImmediate()
}

func (s *Service) requeueInterruptedTask(item pendingTask) {
	s.mu.Lock()
	delete(s.active, item.BusinessKey)
	group := s.groups[item.ParentKey]
	if group != nil && !group.Released {
		delete(group.Active, item.ChildKey)
		group.Pending[item.ChildKey] = item
		s.pending = append([]pendingTask{item}, s.pending...)
	}
	s.mu.Unlock()
	s.syncPendingGroupRecord(item.ParentKey)
	s.emitStateImmediate()
}

func (s *Service) finalizeChildSuccess(item pendingTask, submitItem clientSubmitTaskItem) *groupSubmission {
	s.mu.Lock()
	delete(s.active, item.BusinessKey)
	group := s.groups[item.ParentKey]
	if group == nil || group.Released {
		s.mu.Unlock()
		return nil
	}
	delete(group.Active, item.ChildKey)
	if _, exists := group.Completed[item.ChildKey]; !exists {
		group.Completion = append(group.Completion, item.ChildKey)
	}
	group.Completed[item.ChildKey] = submitItem
	if len(group.Completed) != group.TotalCount {
		s.mu.Unlock()
		s.syncPendingGroupRecord(item.ParentKey)
		return nil
	}
	items := make([]clientSubmitTaskItem, 0, len(group.Task.TaskItems))
	for _, taskItem := range group.Task.TaskItems {
		childKey := buildChildKey(group.Task.TaskID, taskItem)
		if childSubmit, ok := group.Completed[childKey]; ok {
			items = append(items, childSubmit)
		}
	}
	submission := &groupSubmission{
		Task:       group.Task,
		Source:     group.Source,
		SubmitType: "success",
		Message:    "同一 task_id 下全部 SKU 子任务已完成，统一提交上游",
		TaskItems:  items,
	}
	delete(s.groups, item.ParentKey)
	s.mu.Unlock()
	s.runtime.RemovePendingTask(item.Task.TaskID)
	s.emitStateImmediate()
	return submission
}

func (s *Service) finalizeChildFailure(item pendingTask, detail rt.DetailRecord, submitItems []clientSubmitTaskItem) *groupSubmission {
	s.mu.Lock()
	delete(s.active, item.BusinessKey)
	group := s.groups[item.ParentKey]
	if group == nil {
		s.mu.Unlock()
		return nil
	}
	delete(group.Active, item.ChildKey)
	group.Released = true
	group.FinalStatus = detail.Status
	group.FinalMessage = detail.Message
	for pendingKey := range group.Pending {
		s.pending = removePendingByChildKey(s.pending, pendingKey)
	}
	group.Pending = map[string]pendingTask{}
	submission := &groupSubmission{
		Task:       group.Task,
		Source:     group.Source,
		SubmitType: "failure",
		Message:    detail.Message,
		TaskItems:  submitItems,
	}
	delete(s.groups, item.ParentKey)
	s.mu.Unlock()
	s.runtime.RemovePendingTask(item.Task.TaskID)
	s.emitStateImmediate()
	return submission
}

func removePendingByChildKey(items []pendingTask, childKey string) []pendingTask {
	filtered := items[:0]
	for _, item := range items {
		if item.ChildKey != childKey {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func firstSKUID(task clientTask) string {
	if len(task.TaskItems) == 0 {
		return ""
	}
	return task.TaskItems[0].SKUID
}

func firstSubmitItem(items []clientSubmitTaskItem) clientSubmitTaskItem {
	if len(items) == 0 {
		return clientSubmitTaskItem{}
	}
	return items[0]
}

func (s *Service) uploadSuccessCaptures(ctx context.Context, taskItem clientTask, deviceID string, results []skuExecutionResult) ([]clientSubmitTaskItem, []string, error) {
	items := make([]clientSubmitTaskItem, 0, len(results))
	allURLs := make([]string, 0)
	for _, item := range results {
		captureIDs := make([]string, 0, len(item.CaptureBytes))
		captureURLs := make([]string, 0, len(item.CaptureBytes))
		for _, capture := range item.CaptureBytes {
			uploaded, err := s.uploadCapture(ctx, taskItem, deviceID, clientTaskItem{GoodsID: item.GoodsID, SKUID: item.SKUID}, capture)
			if err != nil {
				return nil, nil, err
			}
			captureIDs = append(captureIDs, uploaded.CaptureID)
			captureURLs = append(captureURLs, uploaded.CaptureURL)
			allURLs = append(allURLs, uploaded.CaptureURL)
		}
		items = append(items, clientSubmitTaskItem{
			GoodsID:     item.GoodsID,
			SKUID:       item.SKUID,
			Recognition: item.Recognition,
			Message:     item.Message,
			CaptureIDs:  captureIDs,
			CaptureURLs: captureURLs,
		})
	}
	return items, allURLs, nil
}

func (s *Service) uploadFailureEvidence(ctx context.Context, taskItem clientTask, deviceID string, mode string, recognition string, message string) (clientSubmitTaskItem, []string, error) {
	if s.vision.Mode() == "mock" || len(taskItem.TaskItems) == 0 {
		return clientSubmitTaskItem{}, nil, nil
	}
	capture, err := s.captureForMode(ctx, deviceID, mode)
	if err != nil || len(capture) == 0 {
		return clientSubmitTaskItem{}, nil, err
	}
	current := taskItem.TaskItems[0]
	uploaded, err := s.uploadCapture(ctx, taskItem, deviceID, current, capture)
	if err != nil {
		return clientSubmitTaskItem{}, nil, err
	}
	return clientSubmitTaskItem{
		GoodsID:     current.GoodsID,
		SKUID:       current.SKUID,
		Recognition: recognition,
		Message:     message,
		CaptureIDs:  []string{uploaded.CaptureID},
		CaptureURLs: []string{uploaded.CaptureURL},
	}, []string{uploaded.CaptureURL}, nil
}

func (s *Service) runSteps1to4(ctx context.Context, deviceID string, mode string, item clientTaskItem, cfg rt.SystemConfig) (*verifiedFlowState, bool, matchedTemplateMeta, error) {
	if s.vision.Mode() == "mock" {
		return &verifiedFlowState{
			goodsCapture: []byte("mock-capture"),
			goodsTemplate: template.Record{
				TemplateType:      "goods_confirm",
				RecognitionEngine: "opencv",
			},
		}, false, matchedTemplateMeta{}, nil
	}

	var clickCapture []byte
	var goodsCapture []byte
	var couponCapture []byte
	var goodsTemplate template.Record
	matchedOnceTemplates := make(map[string]struct{})

	// ---- Step 1: Loop up to 10 times, checking account_risk or goods_confirm ----
	goodsFound := false
	var loop int
	for loop = 1; loop <= 10; loop++ {
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.LoopCount = loop
			current.CurrentStage = "waiting_goods_confirm"
			current.CurrentMessage = fmt.Sprintf("第 %d 轮等待商品确认页", loop)
		})
		s.emitState()

		captureBytes, err := s.captureForMode(ctx, deviceID, mode)
		if err != nil {
			return nil, false, matchedTemplateMeta{}, fmt.Errorf("截图失败: %w", err)
		}
		cache := (*vision.OCRCache)(nil)

		// Check account_risk first, fail immediately if matched
		if matched, result, matchedTemplate, nextCache, err := s.matchStage("account_risk", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return nil, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			meta := templateMetaFromRecord(matchedTemplate)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentStage = "account_risk"
				current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中账号风控"), matchedTemplate)
			})
			s.emitState()
			return nil, true, meta, fmt.Errorf("account_risk:%s", result.MatchedTextOrFallback("命中账号风控"))
		} else {
			cache = nextCache
		}

		// Check goods_confirm
		if matched, result, matchedTemplate, nextCache, err := s.matchStage("goods_confirm", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return nil, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			goodsCapture = cloneBytes(captureBytes)
			goodsTemplate = matchedTemplate
			goodsFound = true
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentStage = "goods_confirm_matched"
				current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中商品确认页"), matchedTemplate)
			})
			s.emitState()
			break
		} else {
			cache = nextCache
		}

		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "loop_wait"
			current.CurrentMessage = "未检测到商品确认页，等待下一轮"
		})
		s.emitState()
		time.Sleep(time.Second)
	}

	if !goodsFound {
		return nil, false, matchedTemplateMeta{}, fmt.Errorf("goods_confirm:未检测到商品确认页")
	}

	// ---- Step 2: Check condition_mismatch against goods_confirm capture ----
	cache := (*vision.OCRCache)(nil)
	s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
		current.CurrentStage = "condition_mismatch"
		current.CurrentMessage = "检测条件不满足图"
	})
	s.emitState()
	if matched, result, matchedTemplate, _, err := s.matchStage("condition_mismatch", goodsCapture, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
		return nil, false, matchedTemplateMeta{}, err
	} else if matched {
		rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
		meta := templateMetaFromRecord(matchedTemplate)
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.LastMatchedTemplate = matchedTemplate.Label
			current.LastMatchedTemplateType = matchedTemplate.TemplateType
			current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
			current.CurrentStage = "condition_mismatch"
			current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中条件不满足"), matchedTemplate)
		})
		s.emitState()
		return nil, false, meta, fmt.Errorf("condition_mismatch:%s", result.MatchedTextOrFallback("条件不满足"))
	}

	// ---- Step 3: Check need_coupon against goods_confirm capture, tap if found ----
	s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
		current.CurrentStage = "need_coupon"
		current.CurrentMessage = "检测领券图"
	})
	s.emitState()
	couponClicked := false
	if matched, result, matchedTemplate, _, err := s.matchStage("need_coupon", goodsCapture, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
		return nil, false, matchedTemplateMeta{}, err
	} else if matched {
		rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
		clickCapture = rememberFirstCapture(clickCapture, goodsCapture)
		couponClicked = true
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.LastMatchedTemplate = matchedTemplate.Label
			current.LastMatchedTemplateType = matchedTemplate.TemplateType
			current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
			current.CurrentStage = "coupon_click_action"
			current.CurrentMessage = currentTaskTemplateMessage(
				fmt.Sprintf("命中领券图，执行点击 (%d,%d)", result.Center[0], result.Center[1]),
				matchedTemplate,
			)
		})
		s.emitState()
		if err := s.devices.Tap(ctx, deviceID, result.Center[0], result.Center[1]); err != nil {
			return nil, false, templateMetaFromRecord(matchedTemplate), fmt.Errorf("点击领券失败: %w", err)
		}
		sleepWithContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond))

		// ---- Step 4 (conditional): Capture again after coupon click, check coupon_detail ----
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "coupon_detail"
			current.CurrentMessage = "检测优惠券弹窗图"
		})
		s.emitState()
		postCouponCapture, err := s.captureForMode(ctx, deviceID, mode)
		if err != nil {
			return nil, false, matchedTemplateMeta{}, fmt.Errorf("领券后截图失败: %w", err)
		}
		couponCapture = postCouponCapture

		if matched2, _, matchedTemplate2, _, err := s.matchStage("coupon_detail", postCouponCapture, nil, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return nil, false, matchedTemplateMeta{}, err
		} else if matched2 {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate2)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate2.Label
				current.LastMatchedTemplateType = matchedTemplate2.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate2.RecognitionEngine
				current.CurrentStage = "coupon_close"
				current.CurrentMessage = currentTaskTemplateMessage("命中优惠券弹窗，执行关闭", matchedTemplate2)
			})
			s.emitState()
			if err := s.devices.Tap(ctx, deviceID, 500, 200); err != nil {
				return nil, false, templateMetaFromRecord(matchedTemplate2), fmt.Errorf("关闭优惠券弹窗失败: %w", err)
			}
			sleepWithContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond))
		} else {
			// coupon_detail not found after clicking coupon -- fail
			meta := templateMetaFromRecord(matchedTemplate)
			captures := [][]byte{}
			if len(goodsCapture) > 0 {
				captures = append(captures, cloneBytes(goodsCapture))
			}
			if len(postCouponCapture) > 0 {
				captures = append(captures, cloneBytes(postCouponCapture))
			}
			_ = captures
			return nil, false, meta, fmt.Errorf("coupon_detail:领券后未检测到优惠券弹窗")
		}
	}

	return &verifiedFlowState{
		goodsCapture:  goodsCapture,
		clickCapture:  clickCapture,
		couponCapture: couponCapture,
		goodsTemplate: goodsTemplate,
		couponClicked: couponClicked,
		matchedOnce:   matchedOnceTemplates,
	}, false, matchedTemplateMeta{}, nil
}

func (s *Service) runStealthMode(ctx context.Context, deviceID string, mode string, item clientTaskItem, cfg rt.SystemConfig) (skuExecutionResult, bool, bool, matchedTemplateMeta, error) {
	flowState, accountRisk, meta, err := s.runSteps1to4(ctx, deviceID, mode, item, cfg)
	if err != nil {
		if accountRisk {
			return skuExecutionResult{}, true, false, meta, err
		}
		return skuExecutionResult{}, false, false, meta, err
	}

	captures := [][]byte{cloneBytes(flowState.goodsCapture)}
	if len(flowState.couponCapture) > 0 && len(flowState.clickCapture) > 0 {
		captures = [][]byte{cloneBytes(flowState.clickCapture), cloneBytes(flowState.goodsCapture), cloneBytes(flowState.couponCapture)}
	} else if len(flowState.clickCapture) > 0 {
		captures = [][]byte{cloneBytes(flowState.clickCapture), cloneBytes(flowState.goodsCapture)}
	}

	return skuExecutionResult{
		GoodsID:           item.GoodsID,
		SKUID:             item.SKUID,
		Recognition:       "goods_confirm",
		Message:           "命中商品确认页",
		CaptureBytes:      captures,
		TemplateID:        flowState.goodsTemplate.ID,
		TemplateLabel:     flowState.goodsTemplate.Label,
		RecognitionEngine: flowState.goodsTemplate.RecognitionEngine,
	}, false, flowState.couponClicked, templateMetaFromRecord(flowState.goodsTemplate), nil
}

func (s *Service) runDetailMode(ctx context.Context, deviceID string, mode string, item clientTaskItem, cfg rt.SystemConfig) (skuExecutionResult, bool, bool, matchedTemplateMeta, error) {
	flowState, accountRisk, meta, err := s.runSteps1to4(ctx, deviceID, mode, item, cfg)
	if err != nil {
		if accountRisk {
			return skuExecutionResult{}, true, false, meta, err
		}
		return skuExecutionResult{}, false, false, meta, err
	}

	// ---- Step 5: Tap spec selection at (950,950) ----
	s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
		current.CurrentStage = "spec_selection_tap"
		current.CurrentMessage = "点击规格选择区域"
	})
	s.emitState()
	if err := s.devices.Tap(ctx, deviceID, 950, 950); err != nil {
		return skuExecutionResult{}, false, false, matchedTemplateMeta{}, fmt.Errorf("规格选择点击失败: %w", err)
	}
	sleepWithContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond))

	// ---- Step 6: Capture + OCR verify SKU names ----
	s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
		current.CurrentStage = "sku_ocr_verify"
		current.CurrentMessage = "OCR 校验 SKU 名称"
	})
	s.emitState()
	skuVerifyCapture, err := s.captureForMode(ctx, deviceID, mode)
	if err != nil {
		return skuExecutionResult{}, false, false, matchedTemplateMeta{}, fmt.Errorf("规格选择后截图失败: %w", err)
	}

	if len(item.SKUName) > 0 {
		ocrCache := (*vision.OCRCache)(nil)
		for _, skuName := range item.SKUName {
			skuName = strings.TrimSpace(skuName)
			if skuName == "" {
				continue
			}
			tempTpl := template.Record{
				RecognitionEngine: "ocr",
				ExpectedText:      skuName,
				Threshold:         0.5,
				Method:            "ocr",
			}
			result, nextCache, err := s.vision.Match(tempTpl, skuVerifyCapture, ocrCache)
			if err != nil {
				return skuExecutionResult{}, false, false, matchedTemplateMeta{}, fmt.Errorf("OCR 校验 SKU 名称 '%s' 失败: %w", skuName, err)
			}
			ocrCache = nextCache
			if !result.Found {
				resultCaptures := [][]byte{cloneBytes(flowState.goodsCapture), cloneBytes(skuVerifyCapture)}
				return skuExecutionResult{
					GoodsID:      item.GoodsID,
					SKUID:        item.SKUID,
					Recognition:  "sku_name_mismatch",
					Message:      fmt.Sprintf("未找到 SKU 名称: %s", skuName),
					CaptureBytes: resultCaptures,
				}, false, flowState.couponClicked, matchedTemplateMeta{}, fmt.Errorf("sku_name_mismatch:未找到 SKU 名称 '%s'", skuName)
			}
		}
	}

	// ---- Success with SKU verification ----
	captures := [][]byte{cloneBytes(flowState.goodsCapture), cloneBytes(skuVerifyCapture)}
	if len(flowState.couponCapture) > 0 && len(flowState.clickCapture) > 0 {
		captures = [][]byte{cloneBytes(flowState.clickCapture), cloneBytes(flowState.goodsCapture), cloneBytes(flowState.couponCapture), cloneBytes(skuVerifyCapture)}
	} else if len(flowState.clickCapture) > 0 {
		captures = [][]byte{cloneBytes(flowState.clickCapture), cloneBytes(flowState.goodsCapture), cloneBytes(skuVerifyCapture)}
	}

	return skuExecutionResult{
		GoodsID:           item.GoodsID,
		SKUID:             item.SKUID,
		Recognition:       "goods_confirm",
		Message:           "商品确认 + SKU 名称验证通过",
		CaptureBytes:      captures,
		TemplateID:        flowState.goodsTemplate.ID,
		TemplateLabel:     flowState.goodsTemplate.Label,
		RecognitionEngine: flowState.goodsTemplate.RecognitionEngine,
	}, false, flowState.couponClicked, templateMetaFromRecord(flowState.goodsTemplate), nil
}

type verifiedFlowState struct {
	goodsCapture  []byte
	clickCapture  []byte
	couponCapture []byte
	goodsTemplate template.Record
	couponClicked bool
	matchedOnce   map[string]struct{}
}

func (s *Service) runTaskUntilTerminal(ctx context.Context, deviceID string, mode string, item clientTaskItem, cfg rt.SystemConfig) (skuExecutionResult, bool, bool, matchedTemplateMeta, error) {
	if s.vision.Mode() == "mock" {
		return skuExecutionResult{
			GoodsID:           item.GoodsID,
			SKUID:             item.SKUID,
			Recognition:       "success_image",
			Message:           "视觉 mock 模式命中成功图",
			CaptureBytes:      [][]byte{[]byte("mock-capture")},
			RecognitionEngine: "opencv",
		}, false, true, matchedTemplateMeta{}, nil
	}

	var clickCapture []byte
	matchedOnceTemplates := make(map[string]struct{})
	for loop := 1; loop <= 20; loop++ {
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.LoopCount = loop
			current.CurrentStage = "capture"
			current.CurrentMessage = fmt.Sprintf("第 %d 轮截图识别", loop)
		})
		s.emitState()
		captureBytes, err := s.captureForMode(ctx, deviceID, mode)
		if err != nil {
			return skuExecutionResult{}, false, false, matchedTemplateMeta{}, fmt.Errorf("截图失败: %w", err)
		}
		cache := (*vision.OCRCache)(nil)
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "account_risk"
			current.CurrentMessage = "检测账号风控图"
		})
		s.emitState()
		if matched, result, matchedTemplate, nextCache, err := s.matchStage("account_risk", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return skuExecutionResult{}, false, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			meta := templateMetaFromRecord(matchedTemplate)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentStage = "account_risk"
				current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中账号风控"), matchedTemplate)
			})
			s.emitState()
			return skuExecutionResult{}, true, false, meta, fmt.Errorf("account_risk:%s", result.MatchedTextOrFallback("命中账号风控"))
		} else {
			cache = nextCache
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "fail_release"
			current.CurrentMessage = "检测失败释放图"
		})
		s.emitState()
		if matched, result, matchedTemplate, nextCache, err := s.matchStage("fail_release", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return skuExecutionResult{}, false, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			meta := templateMetaFromRecord(matchedTemplate)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentStage = "fail_release"
				current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中失败释放"), matchedTemplate)
			})
			s.emitState()
			return skuExecutionResult{}, false, false, meta, fmt.Errorf("fail_release:%s", result.MatchedTextOrFallback("命中失败释放"))
		} else {
			cache = nextCache
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "click_image"
			current.CurrentMessage = "检测点击图"
		})
		s.emitState()
		if matched, result, matchedTemplate, nextCache, err := s.matchStage("click_image", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return skuExecutionResult{}, false, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			cache = nextCache
			clickCapture = rememberFirstCapture(clickCapture, captureBytes)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentStage = "click_action"
				current.CurrentMessage = currentTaskTemplateMessage(
					fmt.Sprintf("命中模板，执行点击 (%d,%d)", result.Center[0], result.Center[1]),
					matchedTemplate,
				)
			})
			s.emitState()
			if err := s.devices.Tap(ctx, deviceID, result.Center[0], result.Center[1]); err != nil {
				return skuExecutionResult{}, false, false, templateMetaFromRecord(matchedTemplate), fmt.Errorf("点击失败: %w", err)
			}
			sleepWithContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond))
			continue
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "success_image"
			current.CurrentMessage = "检测成功图"
		})
		s.emitState()
		if matched, result, matchedTemplate, _, err := s.matchStage("success_image", captureBytes, cache, len(clickCapture) > 0, matchedOnceTemplates); err != nil {
			return skuExecutionResult{}, false, false, matchedTemplateMeta{}, err
		} else if matched {
			rememberMatchedTemplate(matchedOnceTemplates, matchedTemplate)
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.CurrentStage = "success"
				current.LastMatchedTemplate = matchedTemplate.Label
				current.LastMatchedTemplateType = matchedTemplate.TemplateType
				current.LastMatchedRecognitionEngine = matchedTemplate.RecognitionEngine
				current.CurrentMessage = currentTaskTemplateMessage(result.MatchedTextOrFallback("命中成功图"), matchedTemplate)
			})
			s.emitState()
			captures := [][]byte{cloneBytes(captureBytes)}
			if len(clickCapture) > 0 {
				captures = [][]byte{cloneBytes(clickCapture), cloneBytes(captureBytes)}
			}
			return skuExecutionResult{
				GoodsID:           item.GoodsID,
				SKUID:             item.SKUID,
				Recognition:       "success_image",
				Message:           result.MatchedTextOrFallback("命中成功图"),
				CaptureBytes:      captures,
				TemplateID:        matchedTemplate.ID,
				TemplateLabel:     matchedTemplate.Label,
				RecognitionEngine: matchedTemplate.RecognitionEngine,
			}, false, len(clickCapture) > 0, templateMetaFromRecord(matchedTemplate), nil
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "loop_wait"
			current.CurrentMessage = "本轮未命中，等待下一轮"
		})
		s.emitState()
		time.Sleep(time.Second)
	}
	return skuExecutionResult{}, false, false, matchedTemplateMeta{}, fmt.Errorf("fail_release:识别超时")
}

func (s *Service) matchStage(stage string, captureBytes []byte, cache *vision.OCRCache, clickTriggered bool, matchedOnceTemplates map[string]struct{}) (bool, vision.MatchResult, template.Record, *vision.OCRCache, error) {
	templates := s.filterStageTemplates(stage, clickTriggered, matchedOnceTemplates)
	currentCache := cache
	for index := 0; index < len(templates); {
		if templates[index].RecognitionEngine == "opencv" {
			next := index
			for next < len(templates) && templates[next].RecognitionEngine == "opencv" {
				next++
			}
			matchedIndex, result, nextCache, err := s.vision.MatchOpenCVBatch(templates[index:next], captureBytes, currentCache)
			if err != nil {
				return false, vision.MatchResult{}, template.Record{}, currentCache, err
			}
			currentCache = nextCache
			if matchedIndex >= 0 && result.Found {
				return true, result, templates[index+matchedIndex], currentCache, nil
			}
			index = next
			continue
		}
		tpl := templates[index]
		index++
		result, nextCache, err := s.vision.Match(tpl, captureBytes, currentCache)
		if err != nil {
			return false, vision.MatchResult{}, template.Record{}, currentCache, err
		}
		currentCache = nextCache
		if result.Found {
			return true, result, tpl, currentCache, nil
		}
	}
	return false, vision.MatchResult{}, template.Record{}, currentCache, nil
}

func (s *Service) filterStageTemplates(stage string, clickTriggered bool, matchedOnceTemplates map[string]struct{}) []template.Record {
	templates := s.tpl.ListEnabledByType(stage)
	filtered := make([]template.Record, 0, len(templates))
	for _, item := range templates {
		if item.MatchOncePerTask {
			if _, exists := matchedOnceTemplates[item.ID]; exists {
				continue
			}
		}
		if stage == "fail_release" && item.RequiresClick && !clickTriggered {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func rememberMatchedTemplate(matchedOnceTemplates map[string]struct{}, item template.Record) {
	if !item.MatchOncePerTask {
		return
	}
	if matchedOnceTemplates == nil {
		return
	}
	matchedOnceTemplates[item.ID] = struct{}{}
}

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	cloned := make([]byte, len(data))
	copy(cloned, data)
	return cloned
}

func rememberFirstCapture(existing []byte, current []byte) []byte {
	if len(existing) > 0 {
		return existing
	}
	return cloneBytes(current)
}

func templateMetaFromRecord(record template.Record) matchedTemplateMeta {
	return matchedTemplateMeta{
		TemplateID:        record.ID,
		TemplateLabel:     record.Label,
		TemplateType:      record.TemplateType,
		RecognitionEngine: record.RecognitionEngine,
	}
}

func currentTaskTemplateMessage(message string, record template.Record) string {
	base := strings.TrimSpace(message)
	summary := matchedTemplateSummary(templateMetaFromRecord(record))
	if summary == "" {
		return base
	}
	if base == "" {
		return "命中模板: " + summary
	}
	return base + " [模板: " + summary + "]"
}

func detailMessageWithTemplate(message string, meta matchedTemplateMeta) string {
	base := strings.TrimSpace(message)
	if meta.TemplateLabel == "" && meta.TemplateID == "" {
		return base
	}
	if meta.TemplateLabel != "" && strings.Contains(base, meta.TemplateLabel) {
		return base
	}
	parts := make([]string, 0, 2)
	if meta.TemplateLabel != "" {
		parts = append(parts, meta.TemplateLabel)
	}
	if meta.TemplateID != "" {
		parts = append(parts, meta.TemplateID)
	}
	if base == "" {
		return "模板命中: " + strings.Join(parts, " / ")
	}
	return base + " [模板: " + strings.Join(parts, " / ") + "]"
}

func matchedTemplateSummary(meta matchedTemplateMeta) string {
	parts := make([]string, 0, 3)
	if meta.TemplateLabel != "" {
		parts = append(parts, meta.TemplateLabel)
	}
	if typeLabel := templateTypeDisplayName(meta.TemplateType); typeLabel != "" {
		parts = append(parts, typeLabel)
	}
	if engineLabel := recognitionEngineDisplayName(meta.RecognitionEngine); engineLabel != "" {
		parts = append(parts, engineLabel)
	}
	return strings.Join(parts, " / ")
}

func templateTypeDisplayName(templateType string) string {
	switch strings.TrimSpace(templateType) {
	case "account_risk":
		return "账号风控"
	case "fail_release":
		return "失败释放"
	case "click_image":
		return "点击图"
	case "success_image":
		return "成功图"
	case "goods_confirm":
		return "商品确认"
	case "condition_mismatch":
		return "条件不满足"
	case "need_coupon":
		return "需要领券"
	case "coupon_detail":
		return "优惠券弹窗"
	default:
		return strings.TrimSpace(templateType)
	}
}

func recognitionEngineDisplayName(engine string) string {
	switch strings.TrimSpace(engine) {
	case "ocr":
		return "OCR"
	case "opencv":
		return "找图"
	default:
		return strings.TrimSpace(engine)
	}
}

type taskURLSelection struct {
	URL           string
	TemplateID    string
	TemplateIndex int
	TemplateTotal int
}

func newDeviceURLTemplateState() deviceURLTemplateState {
	return deviceURLTemplateState{
		CurrentIndex: 0,
		RiskedIDs:    map[string]struct{}{},
	}
}

func (s *Service) cleanupWorkerOnExit(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.urlTemplateState, deviceID)
	if _, exists := s.workers[deviceID]; !exists {
		return false
	}
	delete(s.workers, deviceID)
	if len(s.workers) == 0 && s.prefetchCancel != nil {
		s.prefetchCancel()
		s.prefetchCancel = nil
		return true
	}
	return false
}

func (s *Service) applyCurrentURLTemplateStatus(deviceID string) {
	cfg := s.runtime.SystemConfig()
	activeTemplates := s.activeURLTemplatesForDevice(deviceID, cfg)
	index, total, templateID := s.currentDeviceURLTemplateStatus(deviceID, activeTemplates)
	s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
		current.URLTemplateID = templateID
		current.URLTemplateIndex = index
		current.URLTemplateTotal = total
	})
}

func (s *Service) currentDeviceURLTemplateStatus(deviceID string, activeTemplates []rt.URLTemplateRecord) (int, int, string) {
	if len(activeTemplates) == 0 {
		return 0, 0, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.urlTemplateState[deviceID]
	nextIndex := resolveDeviceURLTemplateIndex(state, activeTemplates)
	if nextIndex < 0 {
		return 0, len(activeTemplates), ""
	}
	state.CurrentIndex = nextIndex
	s.urlTemplateState[deviceID] = state
	return nextIndex + 1, len(activeTemplates), activeTemplates[nextIndex].ID
}

func (s *Service) selectTaskURLForDevice(deviceID string, cfg rt.SystemConfig, item clientTaskItem) taskURLSelection {
	activeTemplates := s.activeURLTemplatesForDevice(deviceID, cfg)
	if !cfg.UseURLTemplates || len(activeTemplates) == 0 {
		return taskURLSelection{
			URL: fmt.Sprintf("https://mobile.yangkeduo.com/order_checkout.html?goods_id=%s&sku_id=%s", item.GoodsID, item.SKUID),
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.urlTemplateState[deviceID]
	nextIndex := resolveDeviceURLTemplateIndex(state, activeTemplates)
	if nextIndex < 0 {
		return taskURLSelection{
			URL:           fmt.Sprintf("https://mobile.yangkeduo.com/order_checkout.html?goods_id=%s&sku_id=%s", item.GoodsID, item.SKUID),
			TemplateTotal: len(activeTemplates),
		}
	}
	state.CurrentIndex = nextIndex
	s.urlTemplateState[deviceID] = state
	tpl := activeTemplates[nextIndex]
	return taskURLSelection{
		URL:           rewriteTemplateURL(tpl.Template, item.GoodsID, item.SKUID),
		TemplateID:    tpl.ID,
		TemplateIndex: nextIndex + 1,
		TemplateTotal: len(activeTemplates),
	}
}

func (s *Service) advanceDeviceURLTemplateAfterRisk(deviceID string, cfg rt.SystemConfig, templateID string) (bool, bool) {
	activeTemplates := s.activeURLTemplatesForDevice(deviceID, cfg)
	if len(activeTemplates) == 0 || templateID == "" {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.urlTemplateState[deviceID]
	if state.RiskedIDs == nil {
		state.RiskedIDs = map[string]struct{}{}
	}
	state.RiskedIDs[templateID] = struct{}{}
	nextIndex := -1
	for index := state.CurrentIndex + 1; index < len(activeTemplates); index++ {
		if _, risked := state.RiskedIDs[activeTemplates[index].ID]; risked {
			continue
		}
		nextIndex = index
		break
	}
	if nextIndex >= 0 {
		state.CurrentIndex = nextIndex
		s.urlTemplateState[deviceID] = state
		return true, false
	}
	state.CurrentIndex = len(activeTemplates)
	s.urlTemplateState[deviceID] = state
	return true, true
}

func (s *Service) activeURLTemplatesForDevice(deviceID string, cfg rt.SystemConfig) []rt.URLTemplateRecord {
	activeTemplates := activeURLTemplates(cfg)
	if s.devices == nil {
		return activeTemplates
	}
	selectedIDs := s.devices.SelectedURLTemplateIDs(deviceID)
	if len(activeTemplates) == 0 || len(selectedIDs) == 0 {
		return activeTemplates
	}
	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		selectedSet[id] = struct{}{}
	}
	if len(selectedSet) == 0 {
		return activeTemplates
	}
	filtered := make([]rt.URLTemplateRecord, 0, len(activeTemplates))
	for _, tpl := range activeTemplates {
		if _, ok := selectedSet[tpl.ID]; ok {
			filtered = append(filtered, tpl)
		}
	}
	return filtered
}

func activeURLTemplates(cfg rt.SystemConfig) []rt.URLTemplateRecord {
	if !cfg.UseURLTemplates {
		return nil
	}
	return configuredURLTemplates(cfg)
}

func configuredURLTemplates(cfg rt.SystemConfig) []rt.URLTemplateRecord {
	result := make([]rt.URLTemplateRecord, 0, len(cfg.URLTemplates))
	for _, tpl := range cfg.URLTemplates {
		if strings.TrimSpace(tpl.Template) == "" {
			continue
		}
		result = append(result, tpl)
	}
	return result
}

func resolveDeviceURLTemplateIndex(state deviceURLTemplateState, activeTemplates []rt.URLTemplateRecord) int {
	if len(activeTemplates) == 0 {
		return -1
	}
	start := state.CurrentIndex
	if start < 0 {
		start = 0
	}
	if start >= len(activeTemplates) {
		return -1
	}
	for index := start; index < len(activeTemplates); index++ {
		if _, risked := state.RiskedIDs[activeTemplates[index].ID]; risked {
			continue
		}
		return index
	}
	return -1
}

func buildUpstreamOptions(items []upstream.Record) []map[string]any {
	options := make([]map[string]any, 0, len(items))
	for _, item := range items {
		options = append(options, map[string]any{
			"code":          item.Code,
			"name":          item.Name,
			"upstream_type": item.UpstreamType,
			"enabled":       item.Enabled,
		})
	}
	return options
}

func buildTaskURL(cfg rt.SystemConfig, item clientTaskItem) string {
	return selectTaskURL(cfg, item).URL
}

func selectTaskURL(cfg rt.SystemConfig, item clientTaskItem) taskURLSelection {
	if cfg.UseURLTemplates {
		for _, tpl := range cfg.URLTemplates {
			if strings.TrimSpace(tpl.Template) == "" {
				continue
			}
			return taskURLSelection{
				URL:        rewriteTemplateURL(tpl.Template, item.GoodsID, item.SKUID),
				TemplateID: tpl.ID,
			}
		}
	}
	return taskURLSelection{
		URL: fmt.Sprintf("https://mobile.yangkeduo.com/order_checkout.html?goods_id=%s&sku_id=%s", item.GoodsID, item.SKUID),
	}
}

var (
	legacyGoodsIDPattern = regexp.MustCompile(`\{\{\s*goods_id\s*\}\}|\{\s*goods_id\s*\}`)
	legacySKUIDPattern   = regexp.MustCompile(`\{\{\s*sku_id\s*\}\}|\{\s*sku_id\s*\}`)
	queryGoodsIDPattern  = regexp.MustCompile(`(^|[?&])goods_id=[^&#]*`)
	querySKUIDPattern    = regexp.MustCompile(`(^|[?&])sku_id=[^&#]*`)
	jsonGoodsIDPattern   = regexp.MustCompile(`("goods_id"\s*:\s*)\d+`)
	jsonSKUIDPattern     = regexp.MustCompile(`("sku_id"\s*:\s*)\d+`)
)

func rewriteTemplateURL(rawTemplate string, goodsID string, skuID string) string {
	normalized := strings.TrimSpace(strings.Trim(strings.TrimSpace(rawTemplate), "`\""))
	if normalized == "" {
		return ""
	}
	normalized = replaceIDsInText(normalized, goodsID, skuID)
	parsed, err := url.Parse(normalized)
	if err != nil || (parsed.Scheme == "" && parsed.Host == "" && parsed.RawQuery == "" && !strings.Contains(normalized, "?")) {
		return normalized
	}
	rewriteParsedURL(parsed, goodsID, skuID, 0)
	return parsed.String()
}

func rewriteParsedURL(parsed *url.URL, goodsID string, skuID string, depth int) {
	if parsed == nil || depth > 4 {
		return
	}
	query := parsed.Query()
	for key, values := range query {
		switch key {
		case "goods_id":
			query.Set(key, goodsID)
			continue
		case "sku_id":
			query.Set(key, skuID)
			continue
		}
		for index, value := range values {
			values[index] = rewriteTemplateValue(value, goodsID, skuID, depth+1)
		}
		query[key] = values
	}
	parsed.RawQuery = query.Encode()
	if strings.TrimSpace(parsed.Fragment) != "" {
		parsed.Fragment = rewriteTemplateValue(parsed.Fragment, goodsID, skuID, depth+1)
	}
}

func rewriteTemplateValue(value string, goodsID string, skuID string, depth int) string {
	if depth > 4 {
		return replaceIDsInText(value, goodsID, skuID)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return value
	}
	if parsed, err := url.Parse(trimmed); err == nil && (parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || strings.Contains(trimmed, "?")) {
		rewriteParsedURL(parsed, goodsID, skuID, depth+1)
		return parsed.String()
	}
	current := trimmed
	decodeCount := 0
	for decodeCount < 3 {
		next, err := url.QueryUnescape(current)
		if err != nil || next == current {
			break
		}
		current = next
		decodeCount++
	}
	current = replaceIDsInText(current, goodsID, skuID)
	if parsed, err := url.Parse(current); err == nil && (parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || strings.Contains(current, "?")) {
		rewriteParsedURL(parsed, goodsID, skuID, depth+1)
		current = parsed.String()
	}
	for i := 0; i < decodeCount; i++ {
		current = url.QueryEscape(current)
	}
	return current
}

func replaceIDsInText(input string, goodsID string, skuID string) string {
	result := legacyGoodsIDPattern.ReplaceAllString(input, goodsID)
	result = legacySKUIDPattern.ReplaceAllString(result, skuID)
	result = queryGoodsIDPattern.ReplaceAllString(result, "${1}goods_id="+goodsID)
	result = querySKUIDPattern.ReplaceAllString(result, "${1}sku_id="+skuID)
	result = jsonGoodsIDPattern.ReplaceAllString(result, "${1}"+goodsID)
	result = jsonSKUIDPattern.ReplaceAllString(result, "${1}"+skuID)
	return result
}

func eventToMap(record rt.EventRecord) map[string]any {
	return map[string]any{
		"id":        record.ID,
		"timestamp": record.Timestamp,
		"device_id": record.DeviceID,
		"level":     record.Level,
		"message":   record.Message,
		"payload":   record.Payload,
	}
}

func detailToMap(record rt.DetailRecord) map[string]any {
	return map[string]any{
		"id":                 record.ID,
		"timestamp":          record.Timestamp,
		"task_id":            record.TaskID,
		"upstream_task_ref":  record.UpstreamTaskRef,
		"task_mode":          record.TaskMode,
		"device_id":          record.DeviceID,
		"goods_id":           record.GoodsID,
		"sku_id":             record.SKUID,
		"url":                record.URL,
		"status":             record.Status,
		"recognition":        record.Recognition,
		"image_count":        record.ImageCount,
		"capture_url":        record.CaptureURL,
		"capture_urls":       record.CaptureURLs,
		"message":            record.Message,
		"template_id":        record.TemplateID,
		"template_label":     record.TemplateLabel,
		"recognition_engine": record.RecognitionEngine,
		"adb_command":        record.ADBCommand,
		"submit_status_code": record.SubmitStatusCode,
		"submit_error":       record.SubmitError,
	}
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func durationFromSeconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func buildBusinessKey(task clientTask) string {
	if strings.TrimSpace(task.SourceCode) != "" && strings.TrimSpace(task.UpstreamTaskRef) != "" {
		return task.SourceCode + ":" + task.UpstreamTaskRef
	}
	return task.TaskID
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
