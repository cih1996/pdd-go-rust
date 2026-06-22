package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"unified-server/internal/upstream"
)

type adapterUpstreamRecord struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

type adapterUpstreamPayload struct {
	Name         string  `json:"name,omitempty"`
	Code         string  `json:"code,omitempty"`
	UpstreamType string  `json:"upstream_type"`
	Enabled      bool    `json:"enabled"`
	Priority     int     `json:"priority"`
	BaseURL      string  `json:"base_url,omitempty"`
	ProxyURL     string  `json:"proxy_url,omitempty"`
	Notes        *string `json:"notes,omitempty"`
}

func (d RouterDeps) syncAllUpstreamsToAdapter(ctx context.Context) error {
	items := d.Upstream.List()
	adapterItems, err := d.listAdapterUpstreams(ctx)
	if err != nil {
		return err
	}
	byCode := make(map[string]adapterUpstreamRecord, len(adapterItems))
	for _, item := range adapterItems {
		byCode[item.Code] = item
	}
	for _, item := range items {
		payload := buildAdapterUpstreamPayload(item)
		method := http.MethodPost
		url := strings.TrimRight(d.Config.AdapterBaseURL, "/") + "/api/upstreams"
		if remote, ok := byCode[item.Code]; ok {
			method = http.MethodPut
			url = strings.TrimRight(d.Config.AdapterBaseURL, "/") + "/api/upstreams/" + remote.ID
		}
		if err := d.sendAdapterJSON(ctx, method, url, payload); err != nil {
			return fmt.Errorf("sync upstream %s failed: %w", item.Code, err)
		}
	}
	return nil
}

func (d RouterDeps) listAdapterUpstreams(ctx context.Context) ([]adapterUpstreamRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.Config.AdapterBaseURL, "/")+"/api/upstreams", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("adapter upstream list status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var items []adapterUpstreamRecord
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

func (d RouterDeps) sendAdapterJSON(ctx context.Context, method, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func notesPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func buildAdapterUpstreamPayload(item upstream.Record) adapterUpstreamPayload {
	return adapterUpstreamPayload{
		Name:         item.Name,
		Code:         item.Code,
		UpstreamType: item.UpstreamType,
		Enabled:      item.Enabled,
		Priority:     item.Priority,
		BaseURL:      item.BaseURL,
		ProxyURL:     item.ProxyURL,
		Notes:        notesPtr(item.Notes),
	}
}
