package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"unified-server/internal/config"
	"unified-server/internal/device"
	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
	"unified-server/internal/vision"
	"unified-server/internal/ws"
)

type debugWSRequest struct {
	RequestID string `json:"request_id"`
	DeviceID  string `json:"device_id"`
	Mode      string `json:"mode"`
	URL       string `json:"url"`
}

type debugCommandHandler struct {
	hub     *ws.Hub
	devices *device.Service
	runtime *rt.Store
	vision  *vision.Engine
	tpl     *template.Store
	config  config.Config

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newDebugCommandHandler(hub *ws.Hub, cfg config.Config, devices *device.Service, runtimeStore *rt.Store, visionEngine *vision.Engine, tpl *template.Store) *debugCommandHandler {
	return &debugCommandHandler{
		hub:     hub,
		devices: devices,
		runtime: runtimeStore,
		vision:  visionEngine,
		tpl:     tpl,
		config:  cfg,
		running: map[string]context.CancelFunc{},
	}
}

func (h *debugCommandHandler) Handle(clientID string, message ws.IncomingMessage) {
	switch message.Type {
	case "debug_run":
		var payload debugWSRequest
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			h.sendError(clientID, "", "invalid debug_run payload")
			return
		}
		h.startDebug(clientID, payload)
	case "debug_cancel":
		var payload struct {
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(message.Data, &payload); err != nil {
			return
		}
		h.cancel(payload.RequestID)
	}
}

func (h *debugCommandHandler) startDebug(clientID string, payload debugWSRequest) {
	payload.RequestID = strings.TrimSpace(payload.RequestID)
	payload.DeviceID = strings.TrimSpace(payload.DeviceID)
	payload.Mode = strings.TrimSpace(payload.Mode)
	payload.URL = strings.TrimSpace(payload.URL)

	if payload.RequestID == "" {
		h.sendError(clientID, "", "request_id is required")
		return
	}
	if payload.DeviceID == "" {
		h.sendError(clientID, payload.RequestID, "device_id is required")
		return
	}
	if payload.Mode == "" {
		payload.Mode = "current"
	}
	if payload.Mode != "url" && payload.Mode != "current" {
		h.sendError(clientID, payload.RequestID, "mode must be url or current")
		return
	}
	if payload.Mode == "url" && payload.URL == "" {
		h.sendError(clientID, payload.RequestID, "url is required when mode=url")
		return
	}
	if err := validateDebugTemplates(h.tpl); err != nil {
		h.sendError(clientID, payload.RequestID, err.Error())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.register(payload.RequestID, cancel)
	go h.runDebug(ctx, clientID, payload)
}

func (h *debugCommandHandler) runDebug(ctx context.Context, clientID string, payload debugWSRequest) {
	defer h.unregister(payload.RequestID)

	cfg := h.runtime.SystemConfig()
	taskID := fmt.Sprintf("debug_%d", time.Now().UTC().UnixNano())
	debugResults := make([]map[string]any, 0)
	captureSteps := make([]debugCaptureStep, 0, 5)
	captureURLs := make([]string, 0, 5)
	openURLElapsedMS := any(nil)
	totalStart := time.Now()
	finalRecognition := "no_match"
	finalStatus := "failure"
	finalMessage := "5 轮内未命中任何终态模板"
	finalTemplateID := ""
	finalTemplateLabel := ""
	matched := false
	shouldStop := false

	h.send(clientID, "debug_run_started", map[string]any{
		"request_id": payload.RequestID,
		"task_id":    taskID,
		"device_id":  payload.DeviceID,
		"mode":       payload.Mode,
		"url":        payload.URL,
		"max_loops":  5,
	})

	if payload.Mode == "url" {
		openStart := time.Now()
		if err := h.devices.OpenURL(ctx, payload.DeviceID, payload.URL); err != nil {
			h.sendError(clientID, payload.RequestID, "打开链接失败: "+err.Error())
			return
		}
		openURLElapsedMS = elapsedMillis(openStart)
		h.send(clientID, "debug_run_url_opened", map[string]any{
			"request_id":       payload.RequestID,
			"elapsed_ms":       openURLElapsedMS,
			"open_url_seconds": cfg.OpenURLDelaySeconds,
		})
		if !sleepContext(ctx, durationFromSeconds(cfg.OpenURLDelaySeconds)) {
			h.send(clientID, "debug_run_cancelled", map[string]any{"request_id": payload.RequestID})
			return
		}
	}

	for loop := 1; loop <= 5; loop++ {
		h.send(clientID, "debug_run_loop_started", map[string]any{
			"request_id": payload.RequestID,
			"loop_count": loop,
		})

		captureStart := time.Now()
		captureBytes, err := h.devices.Capture(ctx, payload.DeviceID)
		if err != nil {
			h.sendError(clientID, payload.RequestID, "截图失败: "+err.Error())
			return
		}
		captureElapsed := elapsedMillis(captureStart)
		captureSteps = append(captureSteps, debugCaptureStep{LoopCount: loop, ElapsedMS: captureElapsed})

		captureURL, err := saveDebugCapture(captureBytes)
		if err != nil {
			h.sendError(clientID, payload.RequestID, "保存截图失败: "+err.Error())
			return
		}
		captureURLs = append(captureURLs, captureURL)
		h.send(clientID, "debug_run_capture", map[string]any{
			"request_id":  payload.RequestID,
			"loop_count":  loop,
			"elapsed_ms":  captureElapsed,
			"capture_url": captureURL,
		})

		cache := (*vision.OCRCache)(nil)
		for _, stage := range []string{"account_risk", "fail_release", "click_image", "success_image"} {
			stageName := stageDisplayName(stage)
			templates := h.tpl.ListEnabledByType(stage)
			h.send(clientID, "debug_run_stage_started", map[string]any{
				"request_id":     payload.RequestID,
				"loop_count":     loop,
				"stage_key":      stage,
				"stage_name":     stageName,
				"template_count": len(templates),
				"capture_url":    captureURL,
			})

			for _, item := range templates {
				select {
				case <-ctx.Done():
					h.send(clientID, "debug_run_cancelled", map[string]any{"request_id": payload.RequestID})
					return
				default:
				}

				requestStart := time.Now()
				match, nextCache, err := h.vision.Match(item, captureBytes, cache)
				requestElapsed := elapsedMillis(requestStart)
				if err != nil {
					h.sendError(clientID, payload.RequestID, fmt.Sprintf("模板识别失败[%s/%s]: %s", stage, item.Label, err.Error()))
					return
				}
				cache = nextCache
				debugItem := buildDebugTemplateResult(loop, stageName, item, match, requestElapsed)
				debugResults = append(debugResults, debugItem)
				h.send(clientID, "debug_run_template_result", map[string]any{
					"request_id":          payload.RequestID,
					"loop_count":          loop,
					"stage_key":           stage,
					"stage_name":          stageName,
					"template_id":         item.ID,
					"template_label":      item.Label,
					"template_type":       item.TemplateType,
					"recognition_engine":  item.RecognitionEngine,
					"found":               match.Found,
					"elapsed_ms":          match.ElapsedMS,
					"request_elapsed_ms":  requestElapsed,
					"confidence":          match.Confidence,
					"ocr_used_cache":      match.OCRUsedCache,
					"ocr_executed":        match.OCRExecuted,
					"ocr_exec_elapsed_ms": match.OCRExecElapsedMS,
					"matched_text":        nullableString(match.MatchedText),
				})

				if !match.Found {
					continue
				}
				matched = true
				finalRecognition = stage
				finalTemplateID = item.ID
				finalTemplateLabel = item.Label
				finalMessage = match.MatchedTextOrFallback(item.Label)

				switch stage {
				case "account_risk":
					finalStatus = "failure"
					shouldStop = true
					h.finish(clientID, payload, taskID, matched, shouldStop, finalStatus, finalRecognition, finalMessage, finalTemplateID, finalTemplateLabel, captureURLs, debugResults, captureSteps, openURLElapsedMS, totalStart)
					return
				case "fail_release":
					finalStatus = "failure"
					h.finish(clientID, payload, taskID, matched, shouldStop, finalStatus, finalRecognition, finalMessage, finalTemplateID, finalTemplateLabel, captureURLs, debugResults, captureSteps, openURLElapsedMS, totalStart)
					return
				case "click_image":
					clickStart := time.Now()
					if err := h.devices.Tap(ctx, payload.DeviceID, match.Center[0], match.Center[1]); err != nil {
						h.sendError(clientID, payload.RequestID, "点击失败: "+err.Error())
						return
					}
					h.send(clientID, "debug_run_click_performed", map[string]any{
						"request_id":  payload.RequestID,
						"loop_count":  loop,
						"template_id": item.ID,
						"elapsed_ms":  elapsedMillis(clickStart),
						"center":      nullablePoint(match.Center),
					})
					if !sleepContext(ctx, durationFromSeconds(cfg.ClickImageDelaySecond)) {
						h.send(clientID, "debug_run_cancelled", map[string]any{"request_id": payload.RequestID})
						return
					}
					goto nextLoop
				case "success_image":
					finalStatus = "success"
					h.finish(clientID, payload, taskID, matched, shouldStop, finalStatus, finalRecognition, finalMessage, finalTemplateID, finalTemplateLabel, captureURLs, debugResults, captureSteps, openURLElapsedMS, totalStart)
					return
				}
			}
		}
	nextLoop:
		continue
	}

	h.finish(clientID, payload, taskID, matched, shouldStop, finalStatus, finalRecognition, finalMessage, finalTemplateID, finalTemplateLabel, captureURLs, debugResults, captureSteps, openURLElapsedMS, totalStart)
}

func (h *debugCommandHandler) finish(clientID string, payload debugWSRequest, taskID string, matched bool, shouldStop bool, finalStatus string, finalRecognition string, finalMessage string, finalTemplateID string, finalTemplateLabel string, captureURLs []string, debugResults []map[string]any, captureSteps []debugCaptureStep, openURLElapsedMS any, totalStart time.Time) {
	detail := rt.DetailRecord{
		TaskID:        taskID,
		TaskMode:      "debug",
		DeviceID:      payload.DeviceID,
		URL:           payload.URL,
		Status:        finalStatus,
		Recognition:   finalRecognition,
		ImageCount:    len(captureURLs),
		CaptureURL:    lastString(captureURLs),
		CaptureURLs:   captureURLs,
		Message:       finalMessage,
		TemplateID:    finalTemplateID,
		TemplateLabel: finalTemplateLabel,
	}
	h.send(clientID, "debug_run_finished", map[string]any{
		"request_id": payload.RequestID,
		"result": map[string]any{
			"task_id":        taskID,
			"matched":        matched,
			"should_stop":    shouldStop,
			"detail":         detail,
			"opencv_results": debugResults,
			"timing": map[string]any{
				"total_elapsed_ms":    elapsedMillis(totalStart),
				"open_url_elapsed_ms": openURLElapsedMS,
				"capture_steps":       captureSteps,
			},
		},
	})
}

func (h *debugCommandHandler) send(clientID string, eventType string, data map[string]any) {
	h.hub.SendTo(clientID, ws.Event{Type: eventType, Data: data})
}

func (h *debugCommandHandler) sendError(clientID string, requestID string, message string) {
	h.send(clientID, "debug_run_error", map[string]any{
		"request_id": requestID,
		"message":    message,
	})
}

func (h *debugCommandHandler) register(requestID string, cancel context.CancelFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing := h.running[requestID]; existing != nil {
		existing()
	}
	h.running[requestID] = cancel
}

func (h *debugCommandHandler) unregister(requestID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.running, requestID)
}

func (h *debugCommandHandler) cancel(requestID string) {
	h.mu.Lock()
	cancel := h.running[requestID]
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
