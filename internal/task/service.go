package task

import (
    "unified-server/internal/device"
    "unified-server/internal/template"
    "unified-server/internal/vision"
    "unified-server/internal/ws"
)

type Service struct {
    hub     *ws.Hub
    tpl     *template.Store
    vision  *vision.Engine
    devices *device.Service
}

func NewService(hub *ws.Hub, tpl *template.Store, visionEngine *vision.Engine, devices *device.Service) *Service {
    return &Service{hub: hub, tpl: tpl, vision: visionEngine, devices: devices}
}

func (s *Service) RuntimePlan() map[string]any {
    return map[string]any{
        "adapter_mode": "standalone rust service",
        "ws_push": true,
        "vision": s.vision.Plan(),
        "template_total": s.tpl.Count(),
        "device_total": len(s.devices.List()),
    }
}