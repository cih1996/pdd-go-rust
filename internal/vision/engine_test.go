package vision

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"unified-server/internal/config"
	"unified-server/internal/template"
)

func TestRunOpenCVBatch_SkipsTemplatesWithoutImagePath(t *testing.T) {
	templatePath := filepath.Join(t.TempDir(), "valid.png")
	if err := os.WriteFile(templatePath, []byte("fake-template"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources":
			_ = json.NewEncoder(w).Encode(map[string]string{"source_id": "src-1"})
		case "/match-batch":
			var payload openCVBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if len(payload.Templates) != 1 {
				t.Fatalf("expected 1 valid template to be sent, got %d", len(payload.Templates))
			}
			if payload.Templates[0].TemplateID != "valid" {
				t.Fatalf("expected valid template to be matched, got %s", payload.Templates[0].TemplateID)
			}
			_ = json.NewEncoder(w).Encode(openCVBatchResponse{
				MatchedIndex: 0,
				Results: []openCVBatchResult{
					{
						TemplateID: "valid",
						Found:      true,
						Method:     "ccoeff_normed",
						Threshold:  0.8,
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	engine := &Engine{
		cfg:    config.Config{OpenCVBaseURL: server.URL},
		client: server.Client(),
	}

	index, result, _, err := engine.runOpenCVBatch([]template.Record{
		{
			ID:                "broken",
			Label:             "OpenCV Demo",
			RecognitionEngine: "opencv",
			Method:            "ccoeff_normed",
			Threshold:         0.8,
		},
		{
			ID:                "valid",
			Label:             "Valid",
			RecognitionEngine: "opencv",
			ImagePath:         templatePath,
			Method:            "ccoeff_normed",
			Threshold:         0.8,
		},
	}, []byte("fake-source"), nil)
	if err != nil {
		t.Fatalf("runOpenCVBatch returned error: %v", err)
	}
	if index != 1 {
		t.Fatalf("expected matched index 1, got %d", index)
	}
	if !result.Found {
		t.Fatalf("expected found result")
	}
}

func TestMatchOCR_UsesHTTPOneTimePerCache(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ocr":
			requestCount++
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Fatalf("parse multipart: %v", err)
			}
			file, _, err := r.FormFile("image")
			if err != nil {
				t.Fatalf("missing image form file: %v", err)
			}
			defer file.Close()
			payload := bytes.Buffer{}
			if _, err := payload.ReadFrom(file); err != nil {
				t.Fatalf("read uploaded image: %v", err)
			}
			if payload.Len() == 0 {
				t.Fatalf("expected uploaded image bytes")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success":   true,
				"full_text": "店铺优惠 立即支付",
				"results": []map[string]any{
					{
						"text":         "店铺优惠",
						"confidence":   0.98,
						"bounding_box": [][]int{{0, 0}, {40, 0}, {40, 10}, {0, 10}},
					},
					{
						"text":         "立即支付",
						"confidence":   0.96,
						"bounding_box": [][]int{{50, 0}, {90, 0}, {90, 10}, {50, 10}},
					},
				},
			})
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	engine := &Engine{
		cfg:    config.Config{OCRBaseURL: server.URL},
		client: server.Client(),
	}

	recordA := template.Record{
		ID:                "ocr-a",
		Label:             "OCR A",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "店铺优惠",
		Threshold:         0.8,
	}
	recordB := template.Record{
		ID:                "ocr-b",
		Label:             "OCR B",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "立即支付",
		Threshold:         0.8,
	}

	first, cache, err := engine.Match(recordA, []byte("fake-image"), nil)
	if err != nil {
		t.Fatalf("first OCR match returned error: %v", err)
	}
	if !first.Found || !first.OCRExecuted || first.OCRUsedCache {
		t.Fatalf("expected first OCR match to execute remote OCR once")
	}

	second, _, err := engine.Match(recordB, []byte("fake-image"), cache)
	if err != nil {
		t.Fatalf("second OCR match returned error: %v", err)
	}
	if !second.Found || second.OCRExecuted || !second.OCRUsedCache {
		t.Fatalf("expected second OCR match to reuse cached OCR results")
	}
	if requestCount != 1 {
		t.Fatalf("expected exactly one OCR HTTP request, got %d", requestCount)
	}
}

func TestMatchOCR_DerivesTapCenterFromOCRBoxes(t *testing.T) {
	record := template.Record{
		ID:                "ocr-click",
		Label:             "立即支付",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "立即支付",
		Threshold:         0.8,
	}
	cache := &OCRCache{
		Results: []OCRResult{
			{
				Text:       "立即支付",
				Confidence: 0.99,
				Box:        [][2]int{{100, 200}, {180, 200}, {180, 240}, {100, 240}},
			},
		},
		FullText: "立即支付",
	}

	result := matchOCR(record, cache)
	if !result.Found {
		t.Fatalf("expected OCR match to succeed")
	}
	if result.Center != [2]int{140, 220} {
		t.Fatalf("expected tap center [140 220], got %v", result.Center)
	}
	if result.TopLeft != [2]int{100, 200} || result.Width != 80 || result.Height != 40 {
		t.Fatalf("expected OCR box geometry to be preserved, got topLeft=%v width=%d height=%d", result.TopLeft, result.Width, result.Height)
	}
}

func TestMatchOCR_DoesNotUseWholeScreenWhenOnlyOneTokenMatches(t *testing.T) {
	record := template.Record{
		ID:                "ocr-click",
		Label:             "立即支付",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "立即支付",
		Threshold:         0.8,
	}
	cache := &OCRCache{
		Results: []OCRResult{
			{
				Text:       "首页",
				Confidence: 0.91,
				Box:        [][2]int{{10, 10}, {90, 10}, {90, 40}, {10, 40}},
			},
			{
				Text:       "立即支付",
				Confidence: 0.99,
				Box:        [][2]int{{300, 500}, {420, 500}, {420, 560}, {300, 560}},
			},
			{
				Text:       "活动说明",
				Confidence: 0.93,
				Box:        [][2]int{{600, 900}, {760, 900}, {760, 950}, {600, 950}},
			},
		},
		FullText: "首页 立即支付 活动说明",
	}

	result := matchOCR(record, cache)
	if !result.Found {
		t.Fatalf("expected OCR match to succeed")
	}
	if result.Center != [2]int{360, 530} {
		t.Fatalf("expected tap center [360 530], got %v", result.Center)
	}
	if result.TopLeft != [2]int{300, 500} || result.Width != 120 || result.Height != 60 {
		t.Fatalf("expected OCR box to use only matched token region, got topLeft=%v width=%d height=%d", result.TopLeft, result.Width, result.Height)
	}
}

func TestMatchOCR_UsesFirstExpectedTokenForTapCenter(t *testing.T) {
	record := template.Record{
		ID:                "ocr-click",
		Label:             "店铺优惠",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "店铺优惠&-",
		Threshold:         0.8,
	}
	cache := &OCRCache{
		Results: []OCRResult{
			{
				Text:       "店铺",
				Confidence: 0.97,
				Box:        [][2]int{{100, 200}, {140, 200}, {140, 240}, {100, 240}},
			},
			{
				Text:       "优惠",
				Confidence: 0.98,
				Box:        [][2]int{{145, 200}, {205, 200}, {205, 240}, {145, 240}},
			},
			{
				Text:       "-",
				Confidence: 0.99,
				Box:        [][2]int{{500, 800}, {520, 800}, {520, 820}, {500, 820}},
			},
		},
		FullText: "店铺 优惠 -",
	}

	result := matchOCR(record, cache)
	if !result.Found {
		t.Fatalf("expected OCR match to succeed")
	}
	if result.Center != [2]int{152, 220} {
		t.Fatalf("expected tap center [152 220], got %v", result.Center)
	}
	if result.TopLeft != [2]int{100, 200} || result.Width != 105 || result.Height != 40 {
		t.Fatalf("expected OCR box to follow first expected token, got topLeft=%v width=%d height=%d", result.TopLeft, result.Width, result.Height)
	}
}

func TestMatchOCR_SupportsNegatedConditions(t *testing.T) {
	record := template.Record{
		ID:                "ocr-negated",
		Label:             "店铺优惠且未领取",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "店铺优惠&!领取",
		Threshold:         0.8,
	}
	cache := &OCRCache{
		Results: []OCRResult{
			{
				Text:       "店铺优惠",
				Confidence: 0.99,
				Box:        [][2]int{{100, 100}, {180, 100}, {180, 140}, {100, 140}},
			},
		},
		FullText: "店铺优惠",
	}

	result := matchOCR(record, cache)
	if !result.Found {
		t.Fatalf("expected negated OCR condition to match when forbidden text is absent")
	}

	cache.FullText = "店铺优惠 领取"
	cache.Results = append(cache.Results, OCRResult{
		Text:       "领取",
		Confidence: 0.98,
		Box:        [][2]int{{220, 100}, {260, 100}, {260, 140}, {220, 140}},
	})
	result = matchOCR(record, cache)
	if result.Found {
		t.Fatalf("expected negated OCR condition to fail when forbidden text is present")
	}
}

func TestMatchOCR_DoesNotMatchTokenOutsideCropUsingGlobalFullText(t *testing.T) {
	record := template.Record{
		ID:                "ocr-crop",
		Label:             "裁剪区域领券",
		RecognitionEngine: "ocr",
		Method:            "ocr",
		ExpectedText:      "领券",
		Threshold:         0.8,
		Crop: &template.CropRegion{
			X:      0,
			Y:      0,
			Width:  200,
			Height: 200,
		},
	}
	cache := &OCRCache{
		Results: []OCRResult{
			{
				Text:       "店铺优惠",
				Confidence: 0.97,
				Box:        [][2]int{{10, 10}, {100, 10}, {100, 40}, {10, 40}},
			},
			{
				Text:       "领券后7天内有效",
				Confidence: 0.98,
				Box:        [][2]int{{400, 400}, {560, 400}, {560, 440}, {400, 440}},
			},
		},
		FullText: "店铺优惠 领券后7天内有效",
	}

	result := matchOCR(record, cache)
	if result.Found {
		t.Fatalf("expected OCR crop to ignore token outside crop area")
	}
}
