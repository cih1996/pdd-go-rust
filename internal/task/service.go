package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

	mu              sync.Mutex
	workers         map[string]context.CancelFunc
	prefetchCancel  context.CancelFunc
	candidateCursor int
	noCandidateWarn bool
	pending         []pendingTask
	active          map[string]runningTask
	groups          map[string]*groupedTask
}

type startRequest struct {
	DeviceIDs []string `json:"device_ids"`
	Mode      string   `json:"mode"`
}

type stopRequest struct {
	DeviceIDs []string `json:"device_ids"`
}

type clientTaskItem struct {
	GoodsID   string `json:"goods_id"`
	SKUID     string `json:"sku_id"`
	SourceURL string `json:"source_url"`
	StepIndex int    `json:"step_index"`
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
	GoodsID      string
	SKUID        string
	Recognition  string
	Message      string
	CaptureBytes [][]byte
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

func NewService(cfg config.Config, hub *ws.Hub, tpl *template.Store, visionEngine *vision.Engine, devices *device.Service, ups *upstream.Store, accounts *account.Store, runtimeStore *rt.Store) *Service {
	return &Service{
		cfg:      cfg,
		hub:      hub,
		tpl:      tpl,
		vision:   visionEngine,
		devices:  devices,
		upstream: ups,
		accounts: accounts,
		runtime:  runtimeStore,
		client:   &http.Client{Timeout: 30 * time.Second},
		workers:  map[string]context.CancelFunc{},
		active:   map[string]runningTask{},
		groups:   map[string]*groupedTask{},
	}
}

func (s *Service) RuntimePlan() map[string]any {
	return map[string]any{
		"adapter_mode":   "standalone rust service",
		"ws_push":        true,
		"vision":         s.vision.Plan(),
		"template_total": s.tpl.Count(),
		"device_total":   len(s.devices.List()),
		"worker_total":   s.workerCount(),
	}
}

func (s *Service) Start(deviceIDs []string, mode string) ([]string, []string) {
	started := make([]string, 0, len(deviceIDs))
	skipped := make([]string, 0, len(deviceIDs))

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, deviceID := range deviceIDs {
		if _, exists := s.workers[deviceID]; exists {
			skipped = append(skipped, deviceID)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.workers[deviceID] = cancel
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
	s.emitState()

	defer func() {
		s.devices.SetCurrentTask(deviceID, nil)
		s.emitEvent("info", "设备任务循环已停止", deviceID, nil)
		s.emitState()
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

		s.runtime.AddDetail(detail)
		s.hub.Broadcast(ws.Event{Type: "detail", Data: map[string]any(detailToMap(detail))})

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
				s.emitEvent("error", "提交任务失败", deviceID, map[string]any{"task_id": submission.Task.TaskID, "error": err.Error()})
				detail.Message = strings.TrimSpace(detail.Message + "; submit failed: " + err.Error())
			} else {
				reportSuccess := submission.SubmitType == "success"
				if source.Account != nil {
					s.accounts.RecordSubmit(source.Account.ID, reportSuccess)
				}
				s.upstream.RecordReport(source.Upstream.Code, reportSuccess)
			}
		}

		s.devices.SetCurrentTask(deviceID, &device.CurrentTask{
			TaskID:         taskItem.Task.TaskID,
			TaskMode:       mode,
			StartedAt:      nowString(),
			CurrentStage:   "completed",
			CurrentMessage: detail.Message,
		})
		s.emitState()
		if shouldStop {
			s.emitEvent("warning", "命中账号风控，设备停止后续任务循环", deviceID, map[string]any{"task_id": taskItem.Task.TaskID})
			return
		}
		time.Sleep(time.Second)
	}
}

func (s *Service) executeTask(ctx context.Context, deviceID string, mode string, taskItem clientTask) (bool, bool, []skuExecutionResult, rt.DetailRecord) {
	systemConfig := s.runtime.SystemConfig()
	results := make([]skuExecutionResult, 0, len(taskItem.TaskItems))
	clickTriggered := false

	for index, item := range taskItem.TaskItems {
		taskURL := buildTaskURL(systemConfig, item)
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.TaskID = taskItem.TaskID
			current.TaskMode = mode
			current.CurrentStage = "open_url"
			current.CurrentMessage = fmt.Sprintf("处理 SKU %d/%d", index+1, len(taskItem.TaskItems))
			current.LoopCount = index + 1
		})
		s.emitState()

		if strings.TrimSpace(taskURL) != "" {
			if err := s.devices.OpenURL(ctx, deviceID, taskURL); err != nil {
				return false, false, nil, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, "failure", "open_url_failed", nil, "打开链接失败: "+err.Error())
			}
			sleepWithContext(ctx, durationFromSeconds(systemConfig.OpenURLDelaySeconds))
		}

		skuResult, shouldStop, matchedClick, err := s.runTaskUntilTerminal(ctx, deviceID, mode, item, systemConfig)
		if err != nil {
			status := "failure"
			recognition := "loop_failed"
			message := err.Error()
			if strings.HasPrefix(message, "account_risk:") {
				recognition = "account_risk"
				message = strings.TrimPrefix(message, "account_risk:")
				return false, true, results, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, status, recognition, nil, message)
			}
			if strings.HasPrefix(message, "fail_release:") {
				recognition = "fail_release"
				message = strings.TrimPrefix(message, "fail_release:")
			}
			return false, shouldStop, results, s.buildDetail(taskItem, &item, deviceID, mode, taskURL, status, recognition, nil, message)
		}
		clickTriggered = clickTriggered || matchedClick
		results = append(results, skuResult)
	}

	message := "全部 SKU 识别完成"
	if clickTriggered {
		message = "全部 SKU 识别完成，包含点击图链路"
	}
	return true, false, results, s.buildDetail(taskItem, nil, deviceID, mode, buildTaskURL(systemConfig, taskItem.TaskItems[0]), "success", "success_image", nil, message)
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

	var responseBody any
	_ = json.NewDecoder(resp.Body).Decode(&responseBody)
	logRecord.ResponseStatus = resp.StatusCode
	logRecord.ResponsePayload = responseBody
	if resp.StatusCode >= http.StatusBadRequest {
		logRecord.Error = fmt.Sprintf("adapter status %d", resp.StatusCode)
	}
	s.runtime.AddAdapterSubmitLog(logRecord)
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("adapter status %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) uploadCapture(ctx context.Context, taskItem clientTask, deviceID string, item clientTaskItem, capture []byte) (uploadCaptureResponse, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(fmt.Sprintf("%s_%s.png", item.GoodsID, item.SKUID)))
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
			"goods_id": item.GoodsID,
			"sku_id":   item.SKUID,
			"size":     len(capture),
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

func (s *Service) buildDetail(taskItem clientTask, currentItem *clientTaskItem, deviceID string, mode string, rawURL string, status string, recognition string, captureURLs []string, message string) rt.DetailRecord {
	imageCount := 0
	captureURL := ""
	if len(captureURLs) > 0 {
		imageCount = len(captureURLs)
		captureURL = captureURLs[len(captureURLs)-1]
	}
	goodsID, skuID := detailItemIDs(taskItem, currentItem)
	return rt.DetailRecord{
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
		Message:         message,
	}
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
	summary, events, details, pending, adapterLogs, systemConfig := s.runtime.Snapshot()
	s.hub.Broadcast(ws.Event{
		Type: "state",
		Data: map[string]any{
			"devices":             s.devices.List(),
			"templates":           s.tpl.List(),
			"details":             details,
			"summary":             summary,
			"event_log":           events,
			"pending_tasks":       pending,
			"adapter_submit_logs": adapterLogs,
			"system_config":       systemConfig,
			"upstream_configs":    s.upstream.List(),
			"platform_accounts":   s.accounts.List(),
			"upstream_options":    buildUpstreamOptions(s.upstream.List()),
		},
	})
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
			s.fillPending(ctx)
		}
	}
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

func (s *Service) fetchTaskForCandidate(ctx context.Context, candidate sourceCandidate) (*clientTask, error) {
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
	if candidate.Account != nil {
		s.accounts.RecordFetch(candidate.Account.ID)
	}
	s.upstream.RecordFetch(candidate.Upstream.Code)
	return &taskItem, nil
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
	s.emitState()
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
	return false
}

func (s *Service) hasBusinessKey(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.groups[key]
	return ok
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
	s.emitState()
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
	s.emitState()
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
	s.emitState()
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
	s.emitState()
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

func (s *Service) runTaskUntilTerminal(ctx context.Context, deviceID string, mode string, item clientTaskItem, cfg rt.SystemConfig) (skuExecutionResult, bool, bool, error) {
	if s.vision.Mode() == "mock" {
		return skuExecutionResult{
			GoodsID:      item.GoodsID,
			SKUID:        item.SKUID,
			Recognition:  "success_image",
			Message:      "视觉 mock 模式命中成功图",
			CaptureBytes: [][]byte{[]byte("mock-capture")},
		}, false, true, nil
	}

	var clickCapture []byte
	for loop := 1; loop <= 20; loop++ {
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.LoopCount = loop
			current.CurrentStage = "capture"
			current.CurrentMessage = fmt.Sprintf("第 %d 轮截图识别", loop)
		})
		s.emitState()
		captureBytes, err := s.captureForMode(ctx, deviceID, mode)
		if err != nil {
			return skuExecutionResult{}, false, false, fmt.Errorf("截图失败: %w", err)
		}
		cache := (*vision.OCRCache)(nil)
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "account_risk"
			current.CurrentMessage = "检测账号风控图"
		})
		s.emitState()
		if matched, result, nextCache, err := s.matchStage("account_risk", captureBytes, cache); err != nil {
			return skuExecutionResult{}, false, false, err
		} else if matched {
			return skuExecutionResult{}, true, false, fmt.Errorf("account_risk:%s", result.MatchedTextOrFallback("命中账号风控"))
		} else {
			cache = nextCache
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "fail_release"
			current.CurrentMessage = "检测失败释放图"
		})
		s.emitState()
		if matched, result, nextCache, err := s.matchStage("fail_release", captureBytes, cache); err != nil {
			return skuExecutionResult{}, false, false, err
		} else if matched {
			return skuExecutionResult{}, false, false, fmt.Errorf("fail_release:%s", result.MatchedTextOrFallback("命中失败释放"))
		} else {
			cache = nextCache
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "click_image"
			current.CurrentMessage = "检测点击图"
		})
		s.emitState()
		if matched, result, nextCache, err := s.matchStage("click_image", captureBytes, cache); err != nil {
			return skuExecutionResult{}, false, false, err
		} else if matched {
			cache = nextCache
			clickCapture = captureBytes
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.LastMatchedTemplate = "click_image"
				current.CurrentStage = "click_action"
				current.CurrentMessage = fmt.Sprintf("命中点击图，执行点击 (%d,%d)", result.Center[0], result.Center[1])
			})
			s.emitState()
			if err := s.devices.Tap(ctx, deviceID, result.Center[0], result.Center[1]); err != nil {
				return skuExecutionResult{}, false, false, fmt.Errorf("点击失败: %w", err)
			}
			sleepWithContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond))
			continue
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "success_image"
			current.CurrentMessage = "检测成功图"
		})
		s.emitState()
		if matched, result, _, err := s.matchStage("success_image", captureBytes, cache); err != nil {
			return skuExecutionResult{}, false, false, err
		} else if matched {
			s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
				current.CurrentStage = "success"
				current.CurrentMessage = result.MatchedTextOrFallback("命中成功图")
			})
			s.emitState()
			captures := [][]byte{captureBytes}
			if len(clickCapture) > 0 {
				captures = [][]byte{clickCapture, captureBytes}
			}
			return skuExecutionResult{
				GoodsID:      item.GoodsID,
				SKUID:        item.SKUID,
				Recognition:  "success_image",
				Message:      result.MatchedTextOrFallback("命中成功图"),
				CaptureBytes: captures,
			}, false, len(clickCapture) > 0, nil
		}
		s.devices.UpdateCurrentTask(deviceID, func(current *device.CurrentTask) {
			current.CurrentStage = "loop_wait"
			current.CurrentMessage = "本轮未命中，等待下一轮"
		})
		s.emitState()
		time.Sleep(time.Second)
	}
	return skuExecutionResult{}, false, false, fmt.Errorf("fail_release:识别超时")
}

func (s *Service) matchStage(stage string, captureBytes []byte, cache *vision.OCRCache) (bool, vision.MatchResult, *vision.OCRCache, error) {
	templates := s.tpl.ListEnabledByType(stage)
	currentCache := cache
	for _, tpl := range templates {
		result, nextCache, err := s.vision.Match(tpl, captureBytes, currentCache)
		if err != nil {
			return false, vision.MatchResult{}, currentCache, err
		}
		currentCache = nextCache
		if result.Found {
			return true, result, currentCache, nil
		}
	}
	return false, vision.MatchResult{}, currentCache, nil
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
	if cfg.UseURLTemplates {
		for _, tpl := range cfg.URLTemplates {
			if strings.TrimSpace(tpl.Template) == "" {
				continue
			}
			return rewriteTemplateURL(tpl.Template, item.GoodsID, item.SKUID)
		}
	}
	return fmt.Sprintf("https://mobile.yangkeduo.com/order_checkout.html?goods_id=%s&sku_id=%s", item.GoodsID, item.SKUID)
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
		"id":                record.ID,
		"timestamp":         record.Timestamp,
		"task_id":           record.TaskID,
		"upstream_task_ref": record.UpstreamTaskRef,
		"task_mode":         record.TaskMode,
		"device_id":         record.DeviceID,
		"goods_id":          record.GoodsID,
		"sku_id":            record.SKUID,
		"url":               record.URL,
		"status":            record.Status,
		"recognition":       record.Recognition,
		"image_count":       record.ImageCount,
		"capture_url":       record.CaptureURL,
		"capture_urls":      record.CaptureURLs,
		"message":           record.Message,
		"template_id":       record.TemplateID,
		"template_label":    record.TemplateLabel,
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
