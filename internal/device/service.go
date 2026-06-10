package device

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"unified-server/internal/ws"
)

type Info struct {
	ID          string       `json:"id"`
	Serial      string       `json:"serial"`
	Status      string       `json:"status"`
	Connected   bool         `json:"connected"`
	Running     bool         `json:"running"`
	Stats       Stats        `json:"stats"`
	CurrentTask *CurrentTask `json:"current_task,omitempty"`
}

type Service struct {
	hub    *ws.Hub
	adbBin string
	mu     sync.RWMutex
	items  map[string]Info
}

func NewService(hub *ws.Hub, adbBin string) *Service {
	return &Service{
		hub:    hub,
		adbBin: adbBin,
		items:  map[string]Info{},
	}
}

type Stats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failure int `json:"failure"`
}

type CurrentTask struct {
	TaskID              string `json:"task_id"`
	TaskMode            string `json:"task_mode"`
	StartedAt           string `json:"started_at"`
	LoopCount           int    `json:"loop_count"`
	CurrentStage        string `json:"current_stage"`
	CurrentMessage      string `json:"current_message"`
	LastMatchedTemplate string `json:"last_matched_template,omitempty"`
	ClickCaptureURL     string `json:"click_capture_url,omitempty"`
}

func (s *Service) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Info, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item)
	}
	return result
}

func (s *Service) Scan(ctx context.Context) ([]Info, error) {
	output, err := s.runADB(ctx, "devices")
	if err != nil {
		return s.List(), err
	}

	next := map[string]Info{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices attached") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		serial := fields[0]
		status := fields[1]
		info := s.get(serial)
		info.ID = serial
		info.Serial = serial
		info.Status = status
		info.Connected = status == "device"
		next[serial] = info
	}

	s.mu.Lock()
	s.items = next
	s.mu.Unlock()
	return s.List(), nil
}

func (s *Service) Connect(ctx context.Context, endpoint string) (Info, error) {
	if strings.TrimSpace(endpoint) == "" {
		return Info{}, fmt.Errorf("endpoint is required")
	}
	if _, err := s.runADB(ctx, "connect", endpoint); err != nil {
		return Info{}, err
	}
	if _, err := s.Scan(ctx); err != nil {
		return Info{}, err
	}
	item := s.get(endpoint)
	if item.ID == "" {
		item = s.get(strings.TrimSuffix(endpoint, ":5555"))
	}
	return item, nil
}

func (s *Service) OpenURL(ctx context.Context, serial string, rawURL string) error {
	_, err := s.runADB(ctx, "-s", serial, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", rawURL)
	return err
}

func (s *Service) Tap(ctx context.Context, serial string, x int, y int) error {
	_, err := s.runADB(ctx, "-s", serial, "shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	return err
}

func (s *Service) Capture(ctx context.Context, serial string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.adbBin, "-s", serial, "exec-out", "screencap", "-p")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	// exec-out returns raw PNG bytes; normalizing CRLF would corrupt the image.
	return stdout.Bytes(), nil
}

func (s *Service) SetCurrentTask(serial string, task *CurrentTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[serial]
	item.ID = serial
	item.Serial = serial
	item.Connected = item.Status == "device" || item.Connected
	item.Running = task != nil
	item.CurrentTask = task
	s.items[serial] = item
}

func (s *Service) RecordResult(serial string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[serial]
	item.Stats.Total++
	if success {
		item.Stats.Success++
	} else {
		item.Stats.Failure++
	}
	s.items[serial] = item
}

func (s *Service) UpdateCurrentTask(serial string, mutate func(*CurrentTask)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[serial]
	if item.CurrentTask == nil {
		return
	}
	mutate(item.CurrentTask)
	s.items[serial] = item
}

func (s *Service) get(serial string) Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.items[serial]
}

func (s *Service) runADB(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, s.adbBin, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
