use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock};

use reqwest::Client;
use serde_json::{json, Value};
use uuid::Uuid;

use crate::models::{
    AdapterLog, AdapterStateResponse, AdapterSummary, ClientTask, IssuedTaskContext, UpstreamConfig,
    UpstreamInput, UpstreamStats, UpstreamType,
};

const LOG_LIMIT: usize = 120;
const REPORT_LIMIT: usize = 120;

#[derive(Default)]
pub struct AdapterRuntime {
    pub upstreams: Vec<UpstreamConfig>,
    pub recent_logs: Vec<AdapterLog>,
    pub recent_reports: Vec<Value>,
    pub issued_tasks: HashMap<String, IssuedTaskContext>,
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
            recent_logs: Vec::new(),
            recent_reports: Vec::new(),
            issued_tasks: HashMap::new(),
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
            summary: AdapterSummary {
                total_upstreams: runtime.upstreams.len(),
                enabled_upstreams: runtime.upstreams.iter().filter(|item| item.enabled).count(),
                recent_reports: runtime.recent_reports.len(),
            },
        }
    }

    pub fn list_upstreams(&self) -> Vec<UpstreamConfig> {
        self.runtime.read().expect("runtime poisoned").upstreams.clone()
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

    pub fn update_upstream(&self, upstream_id: &str, payload: UpstreamInput) -> Result<UpstreamConfig, String> {
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
        self.add_log("已更新上游配置", json!({"code": updated.code, "base_url": updated.base_url}));
        Ok(updated)
    }

    pub fn toggle_upstream(&self, upstream_id: &str, enabled: bool) -> Result<UpstreamConfig, String> {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        let upstream = runtime
            .upstreams
            .iter_mut()
            .find(|item| item.id == upstream_id)
            .ok_or_else(|| "上游不存在".to_string())?;
        upstream.enabled = enabled;
        let updated = upstream.clone();
        drop(runtime);
        self.add_log("已切换上游启用状态", json!({"code": updated.code, "enabled": enabled}));
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

    pub fn record_fetch(&self, upstream_code: &str) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if let Some(upstream) = runtime.upstreams.iter_mut().find(|item| item.code == upstream_code) {
            upstream.stats.fetched_count += 1;
        }
    }

    pub fn record_report(&self, upstream_code: &str, success: bool) {
        let mut runtime = self.runtime.write().expect("runtime poisoned");
        if let Some(upstream) = runtime.upstreams.iter_mut().find(|item| item.code == upstream_code) {
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
        fetch_path: None,
        report_success_path: None,
        report_failure_path: None,
        token: None,
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
        fetch_path: Some("/api/item/fetch".to_string()),
        report_success_path: Some("/api/item/work".to_string()),
        report_failure_path: Some("/api/item/drop".to_string()),
        token: None,
        headers: HashMap::new(),
        notes: Some("首版保留老钱 provider 与接口骨架".to_string()),
        created_at: now_string(),
        stats: UpstreamStats::default(),
    }
}

fn build_upstream_config(payload: UpstreamInput, current: Option<&UpstreamConfig>) -> UpstreamConfig {
    let (default_name, default_code, fetch_path, success_path, failure_path, default_base_url) = match payload.upstream_type {
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
            .or_else(|| current.map(|item| item.base_url.clone()).filter(|item| !item.is_empty()))
            .unwrap_or(default_base_url),
        fetch_path: normalize_text(payload.fetch_path)
            .or_else(|| current.and_then(|item| item.fetch_path.clone()))
            .or(fetch_path),
        report_success_path: normalize_text(payload.report_success_path)
            .or_else(|| current.and_then(|item| item.report_success_path.clone()))
            .or(success_path),
        report_failure_path: normalize_text(payload.report_failure_path)
            .or_else(|| current.and_then(|item| item.report_failure_path.clone()))
            .or(failure_path),
        token: normalize_text(payload.token).or_else(|| current.and_then(|item| item.token.clone())),
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
    value.map(|item| item.trim().to_string()).filter(|item| !item.is_empty())
}
