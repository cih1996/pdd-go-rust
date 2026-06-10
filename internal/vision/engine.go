package vision

import (
    "unified-server/internal/config"
    "unified-server/internal/template"
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
    Found      bool        `json:"found"`
    Method     string      `json:"method"`
    Confidence float64     `json:"confidence"`
    Center     [2]int      `json:"center,omitempty"`
    OCRResults []OCRResult `json:"ocr_results,omitempty"`
}

func NewEngine(cfg config.Config, tpl *template.Store) *Engine { return &Engine{cfg: cfg, tpl: tpl} }
func (e *Engine) Mode() string { if e.cfg.EnableVisionMock { return "mock" }; return "native" }
func (e *Engine) HasOCRTemplates() bool { return e.tpl.CountByEngine("ocr") > 0 }
func (e *Engine) Plan() map[string]any {
    return map[string]any{
        "opencv": "built into go process",
        "ocr": "built into go process",
        "loop_ocr_policy": "run OCR once per loop only when OCR templates exist",
        "transport": "no local OCR/OpenCV HTTP hop in final design",
    }
}