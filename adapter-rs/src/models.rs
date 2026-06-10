use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum UpstreamType {
    #[default]
    MockUpstream,
    LaoqianWorker,
    CustomHttp,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct UpstreamStats {
    pub fetched_count: u64,
    pub reported_success_count: u64,
    pub reported_failure_count: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpstreamConfig {
    pub id: String,
    pub name: String,
    pub code: String,
    pub upstream_type: UpstreamType,
    pub enabled: bool,
    pub priority: i32,
    pub base_url: String,
    pub fetch_path: Option<String>,
    pub report_success_path: Option<String>,
    pub report_failure_path: Option<String>,
    pub token: Option<String>,
    #[serde(default)]
    pub headers: HashMap<String, String>,
    pub notes: Option<String>,
    pub created_at: String,
    #[serde(default)]
    pub stats: UpstreamStats,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UpstreamInput {
    pub name: Option<String>,
    pub code: Option<String>,
    #[serde(default)]
    pub upstream_type: UpstreamType,
    #[serde(default = "default_enabled")]
    pub enabled: bool,
    #[serde(default = "default_priority")]
    pub priority: i32,
    pub base_url: Option<String>,
    pub fetch_path: Option<String>,
    pub report_success_path: Option<String>,
    pub report_failure_path: Option<String>,
    pub token: Option<String>,
    #[serde(default)]
    pub headers: HashMap<String, String>,
    pub notes: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToggleUpstreamRequest {
    pub enabled: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClientTaskItem {
    pub goods_id: String,
    pub sku_id: String,
    #[serde(default)]
    pub step_index: i32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClientTask {
    pub task_id: String,
    pub upstream_task_ref: Option<String>,
    pub source_code: String,
    pub source_name: String,
    pub account_id: Option<String>,
    pub account_name: Option<String>,
    #[serde(default)]
    pub task_items: Vec<ClientTaskItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClientFetchRequest {
    pub device_id: Option<String>,
    pub source_code: Option<String>,
    pub token: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TaskSubmitType {
    Success,
    Failure,
    Cancelled,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ClientSubmitTaskItem {
    pub goods_id: Option<String>,
    pub sku_id: String,
    pub recognition: Option<String>,
    pub message: Option<String>,
    #[serde(default)]
    pub capture_ids: Vec<String>,
    #[serde(default)]
    pub capture_urls: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClientSubmitRequest {
    pub task_id: String,
    #[serde(rename = "type")]
    pub submit_type: TaskSubmitType,
    pub device_id: Option<String>,
    pub message: Option<String>,
    #[serde(default)]
    pub task_items: Vec<ClientSubmitTaskItem>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AdapterLog {
    pub id: String,
    pub timestamp: String,
    pub level: String,
    pub message: String,
    #[serde(default)]
    pub payload: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct AdapterSummary {
    pub total_upstreams: usize,
    pub enabled_upstreams: usize,
    pub recent_reports: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthResponse {
    pub success: bool,
    pub service: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AdapterStateResponse {
    pub upstreams: Vec<UpstreamConfig>,
    pub recent_logs: Vec<AdapterLog>,
    pub recent_reports: Vec<Value>,
    pub summary: AdapterSummary,
}

#[derive(Debug, Clone)]
pub struct IssuedTaskContext {
    pub task: ClientTask,
    pub upstream_type: UpstreamType,
    pub item_id: Option<String>,
    pub goods_id: Option<String>,
    pub share_url: Option<String>,
    pub laoqian_session_token: Option<String>,
}

pub fn default_enabled() -> bool {
    true
}

pub fn default_priority() -> i32 {
    100
}
