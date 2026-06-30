use axum::{
    extract::{Multipart, Path, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post, put},
    Json, Router,
};
use serde_json::{json, Value};
use std::error::Error as _;
use std::time::Duration;
use tokio::time::sleep;
use url::Url;

use crate::models::{
    ClientFetchRequest, ClientSubmitRequest, ClientSubmitTaskItem, ClientTask, ClientTaskItem,
    HealthResponse, IssuedTaskContext, MockDataImportRequest, MockDataImportResponse,
    ToggleUpstreamRequest, UpstreamConfig, UpstreamInput, UpstreamType,
};
use crate::state::AppState;

const LAOQIAN_DEFAULT_ORIGIN: &str = "https://frontend.yqlaoqian111.com";
const LAOQIAN_UPLOAD_RETRY_DELAYS_MS: &[u64] = &[800, 1500];

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/health", get(health))
        .route("/api/health", get(health))
        .route("/api/state", get(state))
        .route("/api/upstreams", get(list_upstreams).post(create_upstream))
        .route(
            "/api/upstreams/:upstream_id",
            put(update_upstream).delete(delete_upstream),
        )
        .route("/api/upstreams/:upstream_id/toggle", post(toggle_upstream))
        .route("/api/mock-data/import", post(import_mock_data))
        .route("/api/client/fetch-task", post(fetch_task))
        .route("/api/client/upload-capture", post(upload_capture))
        .route("/api/client/submit-task", post(submit_task))
        .route("/api/captures/:capture_id", get(get_capture))
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

async fn create_upstream(
    State(state): State<AppState>,
    Json(payload): Json<UpstreamInput>,
) -> Json<Value> {
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

async fn delete_upstream(
    State(state): State<AppState>,
    Path(upstream_id): Path<String>,
) -> Response {
    match state.delete_upstream(&upstream_id) {
        Ok(payload) => Json(payload).into_response(),
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

async fn import_mock_data(
    State(state): State<AppState>,
    Json(payload): Json<MockDataImportRequest>,
) -> Json<MockDataImportResponse> {
    let mut records = payload.records;
    if let Some(lines) = payload.lines {
        records.extend(build_mock_records_from_lines(&lines));
    }
    let (imported_count, total_count) =
        state.import_mock_records(records, payload.replace_existing);
    Json(MockDataImportResponse {
        imported_count,
        total_count,
        replace_existing: payload.replace_existing,
    })
}

async fn fetch_task(
    State(state): State<AppState>,
    Json(payload): Json<ClientFetchRequest>,
) -> Response {
    if payload.token.trim().is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "fetch-task 必须传 token");
    }

    let candidates = select_upstreams(&state, payload.source_code.as_deref());
    if candidates.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "未找到已启用的目标上游");
    }

    for upstream in candidates {
        let result = match upstream.upstream_type {
            UpstreamType::MockUpstream | UpstreamType::CustomHttp => {
                fetch_from_mock_http(&state, &upstream).await
            }
            UpstreamType::LaoqianWorker => {
                fetch_from_laoqian_http(&state, &upstream, payload.token.trim()).await
            }
        };

        match result {
            Ok(Some((task, issued))) => {
                state.issue_task(issued);
                state.record_fetch(&task.source_code);
                state.add_snapshot(
                    "fetch",
                    "issued",
                    Some(&task.source_code),
                    Some(&task.task_id),
                    task.upstream_task_ref.as_deref(),
                    Some("适配器已向业务端返回任务"),
                    json!({
                        "task_item_count": task.task_items.len(),
                        "account_id": task.account_id,
                        "account_name": task.account_name,
                    }),
                );
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
                state.add_log(
                    "拉取任务失败",
                    json!({"source_code": upstream.code, "error": message}),
                );
                if payload.source_code.is_some() {
                    return error_response(StatusCode::BAD_REQUEST, &message);
                }
            }
        }
    }

    StatusCode::NO_CONTENT.into_response()
}

async fn submit_task(
    State(state): State<AppState>,
    Json(payload): Json<ClientSubmitRequest>,
) -> Response {
    let Some(mut context) = state.take_issued_task(&payload.task_id) else {
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
        UpstreamType::MockUpstream | UpstreamType::CustomHttp => {
            submit_to_mock_http(&state, &upstream, &context, &payload).await
        }
        UpstreamType::LaoqianWorker => {
            submit_to_laoqian_http(&state, &upstream, &mut context, &payload).await
        }
    };

    match result {
        Ok(response_payload) => {
            if payload.submit_type == crate::models::TaskSubmitType::Success {
                state.record_report(&upstream.code, true);
            } else if payload.submit_type == crate::models::TaskSubmitType::Failure {
                state.record_report(&upstream.code, false);
            }
            state.add_snapshot(
                "submit",
                if payload.submit_type == crate::models::TaskSubmitType::Success {
                    "success"
                } else if payload.submit_type == crate::models::TaskSubmitType::Failure {
                    "failure"
                } else {
                    "cancelled"
                },
                Some(&upstream.code),
                Some(&payload.task_id),
                context.task.upstream_task_ref.as_deref(),
                payload.message.as_deref(),
                json!({
                    "submit_type": payload.submit_type,
                    "task_items": payload.task_items,
                    "response": response_payload,
                }),
            );
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

async fn upload_capture(State(state): State<AppState>, mut multipart: Multipart) -> Response {
    let mut file_name = "capture.bin".to_string();
    let mut bytes = Vec::new();
    let mut task_id = String::new();
    let mut device_id = String::new();
    let mut goods_id = String::new();
    let mut sku_id = String::new();
    while let Ok(Some(field)) = multipart.next_field().await {
        let Some(name) = field.name().map(|value| value.to_string()) else {
            continue;
        };
        if name == "file" {
            if let Some(value) = field.file_name() {
                file_name = value.to_string();
            }
            match field.bytes().await {
                Ok(value) => bytes = value.to_vec(),
                Err(err) => {
                    return error_response(
                        StatusCode::BAD_REQUEST,
                        &format!("读取上传文件失败: {err}"),
                    )
                }
            }
            continue;
        }
        let text = field.text().await.unwrap_or_default();
        match name.as_str() {
            "task_id" => task_id = text,
            "device_id" => device_id = text,
            "goods_id" => goods_id = text,
            "sku_id" => sku_id = text,
            _ => {}
        }
    }

    if bytes.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "缺少上传文件");
    }

    match state.store_capture(file_name.clone(), bytes) {
        Ok(payload) => {
            state.add_snapshot(
                "upload_capture",
                "stored",
                None,
                if task_id.is_empty() {
                    None
                } else {
                    Some(task_id.as_str())
                },
                None,
                Some("适配器已接收截图"),
                json!({
                    "device_id": device_id,
                    "goods_id": goods_id,
                    "sku_id": sku_id,
                    "capture_id": payload.capture_id,
                    "file_name": file_name,
                }),
            );
            Json(json!(payload)).into_response()
        }
        Err(message) => error_response(StatusCode::BAD_REQUEST, &message),
    }
}

async fn get_capture(State(state): State<AppState>, Path(capture_id): Path<String>) -> Response {
    let Some(capture) = state.get_capture(&capture_id) else {
        return error_response(StatusCode::NOT_FOUND, "截图不存在");
    };
    match std::fs::read(&capture.path) {
        Ok(bytes) => (
            [(
                axum::http::header::CONTENT_TYPE,
                guess_content_type(&capture.file_name),
            )],
            bytes,
        )
            .into_response(),
        Err(err) => error_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            &format!("读取截图失败: {err}"),
        ),
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

async fn fetch_from_mock_http(
    state: &AppState,
    upstream: &UpstreamConfig,
) -> Result<Option<(ClientTask, IssuedTaskContext)>, String> {
    let Some(url) = upstream_url(upstream, upstream.fetch_path.as_deref()) else {
        if let Some(payload) = state.take_mock_record() {
            return build_mock_task_from_payload(state, upstream, payload);
        }
        return Ok(None);
    };

    let client = upstream_http_client(state, upstream)?;
    let response = client
        .post(url.clone())
        .json(&json!({}))
        .send()
        .await
        .map_err(|err| format_reqwest_error("请求本地模拟上游失败", &err))?;

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
    let (account, secret_key, explicit_upload_token) = parse_laoqian_token(raw_token)?;
    let session_token = login_laoqian(state, upstream, &account, &secret_key).await?;
    let origin = laoqian_origin(upstream);
    let client = upstream_http_client(state, upstream)?;

    let fetch_url = upstream_url(upstream, upstream.fetch_path.as_deref())
        .ok_or_else(|| "老钱上游缺少 fetch_path".to_string())?;
    for _attempt in 0..5 {
        let fetch_response = client
            .post(fetch_url.clone())
            .header(reqwest::header::ORIGIN, origin.as_str())
            .header(reqwest::header::COOKIE, format!("token={session_token}"))
            .json(&json!({"item_type": "ds_detail"}))
            .send()
            .await
            .map_err(|err| format_reqwest_error("老钱获取任务失败", &err))?;

        let fetch_status = fetch_response.status();
        if fetch_status == StatusCode::NO_CONTENT {
            return Ok(None);
        }
        if !fetch_status.is_success() {
            let text = fetch_response.text().await.unwrap_or_default();
            return Err(format!("老钱获取任务失败: {text}"));
        }

        let raw_payload = fetch_response.text().await.unwrap_or_default();
        println!(
            "[老钱][fetch-task] source_code={} status={} raw={}",
            upstream.code,
            fetch_status,
            raw_payload
        );
        let payload: Value = serde_json::from_str(&raw_payload)
            .map_err(|err| format!("老钱获取任务响应解析失败: {err}; raw={raw_payload}"))?;

        if !laoqian_business_success(&payload) {
            let message = laoqian_message(&payload).unwrap_or_else(|| raw_payload.clone());
            if is_laoqian_no_task_message(&message) {
                state.add_log(
                    "老钱当前暂无可领取任务",
                    json!({
                        "source_code": upstream.code,
                        "raw": payload,
                        "raw_text": raw_payload,
                    }),
                );
                return Ok(None);
            }
            return Err(format!("老钱获取任务失败: {message}; raw={raw_payload}"));
        }
        match build_laoqian_task_from_payload(
            state,
            upstream,
            payload,
            &raw_payload,
            &account,
            &secret_key,
            session_token.clone(),
            explicit_upload_token.clone(),
        )? {
            LaoqianFetchDecision::Issue(task, context) => return Ok(Some((task, context))),
            LaoqianFetchDecision::Drop {
                item_id,
                goods_id,
                reason,
            } => {
                drop_laoqian_item(state, upstream, &session_token, &item_id).await?;
                if !goods_id.is_empty() {
                    state.block_laoqian_goods(&goods_id, &reason);
                }
                state.add_log(
                    "老钱任务已自动释放",
                    json!({"item_id": item_id, "goods_id": goods_id, "reason": reason}),
                );
            }
        }
    }
    Ok(None)
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
    let client = upstream_http_client(state, upstream)?;
    let response = client
        .post(url)
        .json(&request_payload)
        .send()
        .await
        .map_err(|err| format_reqwest_error("提交本地模拟上游失败", &err))?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() {
        return Err(format!("提交本地模拟上游失败: {body}"));
    }
    Ok(body)
}

async fn submit_to_laoqian_http(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &mut IssuedTaskContext,
    payload: &ClientSubmitRequest,
) -> Result<Value, String> {
    let item_id = context
        .item_id
        .clone()
        .ok_or_else(|| "老钱任务缺少 item_id".to_string())?;
    let mut session = context
        .laoqian_session_token
        .clone()
        .ok_or_else(|| "老钱任务缺少登录会话".to_string())?;

    let is_success_submit = payload.submit_type == crate::models::TaskSubmitType::Success;
    let origin = laoqian_origin(upstream);
    let url = if is_success_submit {
        format!("{}/api/item/work", upstream.base_url.trim_end_matches('/'))
    } else {
        format!("{}/api/item/drop", upstream.base_url.trim_end_matches('/'))
    };

    if is_success_submit && payload.task_items.len() > 1 {
        let share_url = context
            .share_url
            .clone()
            .or_else(|| context.source_urls.first().cloned())
            .ok_or_else(|| "老钱多 SKU 任务缺少 source_url，无法执行链接校验".to_string())?;
        let check_result =
            check_laoqian_link_goods_id(state, upstream, &session, &item_id, &share_url).await;
        if let Err(err) = check_result {
            let _ = drop_laoqian_item(state, upstream, &session, &item_id).await;
            return Err(format!("老钱链接校验失败，任务已释放: {err}"));
        }
    }

    let request_payload = if is_success_submit {
        let mut skus = Vec::with_capacity(payload.task_items.len());
        for item in &payload.task_items {
            let (xiangqingjietu, youhuiquan) =
                build_laoqian_capture_fields(state, upstream, context, item).await?;
            skus.push(json!({
                "sku_id": item.sku_id,
                "status": "款式存在",
                "youhuiquan": youhuiquan,
                "xiangqingjietu": xiangqingjietu,
            }));
        }
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

    // Uploading captures may refresh the session token; always use the latest one.
    session = context
        .laoqian_session_token
        .clone()
        .unwrap_or(session);

    let submit_result = send_laoqian_submit_request(
        state,
        upstream,
        origin.as_str(),
        url.as_str(),
        &session,
        &request_payload,
    )
    .await;
    let body = match submit_result {
        Ok(payload) => payload,
        Err((status, payload)) if should_retry_laoqian_upload_after_relogin(status, &payload) => {
            refresh_laoqian_session_token(state, upstream, context).await?;
            session = context
                .laoqian_session_token
                .clone()
                .ok_or_else(|| "老钱自动重登后缺少 session_token".to_string())?;
            match send_laoqian_submit_request(
                state,
                upstream,
                origin.as_str(),
                url.as_str(),
                &session,
                &request_payload,
            )
            .await
            {
                Ok(payload) => payload,
                Err((retry_status, retry_payload)) => {
                    return Err(format_laoqian_submit_error(
                        retry_status,
                        &retry_payload,
                        is_success_submit,
                    ));
                }
            }
        }
        Err((status, payload)) => {
            return Err(format_laoqian_submit_error(status, &payload, is_success_submit));
        }
    };
    if !is_success_submit && should_block_goods_on_failure(payload) {
        if let Some(goods_id) = context.goods_id.as_deref() {
            state.block_laoqian_goods(goods_id, "命中失败释放，已自动拉黑");
        }
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
        raw_upstream: Some(payload.clone()),
    };
    let context = IssuedTaskContext {
        task: task.clone(),
        item_id: upstream_task_ref.clone(),
        goods_id: task_items.first().map(|item| item.goods_id.clone()),
        share_url: string_field(object.get("url"))
            .or_else(|| string_field(object.get("source_url"))),
        source_urls: object
            .get("url")
            .and_then(|value| value.as_str())
            .map(|value| vec![value.to_string()])
            .or_else(|| {
                object
                    .get("source_url")
                    .and_then(|value| value.as_str())
                    .map(|value| vec![value.to_string()])
            })
            .unwrap_or_default(),
        laoqian_account: None,
        laoqian_secret_key: None,
        laoqian_session_token: None,
        laoqian_upload_token: None,
    };
    Ok(Some((task, context)))
}

enum LaoqianFetchDecision {
    Issue(ClientTask, IssuedTaskContext),
    Drop {
        item_id: String,
        goods_id: String,
        reason: String,
    },
}

fn build_laoqian_task_from_payload(
    state: &AppState,
    upstream: &UpstreamConfig,
    payload: Value,
    raw_payload: &str,
    account: &str,
    secret_key: &str,
    session_token: String,
    explicit_upload_token: Option<String>,
) -> Result<LaoqianFetchDecision, String> {
    let item = payload
        .get("item")
        .and_then(|value| value.as_object())
        .ok_or_else(|| {
            let message = format!("老钱响应缺少 item; raw={raw_payload}");
            state.add_log(
                "老钱获取任务返回缺少 item",
                json!({
                    "source_code": upstream.code,
                    "raw": payload,
                    "raw_text": raw_payload,
                }),
            );
            state.add_snapshot(
                "fetch_task",
                "failure",
                Some(&upstream.code),
                None,
                None,
                Some("老钱获取任务返回缺少 item"),
                json!({
                    "raw": payload,
                    "raw_text": raw_payload,
                }),
            );
            message
        })?;
    let item_id = string_field(item.get("item_id")).ok_or_else(|| {
        format!(
            "老钱响应缺少 item_id; raw={raw_payload}; item={}",
            Value::Object(item.clone())
        )
    })?;
    let content_raw = string_field(item.get("content")).unwrap_or_else(|| "{}".to_string());
    let content: Value = serde_json::from_str(&content_raw)
        .map_err(|err| format!("老钱 content 解析失败: {err}"))?;
    let goods_id = string_field(content.get("spu_id")).unwrap_or_default();
    let source_urls = content
        .get("skus")
        .and_then(|value| value.as_array())
        .map(|items| {
            items
                .iter()
                .filter_map(|item| item.get("source_url").and_then(|value| value.as_str()))
                .map(|value| value.to_string())
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    let share_url = source_urls.first().cloned();
    if goods_id.is_empty() {
        return Ok(LaoqianFetchDecision::Drop {
            item_id,
            goods_id,
            reason: "缺少 goods_id".to_string(),
        });
    }
    if state.is_laoqian_goods_blocked(&goods_id) {
        return Ok(LaoqianFetchDecision::Drop {
            item_id,
            goods_id,
            reason: "goods_id 已拉黑".to_string(),
        });
    }
    if state.has_seen_laoqian_goods(&goods_id) {
        return Ok(LaoqianFetchDecision::Drop {
            item_id,
            goods_id,
            reason: "goods_id 已领取过，按规则直接释放".to_string(),
        });
    }
    let goods_name = string_field(content.get("spu_name")).unwrap_or_default();
    let mut task_items = Vec::new();
    if let Some(skus) = content.get("skus").and_then(|value| value.as_array()) {
        for (index, item) in skus.iter().enumerate() {
            let sku_id = item
                .get("source_sku_id")
                .and_then(|value| value.as_str())
                .unwrap_or_default()
                .trim()
                .to_string();
            if sku_id.is_empty() || goods_id.is_empty() {
                continue;
            }
            let mut sku_label = Vec::new();
            if let Some(props) = item.get("sku_props").and_then(|value| value.as_array()) {
                for prop in props {
                    if let Some(value) = prop.get("v_custom").and_then(|value| value.as_str()) {
                        let value = value.trim();
                        if !value.is_empty() {
                            sku_label.push(value.to_string());
                        }
                    }
                }
            }
            task_items.push(ClientTaskItem {
                goods_id: goods_id.clone(),
                goods_name: Some(goods_name.clone()),
                sku_id,
                sku_name: sku_label,
                source_url: source_urls.get(index).cloned(),
                step_index: index as i32,
            });
        }
    }

    if task_items.is_empty() {
        return Ok(LaoqianFetchDecision::Drop {
            item_id,
            goods_id,
            reason: "未解析到有效 sku_id".to_string(),
        });
    }
    if task_items.len() > 2 {
        return Ok(LaoqianFetchDecision::Drop {
            item_id,
            goods_id,
            reason: "老钱任务 sku 数量超过 2，已自动释放".to_string(),
        });
    }
    state.remember_laoqian_goods(&goods_id);
    let task_id = format!("{}-{}-{}", upstream.code, item_id, state.next_id("issued"));
    let task = ClientTask {
        task_id: task_id.clone(),
        upstream_task_ref: Some(item_id.clone()),
        source_code: upstream.code.clone(),
        source_name: upstream.name.clone(),
        account_id: None,
        account_name: None,
        task_items: task_items.clone(),
        raw_upstream: Some(payload.clone()),
    };
    let context = IssuedTaskContext {
        task: task.clone(),
        item_id: Some(item_id.clone()),
        goods_id: Some(goods_id),
        share_url,
        source_urls,
        laoqian_account: Some(account.to_string()),
        laoqian_secret_key: Some(secret_key.to_string()),
        laoqian_session_token: Some(session_token),
        laoqian_upload_token: explicit_upload_token,
    };
    Ok(LaoqianFetchDecision::Issue(task, context))
}

fn extract_task_items_from_value(payload: &Value) -> Vec<ClientTaskItem> {
    if let Some(items) = payload.get("task_items").and_then(|value| value.as_array()) {
        let direct = items
            .iter()
            .enumerate()
            .filter_map(|(index, item)| {
                Some(ClientTaskItem {
                    goods_id: item.get("goods_id")?.as_str()?.trim().to_string(),
                    goods_name: string_field(item.get("goods_name")),
                    sku_id: item.get("sku_id")?.as_str()?.trim().to_string(),
                    sku_name: item
                        .get("sku_name")
                        .and_then(|value| value.as_array())
                        .map(|values| {
                            values
                                .iter()
                                .filter_map(|value| value.as_str())
                                .map(|value| value.trim().to_string())
                                .filter(|value| !value.is_empty())
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default(),
                    source_url: string_field(item.get("source_url"))
                        .or_else(|| string_field(item.get("url"))),
                    step_index: item
                        .get("step_index")
                        .and_then(|value| value.as_i64())
                        .unwrap_or(index as i64) as i32,
                })
            })
            .filter(|item| !item.goods_id.is_empty() && !item.sku_id.is_empty())
            .collect::<Vec<_>>();
        if !direct.is_empty() {
            return direct;
        }
    }

    let url_value =
        string_field(payload.get("url")).or_else(|| string_field(payload.get("source_url")));
    let goods_id = string_field(payload.get("goods_id")).unwrap_or_default();
    let sku_id = string_field(payload.get("sku_id")).unwrap_or_default();

    // 检查是否有 enrich 标记（来自 | 分隔的行解析）
    let has_enrich = payload.get("_has_enrich").and_then(|v| v.as_bool()).unwrap_or(false);

    if !goods_id.is_empty() || has_enrich {
        let enriched_goods_name = if has_enrich {
            string_field(payload.get("goods_name"))
        } else {
            None
        };
        let enriched_sku_names = if has_enrich {
            payload
                .get("sku_names")
                .and_then(|v| v.as_array())
                .map(|values| {
                    values
                        .iter()
                        .filter_map(|v| v.as_str())
                        .map(|v| v.trim().to_string())
                        .filter(|v| !v.is_empty())
                        .collect::<Vec<_>>()
                })
                .unwrap_or_default()
        } else {
            Vec::new()
        };
        return vec![ClientTaskItem {
            goods_id: if goods_id.is_empty() {
                // 从 URL 提取或 fallback
                extract_goods_id_from_url(url_value.as_deref().unwrap_or_default())
            } else {
                goods_id.clone()
            },
            goods_name: enriched_goods_name,
            sku_id: if sku_id.is_empty() {
                // 从 URL 提取 sku_id
                extract_sku_id_from_url(url_value.as_deref().unwrap_or_default())
            } else {
                sku_id
            },
            sku_name: enriched_sku_names,
            source_url: url_value.clone(),
            step_index: 0,
        }];
    }
    url_value
        .map(|url| extract_mock_task_items_from_url(&url))
        .unwrap_or_default()
}

fn extract_goods_id_from_url(url: &str) -> String {
    if let Ok(parsed) = Url::parse(url) {
        for (key, value) in parsed.query_pairs() {
            if key == "goods_id" {
                return value.trim().to_string();
            }
        }
    }
    // 兜底：从 URL 中正则匹配 goods_id 数字
    for key in &["goods_id=", "goods_id%3D", "goods_id\":"] {
        if let Some(pos) = url.find(key) {
            let start = pos + key.len();
            let mut id = String::new();
            for ch in url[start..].chars() {
                if ch.is_ascii_digit() {
                    id.push(ch);
                } else {
                    break;
                }
            }
            if !id.is_empty() {
                return id;
            }
        }
    }
    String::new()
}

fn extract_sku_id_from_url(url: &str) -> String {
    if let Ok(parsed) = Url::parse(url) {
        for (key, value) in parsed.query_pairs() {
            if key == "sku_id" {
                return value.trim().to_string();
            }
        }
    }
    for key in &["sku_id=", "sku_id%3D", "sku_id\":"] {
        if let Some(pos) = url.find(key) {
            let start = pos + key.len();
            let mut id = String::new();
            for ch in url[start..].chars() {
                if ch.is_ascii_digit() {
                    id.push(ch);
                } else {
                    break;
                }
            }
            if !id.is_empty() {
                return id;
            }
        }
    }
    String::new()
}

fn extract_mock_task_items_from_url(raw_url: &str) -> Vec<ClientTaskItem> {
    extract_mock_task_items_from_source(raw_url, 0)
}

fn extract_mock_task_items_from_source(raw: &str, depth: usize) -> Vec<ClientTaskItem> {
    if depth > 4 {
        return Vec::new();
    }
    let normalized = normalize_raw_link(raw);
    if normalized.is_empty() {
        return Vec::new();
    }
    if let Ok(value) = serde_json::from_str::<Value>(&normalized) {
        let items = extract_task_items_from_value(&value);
        if !items.is_empty() {
            return items
                .into_iter()
                .map(|mut item| {
                    if item.source_url.is_none() {
                        item.source_url = Some(normalized.clone());
                    }
                    item
                })
                .collect();
        }
    }
    let query_pairs = Url::parse(&normalized)
        .ok()
        .map(|parsed| {
            parsed
                .query_pairs()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    if let Some(goods_list_raw) = query_pairs
        .iter()
        .find(|(key, _)| key == "goods_list")
        .map(|(_, value)| value)
    {
        if let Ok(decoded) = serde_json::from_str::<Vec<Value>>(goods_list_raw) {
            let mut items = Vec::new();
            for (index, item) in decoded.iter().enumerate() {
                let goods_id = item
                    .get("goods_id")
                    .and_then(|value| value.as_str())
                    .unwrap_or_default()
                    .trim()
                    .to_string();
                let sku_id = item
                    .get("sku_id")
                    .and_then(|value| value.as_str())
                    .unwrap_or_default()
                    .trim()
                    .to_string();
                if goods_id.is_empty() || sku_id.is_empty() {
                    continue;
                }
                items.push(ClientTaskItem {
                    goods_id,
                    goods_name: string_field(item.get("goods_name")),
                    sku_id,
                    sku_name: item
                        .get("sku_name")
                        .and_then(|value| value.as_array())
                        .map(|values| {
                            values
                                .iter()
                                .filter_map(|value| value.as_str())
                                .map(|value| value.trim().to_string())
                                .filter(|value| !value.is_empty())
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default(),
                    source_url: string_field(item.get("source_url"))
                        .or_else(|| string_field(item.get("url")))
                        .map(|value| normalize_raw_link(&value)),
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
        .filter(|value| !value.is_empty())
        .or_else(|| extract_field_from_raw_link(&normalized, &["goods_id", "goods_id\":"]))
        .unwrap_or_default();
    let sku_id = query_pairs
        .iter()
        .find(|(key, _)| key == "sku_id")
        .map(|(_, value)| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .or_else(|| extract_field_from_raw_link(&normalized, &["sku_id", "sku_id\":"]))
        .unwrap_or_default();
    if !goods_id.is_empty() {
        return vec![ClientTaskItem {
            goods_id: goods_id.clone(),
            goods_name: None,
            sku_id: if sku_id.is_empty() { goods_id } else { sku_id },
            sku_name: Vec::new(),
            source_url: Some(normalized.to_string()),
            step_index: 0,
        }];
    }

    for (_, value) in &query_pairs {
        let items = extract_mock_task_items_from_source(value, depth + 1);
        if !items.is_empty() {
            return items;
        }
    }

    let decoded = best_effort_decode(&normalized);
    if decoded != normalized {
        let items = extract_mock_task_items_from_source(&decoded, depth + 1);
        if !items.is_empty() {
            return items;
        }
    }
    Vec::new()
}

/// 解析行格式：`<URL>|<sku_name1,sku_name2,...>|<goods_name>`
/// 或纯 URL：`<URL>`
fn build_mock_records_from_lines(lines: &str) -> Vec<Value> {
    lines
        .lines()
        .map(|line| line.trim())
        .filter(|line| !line.is_empty())
        .map(|line| {
            let raw = line.trim_matches('`').trim_matches('"');
            let parts: Vec<&str> = raw.splitn(3, '|').map(|s| s.trim()).collect();
            match parts.len() {
                3 => {
                    let url = parts[0];
                    let sku_names: Vec<String> = parts[1]
                        .split(',')
                        .map(|s| s.trim().to_string())
                        .filter(|s| !s.is_empty())
                        .collect();
                    let goods_name = parts[2].to_string();
                    json!({
                        "url": url,
                        "source_url": url,
                        "goods_name": goods_name,
                        "sku_names": sku_names,
                        "_has_enrich": true
                    })
                }
                2 => {
                    let url = parts[0];
                    let sku_names: Vec<String> = parts[1]
                        .split(',')
                        .map(|s| s.trim().to_string())
                        .filter(|s| !s.is_empty())
                        .collect();
                    json!({
                        "url": url,
                        "source_url": url,
                        "sku_names": sku_names,
                        "_has_enrich": true
                    })
                }
                _ => json!({ "url": raw }),
            }
        })
        .collect()
}

fn extract_field_from_raw_link(raw: &str, keys: &[&str]) -> Option<String> {
    let decoded = best_effort_decode(raw);
    for source in [decoded.as_str(), raw] {
        for key in keys {
            if let Some(value) = scan_numeric_value(source, key) {
                return Some(value);
            }
        }
    }
    None
}

fn normalize_raw_link(raw: &str) -> String {
    raw.trim()
        .trim_matches('`')
        .trim_matches('"')
        .trim()
        .to_string()
}

fn best_effort_decode(raw: &str) -> String {
    let normalized = normalize_raw_link(raw);
    let mut decoded = normalized.clone();
    for _ in 0..3 {
        let next = percent_decode_once(&decoded);
        if next == decoded {
            break;
        }
        decoded = next;
    }
    decoded
}

fn percent_decode_once(raw: &str) -> String {
    let bytes = raw.as_bytes();
    let mut result = String::with_capacity(raw.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' && index+2 < bytes.len() {
            let hi = bytes[index + 1] as char;
            let lo = bytes[index + 2] as char;
            if hi.is_ascii_hexdigit() && lo.is_ascii_hexdigit() {
                let value = u8::from_str_radix(&raw[index+1..index+3], 16).unwrap_or(bytes[index]);
                result.push(value as char);
                index += 3;
                continue;
            }
        }
        result.push(bytes[index] as char);
        index += 1
    }
    result
}

fn scan_numeric_value(source: &str, key: &str) -> Option<String> {
    let start = source.find(key)?;
    let mut buffer = String::new();
    let mut started = false;
    for ch in source[start + key.len()..].chars() {
        if ch.is_ascii_digit() {
            buffer.push(ch);
            started = true;
            continue;
        }
        if started {
            break;
        }
    }
    if buffer.is_empty() {
        None
    } else {
        Some(buffer)
    }
}

async fn drop_laoqian_item(
    state: &AppState,
    upstream: &UpstreamConfig,
    session: &str,
    item_id: &str,
) -> Result<Value, String> {
    let origin = laoqian_origin(upstream);
    send_laoqian_submit_request(
        state,
        upstream,
        origin.as_str(),
        &format!(
            "{}/api/item/drop",
            upstream.base_url.trim_end_matches('/')
        ),
        session,
        &json!({ "item_id": item_id.parse::<i64>().unwrap_or_default() }),
    )
    .await
    .map_err(|(status, payload)| format_laoqian_submit_error(status, &payload, false))
}

async fn check_laoqian_link_goods_id(
    state: &AppState,
    upstream: &UpstreamConfig,
    session: &str,
    item_id: &str,
    share_url: &str,
) -> Result<Value, String> {
    let origin = laoqian_origin(upstream);
    send_laoqian_submit_request(
        state,
        upstream,
        origin.as_str(),
        &format!(
            "{}/api/item/check_link_goods_id",
            upstream.base_url.trim_end_matches('/')
        ),
        session,
        &json!({
            "item_id": item_id.parse::<i64>().unwrap_or_default(),
            "share_url": share_url,
        }),
    )
    .await
    .map_err(|(status, payload)| {
        let fallback = payload.to_string();
        let message = laoqian_message(&payload).unwrap_or(fallback.clone());
        if !status.is_success() {
            format!(
                "老钱链接校验失败: HTTP {}: {}",
                status.as_u16(),
                if message.trim().is_empty() {
                    fallback
                } else {
                    message
                }
            )
        } else {
            format!(
                "老钱链接校验失败: {}",
                if message.trim().is_empty() {
                    fallback
                } else {
                    message
                }
            )
        }
    })
}

fn should_block_goods_on_failure(payload: &ClientSubmitRequest) -> bool {
    payload
        .task_items
        .iter()
        .any(|item| item.recognition.as_deref() == Some("fail_release"))
        || payload
            .message
            .as_deref()
            .map(|value| value.contains("失败释放") || value.contains("fail_release"))
            .unwrap_or(false)
}

fn parse_laoqian_token(raw: &str) -> Result<(String, String, Option<String>), String> {
    let parts: Vec<&str> = raw.split('|').map(|item| item.trim()).collect();
    if parts.len() < 2 || parts[0].is_empty() || parts[1].is_empty() {
        return Err("老钱 token 格式错误，应为 account|secret_key".to_string());
    }
    let upload_token = parts
        .get(2)
        .map(|value| value.to_string())
        .filter(|value| !value.is_empty());
    Ok((parts[0].to_string(), parts[1].to_string(), upload_token))
}

fn laoqian_origin(upstream: &UpstreamConfig) -> String {
    Url::parse(upstream.base_url.trim())
        .ok()
        .and_then(|parsed| {
            let scheme = parsed.scheme();
            let host = parsed.host_str()?;
            let mut origin = format!("{scheme}://{host}");
            if let Some(port) = parsed.port() {
                origin.push(':');
                origin.push_str(&port.to_string());
            }
            Some(origin)
        })
        .unwrap_or_else(|| LAOQIAN_DEFAULT_ORIGIN.to_string())
}

fn upstream_url(upstream: &UpstreamConfig, path: Option<&str>) -> Option<String> {
    let path = path?.trim();
    if path.is_empty() {
        return None;
    }
    Some(format!(
        "{}/{}",
        upstream.base_url.trim_end_matches('/'),
        path.trim_start_matches('/')
    ))
}

fn string_field(value: Option<&Value>) -> Option<String> {
    match value? {
        Value::String(item) => {
            let trimmed = item.trim();
            if trimmed.is_empty() {
                None
            } else {
                Some(trimmed.to_string())
            }
        }
        Value::Number(item) => Some(item.to_string()),
        _ => None,
    }
}

fn laoqian_business_success(payload: &Value) -> bool {
    match payload.get("status").and_then(|item| item.as_i64()) {
        Some(200) => true,
        Some(_) => false,
        None => true,
    }
}

fn laoqian_message(payload: &Value) -> Option<String> {
    payload
        .get("message")
        .and_then(|item| item.as_str())
        .map(|item| item.trim().to_string())
        .filter(|item| !item.is_empty())
}

fn should_retry_laoqian_upload_after_relogin(status: StatusCode, payload: &Value) -> bool {
    status == StatusCode::UNAUTHORIZED || laoqian_message(payload).as_deref() == Some("请登录")
}

fn should_retry_laoqian_upload_after_delay(
    status: StatusCode,
    payload: &Value,
    raw_payload: &str,
) -> bool {
    if status != StatusCode::BAD_GATEWAY {
        return false;
    }
    let mut candidates = Vec::with_capacity(2);
    if let Some(message) = laoqian_message(payload) {
        candidates.push(message.to_lowercase());
    }
    let raw = raw_payload.trim();
    if !raw.is_empty() {
        candidates.push(raw.to_lowercase());
    }
    candidates.iter().any(|item| {
        item.contains("error sending request")
            || item.contains("connection error")
            || item.contains("sendrequest")
            || item.contains("os error 10054")
            || item.contains("远程主机强迫关闭了一个现有的连接")
            || item.contains("connection reset")
            || item.contains("timed out")
    })
}

fn format_laoqian_upload_error(status: StatusCode, payload: &Value, raw_payload: &str) -> String {
    if !status.is_success() {
        let message = laoqian_message(payload).unwrap_or_else(|| raw_payload.to_string());
        return format!(
            "上传老钱截图失败: HTTP {}: {}",
            status.as_u16(),
            if message.trim().is_empty() {
                raw_payload.to_string()
            } else {
                message
            }
        );
    }
    if !laoqian_business_success(payload) {
        let message = laoqian_message(payload).unwrap_or_else(|| raw_payload.to_string());
        return format!(
            "上传老钱截图失败: {}",
            if message.trim().is_empty() {
                raw_payload.to_string()
            } else {
                message
            }
        );
    }
    format!("老钱上传响应缺少 path; raw={raw_payload}")
}

fn format_laoqian_submit_error(status: StatusCode, payload: &Value, is_success_submit: bool) -> String {
    let action = if is_success_submit { "提交老钱上游失败" } else { "释放老钱上游失败" };
    let fallback = payload.to_string();
    if !status.is_success() {
        let message = laoqian_message(payload).unwrap_or_else(|| fallback.clone());
        return format!(
            "{}: HTTP {}: {}",
            action,
            status.as_u16(),
            if message.trim().is_empty() {
                fallback
            } else {
                message
            }
        );
    }
    let message = laoqian_message(payload).unwrap_or(fallback.clone());
    format!(
        "{}: {}",
        action,
        if message.trim().is_empty() {
            fallback
        } else {
            message
        }
    )
}

async fn send_laoqian_submit_request(
    state: &AppState,
    upstream: &UpstreamConfig,
    origin: &str,
    url: &str,
    session: &str,
    request_payload: &Value,
) -> Result<Value, (StatusCode, Value)> {
    let client =
        upstream_http_client(state, upstream).map_err(|err| (StatusCode::BAD_GATEWAY, Value::String(err)))?;
    let response = client
        .post(url)
        .header(reqwest::header::ORIGIN, origin)
        .header(reqwest::header::COOKIE, format!("token={session}"))
        .json(request_payload)
        .send()
        .await
        .map_err(|err| {
            (
                StatusCode::BAD_GATEWAY,
                Value::String(format_reqwest_error("提交老钱上游失败", &err)),
            )
        })?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() || !laoqian_business_success(&body) {
        return Err((status, body));
    }
    Ok(body)
}

async fn login_laoqian(
    state: &AppState,
    upstream: &UpstreamConfig,
    account: &str,
    secret_key: &str,
) -> Result<String, String> {
    let origin = laoqian_origin(upstream);
    let login_url = format!("{}/api/user/login", upstream.base_url.trim_end_matches('/'));
    let client = upstream_http_client(state, upstream)?;
    let login_response = client
        .post(login_url)
        .header(reqwest::header::ORIGIN, origin.as_str())
        .json(&json!({"account": account, "secret_key": secret_key}))
        .send()
        .await
        .map_err(|err| format_reqwest_error("老钱登录失败", &err))?;
    let status = login_response.status();
    let raw_payload = login_response.text().await.unwrap_or_default();
    let payload: Value = serde_json::from_str(&raw_payload).unwrap_or_else(|_| {
        if raw_payload.trim().is_empty() {
            Value::Null
        } else {
            Value::String(raw_payload.clone())
        }
    });
    if !status.is_success() {
        let message = laoqian_message(&payload).unwrap_or_else(|| raw_payload.clone());
        return Err(format!(
            "老钱登录失败: HTTP {}: {}",
            status.as_u16(),
            if message.trim().is_empty() {
                raw_payload
            } else {
                message
            }
        ));
    }
    if !laoqian_business_success(&payload) {
        return Err(format!(
            "老钱登录失败: {}",
            laoqian_message(&payload).unwrap_or_else(|| raw_payload.clone())
        ));
    }
    payload
        .get("token")
        .and_then(|item| item.as_str())
        .filter(|item| !item.trim().is_empty())
        .map(|item| item.to_string())
        .ok_or_else(|| {
            format!(
                "老钱登录响应缺少 token: {}",
                laoqian_message(&payload).unwrap_or_else(|| raw_payload)
            )
        })
}

async fn refresh_laoqian_session_token(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &mut IssuedTaskContext,
) -> Result<(), String> {
    let account = context
        .laoqian_account
        .as_deref()
        .ok_or_else(|| "老钱任务缺少登录账号，无法自动重登".to_string())?;
    let secret_key = context
        .laoqian_secret_key
        .as_deref()
        .ok_or_else(|| "老钱任务缺少登录密钥，无法自动重登".to_string())?;
    let session_token = login_laoqian(state, upstream, account, secret_key).await?;
    context.laoqian_session_token = Some(session_token);
    Ok(())
}

fn is_laoqian_no_task_message(message: &str) -> bool {
    let value = message.trim();
    value.contains("暂时没有题可做了")
        || value.contains("没有题可做")
        || value.contains("暂无任务")
        || value.contains("没有任务")
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

fn guess_content_type(file_name: &str) -> &'static str {
    if file_name.ends_with(".png") {
        "image/png"
    } else if file_name.ends_with(".jpg") || file_name.ends_with(".jpeg") {
        "image/jpeg"
    } else if file_name.ends_with(".webp") {
        "image/webp"
    } else {
        "application/octet-stream"
    }
}

#[cfg(test)]
mod tests {
    use super::{
        build_laoqian_task_from_payload, extract_mock_task_items_from_url,
        format_laoqian_submit_error, should_retry_laoqian_upload_after_delay,
        should_retry_laoqian_upload_after_relogin,
    };
    use crate::models::UpstreamType;
    use crate::state::AppState;
    use axum::http::StatusCode;
    use serde_json::json;

    #[test]
    fn extract_nested_login_url_goods_and_sku_ids() {
        let raw = " `https://mobile.yangkeduo.com/login.html?from=https%3A%2F%2Fmobile.yangkeduo.com%2Forder_checkout.html%3Fgoods_id%3D654097226224%26sku_id%3D1888467061279&refer_page_name=order_checkout&refer_page_id=10004_1780934625846_br4gfaun8e&refer_page_sn=10004` ";
        let items = extract_mock_task_items_from_url(raw);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].goods_id, "654097226224");
        assert_eq!(items[0].sku_id, "1888467061279");
    }

    #[test]
    fn build_laoqian_task_accepts_numeric_item_id() {
        let state = AppState::new();
        let upstream = state
            .list_upstreams()
            .into_iter()
            .find(|item| item.upstream_type == UpstreamType::LaoqianWorker)
            .expect("laoqian upstream should exist");
        let payload = json!({
            "status": 200,
            "message": "ok",
            "item": {
                "item_id": 749844241676087_u64,
                "content": "{\"spu_id\":\"925751104085\",\"skus\":[{\"source_sku_id\":\"1874695437353\",\"source_url\":\"https://example.com/a\"}]}"
            }
        });
        let raw_payload = payload.to_string();

        let decision = build_laoqian_task_from_payload(
            &state,
            &upstream,
            payload,
            &raw_payload,
            "demo-account",
            "demo-secret",
            "session-token".to_string(),
            None,
        )
        .expect("numeric item_id should be accepted");

        match decision {
            super::LaoqianFetchDecision::Issue(task, context) => {
                assert_eq!(task.upstream_task_ref.as_deref(), Some("749844241676087"));
                assert_eq!(context.item_id.as_deref(), Some("749844241676087"));
                assert_eq!(task.task_items.len(), 1);
                assert_eq!(task.task_items[0].goods_id, "925751104085");
                assert_eq!(task.task_items[0].sku_id, "1874695437353");
            }
            super::LaoqianFetchDecision::Drop { reason, .. } => {
                panic!("expected task issue, got drop: {reason}")
            }
        }
    }

    #[test]
    fn retry_laoqian_upload_after_relogin_only_for_documented_signals() {
        assert!(should_retry_laoqian_upload_after_relogin(
            StatusCode::UNAUTHORIZED,
            &json!({"message": "其他错误"})
        ));
        assert!(should_retry_laoqian_upload_after_relogin(
            StatusCode::OK,
            &json!({"message": "请登录"})
        ));
        assert!(!should_retry_laoqian_upload_after_relogin(
            StatusCode::OK,
            &json!({"message": "token 已过期"})
        ));
    }

    #[test]
    fn retry_laoqian_upload_after_delay_only_for_transport_failures() {
        assert!(should_retry_laoqian_upload_after_delay(
            StatusCode::BAD_GATEWAY,
            &json!("上传老钱截图失败: error sending request | caused by: connection error"),
            "上传老钱截图失败: error sending request | caused by: connection error"
        ));
        assert!(should_retry_laoqian_upload_after_delay(
            StatusCode::BAD_GATEWAY,
            &json!("远程主机强迫关闭了一个现有的连接。 (os error 10054)"),
            "远程主机强迫关闭了一个现有的连接。 (os error 10054)"
        ));
        assert!(!should_retry_laoqian_upload_after_delay(
            StatusCode::OK,
            &json!({"message": "请登录"}),
            r#"{"message":"请登录"}"#
        ));
        assert!(!should_retry_laoqian_upload_after_delay(
            StatusCode::BAD_GATEWAY,
            &json!("老钱题目已失效"),
            "老钱题目已失效"
        ));
    }

    #[test]
    fn format_laoqian_submit_error_requires_business_success() {
        assert_eq!(
            format_laoqian_submit_error(
                StatusCode::OK,
                &json!({"status": 500, "message": "无当前题目作业权限，请重新领题"}),
                true
            ),
            "提交老钱上游失败: 无当前题目作业权限，请重新领题"
        );
        assert_eq!(
            format_laoqian_submit_error(
                StatusCode::BAD_REQUEST,
                &json!({"message": "参数错误"}),
                false
            ),
            "释放老钱上游失败: HTTP 400: 参数错误"
        );
    }
}

async fn build_laoqian_capture_fields(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &mut IssuedTaskContext,
    item: &ClientSubmitTaskItem,
) -> Result<(Vec<String>, Vec<String>), String> {
    let mut uploaded = Vec::new();
    for capture_id in &item.capture_ids {
        let Some(capture) = state.get_capture(capture_id) else {
            continue;
        };
        uploaded
            .push(
                upload_laoqian_capture(state, upstream, context, &capture.path, &capture.file_name)
                    .await?,
            );
    }
    if uploaded.is_empty() {
        uploaded.extend(item.capture_urls.clone());
    }
    if uploaded.is_empty() {
        return Ok((Vec::new(), Vec::new()));
    }
    if uploaded.len() == 1 {
        return Ok((uploaded.clone(), uploaded));
    }
    Ok((
        vec![uploaded[0].clone()],
        vec![uploaded[uploaded.len() - 1].clone()],
    ))
}

async fn upload_laoqian_capture(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &mut IssuedTaskContext,
    path: &std::path::Path,
    file_name: &str,
) -> Result<String, String> {
    let first_attempt_token = context
        .laoqian_upload_token
        .clone()
        .or_else(|| context.laoqian_session_token.clone())
        .ok_or_else(|| "老钱任务缺少上传 token".to_string())?;
    match send_laoqian_capture_upload_with_retry(
        state,
        upstream,
        context,
        path,
        file_name,
        &first_attempt_token,
    )
    .await
    {
        Ok(uploaded_path) => Ok(uploaded_path),
        Err((status, payload, raw_payload)) => {
            if should_retry_laoqian_upload_after_relogin(status, &payload) {
                refresh_laoqian_session_token(state, upstream, context).await?;
                let retry_token = context
                    .laoqian_session_token
                    .clone()
                    .ok_or_else(|| "老钱自动重登后缺少 session_token".to_string())?;
                match send_laoqian_capture_upload_with_retry(
                    state,
                    upstream,
                    context,
                    path,
                    file_name,
                    &retry_token,
                )
                .await
                {
                    Ok(uploaded_path) => Ok(uploaded_path),
                    Err((retry_status, retry_payload, retry_raw_payload)) => {
                        Err(format_laoqian_upload_error(retry_status, &retry_payload, &retry_raw_payload))
                    }
                }
            } else {
                Err(format_laoqian_upload_error(status, &payload, &raw_payload))
            }
        }
    }
}

async fn send_laoqian_capture_upload_with_retry(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &IssuedTaskContext,
    path: &std::path::Path,
    file_name: &str,
    upload_token: &str,
) -> Result<String, (StatusCode, Value, String)> {
    let mut last_error = None;
    for (attempt, delay_ms) in std::iter::once(0)
        .chain((1..=LAOQIAN_UPLOAD_RETRY_DELAYS_MS.len()).map(|index| index as u64))
        .zip(std::iter::once(0).chain(LAOQIAN_UPLOAD_RETRY_DELAYS_MS.iter().copied()))
    {
        if delay_ms > 0 {
            sleep(Duration::from_millis(delay_ms)).await;
        }
        match send_laoqian_capture_upload(state, upstream, context, path, file_name, upload_token).await {
            Ok(uploaded_path) => return Ok(uploaded_path),
            Err((status, payload, raw_payload)) => {
                let should_retry = should_retry_laoqian_upload_after_delay(status, &payload, &raw_payload);
                if should_retry && (attempt as usize) < LAOQIAN_UPLOAD_RETRY_DELAYS_MS.len() {
                    last_error = Some((status, payload, raw_payload));
                    continue;
                }
                return Err((status, payload, raw_payload));
            }
        }
    }
    Err(last_error.unwrap_or((
        StatusCode::BAD_GATEWAY,
        Value::String("老钱上传重试失败".to_string()),
        "老钱上传重试失败".to_string(),
    )))
}

async fn send_laoqian_capture_upload(
    state: &AppState,
    upstream: &UpstreamConfig,
    context: &IssuedTaskContext,
    path: &std::path::Path,
    file_name: &str,
    upload_token: &str,
) -> Result<String, (StatusCode, Value, String)> {
    let Some(item_id) = context.item_id.as_deref() else {
        return Err((
            StatusCode::BAD_REQUEST,
            Value::String("老钱任务缺少 item_id".to_string()),
            "老钱任务缺少 item_id".to_string(),
        ));
    };
    let bytes = std::fs::read(path).map_err(|err| {
        (
            StatusCode::INTERNAL_SERVER_ERROR,
            Value::String(format!("读取本地截图失败: {err}")),
            format!("读取本地截图失败: {err}"),
        )
    })?;
    let form = reqwest::multipart::Form::new()
        .part(
            "file",
            reqwest::multipart::Part::bytes(bytes.clone()).file_name(file_name.to_string()),
        )
        .text("file_name", file_name.to_string())
        .text("file_size", bytes.len().to_string())
        .text("item_id", format!("tw/{item_id}"))
        .text("token", upload_token.to_string());
    let client = upstream_http_client(state, upstream).map_err(|message| {
        (
            StatusCode::BAD_GATEWAY,
            Value::String(message.clone()),
            message,
        )
    })?;
    let response = client
        .put("https://file.yqlaoqian111.com/upload")
        .header(reqwest::header::ORIGIN, LAOQIAN_DEFAULT_ORIGIN)
        .multipart(form)
        .send()
        .await
        .map_err(|err| {
            let message = format_reqwest_error("上传老钱截图失败", &err);
            (StatusCode::BAD_GATEWAY, Value::String(message.clone()), message)
        })?;
    let status = response.status();
    let raw_payload = response.text().await.unwrap_or_default();
    let payload: Value = serde_json::from_str(&raw_payload).unwrap_or_else(|_| {
        if raw_payload.trim().is_empty() {
            Value::Null
        } else {
            Value::String(raw_payload.clone())
        }
    });
    if !status.is_success() || !laoqian_business_success(&payload) {
        return Err((status, payload, raw_payload));
    }
    payload
        .get("path")
        .and_then(|value| value.as_str())
        .map(|value| value.to_string())
        .ok_or_else(|| (status, payload, raw_payload))
}

fn upstream_http_client(state: &AppState, upstream: &UpstreamConfig) -> Result<reqwest::Client, String> {
    let proxy_url = upstream
        .proxy_url
        .as_deref()
        .map(str::trim)
        .unwrap_or_default();
    if proxy_url.is_empty() {
        return Ok(state.client.clone());
    }
    let proxy = reqwest::Proxy::all(proxy_url)
        .map_err(|err| format!("上游代理配置无效: {err}"))?;
    reqwest::Client::builder()
        .danger_accept_invalid_certs(true)
        .proxy(proxy)
        .build()
        .map_err(|err| format!("创建上游代理客户端失败: {err}"))
}

fn format_reqwest_error(prefix: &str, err: &reqwest::Error) -> String {
    let mut parts = vec![err.to_string()];
    let mut current = err.source();
    while let Some(source) = current {
        let message = source.to_string();
        if !message.is_empty() && !parts.iter().any(|item| item == &message) {
            parts.push(message);
        }
        current = source.source();
    }
    format!("{prefix}: {}", parts.join(" | caused by: "))
}
