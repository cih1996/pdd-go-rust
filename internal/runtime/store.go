package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	eventLimit       = 500
	detailLimit      = 2000
	adapterLogLimit  = 500
	pendingTaskLimit = 500
)

type URLTemplateRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Template     string `json:"template"`
	TriggerCount int    `json:"trigger_count"`
	SuccessCount int    `json:"success_count"`
	RiskCount    int    `json:"risk_count"`
}

type SystemConfig struct {
	OpenURLDelaySeconds   float64             `json:"open_url_delay_seconds"`
	ClickImageDelaySecond float64             `json:"click_image_delay_seconds"`
	MaxTaskSKUCount       int                 `json:"max_task_sku_count"`
	ExternalAPIEnabled    bool                `json:"external_api_enabled"`
	UseURLTemplates       bool                `json:"use_url_templates"`
	URLTemplates          []URLTemplateRecord `json:"url_templates"`
}

type EventRecord struct {
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	DeviceID  string         `json:"device_id,omitempty"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload"`
}

type DetailRecord struct {
	ID                string   `json:"id"`
	Timestamp         string   `json:"timestamp"`
	TaskID            string   `json:"task_id"`
	UpstreamTaskRef   string   `json:"upstream_task_ref,omitempty"`
	TaskMode          string   `json:"task_mode"`
	DeviceID          string   `json:"device_id"`
	GoodsID           string   `json:"goods_id,omitempty"`
	SKUID             string   `json:"sku_id,omitempty"`
	URL               string   `json:"url,omitempty"`
	Status            string   `json:"status"`
	Recognition       string   `json:"recognition"`
	ImageCount        int      `json:"image_count"`
	CaptureURL        string   `json:"capture_url,omitempty"`
	CaptureURLs       []string `json:"capture_urls,omitempty"`
	Message           string   `json:"message,omitempty"`
	SubmitStatusCode  int      `json:"submit_status_code,omitempty"`
	SubmitError       string   `json:"submit_error,omitempty"`
	TemplateID        string   `json:"template_id,omitempty"`
	TemplateLabel     string   `json:"template_label,omitempty"`
	RecognitionEngine string   `json:"recognition_engine,omitempty"`
	ADBCommand        string   `json:"adb_command,omitempty"`
}

type PendingTaskRecord struct {
	TaskID          string                  `json:"task_id"`
	UpstreamTaskRef string                  `json:"upstream_task_ref,omitempty"`
	SourceCode      string                  `json:"source_code,omitempty"`
	SourceName      string                  `json:"source_name,omitempty"`
	AccountID       string                  `json:"account_id,omitempty"`
	AccountName     string                  `json:"account_name,omitempty"`
	TaskItems       []PendingTaskItemRecord `json:"task_items,omitempty"`
	ItemCount       int                     `json:"item_count"`
	TotalItemCount  int                     `json:"total_item_count,omitempty"`
	PendingCount    int                     `json:"pending_count,omitempty"`
	ActiveCount     int                     `json:"active_count,omitempty"`
	CompletedCount  int                     `json:"completed_count,omitempty"`
	Status          string                  `json:"status,omitempty"`
	PrefetchedAt    string                  `json:"prefetched_at,omitempty"`
}

type PendingTaskItemRecord struct {
	GoodsID   string `json:"goods_id,omitempty"`
	SKUID     string `json:"sku_id,omitempty"`
	StepIndex int    `json:"step_index,omitempty"`
}

type AdapterSubmitLogRecord struct {
	ID              string `json:"id"`
	Timestamp       string `json:"timestamp"`
	Action          string `json:"action"`
	RequestMethod   string `json:"request_method"`
	Endpoint        string `json:"endpoint"`
	TaskID          string `json:"task_id,omitempty"`
	UpstreamTaskRef string `json:"upstream_task_ref,omitempty"`
	SourceCode      string `json:"source_code,omitempty"`
	DeviceID        string `json:"device_id,omitempty"`
	SubmitType      string `json:"submit_type,omitempty"`
	RequestPayload  any    `json:"request_payload,omitempty"`
	ResponseStatus  int    `json:"response_status,omitempty"`
	ResponsePayload any    `json:"response_payload,omitempty"`
	Error           string `json:"error,omitempty"`
}

type DailyStats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failure int `json:"failure"`
}

type Summary struct {
	Total   int                    `json:"total"`
	Success int                    `json:"success"`
	Failure int                    `json:"failure"`
	Daily   map[string]*DailyStats `json:"daily"`
}

type PersistedData struct {
	Summary          Summary                  `json:"summary"`
	EventLog         []EventRecord            `json:"event_log"`
	Details          []DetailRecord           `json:"details"`
	PendingTasks     []PendingTaskRecord      `json:"pending_tasks"`
	AdapterSubmitLog []AdapterSubmitLogRecord `json:"adapter_submit_log"`
	SystemConfig     SystemConfig             `json:"system_config"`
	SubmitCount      int                      `json:"submit_count"`
}

type Backend interface {
	LoadRuntimeData() (PersistedData, error)
	SaveRuntimeData(PersistedData) error
	SaveSystemConfig(SystemConfig) error
}

type Store struct {
	mu               sync.RWMutex
	eventLog         []EventRecord
	details          []DetailRecord
	pendingTasks     []PendingTaskRecord
	adapterSubmitLog []AdapterSubmitLogRecord
	systemConfig     SystemConfig
	summary          Summary
	submitCount      int
	backend          Backend
}

func NewStore(backend Backend) *Store {
	store := &Store{
		backend: backend,
		systemConfig: SystemConfig{
			OpenURLDelaySeconds:   2,
			ClickImageDelaySecond: 1.2,
			MaxTaskSKUCount:       0,
			UseURLTemplates:       false,
			URLTemplates:          []URLTemplateRecord{},
		},
		eventLog:         []EventRecord{},
		details:          []DetailRecord{},
		pendingTasks:     []PendingTaskRecord{},
		adapterSubmitLog: []AdapterSubmitLogRecord{},
	}
	if backend != nil {
		if data, err := backend.LoadRuntimeData(); err == nil {
			store.summary = data.Summary
			store.eventLog = data.EventLog
			store.details = data.Details
			store.pendingTasks = data.PendingTasks
			store.adapterSubmitLog = data.AdapterSubmitLog
			store.systemConfig = data.SystemConfig
			store.submitCount = data.SubmitCount
		}
	}
	if store.summary.Daily == nil {
		store.summary.Daily = make(map[string]*DailyStats)
	}

	// 尝试从本地已存的 details 中恢复因升级丢失的每日统计
	recoveredDaily := make(map[string]*DailyStats)
	for _, d := range store.details {
		dateStr := d.Timestamp
		if len(dateStr) >= 10 {
			dateStr = dateStr[:10]
		} else {
			continue
		}
		if recoveredDaily[dateStr] == nil {
			recoveredDaily[dateStr] = &DailyStats{}
		}
		recoveredDaily[dateStr].Total++
		switch d.Status {
		case "success":
			recoveredDaily[dateStr].Success++
		case "failure", "cancelled", "account_risk":
			recoveredDaily[dateStr].Failure++
		}
	}
	for dateStr, rec := range recoveredDaily {
		if existing, ok := store.summary.Daily[dateStr]; !ok || existing.Total < rec.Total {
			store.summary.Daily[dateStr] = rec
		}
	}

	return store
}

func (s *Store) Snapshot() (Summary, []EventRecord, []DetailRecord, []PendingTaskRecord, []AdapterSubmitLogRecord, SystemConfig) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary, cloneEvents(s.eventLog), cloneDetails(s.details), clonePending(s.pendingTasks), cloneAdapterLogs(s.adapterSubmitLog), s.systemConfig
}

func (s *Store) SnapshotWithoutDetails() (Summary, []EventRecord, []PendingTaskRecord, []AdapterSubmitLogRecord, SystemConfig) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summary, cloneEvents(s.eventLog), clonePending(s.pendingTasks), cloneAdapterLogs(s.adapterSubmitLog), s.systemConfig
}

func (s *Store) Summary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSummary(s.summary)
}

func (s *Store) Details() []DetailRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneDetails(s.details)
}

func (s *Store) ClearDetails() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.details = []DetailRecord{}
	s.summary = Summary{Daily: make(map[string]*DailyStats)}
	s.persistLocked()
}

func (s *Store) SystemConfig() SystemConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.systemConfig
}

func (s *Store) UpdateSystemConfig(next SystemConfig) SystemConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.systemConfig = next
	s.persistSystemConfigLocked()
	return s.systemConfig
}

func (s *Store) RecordURLTemplateTrigger(templateID string) {
	s.recordURLTemplate(templateID, func(item *URLTemplateRecord) {
		item.TriggerCount++
	})
}

func (s *Store) RecordURLTemplateSuccess(templateID string) {
	s.recordURLTemplate(templateID, func(item *URLTemplateRecord) {
		item.SuccessCount++
	})
}

func (s *Store) RecordURLTemplateRisk(templateID string) {
	s.recordURLTemplate(templateID, func(item *URLTemplateRecord) {
		item.RiskCount++
	})
}

func (s *Store) recordURLTemplate(templateID string, mutate func(item *URLTemplateRecord)) {
	if templateID == "" || mutate == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.systemConfig.URLTemplates {
		if s.systemConfig.URLTemplates[index].ID != templateID {
			continue
		}
		mutate(&s.systemConfig.URLTemplates[index])
		s.persistLocked()
		return
	}
}

func (s *Store) AddEvent(record EventRecord) EventRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.ID == "" {
		record.ID = newID("evt")
	}
	if record.Timestamp == "" {
		record.Timestamp = nowString()
	}
	if record.Payload == nil {
		record.Payload = map[string]any{}
	}
	s.eventLog = append([]EventRecord{record}, s.eventLog...)
	if len(s.eventLog) > eventLimit {
		s.eventLog = s.eventLog[:eventLimit]
	}
	s.persistLocked()
	return record
}

func (s *Store) AddDetail(record DetailRecord) DetailRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.ID == "" {
		record.ID = newID("detail")
	}
	if record.Timestamp == "" {
		record.Timestamp = nowString()
	}
	s.details = append([]DetailRecord{record}, s.details...)
	if len(s.details) > detailLimit {
		s.details = s.details[:detailLimit]
	}
	if s.summary.Daily == nil {
		s.summary.Daily = make(map[string]*DailyStats)
	}
	dateStr := record.Timestamp
	if len(dateStr) >= 10 {
		dateStr = dateStr[:10]
	} else {
		dateStr = time.Now().UTC().Format("2006-01-02")
	}

	daily, ok := s.summary.Daily[dateStr]
	if !ok {
		daily = &DailyStats{}
		s.summary.Daily[dateStr] = daily
	}

	s.summary.Total++
	daily.Total++
	switch record.Status {
	case "success":
		s.summary.Success++
		daily.Success++
	case "failure", "cancelled", "account_risk":
		s.summary.Failure++
		daily.Failure++
	}
	s.persistLocked()
	return record
}

func (s *Store) SetPendingTask(task PendingTaskRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.pendingTasks[:0]
	for _, item := range s.pendingTasks {
		if item.TaskID != task.TaskID {
			filtered = append(filtered, item)
		}
	}
	if task.PrefetchedAt == "" {
		task.PrefetchedAt = nowString()
	}
	filtered = append([]PendingTaskRecord{task}, filtered...)
	if len(filtered) > pendingTaskLimit {
		filtered = filtered[:pendingTaskLimit]
	}
	s.pendingTasks = filtered
	s.persistLocked()
}

func (s *Store) RemovePendingTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.pendingTasks[:0]
	for _, item := range s.pendingTasks {
		if item.TaskID != taskID {
			filtered = append(filtered, item)
		}
	}
	s.pendingTasks = filtered
	s.persistLocked()
}

func (s *Store) AddAdapterSubmitLog(record AdapterSubmitLogRecord) AdapterSubmitLogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record.ID == "" {
		record.ID = newID("adapter")
	}
	if record.Timestamp == "" {
		record.Timestamp = nowString()
	}
	s.adapterSubmitLog = append([]AdapterSubmitLogRecord{record}, s.adapterSubmitLog...)
	if len(s.adapterSubmitLog) > adapterLogLimit {
		s.adapterSubmitLog = s.adapterSubmitLog[:adapterLogLimit]
	}
	s.persistLocked()
	return record
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newID(prefix string) string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}

func cloneEvents(items []EventRecord) []EventRecord {
	result := make([]EventRecord, len(items))
	copy(result, items)
	return result
}

func cloneDetails(items []DetailRecord) []DetailRecord {
	result := make([]DetailRecord, len(items))
	copy(result, items)
	return result
}

func clonePending(items []PendingTaskRecord) []PendingTaskRecord {
	result := make([]PendingTaskRecord, len(items))
	copy(result, items)
	for i := range result {
		if len(items[i].TaskItems) > 0 {
			result[i].TaskItems = make([]PendingTaskItemRecord, len(items[i].TaskItems))
			copy(result[i].TaskItems, items[i].TaskItems)
		}
	}
	return result
}

func cloneAdapterLogs(items []AdapterSubmitLogRecord) []AdapterSubmitLogRecord {
	result := make([]AdapterSubmitLogRecord, len(items))
	copy(result, items)
	return result
}

func (s *Store) persistLocked() {
	if s.backend == nil {
		return
	}
	_ = s.backend.SaveRuntimeData(PersistedData{
		Summary:          s.summary,
		EventLog:         cloneEvents(s.eventLog),
		Details:          cloneDetails(s.details),
		PendingTasks:     clonePending(s.pendingTasks),
		AdapterSubmitLog: cloneAdapterLogs(s.adapterSubmitLog),
		SystemConfig:     cloneSystemConfig(s.systemConfig),
		SubmitCount:      s.submitCount,
	})
}

func (s *Store) persistSystemConfigLocked() {
	if s.backend == nil {
		return
	}
	_ = s.backend.SaveSystemConfig(cloneSystemConfig(s.systemConfig))
}

func cloneSystemConfig(value SystemConfig) SystemConfig {
	result := value
	if len(value.URLTemplates) > 0 {
		result.URLTemplates = make([]URLTemplateRecord, len(value.URLTemplates))
		copy(result.URLTemplates, value.URLTemplates)
	}
	return result
}

func cloneSummary(value Summary) Summary {
	result := value
	if value.Daily != nil {
		result.Daily = make(map[string]*DailyStats, len(value.Daily))
		for key, item := range value.Daily {
			if item == nil {
				result.Daily[key] = nil
				continue
			}
			copyItem := *item
			result.Daily[key] = &copyItem
		}
	}
	return result
}

func (s *Store) SubmitCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.submitCount
}

func (s *Store) IncrementSubmitCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitCount++
	s.persistLocked()
}

func (s *Store) ResetSubmitCount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.submitCount = 0
	s.persistLocked()
}
