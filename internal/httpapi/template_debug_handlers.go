package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"unified-server/internal/template"
)

func (d RouterDeps) handleTemplateByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("templateId")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing template id"})
		return
	}
	switch r.Method {
	case http.MethodPut:
		input, err := parseTemplateInput(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		item, ok, err := d.Tpl.Update(id, input)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
			return
		}
		writeJSON(w, http.StatusOK, item)
	case http.MethodDelete:
		if !d.Tpl.Delete(id) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": "template deleted"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (d RouterDeps) handleMoveTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	id := r.PathValue("templateId")
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid form"})
		return
	}
	writeJSON(w, http.StatusOK, d.Tpl.Move(id, r.FormValue("direction")))
}

func (d RouterDeps) handleTestTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	id := r.PathValue("templateId")
	record, ok := d.Tpl.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "template not found"})
		return
	}
	imageBytes, captureURL, err := d.debugSourceBytes(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	result, _, err := d.Vision.Match(record, imageBytes, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"template":            record,
		"match":               result,
		"capture_url":         captureURL,
		"recognition_engine":  record.RecognitionEngine,
		"ocr_result": map[string]any{
			"matched_text":   result.MatchedText,
			"full_text":      result.FullText,
			"results":        result.OCRResults,
			"expected_tokens": strings.Split(strings.TrimSpace(record.ExpectedText), "&"),
		},
	})
}

func (d RouterDeps) handleDebugCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid payload"})
		return
	}
	bytes, err := d.Devices.Capture(r.Context(), payload.DeviceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	captureURL, err := saveDebugCapture(bytes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device_id": payload.DeviceID, "capture_url": captureURL})
}

func (d RouterDeps) handleDebugMatchSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid form"})
		return
	}
	sourceBytes, err := readMultipartFile(r, "source_image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	templateBytes, err := readMultipartFile(r, "template_image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	tempDir := filepath.Join(".runtime", "debug")
	_ = os.MkdirAll(tempDir, 0o755)
	templateName := fmt.Sprintf("selection_%d.png", time.Now().UnixNano())
	templatePath := filepath.Join(tempDir, templateName)
	if err := os.WriteFile(templatePath, templateBytes, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	record := template.Record{
		ID:                "debug-selection",
		Label:             "Debug Selection",
		TemplateType:      "click_image",
		RecognitionEngine: "opencv",
		Threshold:         parseFloatDefault(r.FormValue("threshold"), 0.8),
		Method:            defaultString(r.FormValue("method"), "ccoeff_normed"),
		Grayscale:         r.FormValue("grayscale") == "true",
		ImageName:         templateName,
		ImagePath:         templatePath,
		Crop:              parseCrop(r),
	}
	result, _, err := d.Vision.Match(record, sourceBytes, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recognition_engine": "opencv",
		"match":              result,
		"search_crop":        record.Crop,
	})
}

func (d RouterDeps) handleDebugOCRSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid form"})
		return
	}
	sourceBytes, err := readMultipartFile(r, "source_image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	record := template.Record{
		ID:                "debug-ocr",
		Label:             "Debug OCR",
		TemplateType:      "success_image",
		RecognitionEngine: "ocr",
		Threshold:         parseFloatDefault(r.FormValue("threshold"), 0.8),
		Method:            "ocr",
		ExpectedText:      strings.TrimSpace(r.FormValue("expected_text")),
		Crop:              parseCrop(r),
	}
	result, _, err := d.Vision.Match(record, sourceBytes, nil)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recognition_engine": "ocr",
		"match":              result,
		"search_crop":        record.Crop,
		"ocr_result": map[string]any{
			"matched_text":   result.MatchedText,
			"full_text":      result.FullText,
			"results":        result.OCRResults,
			"expected_tokens": strings.Split(record.ExpectedText, "&"),
		},
	})
}

func parseTemplateInput(r *http.Request) (template.UpsertInput, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return template.UpsertInput{}, fmt.Errorf("invalid multipart form")
	}
	input := template.UpsertInput{
		Label:             r.FormValue("label"),
		TemplateType:      r.FormValue("template_type"),
		RecognitionEngine: r.FormValue("recognition_engine"),
		Priority:          parseIntDefault(r.FormValue("priority"), 100),
		ExpectedText:      r.FormValue("expected_text"),
		Threshold:         parseFloatDefault(r.FormValue("threshold"), 0.8),
		Method:            defaultString(r.FormValue("method"), "ccoeff_normed"),
		Grayscale:         r.FormValue("grayscale") == "true",
		Crop:              parseCrop(r),
		Enabled:           true,
	}
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		bytes, readErr := io.ReadAll(file)
		if readErr != nil {
			return template.UpsertInput{}, readErr
		}
		input.ImageName = header.Filename
		input.ImageBytes = bytes
	}
	return input, nil
}

func parseCrop(r *http.Request) *template.CropRegion {
	width := parseIntDefault(r.FormValue("crop_width"), 0)
	height := parseIntDefault(r.FormValue("crop_height"), 0)
	if width <= 0 || height <= 0 {
		return nil
	}
	return &template.CropRegion{
		X:      parseIntDefault(r.FormValue("crop_x"), 0),
		Y:      parseIntDefault(r.FormValue("crop_y"), 0),
		Width:  width,
		Height: height,
	}
}

func readMultipartFile(r *http.Request, key string) ([]byte, error) {
	file, _, err := r.FormFile(key)
	if err != nil {
		return nil, fmt.Errorf("missing %s", key)
	}
	defer file.Close()
	return io.ReadAll(file)
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseFloatDefault(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func saveDebugCapture(data []byte) (string, error) {
	dir := filepath.Join(".runtime", "debug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("debug_%d.png", time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return "/api/assets/debug/" + name, nil
}

func (d RouterDeps) debugSourceBytes(r *http.Request) ([]byte, string, error) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, "", fmt.Errorf("invalid multipart form")
	}
	if file, _, err := r.FormFile("source_image"); err == nil {
		defer file.Close()
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			return nil, "", readErr
		}
		url, err := saveDebugCapture(data)
		return data, url, err
	}
	deviceID := strings.TrimSpace(r.FormValue("device_id"))
	if deviceID == "" {
		return nil, "", fmt.Errorf("missing source_image or device_id")
	}
	data, err := d.Devices.Capture(r.Context(), deviceID)
	if err != nil {
		return nil, "", err
	}
	url, err := saveDebugCapture(data)
	return data, url, err
}

func normalizePNG(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}
