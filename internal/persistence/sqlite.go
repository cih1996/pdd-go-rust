package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"unified-server/internal/account"
	rt "unified-server/internal/runtime"
	"unified-server/internal/template"
	"unified-server/internal/upstream"
)

type SQLite struct {
	db *sql.DB
}

type columnDef struct {
	Name       string
	Definition string
}

type tableDef struct {
	Name    string
	Columns []columnDef
}

func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	instance := &SQLite{db: db}
	if err := instance.bootstrap(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return instance, nil
}

func (s *SQLite) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLite) bootstrap() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, statement := range pragmas {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	for _, table := range schemaTables() {
		if err := s.ensureTable(table); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) ensureTable(table tableDef) error {
	defs := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		defs = append(defs, column.Name+" "+column.Definition)
	}
	createSQL := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", table.Name, strings.Join(defs, ", "))
	if _, err := s.db.Exec(createSQL); err != nil {
		return err
	}
	existing, err := s.readColumns(table.Name)
	if err != nil {
		return err
	}
	for _, column := range table.Columns {
		if existing[column.Name] {
			continue
		}
		if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table.Name, column.Name, column.Definition)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) readColumns(tableName string) (map[string]bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + tableName + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func (s *SQLite) LoadUpstreams() ([]upstream.Record, error) {
	return loadJSONRows[upstream.Record](s.db, "SELECT payload_json FROM upstream_configs ORDER BY priority ASC, created_at ASC")
}

func (s *SQLite) SaveUpstreams(items []upstream.Record) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM upstream_configs"); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO upstream_configs (
			id, code, name, upstream_type, enabled, priority, base_url, token, notes, created_at, stats_json, payload_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, item := range items {
			statsJSON, err := marshalString(item.Stats)
			if err != nil {
				return err
			}
			payloadJSON, err := marshalString(item)
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(
				item.ID,
				item.Code,
				item.Name,
				item.UpstreamType,
				boolToInt(item.Enabled),
				item.Priority,
				item.BaseURL,
				"",
				item.Notes,
				item.CreatedAt.UTC().Format(time.RFC3339Nano),
				statsJSON,
				payloadJSON,
				nowString(),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLite) LoadAccounts() ([]account.Record, error) {
	rows, err := s.db.Query("SELECT payload_json, token FROM platform_accounts ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]account.Record, 0)
	for rows.Next() {
		var payloadJSON string
		var token string
		if err := rows.Scan(&payloadJSON, &token); err != nil {
			return nil, err
		}
		var item account.Record
		if err := json.Unmarshal([]byte(payloadJSON), &item); err != nil {
			return nil, err
		}
		item.Token = token
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLite) SaveAccounts(items []account.Record) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM platform_accounts"); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO platform_accounts (
			id, name, upstream_code, upstream_type, enabled, notes, created_at, stats_json, bound_device_ids_json, token, payload_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, item := range items {
			statsJSON, err := marshalString(item.Stats)
			if err != nil {
				return err
			}
			boundJSON, err := marshalString(item.BoundDeviceIDs)
			if err != nil {
				return err
			}
			payloadJSON, err := marshalString(item)
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(
				item.ID,
				item.Name,
				item.UpstreamCode,
				item.UpstreamType,
				boolToInt(item.Enabled),
				item.Notes,
				item.CreatedAt,
				statsJSON,
				boundJSON,
				item.Token,
				payloadJSON,
				nowString(),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLite) LoadTemplates() ([]template.Record, error) {
	rows, err := s.db.Query("SELECT payload_json, image_path FROM templates ORDER BY priority ASC, created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]template.Record, 0)
	for rows.Next() {
		var payloadJSON string
		var imagePath string
		if err := rows.Scan(&payloadJSON, &imagePath); err != nil {
			return nil, err
		}
		var item template.Record
		if err := json.Unmarshal([]byte(payloadJSON), &item); err != nil {
			return nil, err
		}
		item.ImagePath = imagePath
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLite) SaveTemplates(items []template.Record) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM templates"); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO templates (
			id, label, template_type, recognition_engine, priority, expected_text, image_name, image_url, image_path, threshold, method, grayscale, crop_json, enabled, created_at, payload_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, item := range items {
			cropJSON, err := marshalString(item.Crop)
			if err != nil {
				return err
			}
			payloadJSON, err := marshalString(item)
			if err != nil {
				return err
			}
			if _, err := stmt.Exec(
				item.ID,
				item.Label,
				item.TemplateType,
				item.RecognitionEngine,
				item.Priority,
				item.ExpectedText,
				item.ImageName,
				item.ImageURL,
				item.ImagePath,
				item.Threshold,
				item.Method,
				boolToInt(item.Grayscale),
				cropJSON,
				boolToInt(item.Enabled),
				item.CreatedAt,
				payloadJSON,
				nowString(),
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLite) LoadRuntimeData() (rt.PersistedData, error) {
	result := rt.PersistedData{
		SystemConfig: rt.SystemConfig{
			OpenURLDelaySeconds:   2,
			ClickImageDelaySecond: 1.2,
			MaxTaskSKUCount:       0,
			UseURLTemplates:       false,
			URLTemplates:          []rt.URLTemplateRecord{},
		},
		EventLog:         []rt.EventRecord{},
		Details:          []rt.DetailRecord{},
		PendingTasks:     []rt.PendingTaskRecord{},
		AdapterSubmitLog: []rt.AdapterSubmitLogRecord{},
	}
	if err := s.loadSingleton("runtime_summary", "summary", &result.Summary); err != nil {
		return result, err
	}
	if err := s.loadSingleton("runtime_system_config", "system_config", &result.SystemConfig); err != nil {
		return result, err
	}
	var err error
	result.EventLog, err = loadJSONRows[rt.EventRecord](s.db, "SELECT payload_json FROM runtime_events ORDER BY timestamp DESC")
	if err != nil {
		return result, err
	}
	result.Details, err = loadJSONRows[rt.DetailRecord](s.db, "SELECT payload_json FROM runtime_details ORDER BY timestamp DESC")
	if err != nil {
		return result, err
	}
	result.PendingTasks, err = loadJSONRows[rt.PendingTaskRecord](s.db, "SELECT payload_json FROM runtime_pending_tasks ORDER BY prefetched_at DESC")
	if err != nil {
		return result, err
	}
	result.AdapterSubmitLog, err = loadJSONRows[rt.AdapterSubmitLogRecord](s.db, "SELECT payload_json FROM runtime_adapter_logs ORDER BY timestamp DESC")
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *SQLite) SaveRuntimeData(data rt.PersistedData) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		if err := saveSingleton(tx, "runtime_summary", "summary", data.Summary); err != nil {
			return err
		}
		if err := saveSingleton(tx, "runtime_system_config", "system_config", data.SystemConfig); err != nil {
			return err
		}
		if err := replaceRuntimeEvents(tx, data.EventLog); err != nil {
			return err
		}
		if err := replaceRuntimeDetails(tx, data.Details); err != nil {
			return err
		}
		if err := replaceRuntimePending(tx, data.PendingTasks); err != nil {
			return err
		}
		if err := replaceRuntimeAdapterLogs(tx, data.AdapterSubmitLog); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLite) SaveSystemConfig(config rt.SystemConfig) error {
	return withTx(s.db, func(tx *sql.Tx) error {
		return saveSingleton(tx, "runtime_system_config", "system_config", config)
	})
}

func (s *SQLite) loadSingleton(tableName string, key string, target any) error {
	var payload string
	err := s.db.QueryRow("SELECT payload_json FROM "+tableName+" WHERE singleton_key = ?", key).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(payload), target)
}

func saveSingleton(tx *sql.Tx, tableName string, key string, value any) error {
	payload, err := marshalString(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		fmt.Sprintf("INSERT INTO %s (singleton_key, payload_json, updated_at) VALUES (?, ?, ?) ON CONFLICT(singleton_key) DO UPDATE SET payload_json = excluded.payload_json, updated_at = excluded.updated_at", tableName),
		key,
		payload,
		nowString(),
	)
	return err
}

func replaceRuntimeEvents(tx *sql.Tx, items []rt.EventRecord) error {
	if _, err := tx.Exec("DELETE FROM runtime_events"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO runtime_events (
		id, timestamp, device_id, level, message, payload_json, payload_raw_json, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		payloadJSON, err := marshalString(item.Payload)
		if err != nil {
			return err
		}
		rawJSON, err := marshalString(item)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(item.ID, item.Timestamp, item.DeviceID, item.Level, item.Message, payloadJSON, rawJSON, nowString()); err != nil {
			return err
		}
	}
	return nil
}

func replaceRuntimeDetails(tx *sql.Tx, items []rt.DetailRecord) error {
	if _, err := tx.Exec("DELETE FROM runtime_details"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO runtime_details (
		id, timestamp, task_id, upstream_task_ref, task_mode, device_id, url, status, recognition, image_count, capture_url, capture_urls_json, message, template_id, template_label, payload_json, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		captureJSON, err := marshalString(item.CaptureURLs)
		if err != nil {
			return err
		}
		payloadJSON, err := marshalString(item)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(
			item.ID,
			item.Timestamp,
			item.TaskID,
			item.UpstreamTaskRef,
			item.TaskMode,
			item.DeviceID,
			item.URL,
			item.Status,
			item.Recognition,
			item.ImageCount,
			item.CaptureURL,
			captureJSON,
			item.Message,
			item.TemplateID,
			item.TemplateLabel,
			payloadJSON,
			nowString(),
		); err != nil {
			return err
		}
	}
	return nil
}

func replaceRuntimePending(tx *sql.Tx, items []rt.PendingTaskRecord) error {
	if _, err := tx.Exec("DELETE FROM runtime_pending_tasks"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO runtime_pending_tasks (
		task_id, upstream_task_ref, source_code, source_name, account_id, account_name, item_count, prefetched_at, payload_json, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		payloadJSON, err := marshalString(item)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(
			item.TaskID,
			item.UpstreamTaskRef,
			item.SourceCode,
			item.SourceName,
			item.AccountID,
			item.AccountName,
			item.ItemCount,
			item.PrefetchedAt,
			payloadJSON,
			nowString(),
		); err != nil {
			return err
		}
	}
	return nil
}

func replaceRuntimeAdapterLogs(tx *sql.Tx, items []rt.AdapterSubmitLogRecord) error {
	if _, err := tx.Exec("DELETE FROM runtime_adapter_logs"); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO runtime_adapter_logs (
		id, timestamp, action, request_method, endpoint, task_id, upstream_task_ref, source_code, device_id, submit_type, request_payload_json, response_status, response_payload_json, error, payload_json, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, item := range items {
		requestJSON, err := marshalString(item.RequestPayload)
		if err != nil {
			return err
		}
		responseJSON, err := marshalString(item.ResponsePayload)
		if err != nil {
			return err
		}
		payloadJSON, err := marshalString(item)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(
			item.ID,
			item.Timestamp,
			item.Action,
			item.RequestMethod,
			item.Endpoint,
			item.TaskID,
			item.UpstreamTaskRef,
			item.SourceCode,
			item.DeviceID,
			item.SubmitType,
			requestJSON,
			item.ResponseStatus,
			responseJSON,
			item.Error,
			payloadJSON,
			nowString(),
		); err != nil {
			return err
		}
	}
	return nil
}

func loadJSONRows[T any](db *sql.DB, query string) ([]T, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]T, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func withTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func marshalString(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func schemaTables() []tableDef {
	return []tableDef{
		{
			Name: "runtime_summary",
			Columns: []columnDef{
				{Name: "singleton_key", Definition: "TEXT PRIMARY KEY"},
				{Name: "total", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "success", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "failure", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "runtime_system_config",
			Columns: []columnDef{
				{Name: "singleton_key", Definition: "TEXT PRIMARY KEY"},
				{Name: "open_url_delay_seconds", Definition: "REAL NOT NULL DEFAULT 2"},
				{Name: "click_image_delay_seconds", Definition: "REAL NOT NULL DEFAULT 1.2"},
				{Name: "max_task_sku_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "use_url_templates", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "url_templates_json", Definition: "TEXT NOT NULL DEFAULT '[]'"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "runtime_events",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "timestamp", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "device_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "level", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "message", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "payload_raw_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "runtime_details",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "timestamp", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "task_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "upstream_task_ref", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "task_mode", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "device_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "url", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "status", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "recognition", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "image_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "capture_url", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "capture_urls_json", Definition: "TEXT NOT NULL DEFAULT '[]'"},
				{Name: "message", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "template_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "template_label", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "runtime_pending_tasks",
			Columns: []columnDef{
				{Name: "task_id", Definition: "TEXT PRIMARY KEY"},
				{Name: "upstream_task_ref", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "source_code", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "source_name", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "account_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "account_name", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "item_count", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "prefetched_at", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "runtime_adapter_logs",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "timestamp", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "action", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "request_method", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "endpoint", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "task_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "upstream_task_ref", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "source_code", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "device_id", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "submit_type", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "request_payload_json", Definition: "TEXT NOT NULL DEFAULT 'null'"},
				{Name: "response_status", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "response_payload_json", Definition: "TEXT NOT NULL DEFAULT 'null'"},
				{Name: "error", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "upstream_configs",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "code", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "name", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "upstream_type", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "enabled", Definition: "INTEGER NOT NULL DEFAULT 1"},
				{Name: "priority", Definition: "INTEGER NOT NULL DEFAULT 100"},
				{Name: "base_url", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "token", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "notes", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "stats_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "platform_accounts",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "name", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "upstream_code", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "upstream_type", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "enabled", Definition: "INTEGER NOT NULL DEFAULT 1"},
				{Name: "notes", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "stats_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "bound_device_ids_json", Definition: "TEXT NOT NULL DEFAULT '[]'"},
				{Name: "token", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
		{
			Name: "templates",
			Columns: []columnDef{
				{Name: "id", Definition: "TEXT PRIMARY KEY"},
				{Name: "label", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "template_type", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "recognition_engine", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "priority", Definition: "INTEGER NOT NULL DEFAULT 100"},
				{Name: "expected_text", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "image_name", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "image_url", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "image_path", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "threshold", Definition: "REAL NOT NULL DEFAULT 0.8"},
				{Name: "method", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "grayscale", Definition: "INTEGER NOT NULL DEFAULT 0"},
				{Name: "crop_json", Definition: "TEXT NOT NULL DEFAULT 'null'"},
				{Name: "enabled", Definition: "INTEGER NOT NULL DEFAULT 1"},
				{Name: "created_at", Definition: "TEXT NOT NULL DEFAULT ''"},
				{Name: "payload_json", Definition: "TEXT NOT NULL DEFAULT '{}'"},
				{Name: "updated_at", Definition: "TEXT NOT NULL DEFAULT ''"},
			},
		},
	}
}
