package ws

import (
    "context"
    "encoding/json"
    "net/http"
    "sync"
)

type Event struct {
    Type string         `json:"type"`
    Data map[string]any `json:"data"`
}

type Hub struct {
    mu      sync.RWMutex
    clients int
}

func NewHub() *Hub { return &Hub{} }

func (h *Hub) Run(ctx context.Context) { <-ctx.Done() }

func (h *Hub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
    h.mu.Lock()
    h.clients++
    count := h.clients
    h.mu.Unlock()

    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{
        "type": "ws.placeholder",
        "data": map[string]any{
            "message": "replace with real websocket upgrader implementation",
            "connected_clients": count,
        },
    })

    h.mu.Lock()
    h.clients--
    h.mu.Unlock()
}

func (h *Hub) Broadcast(_ Event) {}

func (h *Hub) ClientCount() int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    return h.clients
}