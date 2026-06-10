package template

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type CropRegion struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type Record struct {
	ID                string      `json:"id"`
	Label             string      `json:"label"`
	TemplateType      string      `json:"template_type"`
	RecognitionEngine string      `json:"recognition_engine"`
	Priority          int         `json:"priority"`
	ExpectedText      string      `json:"expected_text,omitempty"`
	ImageName         string      `json:"image_name,omitempty"`
	ImageURL          string      `json:"image_url,omitempty"`
	Threshold         float64     `json:"threshold"`
	Method            string      `json:"method"`
	Grayscale         bool        `json:"grayscale"`
	Crop              *CropRegion `json:"crop,omitempty"`
	Enabled           bool        `json:"enabled"`
	CreatedAt         string      `json:"created_at"`
	ImagePath         string      `json:"-"`
}

type UpsertInput struct {
	Label             string
	TemplateType      string
	RecognitionEngine string
	Priority          int
	ExpectedText      string
	Threshold         float64
	Method            string
	Grayscale         bool
	Crop              *CropRegion
	Enabled           bool
	ImageName         string
	ImageBytes        []byte
}

type Backend interface {
	LoadTemplates() ([]Record, error)
	SaveTemplates([]Record) error
}

type Store struct {
	mu       sync.RWMutex
	items    []Record
	imageDir string
	backend  Backend
}

func NewStore(backend Backend) *Store {
	rootDir := filepath.Join(".runtime", "templates")
	_ = os.MkdirAll(rootDir, 0o755)
	store := &Store{
		items:    []Record{},
		imageDir: filepath.Join(rootDir, "images"),
		backend:  backend,
	}
	_ = os.MkdirAll(store.imageDir, 0o755)
	if backend != nil {
		if items, err := backend.LoadTemplates(); err == nil && len(items) > 0 {
			store.items = store.repairLoadedItems(items)
		}
	}
	if len(store.items) == 0 {
		store.items = defaultTemplates()
		store.persistLocked()
	}
	return store
}

func (s *Store) repairLoadedItems(items []Record) []Record {
	changed := false
	fixed := clone(items)
	for i := range fixed {
		if strings.TrimSpace(fixed[i].ImageName) == "" {
			continue
		}
		if strings.TrimSpace(fixed[i].ImageURL) == "" {
			fixed[i].ImageURL = "/api/assets/templates/" + fixed[i].ImageName
			changed = true
		}
		if strings.TrimSpace(fixed[i].ImagePath) != "" {
			continue
		}
		candidatePath := filepath.Join(s.imageDir, fixed[i].ImageName)
		if _, err := os.Stat(candidatePath); err == nil {
			fixed[i].ImagePath = candidatePath
			changed = true
		}
	}
	if changed {
		s.items = fixed
		s.persistLocked()
	}
	return fixed
}

func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Record, len(s.items))
	copy(result, s.items)
	sortTemplates(result)
	return result
}

func (s *Store) ListEnabledByType(templateType string) []Record {
	items := s.List()
	result := make([]Record, 0)
	for _, item := range items {
		if item.Enabled && item.TemplateType == templateType {
			result = append(result, item)
		}
	}
	return result
}

func (s *Store) Get(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if item.ID == id {
			return item, true
		}
	}
	return Record{}, false
}

func (s *Store) Create(input UpsertInput) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := buildRecord(input, Record{})
	record.ID = newID("tpl")
	record.CreatedAt = nowString()
	if err := s.writeImage(&record, input); err != nil {
		return Record{}, err
	}
	s.items = append(s.items, record)
	sortTemplates(s.items)
	s.persistLocked()
	return record, nil
}

func (s *Store) Update(id string, input UpsertInput) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != id {
			continue
		}
		updated := buildRecord(input, item)
		updated.ID = item.ID
		if updated.CreatedAt == "" {
			updated.CreatedAt = item.CreatedAt
		}
		if err := s.writeImage(&updated, input); err != nil {
			return Record{}, true, err
		}
		s.items[i] = updated
		sortTemplates(s.items)
		s.persistLocked()
		return updated, true, nil
	}
	return Record{}, false, nil
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != id {
			continue
		}
		if item.ImagePath != "" {
			_ = os.Remove(item.ImagePath)
		}
		s.items = append(s.items[:i], s.items[i+1:]...)
		s.persistLocked()
		return true
	}
	return false
}

func (s *Store) Move(id string, direction string) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, item := range s.items {
		if item.ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return clone(s.items)
	}
	target := index
	if direction == "up" && index > 0 {
		target = index - 1
	}
	if direction == "down" && index < len(s.items)-1 {
		target = index + 1
	}
	s.items[index], s.items[target] = s.items[target], s.items[index]
	for i := range s.items {
		s.items[i].Priority = (i + 1) * 10
	}
	s.persistLocked()
	return clone(s.items)
}

func (s *Store) ImportRecords(records []Record, replaceExisting bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := clone(records)
	for i := range next {
		if strings.TrimSpace(next[i].ID) == "" {
			next[i].ID = newID("tpl")
		}
		if strings.TrimSpace(next[i].CreatedAt) == "" {
			next[i].CreatedAt = nowString()
		}
	}

	if replaceExisting {
		s.items = next
		sortTemplates(s.items)
		s.persistLocked()
		return len(next)
	}

	indexByID := make(map[string]int, len(s.items))
	for i, item := range s.items {
		indexByID[item.ID] = i
	}
	imported := 0
	for _, item := range next {
		if index, ok := indexByID[item.ID]; ok {
			s.items[index] = item
		} else {
			s.items = append(s.items, item)
			imported++
		}
	}
	sortTemplates(s.items)
	s.persistLocked()
	return len(next)
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

func (s *Store) ImageDir() string {
	return s.imageDir
}

func (s *Store) writeImage(record *Record, input UpsertInput) error {
	if len(input.ImageBytes) == 0 || strings.TrimSpace(input.ImageName) == "" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(input.ImageName))
	if ext == "" {
		ext = ".png"
	}
	fileName := fmt.Sprintf("%s%s", record.ID, ext)
	path := filepath.Join(s.imageDir, fileName)
	if err := os.WriteFile(path, input.ImageBytes, 0o644); err != nil {
		return err
	}
	record.ImageName = fileName
	record.ImagePath = path
	record.ImageURL = "/api/assets/templates/" + fileName
	return nil
}

func buildRecord(input UpsertInput, current Record) Record {
	record := current
	if strings.TrimSpace(input.Label) != "" {
		record.Label = strings.TrimSpace(input.Label)
	}
	if strings.TrimSpace(input.TemplateType) != "" {
		record.TemplateType = strings.TrimSpace(input.TemplateType)
	}
	if strings.TrimSpace(input.RecognitionEngine) != "" {
		record.RecognitionEngine = strings.TrimSpace(input.RecognitionEngine)
	}
	record.Priority = input.Priority
	record.ExpectedText = strings.TrimSpace(input.ExpectedText)
	record.Threshold = input.Threshold
	record.Method = strings.TrimSpace(input.Method)
	record.Grayscale = input.Grayscale
	record.Crop = input.Crop
	record.Enabled = input.Enabled
	return record
}

func defaultTemplates() []Record {
	return []Record{
		{
			ID:                "tpl-opencv-demo",
			Label:             "OpenCV Demo",
			TemplateType:      "click_image",
			RecognitionEngine: "opencv",
			Priority:          10,
			Threshold:         0.8,
			Method:            "ccoeff_normed",
			Grayscale:         false,
			Enabled:           true,
			CreatedAt:         nowString(),
		},
		{
			ID:                "tpl-ocr-demo",
			Label:             "OCR Demo",
			TemplateType:      "success_image",
			RecognitionEngine: "ocr",
			Priority:          20,
			ExpectedText:      "店铺优惠&立即支付",
			Threshold:         0.8,
			Method:            "ocr",
			Grayscale:         false,
			Enabled:           true,
			CreatedAt:         nowString(),
		},
	}
}

func sortTemplates(items []Record) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].TemplateType != items[j].TemplateType {
			return typeWeight(items[i].TemplateType) < typeWeight(items[j].TemplateType)
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].CreatedAt < items[j].CreatedAt
	})
}

func typeWeight(value string) int {
	switch value {
	case "account_risk":
		return 1
	case "fail_release":
		return 2
	case "click_image":
		return 3
	case "success_image":
		return 4
	default:
		return 99
	}
}

func clone(items []Record) []Record {
	result := make([]Record, len(items))
	copy(result, items)
	return result
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newID(prefix string) string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

func (s *Store) persistLocked() {
	if s.backend == nil {
		return
	}
	items := clone(s.items)
	_ = s.backend.SaveTemplates(items)
}
