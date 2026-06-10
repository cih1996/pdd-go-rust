package device

import "unified-server/internal/ws"

type Info struct {
    ID     string `json:"id"`
    Status string `json:"status"`
}

type Service struct { hub *ws.Hub }

func NewService(hub *ws.Hub) *Service { return &Service{hub: hub} }

func (s *Service) List() []Info { return []Info{{ID: "emulator-5554", Status: "idle"}} }