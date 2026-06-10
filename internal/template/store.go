package template

import "sync"

type Record struct {
    ID                string `json:"id"`
    Label             string `json:"label"`
    TemplateType      string `json:"template_type"`
    RecognitionEngine string `json:"recognition_engine"`
    ExpectedText      string `json:"expected_text,omitempty"`
    Enabled           bool   `json:"enabled"`
}

type Store struct {
    mu    sync.RWMutex
    items []Record
}

func NewStore() *Store {
    return &Store{items: []Record{
        {ID: "tpl-opencv-demo", Label: "OpenCV Demo", TemplateType: "click_image", RecognitionEngine: "opencv", Enabled: true},
        {ID: "tpl-ocr-demo", Label: "OCR Demo", TemplateType: "success_image", RecognitionEngine: "ocr", ExpectedText: "店铺优惠&立即支付", Enabled: true},
    }}
}

func (s *Store) List() []Record {
    s.mu.RLock()
    defer s.mu.RUnlock()
    result := make([]Record, len(s.items))
    copy(result, s.items)
    return result
}

func (s *Store) Count() int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return len(s.items)
}

func (s *Store) CountByEngine(engine string) int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    total := 0
    for _, item := range s.items {
        if item.Enabled && item.RecognitionEngine == engine {
            total++
        }
    }
    return total
}