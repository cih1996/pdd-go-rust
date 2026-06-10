package upstream

import (
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Record struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Code         string    `json:"code"`
	UpstreamType string    `json:"upstream_type"`
	Enabled      bool      `json:"enabled"`
	Priority     int       `json:"priority"`
	BaseURL      string    `json:"base_url"`
	Notes        string    `json:"notes,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type UpsertRequest struct {
	Name         string `json:"name"`
	Code         string `json:"code"`
	UpstreamType string `json:"upstream_type"`
	Enabled      *bool  `json:"enabled"`
	Priority     *int   `json:"priority"`
	BaseURL      string `json:"base_url"`
	Notes        string `json:"notes"`
}

type Store struct {
	mu    sync.RWMutex
	items []Record
}

func NewStore() *Store {
	return &Store{
		items: []Record{
			{
				ID:           uuid.NewString(),
				Name:         "本地模拟上游",
				Code:         "mock_local",
				UpstreamType: "mock_upstream",
				Enabled:      true,
				Priority:     10,
				BaseURL:      "http://127.0.0.1:8091",
				Notes:        "默认联调 Rust 适配器中的本地模拟 provider",
				CreatedAt:    time.Now().UTC(),
			},
			{
				ID:           uuid.NewString(),
				Name:         "老钱真实上游",
				Code:         "laoqian_worker",
				UpstreamType: "laoqian_worker",
				Enabled:      true,
				Priority:     20,
				BaseURL:      "http://127.0.0.1:8091",
				Notes:        "默认联调 Rust 适配器中的老钱 provider",
				CreatedAt:    time.Now().UTC(),
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
		code = upstreamType + "_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
		ID:           uuid.NewString(),
		Name:         name,
		Code:         code,
		UpstreamType: upstreamType,
		Enabled:      enabled,
		Priority:     priority,
		BaseURL:      baseURL,
		Notes:        strings.TrimSpace(payload.Notes),
		CreatedAt:    time.Now().UTC(),
	}
	s.items = append(s.items, record)
	return record
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
