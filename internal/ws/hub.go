package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type IncomingMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Hub struct {
	mu        sync.RWMutex
	clients   map[*client]struct{}
	byID      map[string]*client
	onMessage func(clientID string, message IncomingMessage)
}

type client struct {
	id   string
	conn net.Conn
	mu   sync.Mutex
}

func NewHub() *Hub {
	return &Hub{
		clients: map[*client]struct{}{},
		byID:    map[string]*client{},
	}
}

func (h *Hub) Run(ctx context.Context) { <-ctx.Done() }

func (h *Hub) SetMessageHandler(handler func(clientID string, message IncomingMessage)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onMessage = handler
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "upgrade required", http.StatusUpgradeRequired)
		return
	}

	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing websocket key", http.StatusBadRequest)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket hijack not supported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	accept := websocketAccept(key)
	if _, err := rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	client := &client{id: newClientID(), conn: conn}
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.byID[client.id] = client
	h.mu.Unlock()

	go h.readLoop(client)
}

func (h *Hub) Broadcast(event Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	h.mu.RLock()
	clients := make([]*client, 0, len(h.clients))
	for item := range h.clients {
		clients = append(clients, item)
	}
	h.mu.RUnlock()

	for _, item := range clients {
		if err := item.writeText(payload); err != nil {
			h.removeClient(item)
		}
	}
}

func (h *Hub) SendTo(clientID string, event Event) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	h.mu.RLock()
	client := h.byID[clientID]
	h.mu.RUnlock()
	if client == nil {
		return false
	}
	if err := client.writeText(payload); err != nil {
		h.removeClient(client)
		return false
	}
	return true
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) readLoop(c *client) {
	defer h.removeClient(c)
	reader := bufio.NewReader(c.conn)
	for {
		header, err := reader.ReadByte()
		if err != nil {
			return
		}
		opcode := header & 0x0F
		maskAndLen, err := reader.ReadByte()
		if err != nil {
			return
		}
		payloadLen := int(maskAndLen & 0x7F)
		switch payloadLen {
		case 126:
			var length uint16
			if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
				return
			}
			payloadLen = int(length)
		case 127:
			var length uint64
			if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
				return
			}
			payloadLen = int(length)
		}
		if maskAndLen&0x80 != 0 {
			mask := make([]byte, 4)
			if _, err := io.ReadFull(reader, mask); err != nil {
				return
			}
			payload := make([]byte, payloadLen)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return
			}
			for i := 0; i < payloadLen; i++ {
				payload[i] ^= mask[i%4]
			}
			if !h.handlePayload(c, opcode, payload) {
				return
			}
			continue
		}
		if payloadLen > 0 {
			payload := make([]byte, payloadLen)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return
			}
			if !h.handlePayload(c, opcode, payload) {
				return
			}
		} else if !h.handlePayload(c, opcode, nil) {
			return
		}
	}
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		delete(h.byID, c.id)
		_ = c.conn.Close()
	}
}

func (h *Hub) handlePayload(c *client, opcode byte, payload []byte) bool {
	switch opcode {
	case 0x8:
		return false
	case 0x9:
		_ = c.writeControl(0xA, payload)
		return true
	case 0x1:
		var message IncomingMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return true
		}
		h.mu.RLock()
		handler := h.onMessage
		h.mu.RUnlock()
		if handler != nil {
			go handler(c.id, message)
		}
		return true
	default:
		return true
	}
}

func (c *client) writeText(payload []byte) error {
	return c.writeFrame(0x81, payload)
}

func (c *client) writeControl(opcode byte, payload []byte) error {
	return c.writeFrame(0x80|opcode, payload)
}

func (c *client) writeFrame(firstByte byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	header := []byte{firstByte}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(length))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(length))
	}

	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

func newClientID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "ws_client"
	}
	return "ws_" + hex.EncodeToString(buf)
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}
