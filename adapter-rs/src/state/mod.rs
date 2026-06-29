use std::collections::{HashMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock};

use reqwest::Client;
use serde_json::{json, Value};
use uuid::Uuid;

use crate::models::{
    AdapterLog, AdapterSnapshot, AdapterStateResponse, AdapterSummary, CaptureUploadResponse,
    IssuedTaskContext, MockDataStatus, UpstreamConfig, UpstreamInput, UpstreamStats, UpstreamType,
};

const LOG_LIMIT: usize = 120;
const REPORT_LIMIT: usize = 120;
const SNAPSHOT_LIMIT: usize = 200;

#[derive(Default)]
pub struct AdapterRuntime {
    pub upstreams: Vec<UpstreamConfig>,
    pub mock_records: Vec<Value>,
    pub laoqian_seen_goods_ids: HashSet<String>,
    pub laoqian_blocked_goods_ids: HashSet<String>,
    pub recent_logs: Vec<AdapterLog>,
    pub recent_reports: Vec<Value>,
    pub recent_snapshots: Vec<AdapterSnapshot>,
    pub issued_tasks: HashMap<String, IssuedTaskContext>,
    pub captures: HashMap<String, StoredCapture>,
    pub mock_imported_total: usize,
    pub mock_consumed_total: usize,
}

#[derive(Debug, Clone)]
pub struct StoredCapture {
    pub file_name: String,
    pub path: PathBuf,
}

#[derive(Clone)]
pub struct AppState {
    pub service_name: String,
    pub client: Client,
    pub runtime: Arc<RwLock<AdapterRuntime>>,
    pub sequence: Arc<AtomicU64>,
}

impl AppState {
    pub fn new() -> Self {
        let client = Client::builder()
            .danger_accept_invalid_certs(true)
            .build()
            .expect("failed to create reqwest client");

        let runtime = AdapterRuntime {
            upstreams: vec![default_mock_upstream(), default_laoqian_upstream()],
            mock_records: Vec::new(),
            laoqian_seen_goods_ids: HashSet::new(),
            laoqian_blocked_goods_ids: HashSet::new(),
            recent_logs: Vec::new(),
            recent_reports: Vec::new(),
            recent_snapshots: Vec::new(),
            issued_tasks: HashMap::new(),
            captures: HashMap::new(),
            mock_imported_total: 0,
            mock_consumed_total: 0,
        };

        let state = Self {
            service_name: "adapter-rs".to_string(),
            client,
            runtime: Arc::new(RwLock::new(runtime)),
            sequence: Arc::new(AtomicU64::new(1)),
        };
        state.add_log_simple("adapter-rs 已初始化，已内置本地模拟与老钱上游");
        state
    }

    pub fn next_id(&self, prefix: &str) -> String {
        let value = self.sequence.fetch_add(1, Ordering::SeqCst);
        format!("{prefix}-{value}")
    }

    pub fn state_response(&self) -> AdapterStateResponse {
        let runtime = self.runtime.read().expect("runtime poisoned");
        AdapterStateResponse {
            upstreams: runtime.upstreams.clone(),
            recent_logs: runtime.recent_logs.clone(),
            recent_reports: runtime.recent_reports.clone(),
            mock_data_status: MockDataStatus {
                imported_total: runtime.mock_imported_total,
                remaining_total: runtime.mock_records.len(),
                consumed_total: runtime.mock_consumed_total,
            },
            recent_snapshots: runtime.recent_snapshots.clone(),
            summary: AdapterSummary {
                total_upstreams: runtime.upstreams.len(),
                enabled_upstreams: runtime.upstreams.iter().filter(|item| item.enabled).count(),
                recent_reports: runtime.recent_reports.len(),
            },
        }
    }

    pub fn list_upstreams(&self) -> Vec<UpstreamConfig> {
        self.runtime
            .read()
            .expect("runtime poisoned")
            .upstreams
            .clone()
    }

    pub fn import_mock_records(
        &self,
        records: Vec<Value>,
        replace_existing: bool,
    ) -> (usize, usize) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if replace_existing {
            runtime.mock_records.clear();
            runtime.mock_consumed_total = 0;
        }
        let imported_count = records.len();
        runtime.mock_records.extend(records);
        runtime.mock_imported_total += imported_count;
        let total_count = runtime.mock_records.len();
        drop(runtime);
        self.add_log(
            "已导入本地模拟数据",
            json!({"imported_count": imported_count, "total_count": total_count, "replace_existing": replace_existing}),
        );
        self.add_snapshot(
            "import_mock_data",
            "success",
            Some("mock_local"),
            None,
            None,
            Some("已导入本地模拟数据"),
            json!({"imported_count": imported_count, "total_count": total_count, "replace_existing": replace_existing}),
        );
        (imported_count, total_count)
    }

    pub fn take_mock_record(&self) -> Option<Value> {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if runtime.mock_records.is_empty() {
            return None;
        }
        runtime.mock_consumed_total += 1;
        Some(runtime.mock_records.remove(0))
    }

    pub fn has_seen_laoqian_goods(&self, goods_id: &str) -> bool {
        self.runtime
            .read()
            .expect("runtime poisoned")
            .laoqian_seen_goods_ids
            .contains(goods_id)
    }

    pub fn remember_laoqian_goods(&self, goods_id: &str) {
        if goods_id.trim().is_empty() {
            return;
        }
        self.runtime
            .write()
            .expect("runtime poisoned")
            .laoqian_seen_goods_ids
            .insert(goods_id.trim().to_string());
    }

    pub fn is_laoqian_goods_blocked(&self, goods_id: &str) -> bool {
        self.runtime
            .read()
            .expect("runtime poisoned")
            .laoqian_blocked_goods_ids
            .contains(goods_id)
    }

    pub fn block_laoqian_goods(&self, goods_id: &str, reason: &str) {
        let goods_id = goods_id.trim();
        if goods_id.is_empty() {
            return;
        }
        let inserted = self
            .runtime
            .write()
            .expect("runtime poisoned")
            .laoqian_blocked_goods_ids
            .insert(goods_id.to_string());
        if inserted {
            self.add_log(
                "已拉黑老钱 goods_id",
                json!({"goods_id": goods_id, "reason": reason}),
            );
        }
    }

    pub fn create_upstream(&self, payload: UpstreamInput) -> UpstreamConfig {
        let upstream = build_upstream_config(payload, None);
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime.upstreams.push(upstream.clone());
        drop(runtime);
        self.add_log(
            "已创建上游配置",
            json!({"code": upstream.code, "upstream_type": upstream.upstream_type, "base_url": upstream.base_url}),
        );
        upstream
    }

    pub fn update_upstream(
        &self,
        upstream_id: &str,
        payload: UpstreamInput,
    ) -> Result<UpstreamConfig, String> {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        let index = runtime
            .upstreams
            .iter()
            .position(|item| item.id == upstream_id)
            .ok_or_else(|| "上游不存在".to_string())?;
        let current = runtime.upstreams[index].clone();
        let updated = build_upstream_config(payload, Some(&current));
        runtime.upstreams[index] = updated.clone();
        drop(runtime);
        self.add_log(
            "已更新上游配置",
            json!({"code": updated.code, "base_url": updated.base_url}),
        );
        Ok(updated)
    }

    pub fn toggle_upstream(
        &self,
        upstream_id: &str,
        enabled: bool,
    ) -> Result<UpstreamConfig, String> {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        let upstream = runtime
            .upstreams
            .iter_mut()
            .find(|item| item.id == upstream_id)
            .ok_or_else(|| "上游不存在".to_string())?;
        upstream.enabled = enabled;
        let updated = upstream.clone();
        drop(runtime);
        self.add_log(
            "已切换上游启用状态",
            json!({"code": updated.code, "enabled": enabled}),
        );
        Ok(updated)
    }

    pub fn delete_upstream(&self, upstream_id: &str) -> Result<Value, String> {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        let index = runtime
            .upstreams
            .iter()
            .position(|item| item.id == upstream_id)
            .ok_or_else(|| "上游不存在".to_string())?;
        let removed = runtime.upstreams.remove(index);
        drop(runtime);
        self.add_log("已删除上游配置", json!({"code": removed.code}));
        Ok(json!({"message": "上游已删除"}))
    }

    pub fn issue_task(&self, context: IssuedTaskContext) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime
            .issued_tasks
            .insert(context.task.task_id.clone(), context.clone());
    }

    pub fn take_issued_task(&self, task_id: &str) -> Option<IssuedTaskContext> {
        self.runtime
            .write()
            .expect("runtime poisoned")
            .issued_tasks
            .remove(task_id)
    }

    pub fn push_report(&self, payload: Value) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime.recent_reports.insert(0, payload);
        runtime.recent_reports.truncate(REPORT_LIMIT);
    }

    pub fn add_snapshot(
        &self,
        action: &str,
        status: &str,
        source_code: Option<&str>,
        task_id: Option<&str>,
        upstream_task_ref: Option<&str>,
        message: Option<&str>,
        payload: Value,
    ) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime.recent_snapshots.insert(
            0,
            AdapterSnapshot {
                id: Uuid::new_v4().simple().to_string(),
                timestamp: now_string(),
                action: action.to_string(),
                status: status.to_string(),
                source_code: source_code.map(|value| value.to_string()),
                task_id: task_id.map(|value| value.to_string()),
                upstream_task_ref: upstream_task_ref.map(|value| value.to_string()),
                message: message.map(|value| value.to_string()),
                payload,
            },
        );
        runtime.recent_snapshots.truncate(SNAPSHOT_LIMIT);
    }

    pub fn record_fetch(&self, upstream_code: &str) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if let Some(upstream) = runtime
            .upstreams
            .iter_mut()
            .find(|item| item.code == upstream_code)
        {
            upstream.stats.fetched_count += 1;
        }
    }

    pub fn record_report(&self, upstream_code: &str, success: bool) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if let Some(upstream) = runtime
            .upstreams
            .iter_mut()
            .find(|item| item.code == upstream_code)
        {
            if success {
                upstream.stats.reported_success_count += 1;
            } else {
                upstream.stats.reported_failure_count += 1;
            }
        }
    }

    pub fn add_log_simple(&self, message: &str) {
        self.add_log(message, Value::Null);
    }

    pub fn add_log(&self, message: &str, payload: Value) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime.recent_logs.insert(
            0,
            AdapterLog {
                id: Uuid::new_v4().simple().to_string(),
                timestamp: now_string(),
                level: "info".to_string(),
                message: message.to_string(),
                payload,
            },
        );
        runtime.recent_logs.truncate(LOG_LIMIT);
    }

    pub fn store_capture(
        &self,
        file_name: String,
        bytes: Vec<u8>,
    ) -> Result<CaptureUploadResponse, String> {
        let capture_id = self.next_id("capture");
        let dir = capture_storage_dir();
        fs::create_dir_all(&dir).map_err(|err| format!("创建截图目录失败: {err}"))?;
        let path = dir.join(format!("{capture_id}_{}", sanitize_file_name(&file_name)));
        fs::write(&path, &bytes).map_err(|err| format!("写入截图失败: {err}"))?;

        let response = CaptureUploadResponse {
            capture_id: capture_id.clone(),
            capture_url: format!("http://127.0.0.1:8091/api/captures/{capture_id}"),
            file_name: file_name.clone(),
            size: bytes.len(),
        };
        let stored = StoredCapture { file_name, path };

        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime.captures.insert(capture_id, stored);
        drop(runtime);
        Ok(response)
    }

    pub fn get_capture(&self, capture_id: &str) -> Option<StoredCapture> {
        if let Some(capture) = self
            .runtime
            .read()
            .expect("runtime poisoned")
            .captures
            .get(capture_id)
            .cloned()
        {
            return Some(capture);
        }

        let restored = restore_capture_from_disk(capture_id)?;
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        runtime
            .captures
            .insert(capture_id.to_string(), restored.clone());
        Some(restored)
    }
}

fn default_mock_upstream() -> UpstreamConfig {
    UpstreamConfig {
        id: "mock-local".to_string(),
        name: "本地模拟上游".to_string(),
        code: "mock_local".to_string(),
        upstream_type: UpstreamType::MockUpstream,
        enabled: true,
        priority: 10,
        base_url: "".to_string(),
        proxy_url: None,
        fetch_path: None,
        report_success_path: None,
        report_failure_path: None,
        headers: HashMap::new(),
        notes: Some("默认走适配器内置本地模拟任务，可改成旧模拟服务地址".to_string()),
        created_at: now_string(),
        stats: UpstreamStats::default(),
    }
}

fn default_laoqian_upstream() -> UpstreamConfig {
    UpstreamConfig {
        id: "laoqian-worker".to_string(),
        name: "老钱真实上游".to_string(),
        code: "laoqian_worker".to_string(),
        upstream_type: UpstreamType::LaoqianWorker,
        enabled: true,
        priority: 20,
        base_url: "https://frontend.yqlaoqian111.com".to_string(),
        proxy_url: None,
        fetch_path: Some("/api/item/fetch".to_string()),
        report_success_path: Some("/api/item/work".to_string()),
        report_failure_path: Some("/api/item/drop".to_string()),
        headers: HashMap::new(),
        notes: Some("首版保留老钱 provider 与接口骨架".to_string()),
        created_at: now_string(),
        stats: UpstreamStats::default(),
    }
}

fn build_upstream_config(
    payload: UpstreamInput,
    current: Option<&UpstreamConfig>,
) -> UpstreamConfig {
    let (default_name, default_code, fetch_path, success_path, failure_path, default_base_url) =
        match payload.upstream_type {
            UpstreamType::MockUpstream => (
                "本地模拟上游",
                "mock_upstream",
                None,
                None,
                None,
                "".to_string(),
            ),
            UpstreamType::LaoqianWorker => (
                "老钱真实上游",
                "laoqian_worker",
                Some("/api/item/fetch".to_string()),
                Some("/api/item/work".to_string()),
                Some("/api/item/drop".to_string()),
                "https://frontend.yqlaoqian111.com".to_string(),
            ),
            UpstreamType::CustomHttp => (
                "自定义 HTTP 上游",
                "custom_http",
                None,
                None,
                None,
                "".to_string(),
            ),
        };

    let code = normalize_text(payload.code)
        .or_else(|| current.map(|item| item.code.clone()))
        .unwrap_or_else(|| format!("{default_code}_{}", Uuid::new_v4().simple()));
    UpstreamConfig {
        id: current
            .map(|item| item.id.clone())
            .unwrap_or_else(|| Uuid::new_v4().simple().to_string()),
        name: payload
            .name
            .clone()
            .and_then(|item| normalize_text(Some(item)))
            .unwrap_or_else(|| default_name.to_string()),
        code,
        upstream_type: payload.upstream_type,
        enabled: payload.enabled,
        priority: payload.priority,
        base_url: normalize_text(payload.base_url)
            .or_else(|| {
                current
                    .map(|item| item.base_url.clone())
                    .filter(|item| !item.is_empty())
            })
            .unwrap_or(default_base_url),
        proxy_url: match payload.proxy_url {
            Some(item) => normalize_text(Some(item)),
            None => current.and_then(|item| item.proxy_url.clone()),
        },
        fetch_path: normalize_text(payload.fetch_path)
            .or_else(|| current.and_then(|item| item.fetch_path.clone()))
            .or(fetch_path),
        report_success_path: normalize_text(payload.report_success_path)
            .or_else(|| current.and_then(|item| item.report_success_path.clone()))
            .or(success_path),
        report_failure_path: normalize_text(payload.report_failure_path)
            .or_else(|| current.and_then(|item| item.report_failure_path.clone()))
            .or(failure_path),
        headers: if payload.headers.is_empty() {
            current.map(|item| item.headers.clone()).unwrap_or_default()
        } else {
            payload.headers
        },
        notes: payload
            .notes
            .and_then(|item| normalize_text(Some(item)))
            .or_else(|| current.and_then(|item| item.notes.clone())),
        created_at: current
            .map(|item| item.created_at.clone())
            .unwrap_or_else(now_string),
        stats: UpstreamStats::default(),
    }
}

pub fn now_string() -> String {
    chrono::Utc::now().to_rfc3339()
}

fn normalize_text(value: Option<String>) -> Option<String> {
    value
        .map(|item| item.trim().to_string())
        .filter(|item| !item.is_empty())
}

fn sanitize_file_name(input: &str) -> String {
    input
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || matches!(ch, '.' | '_' | '-') {
                ch
            } else {
                '_'
            }
        })
        .collect()
}

fn capture_storage_dir() -> PathBuf {
    if let Ok(value) = std::env::var("ADAPTER_CAPTURE_DIR") {
        let trimmed = value.trim();
        if !trimmed.is_empty() {
            return PathBuf::from(trimmed);
        }
    }

    if let Some(base_dir) = default_app_data_dir() {
        return base_dir.join("adapter-rs").join("captures");
    }

    std::env::temp_dir().join("adapter-rs-captures")
}

fn restore_capture_from_disk(capture_id: &str) -> Option<StoredCapture> {
    for dir in capture_search_dirs() {
        let Ok(entries) = fs::read_dir(&dir) else {
            continue;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if !path.is_file() {
                continue;
            }
            let Some(file_name) = file_name_string(&path) else {
                continue;
            };
            let prefix = format!("{capture_id}_");
            if !file_name.starts_with(&prefix) {
                continue;
            }
            let original_name = file_name
                .split_once('_')
                .map(|(_, rest)| rest.to_string())
                .filter(|value| !value.is_empty())
                .unwrap_or_else(|| file_name.clone());
            return Some(StoredCapture {
                file_name: original_name,
                path,
            });
        }
    }
    None
}

fn file_name_string(path: &Path) -> Option<String> {
    path.file_name()
        .and_then(|value| value.to_str())
        .map(|value| value.to_string())
}

fn capture_search_dirs() -> Vec<PathBuf> {
    let mut dirs = vec![capture_storage_dir()];
    if let Ok(current_dir) = std::env::current_dir() {
        dirs.push(current_dir.join(".runtime").join("captures"));
    }
    let legacy_dir = std::env::temp_dir().join("adapter-rs-captures");
    if !dirs.iter().any(|item| item == &legacy_dir) {
        dirs.push(legacy_dir);
    }
    dirs
}

fn default_app_data_dir() -> Option<PathBuf> {
    for key in ["LOCALAPPDATA", "APPDATA"] {
        if let Ok(value) = std::env::var(key) {
            let trimmed = value.trim();
            if !trimmed.is_empty() {
                return Some(PathBuf::from(trimmed).join("PddGoRust"));
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::{capture_storage_dir, restore_capture_from_disk, sanitize_file_name};
    use std::fs;

    #[test]
    fn restore_capture_from_disk_finds_existing_capture_file() {
        let dir = std::env::temp_dir().join(format!(
            "adapter-rs-capture-test-{}",
            uuid::Uuid::new_v4().simple()
        ));
        std::env::set_var("ADAPTER_CAPTURE_DIR", &dir);
        fs::create_dir_all(&dir).expect("create capture dir");
        let capture_id = "capture-123";
        let file_name = "foo bar.png";
        let stored_path = dir.join(format!("{capture_id}_{}", sanitize_file_name(file_name)));
        fs::write(&stored_path, b"test-bytes").expect("write capture");

        let restored = restore_capture_from_disk(capture_id).expect("restore capture from disk");
        assert_eq!(restored.path, stored_path);
        assert_eq!(restored.file_name, sanitize_file_name(file_name));

        fs::remove_dir_all(capture_storage_dir()).ok();
        std::env::remove_var("ADAPTER_CAPTURE_DIR");
    }
}
