package device

import "unified-server/internal/ws"

type Info struct {
    ID        string    `json:"id"`
    Serial    string    `json:"serial"`
    Status    string    `json:"status"`
    Connected bool      `json:"connected"`
    Running   bool      `json:"running"`
    Stats     Stats     `json:"stats"`
    CurrentTask *CurrentTask `json:"current_task,omitempty"`
}

type Service struct { hub *ws.Hub }

func NewService(hub *ws.Hub) *Service { return &Service{hub: hub} }

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
    return []Info{{
        ID:        "emulator-5554",
        Serial:    "emulator-5554",
        Status:    "device",
        Connected: true,
        Running:   false,
        Stats:     Stats{},
        CurrentTask: nil,
    }}
}
