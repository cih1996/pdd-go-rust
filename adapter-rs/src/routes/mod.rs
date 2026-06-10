use axum::{
    extract::{Path, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{delete, get, post, put},
    Json, Router,
};
use serde_json::{json, Value};
use url::Url;

use crate::models::{
    ClientFetchRequest, ClientSubmitRequest, ClientTask, ClientTaskItem, HealthResponse, IssuedTaskContext,
    ToggleUpstreamRequest, UpstreamConfig, UpstreamInput, UpstreamType,
};
use crate::state::AppState;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/health", get(health))
        .route("/api/health", get(health))
        .route("/api/state", get(state))
        .route("/api/upstreams", get(list_upstreams).post(create_upstream))
        .route("/api/upstreams/:upstream_id", put(update_upstream).delete(delete_upstream))
        .route("/api/upstreams/:upstream_id/toggle", post(toggle_upstream))
        .route("/api/client/fetch-task", post(fetch_task))
        .route("/api/client/submit-task", post(submit_task))
}

async fn health(State(state): State<AppState>) -> Json<HealthResponse> {
    Json(HealthResponse {
        success: true,
        service: state.service_name.clone(),
    })
}

async fn state(State(state): State<AppState>) -> Json<Value> {
    Json(serde_json::to_value(state.state_response()).unwrap_or_else(|_| json!({})))
}

async fn list_upstreams(State(state): State<AppState>) -> Json<Value> {
    Json(json!(state.list_upstreams()))
}

async fn create_upstream(State(state): State<AppState>, Json(payload): Json<UpstreamInput>) -> Json<Value> {
    Json(json!(state.create_upstream(payload)))
}

async fn update_upstream(
    State(state): State<AppState>,
    Path(upstream_id): Path<String>,
    Json(payload): Json<UpstreamInput>,
) -> Response {
    match state.update_upstream(&upstream_id, payload) {
        Ok(item) => Json(json!(item)).into_response(),
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

async fn toggle_upstream(
    State(state): State<AppState>,
    Path(upstream_id): Path<String>,
    Json(payload): Json<ToggleUpstreamRequest>,
) -> Response {
    match state.toggle_upstream(&upstream_id, payload.enabled) {
        Ok(item) => Json(json!(item)).into_response(),
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

async fn delete_upstream(State(state): State<AppState>, Path(upstream_id): Path<String>) -> Response {
    match state.delete_upstream(&upstream_id) {
        Ok(payload) => Json(payload).into_response(),
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

async fn fetch_task(State(state): State<AppState>, Json(payload): Json<ClientFetchRequest>) -> Response {
    if payload.token.trim().is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "fetch-task 必须传 token");
    }

    let candidates = select_upstreams(&state, payload.source_code.as_deref());
    if candidates.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "未找到已启用的目标上游");
    }

    for upstream in candidates {
        let result = match upstream.upstream_type {
            UpstreamType::MockUpstream | UpstreamType::CustomHttp => fetch_from_mock_http(&state, &upstream).await,
            UpstreamType::LaoqianWorker => fetch_from_laoqian_http(&state, &upstream, payload.token.trim()).await,
        };

        match result {
            Ok(Some((task, issued))) => {
                state.issue_task(issued);
                state.record_fetch(&task.source_code);
                state.add_log(
                    "已向客户端返回任务",
                    json!({
                        "source_code": task.source_code,
                        "task_id": task.task_id,
                        "upstream_task_ref": task.upstream_task_ref,
                        "task_item_count": task.task_items.len(),
                    }),
                );
                return Json(json!(task)).into_response();
            }
            Ok(None) => continue,
            Err(message) => {
                state.add_log("拉取任务失败", json!({"source_code": upstream.code, "error": message}));
                if payload.source_code.is_some() {
                    return error_response(StatusCode::BAD_REQUEST, &message);
                }
            }
        }
    }

    StatusCode::NO_CONTENT.into_response()
}

async fn submit_task(State(state): State<AppState>, Json(payload): Json<ClientSubmitRequest>) -> Response {
    let Some(context) = state.take_issued_task(&payload.task_id) else {
        return error_response(StatusCode::BAD_REQUEST, "未找到对应的上游任务上下文");
    };

    let upstream = match state
        .list_upstreams()
        .into_iter()
        .find(|item| item.code == context.task.source_code)
    {
        Some(item) => item,
        None => return error_response(StatusCode::BAD_REQUEST, "未找到对应上游配置"),
    };

    let result = match upstream.upstream_type {
        UpstreamType::MockUpstream | UpstreamType::CustomHttp => submit_to_mock_http(&state, &upstream, &context, &payload).await,
        UpstreamType::LaoqianWorker => submit_to_laoqian_http(&state, &upstream, &context, &payload).await,
    };

    match result {
        Ok(response_payload) => {
            if payload.submit_type == crate::models::TaskSubmitType::Success {
                state.record_report(&upstream.code, true);
            } else if payload.submit_type == crate::models::TaskSubmitType::Failure {
                state.record_report(&upstream.code, false);
            }
            state.push_report(json!({
                "task_id": payload.task_id,
                "source_code": upstream.code,
                "submit_type": payload.submit_type,
                "response": response_payload,
            }));
            Json(json!({"success": true, "message": "submit-task 已处理", "response": response_payload})).into_response()
        }
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

fn select_upstreams(state: &AppState, source_code: Option<&str>) -> Vec<UpstreamConfig> {
    let mut upstreams: Vec<UpstreamConfig> = state
        .list_upstreams()
        .into_iter()
        .filter(|item| item.enabled)
        .collect();
    upstreams.sort_by_key(|item| (item.priority, item.code.clone()));
    if let Some(code) = source_code {
        upstreams.retain(|item| item.code == code);
    }
    upstreams
}

async fn fetch_from_mock_http(state: &AppState, upstream: &UpstreamConfig) -> Result<Option<(ClientTask, IssuedTaskContext)>, String> {
    let Some(url) = upstream_url(upstream, upstream.fetch_path.as_deref()) else {
        return Ok(Some(build_builtin_mock_task(state, upstream)));
    };

    let response = state
        .client
        .post(url.clone())
        .json(&json!({}))
        .send()
        .await
        .map_err(|err| format!("请求本地模拟上游失败: {err}"))?;

    if response.status() == StatusCode::NO_CONTENT {
        return Ok(None);
    }
    if !response.status().is_success() {
        let text = response.text().await.unwrap_or_default();
        return Err(format!("本地模拟上游返回异常: {text}"));
    }

    let payload: Value = response
        .json()
        .await
        .map_err(|err| format!("本地模拟上游响应解析失败: {err}"))?;
    build_mock_task_from_payload(state, upstream, payload)
}

async fn fetch_from_laoqian_http(
    state: &AppState,
    upstream: &UpstreamConfig,
    raw_token: &str,
) -> Result<Option<(ClientTask, IssuedTaskContext)>, String> {
    let (account, secret_key) = parse_laoqian_token(raw_token)?;
    let login_url = format!("{}/api/user/login", upstream.base_url.trim_end_matches('/'));
    let login_response = state
        .client
        .post(login_url)
        .json(&json!({"account": account, "secret_key": secret_key}))
        .send()
        .await
        .map_err(|err| format!("老钱登录失败: {err}"))?;
    if !login_response.status().is_success() {
        let text = login_response.text().await.unwrap_or_default();
        return Err(format!("老钱登录失败: {text}"));
    }
    let login_payload: Value = login_response
        .json()
        .await
        .map_err(|err| format!("老钱登录响应解析失败: {err}"))?;
    let session_token = login_payload
        .get("token")
        .and_then(|item| item.as_str())
        .ok_or_else(|| "老钱登录响应缺少 token".to_string())?
        .to_string();

    let fetch_url = upstream_url(upstream, upstream.fetch_path.as_deref()).ok_or_else(|| "老钱上游缺少 fetch_path".to_string())?;
    let fetch_response = state
        .client
        .post(fetch_url)
        .header(reqwest::header::COOKIE, format!("token={session_token}"))
        .json(&json!({"item_type": "ds_detail"}))
        .send()
        .await
        .map_err(|err| format!("老钱获取任务失败: {err}"))?;

    if fetch_response.status() == StatusCode::NO_CONTENT {
        return Ok(None);
    }
    if !fetch_response.status().is_success() {
        let text = fetch_response.text().await.unwrap_or_default();
        return Err(format!("老钱获取任务失败: {text}"));
    }

    let payload: Value = fetch_response
        .json()
        .await
        .map_err(|err| format!("老钱获取任务响应解析失败: {err}"))?;
    build_laoqian_task_from_payload(state, upstream, payload, session_token)
}

async fn submit_to_mock_http(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &IssuedTaskContext,
    payload: &ClientSubmitRequest,
) -> Result<Value, String> {
    let path = if payload.submit_type == crate::models::TaskSubmitType::Success {
        upstream.report_success_path.as_deref()
    } else {
        upstream.report_failure_path.as_deref()
    };
    let Some(url) = upstream_url(upstream, path) else {
        return Ok(json!({
            "reported": true,
            "mode": "builtin_mock",
            "task_id": payload.task_id,
            "submit_type": payload.submit_type,
            "upstream_task_ref": context.task.upstream_task_ref,
            "message": payload.message,
            "task_items": payload.task_items,
        }));
    };

    let request_payload = json!({
        "task_id": payload.task_id,
        "upstream_task_ref": context.task.upstream_task_ref,
        "source_code": context.task.source_code,
        "source_name": context.task.source_name,
        "device_id": payload.device_id,
        "status": payload.submit_type,
        "message": payload.message,
        "task_items": payload.task_items,
    });
    let response = state
        .client
        .post(url)
        .json(&request_payload)
        .send()
        .await
        .map_err(|err| format!("提交本地模拟上游失败: {err}"))?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() {
        return Err(format!("提交本地模拟上游失败: {body}"));
    }
    Ok(body)
}

fn build_builtin_mock_task(state: &AppState, upstream: &UpstreamConfig) -> (ClientTask, IssuedTaskContext) {
    let task_id = format!("{}-{}", upstream.code, state.next_id("task"));
    let upstream_task_ref = Some(state.next_id("mock-upstream"));
    let task_items = vec![
        ClientTaskItem {
            goods_id: "10001".to_string(),
            sku_id: "sku-red-001".to_string(),
            step_index: 0,
        },
        ClientTaskItem {
            goods_id: "10001".to_string(),
            sku_id: "sku-blue-002".to_string(),
            step_index: 1,
        },
    ];
    let task = ClientTask {
        task_id: task_id.clone(),
        upstream_task_ref: upstream_task_ref.clone(),
        source_code: upstream.code.clone(),
        source_name: upstream.name.clone(),
        account_id: None,
        account_name: None,
        task_items: task_items.clone(),
    };
    let context = IssuedTaskContext {
        task: task.clone(),
        upstream_type: upstream.upstream_type.clone(),
        item_id: upstream_task_ref,
        goods_id: Some("10001".to_string()),
        share_url: Some("https://mobile.yangkeduo.com/goods.html?goods_id=10001".to_string()),
        laoqian_session_token: None,
    };
    (task, context)
}

async fn submit_to_laoqian_http(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &IssuedTaskContext,
    payload: &ClientSubmitRequest,
) -> Result<Value, String> {
    let item_id = context
        .item_id
        .clone()
        .ok_or_else(|| "老钱任务缺少 item_id".to_string())?;
    let session = context
        .laoqian_session_token
        .clone()
        .ok_or_else(|| "老钱任务缺少登录会话".to_string())?;

    let url = if payload.submit_type == crate::models::TaskSubmitType::Success {
        format!("{}/api/item/work", upstream.base_url.trim_end_matches('/'))
    } else {
        format!("{}/api/item/drop", upstream.base_url.trim_end_matches('/'))
    };

    let request_payload = if payload.submit_type == crate::models::TaskSubmitType::Success {
        let skus: Vec<Value> = payload
            .task_items
            .iter()
            .map(|item| {
                json!({
                    "sku_id": item.sku_id,
                    "status": "款式存在",
                    "youhuiquan": item.capture_urls,
                    "xiangqingjietu": item.capture_ids,
                })
            })
            .collect();
        json!({
            "item_id": item_id.parse::<i64>().unwrap_or_default(),
            "work_result": serde_json::to_string(&json!({
                "share_url": context.share_url,
                "goods_id": context.goods_id,
                "skus": skus,
            })).unwrap_or_else(|_| "{}".to_string())
        })
    } else {
        json!({
            "item_id": item_id.parse::<i64>().unwrap_or_default()
        })
    };

    let response = state
        .client
        .post(url)
        .header(reqwest::header::COOKIE, format!("token={session}"))
        .json(&request_payload)
        .send()
        .await
        .map_err(|err| format!("提交老钱上游失败: {err}"))?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() {
        return Err(format!("提交老钱上游失败: {body}"));
    }
    Ok(body)
}

fn build_mock_task_from_payload(
    state: &AppState,
    upstream: &UpstreamConfig,
    payload: Value,
) -> Result<Option<(ClientTask, IssuedTaskContext)>, String> {
    let Some(object) = payload.as_object() else {
        return Ok(None);
    };
    let task_items = extract_task_items_from_value(&payload);
    if task_items.is_empty() {
        return Ok(None);
    }
    let upstream_task_ref = string_field(object.get("upstream_task_ref"))
        .or_else(|| string_field(object.get("task_id")))
        .or_else(|| string_field(object.get("id")));
    let task_id = string_field(object.get("task_id"))
        .unwrap_or_else(|| format!("{}-{}", upstream.code, state.next_id("task")));
    let task = ClientTask {
        task_id: task_id.clone(),
        upstream_task_ref: upstream_task_ref.clone(),
        source_code: upstream.code.clone(),
        source_name: upstream.name.clone(),
        account_id: None,
        account_name: None,
        task_items: task_items.clone(),
    };
    let context = IssuedTaskContext {
        task: task.clone(),
        upstream_type: upstream.upstream_type.clone(),
        item_id: upstream_task_ref.clone(),
        goods_id: task_items.first().map(|item| item.goods_id.clone()),
        share_url: string_field(object.get("url")).or_else(|| string_field(object.get("source_url"))),
        laoqian_session_token: None,
    };
    Ok(Some((task, context)))
}

fn build_laoqian_task_from_payload(
    state: &AppState,
    upstream: &UpstreamConfig,
    payload: Value,
    session_token: String,
) -> Result<Option<(ClientTask, IssuedTaskContext)>, String> {
    let item = payload
        .get("item")
        .and_then(|value| value.as_object())
        .ok_or_else(|| "老钱响应缺少 item".to_string())?;
    let item_id = string_field(item.get("item_id")).ok_or_else(|| "老钱响应缺少 item_id".to_string())?;
    let content_raw = string_field(item.get("content")).unwrap_or_else(|| "{}".to_string());
    let content: Value = serde_json::from_str(&content_raw).map_err(|err| format!("老钱 content 解析失败: {err}"))?;
    let goods_id = string_field(content.get("spu_id")).unwrap_or_default();
    let share_url = content
        .get("skus")
        .and_then(|value| value.as_array())
        .and_then(|items| items.first())
        .and_then(|item| item.get("source_url"))
        .and_then(|value| value.as_str())
        .map(|value| value.to_string());
    let mut task_items = Vec::new();
    if let Some(skus) = content.get("skus").and_then(|value| value.as_array()) {
        for (index, item) in skus.iter().enumerate() {
            let sku_id = item.get("source_sku_id").and_then(|value| value.as_str()).unwrap_or_default().trim().to_string();
            if sku_id.is_empty() || goods_id.is_empty() {
                continue;
            }
            task_items.push(ClientTaskItem {
                goods_id: goods_id.clone(),
                sku_id,
                step_index: index as i32,
            });
        }
    }
    if task_items.is_empty() {
        return Ok(None);
    }
    let task_id = format!("{}-{}-{}", upstream.code, item_id, state.next_id("issued"));
    let task = ClientTask {
        task_id: task_id.clone(),
        upstream_task_ref: Some(item_id.clone()),
        source_code: upstream.code.clone(),
        source_name: upstream.name.clone(),
        account_id: None,
        account_name: None,
        task_items: task_items.clone(),
    };
    let context = IssuedTaskContext {
        task: task.clone(),
        upstream_type: upstream.upstream_type.clone(),
        item_id: Some(item_id),
        goods_id: Some(goods_id),
        share_url,
        laoqian_session_token: Some(session_token),
    };
    Ok(Some((task, context)))
}

fn extract_task_items_from_value(payload: &Value) -> Vec<ClientTaskItem> {
    if let Some(items) = payload.get("task_items").and_then(|value| value.as_array()) {
        let direct = items
            .iter()
            .enumerate()
            .filter_map(|(index, item)| {
                Some(ClientTaskItem {
                    goods_id: item.get("goods_id")?.as_str()?.trim().to_string(),
                    sku_id: item.get("sku_id")?.as_str()?.trim().to_string(),
                    step_index: item.get("step_index").and_then(|value| value.as_i64()).unwrap_or(index as i64) as i32,
                })
            })
            .filter(|item| !item.goods_id.is_empty() && !item.sku_id.is_empty())
            .collect::<Vec<_>>();
        if !direct.is_empty() {
            return direct;
        }
    }

    let goods_id = string_field(payload.get("goods_id")).unwrap_or_default();
    let sku_id = string_field(payload.get("sku_id")).unwrap_or_default();
    if !goods_id.is_empty() {
        return vec![ClientTaskItem {
            goods_id: goods_id.clone(),
            sku_id: if sku_id.is_empty() { goods_id } else { sku_id },
            step_index: 0,
        }];
    }

    let url_value = string_field(payload.get("url")).or_else(|| string_field(payload.get("source_url")));
    url_value
        .map(|url| extract_mock_task_items_from_url(&url))
        .unwrap_or_default()
}

fn extract_mock_task_items_from_url(raw_url: &str) -> Vec<ClientTaskItem> {
    let Ok(parsed) = Url::parse(raw_url) else {
        return Vec::new();
    };
    let query_pairs: Vec<(String, String)> = parsed.query_pairs().map(|(k, v)| (k.to_string(), v.to_string())).collect();
    if let Some(goods_list_raw) = query_pairs.iter().find(|(key, _)| key == "goods_list").map(|(_, value)| value) {
        if let Ok(decoded) = serde_json::from_str::<Vec<Value>>(goods_list_raw) {
            let mut items = Vec::new();
            for (index, item) in decoded.iter().enumerate() {
                let goods_id = item.get("goods_id").and_then(|value| value.as_str()).unwrap_or_default().trim().to_string();
                let sku_id = item.get("sku_id").and_then(|value| value.as_str()).unwrap_or_default().trim().to_string();
                if goods_id.is_empty() || sku_id.is_empty() {
                    continue;
                }
                items.push(ClientTaskItem {
                    goods_id,
                    sku_id,
                    step_index: index as i32,
                });
            }
            if !items.is_empty() {
                return items;
            }
        }
    }

    let goods_id = query_pairs
        .iter()
        .find(|(key, _)| key == "goods_id")
        .map(|(_, value)| value.trim().to_string())
        .unwrap_or_default();
    let sku_id = query_pairs
        .iter()
        .find(|(key, _)| key == "sku_id")
        .map(|(_, value)| value.trim().to_string())
        .unwrap_or_default();
    if goods_id.is_empty() {
        return Vec::new();
    }
    vec![ClientTaskItem {
        goods_id: goods_id.clone(),
        sku_id: if sku_id.is_empty() { goods_id } else { sku_id },
        step_index: 0,
    }]
}

fn parse_laoqian_token(raw: &str) -> Result<(String, String), String> {
    let parts: Vec<&str> = raw.split('|').map(|item| item.trim()).collect();
    if parts.len() < 2 || parts[0].is_empty() || parts[1].is_empty() {
        return Err("老钱 token 格式错误，应为 account|secret_key".to_string());
    }
    Ok((parts[0].to_string(), parts[1].to_string()))
}

fn upstream_url(upstream: &UpstreamConfig, path: Option<&str>) -> Option<String> {
    let path = path?.trim();
    if path.is_empty() {
        return None;
    }
    Some(format!("{}/{}", upstream.base_url.trim_end_matches('/'), path.trim_start_matches('/')))
}

fn string_field(value: Option<&Value>) -> Option<String> {
    value
        .and_then(|item| item.as_str())
        .map(|item| item.trim().to_string())
        .filter(|item| !item.is_empty())
}

async fn parse_response_value(response: reqwest::Response) -> Value {
    match response.json::<Value>().await {
        Ok(value) => value,
        Err(_) => Value::String("ok".to_string()),
    }
}

fn error_response(status: StatusCode, message: &str) -> Response {
    (status, Json(json!({"success": false, "detail": message}))).into_response()
}
