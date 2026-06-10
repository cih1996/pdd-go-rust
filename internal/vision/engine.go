package vision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unified-server/internal/template"

	"unified-server/internal/config"
)

type Engine struct {
	cfg config.Config
	tpl *template.Store
}

type OCRResult struct {
	Text       string   `json:"text"`
	Confidence float64  `json:"confidence"`
	Box        [][2]int `json:"box,omitempty"`
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

func NewEngine(cfg config.Config, tpl *template.Store) *Engine { return &Engine{cfg: cfg, tpl: tpl} }

func (e *Engine) Mode() string {
	if e.cfg.EnableVisionMock {
		return "mock"
	}
	return "native"
}

func (e *Engine) HasOCRTemplates() bool { return e.tpl.CountByEngine("ocr") > 0 }

func (e *Engine) Capability() Capability {
	capability := Capability{
		GoCVImported: false,
		Mode:         e.Mode(),
		Reason:       "本地子进程视觉链路可用时，Go 将直接调用本机 OpenCV/OCR，无需额外 HTTP hop",
	}
	if output, err := exec.Command("python3", "-c", "import cv2; print(cv2.__version__)").CombinedOutput(); err == nil {
		capability.OpenCVInstalled = true
		capability.Reason = strings.TrimSpace(string(output))
	}
	if _, err := exec.Command("swift", "-e", "import Vision\nimport Foundation\nprint(\"ok\")").CombinedOutput(); err == nil {
		capability.OCRInstalled = true
	}
	return capability
}

func (e *Engine) Plan() map[string]any {
	capability := e.Capability()
	return map[string]any{
		"opencv":          "target is built into go process",
		"ocr":             "target is built into go process",
		"loop_ocr_policy": "run OCR once per loop only when OCR templates exist",
		"transport":       "no local OCR/OpenCV HTTP hop in final design",
		"capability":      capability,
	}
}

type OCRCache struct {
	Results  []OCRResult
	FullText string
}

func (e *Engine) Match(record template.Record, imageBytes []byte, cache *OCRCache) (MatchResult, *OCRCache, error) {
	start := time.Now()
	if record.RecognitionEngine == "ocr" {
		usedCache := cache != nil
		ocrExecuted := false
		ocrExecElapsedMS := 0.0
		if cache == nil {
			ocrStart := time.Now()
			next, err := e.runOCR(imageBytes)
			if err != nil {
				return MatchResult{}, nil, err
			}
			cache = next
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
	result, err := e.runOpenCV(record, imageBytes)
	result.ElapsedMS = elapsedMillis(start)
	return result, cache, err
}

func (e *Engine) runOpenCV(record template.Record, imageBytes []byte) (MatchResult, error) {
	if strings.TrimSpace(record.ImagePath) == "" {
		return MatchResult{}, fmt.Errorf("opencv template %s missing image path", record.Label)
	}
	sourcePath, cleanup, err := writeTempPNG("capture", imageBytes)
	if err != nil {
		return MatchResult{}, err
	}
	defer cleanup()

	scriptPath := filepath.Join(".runtime", "vision", "opencv_match.py")
	if err = os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		return MatchResult{}, err
	}
	if err = os.WriteFile(scriptPath, []byte(opencvScript), 0o755); err != nil {
		return MatchResult{}, err
	}

	args := []string{
		scriptPath,
		"--source", sourcePath,
		"--template", record.ImagePath,
		"--method", record.Method,
		"--threshold", fmt.Sprintf("%f", record.Threshold),
	}
	if record.Grayscale {
		args = append(args, "--grayscale", "1")
	}
	if record.Crop != nil && record.Crop.Width > 0 && record.Crop.Height > 0 {
		args = append(args,
			"--crop-x", strconv.Itoa(record.Crop.X),
			"--crop-y", strconv.Itoa(record.Crop.Y),
			"--crop-width", strconv.Itoa(record.Crop.Width),
			"--crop-height", strconv.Itoa(record.Crop.Height),
		)
	}
	output, err := exec.Command("python3", args...).CombinedOutput()
	if err != nil {
		return MatchResult{}, fmt.Errorf("opencv match failed: %s", strings.TrimSpace(string(output)))
	}
	var result MatchResult
	if err := json.Unmarshal(output, &result); err != nil {
		return MatchResult{}, err
	}
	result.Method = record.Method
	result.Threshold = record.Threshold
	return result, nil
}

func (e *Engine) runOCR(imageBytes []byte) (*OCRCache, error) {
	sourcePath, cleanup, err := writeTempPNG("ocr", imageBytes)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	scriptPath := filepath.Join(".runtime", "vision", "vision_ocr.swift")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptPath, []byte(swiftOCRScript), 0o755); err != nil {
		return nil, err
	}
	cmd := exec.Command("swift", scriptPath, sourcePath)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("ocr failed: %s", message)
	}
	var payload struct {
		FullText string      `json:"full_text"`
		Results  []OCRResult `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return nil, err
	}
	return &OCRCache{Results: payload.Results, FullText: payload.FullText}, nil
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
	tokens := strings.Split(strings.TrimSpace(record.ExpectedText), "&")
	matched := true
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if !strings.Contains(fullText, token) && !strings.Contains(cache.FullText, token) {
			matched = false
			break
		}
	}
	return MatchResult{
		Found:       matched,
		Method:      "ocr",
		Threshold:   record.Threshold,
		Confidence:  averageConfidence(filtered),
		OCRResults:  filtered,
		MatchedText: strings.TrimSpace(record.ExpectedText),
		FullText:    strings.TrimSpace(fullText),
	}
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

func writeTempPNG(prefix string, data []byte) (string, func(), error) {
	file, err := os.CreateTemp("", prefix+"-*.png")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
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

const swiftOCRScript = `import Foundation
import Vision

struct OCRItem: Codable {
    let text: String
    let confidence: Double
    let box: [[Int]]
}

struct OCRPayload: Codable {
    let full_text: String
    let results: [OCRItem]
}

let path = CommandLine.arguments[1]
let url = URL(fileURLWithPath: path)
let request = VNRecognizeTextRequest()
request.recognitionLevel = .accurate
let handler = try VNImageRequestHandler(url: url)
try handler.perform([request])
let observations = request.results ?? []
var items: [OCRItem] = []
var fullText: [String] = []
for observation in observations {
    guard let candidate = observation.topCandidates(1).first else { continue }
    fullText.append(candidate.string)
    let box = observation.boundingBox
    let points = [
        [Int(box.minX * 1000), Int(box.minY * 1000)],
        [Int(box.maxX * 1000), Int(box.minY * 1000)],
        [Int(box.maxX * 1000), Int(box.maxY * 1000)],
        [Int(box.minX * 1000), Int(box.maxY * 1000)],
    ]
    items.append(OCRItem(text: candidate.string, confidence: Double(candidate.confidence), box: points))
}
let payload = OCRPayload(full_text: fullText.joined(separator: " "), results: items)
let data = try JSONEncoder().encode(payload)
FileHandle.standardOutput.write(data)
`
