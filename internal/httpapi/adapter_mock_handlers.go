package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (d RouterDeps) handleImportMockData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var payload struct {
		Lines           string `json:"lines"`
		ReplaceExisting bool   `json:"replace_existing"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid mock data payload"})
		return
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(d.Config.AdapterBaseURL, "/")+"/api/mock-data/import", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("mock data import failed: %s", err.Error())})
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": string(bytes.TrimSpace(raw))})
		return
	}
	if len(raw) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"message": "mock data imported"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}
