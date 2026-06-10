package account

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"unified-server/internal/upstream"
)

type Stats struct {
	FetchedCount         int `json:"fetched_count"`
	ReportedSuccessCount int `json:"reported_success_count"`
	ReportedFailureCount int `json:"reported_failure_count"`
}

type Record struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	UpstreamCode   string   `json:"upstream_code"`
	UpstreamType   string   `json:"upstream_type"`
	Enabled        bool     `json:"enabled"`
	Notes          string   `json:"notes,omitempty"`
	CreatedAt      string   `json:"created_at"`
	Stats          Stats    `json:"stats"`
	BoundDeviceIDs []string `json:"bound_device_ids"`
	Token          string   `json:"-"`
}

type Backend interface {
	LoadAccounts() ([]Record, error)
	SaveAccounts([]Record) error
}

type Store struct {
	mu      sync.RWMutex
	items   []Record
	backend Backend
}

func NewStore(backend Backend) *Store {
	store := &Store{items: []Record{}, backend: backend}
	if backend != nil {
		if items, err := backend.LoadAccounts(); err == nil {
			store.items = items
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

func (s *Store) Import(upstreamItem upstream.Record, lines string, enabled bool) []Record {
	created := make([]Record, 0)
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := ""
		token := line
		if idx := strings.Index(line, ","); idx >= 0 {
			name = strings.TrimSpace(line[:idx])
			token = strings.TrimSpace(line[idx+1:])
		}
		if token == "" {
			continue
		}
		if name == "" {
			name = token
			if idx := strings.Index(token, "|"); idx >= 0 {
				name = strings.TrimSpace(token[:idx])
			}
		}
		record := Record{
			ID:             newID(),
			Name:           name,
			UpstreamCode:   upstreamItem.Code,
			UpstreamType:   upstreamItem.UpstreamType,
			Enabled:        enabled,
			CreatedAt:      nowString(),
			Stats:          Stats{},
			BoundDeviceIDs: []string{},
			Token:          token,
		}
		s.items = append(s.items, record)
		created = append(created, record)
	}

	s.persistLocked()
	return created
}

func (s *Store) Toggle(accountID string, enabled bool) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != accountID {
			continue
		}
		s.items[i].Enabled = enabled
		s.persistLocked()
		return s.items[i], true
	}
	return Record{}, false
}

func (s *Store) Delete(accountID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != accountID {
			continue
		}
		s.items = append(s.items[:i], s.items[i+1:]...)
		s.persistLocked()
		return true
	}
	return false
}

func (s *Store) FirstEnabledByUpstream(upstreamCode string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if item.Enabled && item.UpstreamCode == upstreamCode {
			return item, true
		}
	}
	return Record{}, false
}

func (s *Store) BindDevice(accountID string, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != accountID {
			continue
		}
		for _, bound := range s.items[i].BoundDeviceIDs {
			if bound == deviceID {
				return
			}
		}
		s.items[i].BoundDeviceIDs = append(s.items[i].BoundDeviceIDs, deviceID)
		s.persistLocked()
		return
	}
}

func (s *Store) UnbindDevice(accountID string, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != accountID {
			continue
		}
		filtered := s.items[i].BoundDeviceIDs[:0]
		for _, bound := range s.items[i].BoundDeviceIDs {
			if bound != deviceID {
				filtered = append(filtered, bound)
			}
		}
		s.items[i].BoundDeviceIDs = filtered
		s.persistLocked()
		return
	}
}

func (s *Store) RecordFetch(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID == accountID {
			s.items[i].Stats.FetchedCount++
			s.persistLocked()
			return
		}
	}
}

func (s *Store) RecordSubmit(accountID string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, item := range s.items {
		if item.ID != accountID {
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

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("acct_%d", time.Now().UTC().UnixNano())
	}
	return "acct_" + hex.EncodeToString(buf)
}

func (s *Store) persistLocked() {
	if s.backend == nil {
		return
	}
	items := make([]Record, len(s.items))
	copy(items, s.items)
	_ = s.backend.SaveAccounts(items)
}
