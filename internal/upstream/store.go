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
	Token        string    `json:"token,omitempty"`
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
	Token        string `json:"token"`
	Notes        string `json:"notes"`
}

type Stats struct {
	FetchedCount         int `json:"fetched_count"`
	ReportedSuccessCount int `json:"reported_success_count"`
	ReportedFailureCount int `json:"reported_failure_count"`
}

type Store struct {
	mu    sync.RWMutex
	items []Record
}

func NewStore() *Store {
	return &Store{
		items: []Record{
			{
				ID:           newID(),
				Name:         "本地模拟上游",
				Code:         "mock_local",
				UpstreamType: "mock_upstream",
				Enabled:      true,
				Priority:     10,
				BaseURL:      "http://127.0.0.1:8091",
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
				Notes:        "默认联调 Rust 适配器中的老钱 provider",
				CreatedAt:    time.Now().UTC(),
				Stats:        Stats{},
			},
		},
	}
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
		Token:        strings.TrimSpace(payload.Token),
		Notes:        strings.TrimSpace(payload.Notes),
		CreatedAt:    time.Now().UTC(),
		Stats:        Stats{},
	}
	s.items = append(s.items, record)
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
		updated.Token = strings.TrimSpace(payload.Token)
		updated.Notes = strings.TrimSpace(payload.Notes)
		s.items[index] = updated
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
		return true
	}

	return false
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
