package vision

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"unified-server/internal/config"
	"unified-server/internal/template"
)

type Engine struct {
	cfg    config.Config
	tpl    *template.Store
	client *http.Client
}

const maxOpenCVBatchSize = 10

type OCRResult struct {
	Text       string   `json:"text"`
	Confidence float64  `json:"confidence"`
	Box        [][2]int `json:"box,omitempty"`
}

type ocrExpectedCondition struct {
	Text    string
	Negated bool
}

type MatchResult struct {
	Found            bool        `json:"found"`
	Method           string      `json:"method"`
	Confidence       float64     `json:"confidence"`
	Threshold        float64     `json:"threshold"`
	Center           [2]int      `json:"center,omitempty"`
	TopLeft          [2]int      `json:"top_left,omitempty"`
	Width            int         `json:"width,omitempty"`
	Height           int         `json:"height,omitempty"`
	OCRResults       []OCRResult `json:"ocr_results,omitempty"`
	MatchedText      string      `json:"matched_text,omitempty"`
	FullText         string      `json:"full_text,omitempty"`
	ElapsedMS        float64     `json:"elapsed_ms"`
	OCRUsedCache     bool        `json:"ocr_used_cache"`
	OCRExecuted      bool        `json:"ocr_executed"`
	OCRExecElapsedMS float64     `json:"ocr_exec_elapsed_ms"`
}

func (m MatchResult) MatchedTextOrFallback(fallback string) string {
	if strings.TrimSpace(m.MatchedText) != "" {
		return strings.TrimSpace(m.MatchedText)
	}
	if strings.TrimSpace(m.FullText) != "" {
		return strings.TrimSpace(m.FullText)
	}
	return fallback
}

type Capability struct {
	GoCVImported    bool   `json:"gocv_imported"`
	OpenCVInstalled bool   `json:"opencv_installed"`
	OCRInstalled    bool   `json:"ocr_installed"`
	Mode            string `json:"mode"`
	Reason          string `json:"reason,omitempty"`
}

func NewEngine(cfg config.Config, tpl *template.Store) *Engine {
	return &Engine{
		cfg: cfg,
		tpl: tpl,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (e *Engine) Mode() string {
	if e.cfg.EnableVisionMock {
		return "mock"
	}
	return "native"
}

func (e *Engine) HasOCRTemplates() bool {
	return e.tpl.CountByEngine("ocr") > 0
}

func (e *Engine) Capability() Capability {
	capability := Capability{
		GoCVImported: false,
		Mode:         e.Mode(),
		Reason:       "OpenCV / OCR 通过独立 HTTP 服务提供",
	}
	if output, err := e.fetchOpenCVHealth(); err == nil {
		capability.OpenCVInstalled = true
		if text := strings.TrimSpace(output); text != "" {
			capability.Reason = text
		}
	}
	if _, err := e.fetchOCRHealth(); err == nil {
		capability.OCRInstalled = true
	}
	return capability
}

func (e *Engine) Plan() map[string]any {
	capability := e.Capability()
	return map[string]any{
		"opencv":          "target is provided by external HTTP service",
		"opencv_base_url": e.cfg.OpenCVBaseURL,
		"ocr":             "target is provided by external HTTP service",
		"ocr_base_url":    e.cfg.OCRBaseURL,
		"loop_ocr_policy": "single loop shared ocr result cache",
		"transport":       "opencv over HTTP, OCR over HTTP",
		"capability":      capability,
	}
}

type OCRCache struct {
	Results          []OCRResult
	FullText         string
	OpenCVSourceID   string
	OpenCVSourceHash string
}

func (e *Engine) Match(record template.Record, imageBytes []byte, cache *OCRCache) (MatchResult, *OCRCache, error) {
	start := time.Now()
	if record.RecognitionEngine == "ocr" {
		cache = ensureVisionCache(cache)
		usedCache := len(cache.Results) > 0 || strings.TrimSpace(cache.FullText) != ""
		ocrExecuted := false
		ocrExecElapsedMS := 0.0
		if !usedCache {
			ocrStart := time.Now()
			results, fullText, err := e.runOCR(imageBytes)
			if err != nil {
				return MatchResult{}, nil, err
			}
			cache.Results = results
			cache.FullText = fullText
			ocrExecuted = true
			ocrExecElapsedMS = elapsedMillis(ocrStart)
		}
		result := matchOCR(record, cache)
		result.ElapsedMS = elapsedMillis(start)
		result.OCRUsedCache = usedCache
		result.OCRExecuted = ocrExecuted
		result.OCRExecElapsedMS = ocrExecElapsedMS
		return result, cache, nil
	}
	result, cache, err := e.runOpenCV(record, imageBytes, cache)
	result.ElapsedMS = elapsedMillis(start)
	return result, cache, err
}

func (e *Engine) MatchOpenCVBatch(records []template.Record, imageBytes []byte, cache *OCRCache) (int, MatchResult, *OCRCache, error) {
	start := time.Now()
	matchedIndex, result, nextCache, err := e.runOpenCVBatch(records, imageBytes, cache)
	result.ElapsedMS = elapsedMillis(start)
	return matchedIndex, result, nextCache, err
}

func (e *Engine) runOpenCV(record template.Record, imageBytes []byte, cache *OCRCache) (MatchResult, *OCRCache, error) {
	_, result, nextCache, err := e.runOpenCVBatch([]template.Record{record}, imageBytes, cache)
	return result, nextCache, err
}

func (e *Engine) runOpenCVBatch(records []template.Record, imageBytes []byte, cache *OCRCache) (int, MatchResult, *OCRCache, error) {
	if len(records) == 0 {
		return -1, MatchResult{}, cache, nil
	}
	validRecords := make([]template.Record, 0, len(records))
	indexMap := make([]int, 0, len(records))
	for index, record := range records {
		if record.RecognitionEngine != "opencv" {
			return -1, MatchResult{}, cache, fmt.Errorf("batch contains non-opencv template %s", record.Label)
		}
		if strings.TrimSpace(record.ImagePath) == "" {
			continue
		}
		validRecords = append(validRecords, record)
		indexMap = append(indexMap, index)
	}
	if len(validRecords) == 0 {
		return -1, MatchResult{
			Found:     false,
			Method:    records[0].Method,
			Threshold: records[0].Threshold,
		}, cache, nil
	}

	nextCache, err := e.ensureOpenCVSource(imageBytes, cache)
	if err != nil {
		return -1, MatchResult{}, cache, err
	}

	baseIndex := 0
	for baseIndex < len(validRecords) {
		endIndex := baseIndex + maxOpenCVBatchSize
		if endIndex > len(validRecords) {
			endIndex = len(validRecords)
		}
		chunk := validRecords[baseIndex:endIndex]
		response, sourceMissing, err := e.requestOpenCVBatch(nextCache.OpenCVSourceID, chunk)
		if err != nil && sourceMissing {
			nextCache.OpenCVSourceID = ""
			nextCache.OpenCVSourceHash = ""
			nextCache, ensureErr := e.ensureOpenCVSource(imageBytes, nextCache)
			if ensureErr != nil {
				return -1, MatchResult{}, cache, ensureErr
			}
			response, _, err = e.requestOpenCVBatch(nextCache.OpenCVSourceID, chunk)
		}
		if err != nil {
			return -1, MatchResult{}, cache, err
		}
		if len(response.Results) == 0 {
			return -1, MatchResult{}, nextCache, fmt.Errorf("opencv batch match returned no results")
		}

		selected := response.Results[len(response.Results)-1]
		if response.MatchedIndex >= 0 && response.MatchedIndex < len(response.Results) {
			selected = response.Results[response.MatchedIndex]
		}
		result := selected.toMatchResult()
		if result.Found {
			return indexMap[baseIndex+response.MatchedIndex], result, nextCache, nil
		}
		if baseIndex == 0 {
			result.Method = chunk[0].Method
			result.Threshold = chunk[0].Threshold
		}
		baseIndex = endIndex
	}
	return -1, MatchResult{
		Found:     false,
		Method:    validRecords[0].Method,
		Threshold: validRecords[0].Threshold,
	}, nextCache, nil
}

func (e *Engine) runOCR(imageBytes []byte) ([]OCRResult, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "capture.png")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(imageBytes); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(e.cfg.OCRBaseURL, "/")+"/ocr", &body)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("ocr http request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("ocr http request failed: %s", strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Success  bool   `json:"success"`
		FullText string `json:"full_text"`
		Results  []struct {
			Text        string      `json:"text"`
			Confidence  float64     `json:"confidence"`
			BoundingBox [][]float64 `json:"bounding_box"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	if !payload.Success && strings.TrimSpace(payload.Error) != "" {
		return nil, "", fmt.Errorf("ocr failed: %s", strings.TrimSpace(payload.Error))
	}

	results := make([]OCRResult, 0, len(payload.Results))
	fullTextParts := make([]string, 0, len(payload.Results))
	for _, item := range payload.Results {
		box := make([][2]int, 0, len(item.BoundingBox))
		for _, point := range item.BoundingBox {
			if len(point) < 2 {
				continue
			}
			box = append(box, [2]int{int(point[0]), int(point[1])})
		}
		results = append(results, OCRResult{
			Text:       strings.TrimSpace(item.Text),
			Confidence: item.Confidence,
			Box:        box,
		})
		if text := strings.TrimSpace(item.Text); text != "" {
			fullTextParts = append(fullTextParts, text)
		}
	}
	fullText := strings.TrimSpace(payload.FullText)
	if fullText == "" {
		fullText = strings.Join(fullTextParts, " ")
	}
	return results, fullText, nil
}

func matchOCR(record template.Record, cache *OCRCache) MatchResult {
	if cache == nil {
		return MatchResult{Method: "ocr", Threshold: record.Threshold}
	}
	filtered := make([]OCRResult, 0, len(cache.Results))
	var fullTextBuilder strings.Builder
	for _, item := range cache.Results {
		if record.Crop != nil && record.Crop.Width > 0 && record.Crop.Height > 0 && !boxInCrop(item.Box, *record.Crop) {
			continue
		}
		filtered = append(filtered, item)
		if fullTextBuilder.Len() > 0 {
			fullTextBuilder.WriteString(" ")
		}
		fullTextBuilder.WriteString(item.Text)
	}
	fullText := fullTextBuilder.String()
	conditions := parseOCRExpectedConditions(record.ExpectedText)
	tokens := positiveOCRExpectedTokens(conditions)
	matched := true
	for _, condition := range conditions {
		hasToken := ocrTokenMatched(filtered, condition.Text) || strings.Contains(fullText, condition.Text)
		if condition.Negated && hasToken {
			matched = false
			break
		}
		if !condition.Negated && !hasToken {
			matched = false
			break
		}
	}
	matchedItems := selectMatchedOCRResults(filtered, tokens)
	boxItems := selectPrimaryOCRResults(filtered, tokens)
	if len(boxItems) == 0 {
		boxItems = matchedItems
	}
	confidenceItems := filtered
	if matched && len(matchedItems) > 0 {
		confidenceItems = matchedItems
	}
	center, topLeft, width, height, hasBox := aggregateOCRBox(boxItems)
	result := MatchResult{
		Found:       matched,
		Method:      "ocr",
		Threshold:   record.Threshold,
		Confidence:  averageConfidence(confidenceItems),
		OCRResults:  filtered,
		MatchedText: strings.TrimSpace(record.ExpectedText),
		FullText:    strings.TrimSpace(fullText),
	}
	if matched && hasBox {
		// OCR click templates also need a tappable center in the runtime pipeline.
		result.Center = center
		result.TopLeft = topLeft
		result.Width = width
		result.Height = height
	}
	return result
}

func selectPrimaryOCRResults(items []OCRResult, tokens []string) []OCRResult {
	if len(tokens) == 0 {
		return nil
	}
	return selectMatchedOCRResults(items, tokens[:1])
}

func ocrTokenMatched(items []OCRResult, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return true
	}
	for _, item := range items {
		if strings.Contains(item.Text, token) {
			return true
		}
	}
	_, _, ok := findOCRTokenWindow(items, token)
	return ok
}

func splitExpectedTokens(value string) []string {
	rawItems := strings.Split(strings.TrimSpace(value), "&")
	result := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parseOCRExpectedConditions(value string) []ocrExpectedCondition {
	tokens := splitExpectedTokens(value)
	conditions := make([]ocrExpectedCondition, 0, len(tokens))
	for _, token := range tokens {
		item := strings.TrimSpace(token)
		if item == "" {
			continue
		}
		condition := ocrExpectedCondition{Text: item}
		if strings.HasPrefix(item, "!") {
			condition.Negated = true
			condition.Text = strings.TrimSpace(strings.TrimPrefix(item, "!"))
		}
		if condition.Text == "" {
			continue
		}
		conditions = append(conditions, condition)
	}
	return conditions
}

func positiveOCRExpectedTokens(conditions []ocrExpectedCondition) []string {
	result := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition.Negated {
			continue
		}
		result = append(result, condition.Text)
	}
	return result
}

func selectMatchedOCRResults(items []OCRResult, tokens []string) []OCRResult {
	if len(items) == 0 || len(tokens) == 0 {
		return nil
	}
	selected := make([]OCRResult, 0, len(tokens))
	selectedIndexes := make(map[int]struct{}, len(tokens))
	for _, token := range tokens {
		found := false
		for index, item := range items {
			if !strings.Contains(item.Text, token) {
				continue
			}
			if _, exists := selectedIndexes[index]; !exists {
				selected = append(selected, item)
				selectedIndexes[index] = struct{}{}
			}
			found = true
		}
		if found {
			continue
		}
		start, end, ok := findOCRTokenWindow(items, token)
		if !ok {
			continue
		}
		for index := start; index <= end; index++ {
			if _, exists := selectedIndexes[index]; exists {
				continue
			}
			selected = append(selected, items[index])
			selectedIndexes[index] = struct{}{}
		}
	}
	return selected
}

func findOCRTokenWindow(items []OCRResult, token string) (int, int, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, 0, false
	}
	for start := 0; start < len(items); start++ {
		var joined strings.Builder
		var joinedWithSpace strings.Builder
		for end := start; end < len(items); end++ {
			text := strings.TrimSpace(items[end].Text)
			joined.WriteString(text)
			if joinedWithSpace.Len() > 0 && text != "" {
				joinedWithSpace.WriteString(" ")
			}
			joinedWithSpace.WriteString(text)
			if strings.Contains(joined.String(), token) || strings.Contains(joinedWithSpace.String(), token) {
				return start, end, true
			}
		}
	}
	return 0, 0, false
}

func averageConfidence(items []OCRResult) float64 {
	if len(items) == 0 {
		return 0
	}
	total := 0.0
	for _, item := range items {
		total += item.Confidence
	}
	return total / float64(len(items))
}

func aggregateOCRBox(items []OCRResult) ([2]int, [2]int, int, int, bool) {
	if len(items) == 0 {
		return [2]int{}, [2]int{}, 0, 0, false
	}
	hasPoint := false
	minX, minY := 0, 0
	maxX, maxY := 0, 0
	for _, item := range items {
		for _, point := range item.Box {
			if !hasPoint {
				minX, maxX = point[0], point[0]
				minY, maxY = point[1], point[1]
				hasPoint = true
				continue
			}
			if point[0] < minX {
				minX = point[0]
			}
			if point[0] > maxX {
				maxX = point[0]
			}
			if point[1] < minY {
				minY = point[1]
			}
			if point[1] > maxY {
				maxY = point[1]
			}
		}
	}
	if !hasPoint {
		return [2]int{}, [2]int{}, 0, 0, false
	}
	width := maxX - minX
	height := maxY - minY
	center := [2]int{minX + width/2, minY + height/2}
	topLeft := [2]int{minX, minY}
	return center, topLeft, width, height, true
}

type openCVSourceUploadResponse struct {
	SourceID string `json:"source_id"`
}

type openCVBatchTemplateRequest struct {
	TemplateID string  `json:"template_id,omitempty"`
	Path       string  `json:"template_path"`
	Threshold  float64 `json:"threshold"`
	Method     string  `json:"method"`
	Grayscale  bool    `json:"grayscale"`
	CropX      *int    `json:"crop_x,omitempty"`
	CropY      *int    `json:"crop_y,omitempty"`
	CropWidth  *int    `json:"crop_width,omitempty"`
	CropHeight *int    `json:"crop_height,omitempty"`
}

type openCVBatchRequest struct {
	SourceID         string                       `json:"source_id"`
	StopOnFirstFound bool                         `json:"stop_on_first_found"`
	Templates        []openCVBatchTemplateRequest `json:"templates"`
}

type openCVBatchResult struct {
	TemplateID   string  `json:"template_id,omitempty"`
	Found        bool    `json:"found"`
	Confidence   float64 `json:"confidence"`
	ElapsedMS    float64 `json:"elapsed_ms"`
	Threshold    float64 `json:"threshold"`
	Method       string  `json:"method"`
	TopLeft      []int   `json:"top_left,omitempty"`
	Center       []int   `json:"center,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	SearchRegion []int   `json:"search_region,omitempty"`
}

type openCVBatchResponse struct {
	SourceID     string              `json:"source_id"`
	MatchedIndex int                 `json:"matched_index"`
	CheckedCount int                 `json:"checked_count"`
	Results      []openCVBatchResult `json:"results"`
}

func (r openCVBatchResult) toMatchResult() MatchResult {
	return MatchResult{
		Found:      r.Found,
		Method:     r.Method,
		Confidence: r.Confidence,
		Threshold:  r.Threshold,
		TopLeft:    intPair(r.TopLeft),
		Center:     intPair(r.Center),
		Width:      r.Width,
		Height:     r.Height,
		ElapsedMS:  r.ElapsedMS,
	}
}

func ensureVisionCache(cache *OCRCache) *OCRCache {
	if cache != nil {
		return cache
	}
	return &OCRCache{}
}

func (e *Engine) ensureOpenCVSource(imageBytes []byte, cache *OCRCache) (*OCRCache, error) {
	nextCache := ensureVisionCache(cache)
	sourceHash := hashBytes(imageBytes)
	if nextCache.OpenCVSourceID != "" && nextCache.OpenCVSourceHash == sourceHash {
		return nextCache, nil
	}
	sourceID, err := e.uploadOpenCVSource(imageBytes, sourceHash)
	if err != nil {
		return nextCache, err
	}
	nextCache.OpenCVSourceID = sourceID
	nextCache.OpenCVSourceHash = sourceHash
	return nextCache, nil
}

func (e *Engine) uploadOpenCVSource(imageBytes []byte, sourceKey string) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("source_image", "capture.png")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(imageBytes); err != nil {
		return "", err
	}
	if err := writer.WriteField("source_key", sourceKey); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(e.cfg.OpenCVBaseURL, "/")+"/sources", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload opencv source failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload opencv source failed: %s", strings.TrimSpace(string(raw)))
	}
	var payload openCVSourceUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.SourceID) == "" {
		return "", fmt.Errorf("upload opencv source failed: missing source_id")
	}
	return payload.SourceID, nil
}

func (e *Engine) requestOpenCVBatch(sourceID string, records []template.Record) (openCVBatchResponse, bool, error) {
	requestPayload := openCVBatchRequest{
		SourceID:         sourceID,
		StopOnFirstFound: true,
		Templates:        make([]openCVBatchTemplateRequest, 0, len(records)),
	}
	for _, record := range records {
		templatePath := record.ImagePath
		if !filepath.IsAbs(templatePath) {
			if absPath, err := filepath.Abs(templatePath); err == nil {
				templatePath = absPath
			}
		}
		item := openCVBatchTemplateRequest{
			TemplateID: record.ID,
			Path:       templatePath,
			Threshold:  record.Threshold,
			Method:     record.Method,
			Grayscale:  record.Grayscale,
		}
		if record.Crop != nil && record.Crop.Width > 0 && record.Crop.Height > 0 {
			item.CropX = &record.Crop.X
			item.CropY = &record.Crop.Y
			item.CropWidth = &record.Crop.Width
			item.CropHeight = &record.Crop.Height
		}
		requestPayload.Templates = append(requestPayload.Templates, item)
	}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		return openCVBatchResponse{}, false, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(e.cfg.OpenCVBaseURL, "/")+"/match-batch", bytes.NewReader(body))
	if err != nil {
		return openCVBatchResponse{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return openCVBatchResponse{}, false, fmt.Errorf("opencv batch match failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return openCVBatchResponse{}, resp.StatusCode == http.StatusNotFound, fmt.Errorf("opencv batch match failed: %s", strings.TrimSpace(string(raw)))
	}
	var payload openCVBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return openCVBatchResponse{}, false, err
	}
	return payload, false, nil
}

func (e *Engine) fetchOpenCVHealth() (string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(e.cfg.OpenCVBaseURL, "/")+"/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("opencv status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (e *Engine) fetchOCRHealth() (string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(e.cfg.OCRBaseURL, "/")+"/health", nil)
	if err != nil {
		return "", err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ocr status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func hashBytes(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}

func intPair(values []int) [2]int {
	if len(values) >= 2 {
		return [2]int{values[0], values[1]}
	}
	return [2]int{}
}

func boxInCrop(box [][2]int, crop template.CropRegion) bool {
	if len(box) == 0 {
		return true
	}
	minX, minY := box[0][0], box[0][1]
	maxX, maxY := minX, minY
	for _, point := range box {
		if point[0] < minX {
			minX = point[0]
		}
		if point[1] < minY {
			minY = point[1]
		}
		if point[0] > maxX {
			maxX = point[0]
		}
		if point[1] > maxY {
			maxY = point[1]
		}
	}
	return minX >= crop.X && minY >= crop.Y && maxX <= crop.X+crop.Width && maxY <= crop.Y+crop.Height
}

func elapsedMillis(startTime time.Time) float64 {
	return float64(time.Since(startTime).Microseconds()) / 1000.0
}

const opencvScript = `#!/usr/bin/env python3
import argparse, json, cv2

parser = argparse.ArgumentParser()
parser.add_argument("--source", required=True)
parser.add_argument("--template", required=True)
parser.add_argument("--method", default="ccoeff_normed")
parser.add_argument("--threshold", type=float, default=0.8)
parser.add_argument("--grayscale", default="0")
parser.add_argument("--crop-x", type=int, default=0)
parser.add_argument("--crop-y", type=int, default=0)
parser.add_argument("--crop-width", type=int, default=0)
parser.add_argument("--crop-height", type=int, default=0)
args = parser.parse_args()

flag = cv2.IMREAD_GRAYSCALE if args.grayscale == "1" else cv2.IMREAD_COLOR
source = cv2.imread(args.source, flag)
template = cv2.imread(args.template, flag)
if source is None or template is None:
    print(json.dumps({"found": False, "confidence": 0, "method": args.method, "threshold": args.threshold}))
    raise SystemExit(0)
offset_x, offset_y = 0, 0
search = source
if args.crop_width > 0 and args.crop_height > 0:
    offset_x, offset_y = args.crop_x, args.crop_y
    search = source[args.crop_y:args.crop_y+args.crop_height, args.crop_x:args.crop_x+args.crop_width]

method_map = {
    "ccoeff_normed": cv2.TM_CCOEFF_NORMED,
    "ccorr_normed": cv2.TM_CCORR_NORMED,
    "sqdiff_normed": cv2.TM_SQDIFF_NORMED,
}
method = method_map.get(args.method, cv2.TM_CCOEFF_NORMED)
result = cv2.matchTemplate(search, template, method)
min_val, max_val, min_loc, max_loc = cv2.minMaxLoc(result)
if method == cv2.TM_SQDIFF_NORMED:
    confidence = 1.0 - float(min_val)
    found = min_val <= (1.0 - args.threshold)
    top_left = min_loc
else:
    confidence = float(max_val)
    found = max_val >= args.threshold
    top_left = max_loc
w, h = template.shape[1], template.shape[0]
center = [int(top_left[0] + w / 2 + offset_x), int(top_left[1] + h / 2 + offset_y)]
print(json.dumps({
    "found": bool(found),
    "method": args.method,
    "confidence": round(confidence, 6),
    "threshold": args.threshold,
    "top_left": [int(top_left[0] + offset_x), int(top_left[1] + offset_y)],
    "center": center,
    "width": int(w),
    "height": int(h),
}))
`
