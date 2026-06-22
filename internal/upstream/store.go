package upstream

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Record struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Code         string    `json:"code"`
	UpstreamType string    `json:"upstream_type"`
	Enabled      bool      `json:"enabled"`
	Priority     int       `json:"priority"`
	BaseURL      string    `json:"base_url"`
	ProxyURL     string    `json:"proxy_url,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Stats        Stats     `json:"stats"`
}

type UpsertRequest struct {
	Name         string `json:"name"`
	Code         string `json:"code"`
	UpstreamType string `json:"upstream_type"`
	Enabled      *bool  `json:"enabled"`
	Priority     *int   `json:"priority"`
	BaseURL      string `json:"base_url"`
	ProxyURL     string `json:"proxy_url"`
	Notes        string `json:"notes"`
}

type Stats struct {
	FetchedCount         int `json:"fetched_count"`
	ReportedSuccessCount int `json:"reported_success_count"`
	ReportedFailureCount int `json:"reported_failure_count"`
}

type Backend interface {
	LoadUpstreams() ([]Record, error)
	SaveUpstreams([]Record) error
}

type Store struct {
	mu      sync.RWMutex
	items   []Record
	backend Backend
}

func NewStore(backend Backend) *Store {
	store := &Store{
		backend: backend,
		items: []Record{
			{
				ID:           newID(),
				Name:         "本地模拟上游",
				Code:         "mock_local",
				UpstreamType: "mock_upstream",
				Enabled:      true,
				Priority:     10,
				BaseURL:      "http://127.0.0.1:8091",
				ProxyURL:     "",
				Notes:        "默认联调 Rust 适配器中的本地模拟 provider",
				CreatedAt:    time.Now().UTC(),
				Stats:        Stats{},
			},
			{
				ID:           newID(),
				Name:         "老钱真实上游",
				Code:         "laoqian_worker",
				UpstreamType: "laoqian_worker",
				Enabled:      true,
				Priority:     20,
				BaseURL:      "http://127.0.0.1:8091",
				ProxyURL:     "",
				Notes:        "默认联调 Rust 适配器中的老钱 provider",
				CreatedAt:    time.Now().UTC(),
				Stats:        Stats{},
			},
		},
	}
	if backend != nil {
		if items, err := backend.LoadUpstreams(); err == nil && len(items) > 0 {
			store.items = items
		} else if len(store.items) > 0 {
			store.persistLocked()
		}
	}
	return store
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

func (s *Store) Create(payload UpsertRequest) Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	upstreamType := strings.TrimSpace(payload.UpstreamType)
	if upstreamType == "" {
		upstreamType = "mock_upstream"
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = defaultNameForType(upstreamType)
	}
	code := strings.TrimSpace(payload.Code)
	if code == "" {
		code = upstreamType + "_" + shortID()
	}
	baseURL := strings.TrimSpace(payload.BaseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8091"
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	priority := 100
	if payload.Priority != nil {
		priority = *payload.Priority
	}
	record := Record{
		ID:           newID(),
		Name:         name,
		Code:         code,
		UpstreamType: upstreamType,
		Enabled:      enabled,
		Priority:     priority,
		BaseURL:      baseURL,
		ProxyURL:     strings.TrimSpace(payload.ProxyURL),
		Notes:        strings.TrimSpace(payload.Notes),
		CreatedAt:    time.Now().UTC(),
		Stats:        Stats{},
	}
	s.items = append(s.items, record)
	s.persistLocked()
	return record
}

func (s *Store) Update(id string, payload UpsertRequest) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for index, item := range s.items {
		if item.ID != id {
			continue
		}
		updated := item
		if name := strings.TrimSpace(payload.Name); name != "" {
			updated.Name = name
		}
		if upstreamType := strings.TrimSpace(payload.UpstreamType); upstreamType != "" {
			updated.UpstreamType = upstreamType
		}
		if payload.Enabled != nil {
			updated.Enabled = *payload.Enabled
		}
		if payload.Priority != nil {
			updated.Priority = *payload.Priority
		}
		if baseURL := strings.TrimSpace(payload.BaseURL); baseURL != "" {
			updated.BaseURL = baseURL
		}
		updated.ProxyURL = strings.TrimSpace(payload.ProxyURL)
		updated.Notes = strings.TrimSpace(payload.Notes)
		s.items[index] = updated
		s.persistLocked()
		return updated, true
	}

	return Record{}, false
}

func (s *Store) Toggle(id string, enabled bool) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for index, item := range s.items {
		if item.ID != id {
			continue
		}
		s.items[index].Enabled = enabled
		s.persistLocked()
		return s.items[index], true
	}

	return Record{}, false
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for index, item := range s.items {
		if item.ID != id {
			continue
		}
		s.items = append(s.items[:index], s.items[index+1:]...)
		s.persistLocked()
		return true
	}

	return false
}

func (s *Store) RecordFetch(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.Code == code {
			s.items[i].Stats.FetchedCount++
			s.persistLocked()
			return
		}
	}
}

func (s *Store) RecordReport(code string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.Code != code {
			continue
		}
		if success {
			s.items[i].Stats.ReportedSuccessCount++
		} else {
			s.items[i].Stats.ReportedFailureCount++
		}
		s.persistLocked()
		return
	}
}

func defaultNameForType(upstreamType string) string {
	switch upstreamType {
	case "laoqian_worker":
		return "老钱真实上游"
	case "custom_http":
		return "自定义 HTTP 上游"
	default:
		return "本地模拟上游"
	}
}

func newID() string {
	return fmt.Sprintf("%d_%s", time.Now().UTC().UnixNano(), shortID())
}

func shortID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%08x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (s *Store) persistLocked() {
	if s.backend == nil {
		return
	}
	items := make([]Record, len(s.items))
	copy(items, s.items)
	_ = s.backend.SaveUpstreams(items)
}
