package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
	"unified-server/internal/vision"
)

type debugRunRequest struct {
	DeviceID string `json:"device_id"`
	Mode     string `json:"mode"`
	URL      string `json:"url"`
}

type debugCaptureStep struct {
	LoopCount int     `json:"loop_count"`
	ElapsedMS float64 `json:"elapsed_ms"`
}

func (d RouterDeps) handleDebugRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var payload debugRunRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid debug run payload"})
		return
	}
	if payload.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_id is required"})
		return
	}
	if payload.Mode == "" {
		payload.Mode = "current"
	}
	if payload.Mode == "url" && payload.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "url is required when mode=url"})
		return
	}
	if payload.Mode != "url" && payload.Mode != "current" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "mode must be url or current"})
		return
	}
	if err := validateDebugTemplates(d.Tpl); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	result, err := d.runSingleDebug(r, payload)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (d RouterDeps) runSingleDebug(r *http.Request, payload debugRunRequest) (map[string]any, error) {
	ctx := r.Context()
	cfg := d.Runtime.SystemConfig()
	taskID := fmt.Sprintf("debug_%d", time.Now().UTC().UnixNano())
	debugResults := make([]map[string]any, 0)
	captureSteps := make([]debugCaptureStep, 0, 5)
	captureURLs := make([]string, 0, 5)
	shouldStop := false
	matched := false
	clickTriggered := false
	matchedOnceTemplates := make(map[string]struct{})
	finalRecognition := "no_match"
	finalMessage := "5 轮内未命中任何终态模板"
	finalStatus := "failure"
	finalTemplateID := ""
	finalTemplateLabel := ""
	openURLElapsedMS := any(nil)
	totalStart := time.Now()

	if payload.Mode == "url" {
		openStart := time.Now()
		if err := d.Devices.OpenURL(ctx, payload.DeviceID, payload.URL); err != nil {
			return nil, fmt.Errorf("打开链接失败: %w", err)
		}
		openURLElapsedMS = elapsedMillis(openStart)
		time.Sleep(durationFromSeconds(cfg.OpenURLDelaySeconds))
	}

	for loop := 1; loop <= 5; loop++ {
		captureStart := time.Now()
		captureBytes, err := d.Devices.Capture(ctx, payload.DeviceID)
		if err != nil {
			return nil, fmt.Errorf("截图失败: %w", err)
		}
		captureSteps = append(captureSteps, debugCaptureStep{
			LoopCount: loop,
			ElapsedMS: elapsedMillis(captureStart),
		})

		captureURL, err := saveDebugCapture(d.Config.DebugAssetDir, captureBytes)
		if err != nil {
			return nil, fmt.Errorf("保存截图失败: %w", err)
		}
		captureURLs = append(captureURLs, captureURL)

		cache := (*vision.OCRCache)(nil)
		stageMatched := false
		for _, stage := range []string{"account_risk", "fail_release", "click_image", "success_image"} {
			stageName := stageDisplayName(stage)
			templates := filterDebugTemplates(d.Tpl.ListEnabledByType(stage), stage, clickTriggered, matchedOnceTemplates)
			for _, item := range templates {
				requestStart := time.Now()
				match, nextCache, err := d.Vision.Match(item, captureBytes, cache)
				requestElapsed := elapsedMillis(requestStart)
				if err != nil {
					return nil, fmt.Errorf("模板识别失败[%s/%s]: %w", stage, item.Label, err)
				}
				cache = nextCache

				debugResults = append(debugResults, buildDebugTemplateResult(loop, stageName, item, match, requestElapsed))
				if !match.Found {
					continue
				}
				rememberDebugMatchedTemplate(matchedOnceTemplates, item)

				stageMatched = true
				matched = true
				finalTemplateID = item.ID
				finalTemplateLabel = item.Label
				finalRecognition = stage
				finalMessage = match.MatchedTextOrFallback(item.Label)

				switch stage {
				case "account_risk":
					shouldStop = true
					finalStatus = "failure"
				case "fail_release":
					finalStatus = "failure"
				case "click_image":
					clickTriggered = true
					if err := d.Devices.Tap(ctx, payload.DeviceID, match.Center[0], match.Center[1]); err != nil {
						return nil, fmt.Errorf("点击失败: %w", err)
					}
					time.Sleep(durationFromSeconds(cfg.ClickImageDelaySecond))
					goto nextLoop
				case "success_image":
					finalStatus = "success"
				}

				detail := rt.DetailRecord{
					TaskID:        taskID,
					TaskMode:      "debug",
					DeviceID:      payload.DeviceID,
					URL:           payload.URL,
					Status:        finalStatus,
					Recognition:   finalRecognition,
					ImageCount:    len(captureURLs),
					CaptureURL:    captureURLs[len(captureURLs)-1],
					CaptureURLs:   captureURLs,
					Message:       finalMessage,
					TemplateID:    finalTemplateID,
					TemplateLabel: finalTemplateLabel,
				}
				return map[string]any{
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
				}, nil
			}
			if stageMatched {
				break
			}
		}

	nextLoop:
		continue
	}

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
	return map[string]any{
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
	}, nil
}

func buildDebugTemplateResult(loop int, stageName string, item template.Record, match vision.MatchResult, requestElapsed float64) map[string]any {
	ocrResult := any(nil)
	if item.RecognitionEngine == "ocr" {
		results := make([]map[string]any, 0, len(match.OCRResults))
		candidateTexts := make([]string, 0, len(match.OCRResults))
		for _, candidate := range match.OCRResults {
			results = append(results, map[string]any{
				"text":         candidate.Text,
				"confidence":   candidate.Confidence,
				"bounding_box": candidate.Box,
			})
			candidateTexts = append(candidateTexts, candidate.Text)
		}
		ocrResult = map[string]any{
			"matched_text":        match.MatchedText,
			"full_text":           match.FullText,
			"expected_tokens":     splitExpectedText(item.ExpectedText),
			"results":             results,
			"used_cache":          match.OCRUsedCache,
			"executed":            match.OCRExecuted,
			"ocr_exec_elapsed_ms": match.OCRExecElapsedMS,
		}
		_ = candidateTexts
	}

	return map[string]any{
		"template_id":        item.ID,
		"template_label":     item.Label,
		"template_type":      item.TemplateType,
		"recognition_engine": item.RecognitionEngine,
		"loop_count":         loop,
		"stage_name":         stageName,
		"request_elapsed_ms": requestElapsed,
		"ocr_result":         ocrResult,
		"match": map[string]any{
			"found":               match.Found,
			"confidence":          match.Confidence,
			"elapsed_ms":          match.ElapsedMS,
			"threshold":           item.Threshold,
			"method":              methodOrOCR(item.Method, item.RecognitionEngine),
			"top_left":            nullablePoint(match.TopLeft),
			"center":              nullablePoint(match.Center),
			"width":               nullableInt(match.Width),
			"height":              nullableInt(match.Height),
			"search_region":       cropToSearchRegion(item.Crop),
			"matched_text":        nullableString(match.MatchedText),
			"full_text":           nullableString(match.FullText),
			"candidate_texts":     extractCandidateTexts(match.OCRResults),
			"ocr_used_cache":      match.OCRUsedCache,
			"ocr_executed":        match.OCRExecuted,
			"ocr_exec_elapsed_ms": match.OCRExecElapsedMS,
		},
	}
}

func stageDisplayName(stage string) string {
	switch stage {
	case "account_risk":
		return "账号风控"
	case "fail_release":
		return "失败释放"
	case "click_image":
		return "点击图"
	case "success_image":
		return "成功图"
	default:
		return stage
	}
}

func splitExpectedText(value string) []string {
	if value == "" {
		return []string{}
	}
	result := make([]string, 0)
	for _, item := range strings.Split(value, "&") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func cropToSearchRegion(crop *template.CropRegion) any {
	if crop == nil || crop.Width <= 0 || crop.Height <= 0 {
		return nil
	}
	return []int{crop.X, crop.Y, crop.Width, crop.Height}
}

func extractCandidateTexts(items []vision.OCRResult) []string {
	if len(items) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Text)
	}
	return result
}

func methodOrOCR(method string, engine string) string {
	if engine == "ocr" {
		return "ocr"
	}
	if method == "" {
		return "ccoeff_normed"
	}
	return method
}

func nullablePoint(point [2]int) any {
	if point[0] == 0 && point[1] == 0 {
		return nil
	}
	return point
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func lastString(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}

func elapsedMillis(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000.0
}

func durationFromSeconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

func validateDebugTemplates(store *template.Store) error {
	for _, item := range store.List() {
		if !item.Enabled || item.RecognitionEngine != "opencv" {
			continue
		}
		if strings.TrimSpace(item.ImagePath) == "" {
			return fmt.Errorf("OpenCV 模板缺少有效小图文件，请重新上传模板图片: %s", item.Label)
		}
	}
	return nil
}

func filterDebugTemplates(items []template.Record, stage string, clickTriggered bool, matchedOnceTemplates map[string]struct{}) []template.Record {
	filtered := make([]template.Record, 0, len(items))
	for _, item := range items {
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

func rememberDebugMatchedTemplate(matchedOnceTemplates map[string]struct{}, item template.Record) {
	if !item.MatchOncePerTask || matchedOnceTemplates == nil {
		return
	}
	matchedOnceTemplates[item.ID] = struct{}{}
}
