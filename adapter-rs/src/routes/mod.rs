use axum::{
    extract::{Multipart, Path, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post, put},
    Json, Router,
};
use serde_json::{json, Value};
use url::Url;

use crate::models::{
    ClientFetchRequest, ClientSubmitRequest, ClientSubmitTaskItem, ClientTask, ClientTaskItem,
    HealthResponse, IssuedTaskContext, MockDataImportRequest, MockDataImportResponse,
    ToggleUpstreamRequest, UpstreamConfig, UpstreamInput, UpstreamType,
};
use crate::state::AppState;

const LAOQIAN_DEFAULT_ORIGIN: &str = "https://frontend.yqlaoqian111.com";

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
        UpstreamType::MockUpstream | UpstreamType::CustomHttp => {
            submit_to_mock_http(&state, &upstream, &context, &payload).await
        }
        UpstreamType::LaoqianWorker => {
            submit_to_laoqian_http(&state, &upstream, &context, &payload).await
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
    let (account, secret_key, explicit_upload_token) = parse_laoqian_token(raw_token)?;
    let origin = laoqian_origin(upstream);
    let login_url = format!("{}/api/user/login", upstream.base_url.trim_end_matches('/'));
    let login_response = state
        .client
        .post(login_url)
        .header(reqwest::header::ORIGIN, origin.as_str())
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
    if !laoqian_business_success(&login_payload) {
        return Err(format!(
            "老钱登录失败: {}",
            laoqian_message(&login_payload).unwrap_or_else(|| login_payload.to_string())
        ));
    }
    let session_token = login_payload
        .get("token")
        .and_then(|item| item.as_str())
        .filter(|item| !item.trim().is_empty())
        .ok_or_else(|| {
            format!(
                "老钱登录响应缺少 token: {}",
                laoqian_message(&login_payload).unwrap_or_else(|| login_payload.to_string())
            )
        })?
        .to_string();

    let fetch_url = upstream_url(upstream, upstream.fetch_path.as_deref())
        .ok_or_else(|| "老钱上游缺少 fetch_path".to_string())?;
    for _attempt in 0..5 {
        let fetch_response = state
            .client
            .post(fetch_url.clone())
            .header(reqwest::header::ORIGIN, origin.as_str())
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

        let raw_payload = fetch_response.text().await.unwrap_or_default();
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
                build_laoqian_capture_fields(state, context, item).await?;
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

    let response = state
        .client
        .post(url)
        .header(reqwest::header::ORIGIN, origin.as_str())
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
            task_items.push(ClientTaskItem {
                goods_id: goods_id.clone(),
                sku_id,
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
                    sku_id: item.get("sku_id")?.as_str()?.trim().to_string(),
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
    if !goods_id.is_empty() {
        return vec![ClientTaskItem {
            goods_id: goods_id.clone(),
            sku_id: if sku_id.is_empty() { goods_id } else { sku_id },
            source_url: url_value.clone(),
            step_index: 0,
        }];
    }
    url_value
        .map(|url| extract_mock_task_items_from_url(&url))
        .unwrap_or_default()
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
                    sku_id,
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
            sku_id: if sku_id.is_empty() { goods_id } else { sku_id },
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

fn build_mock_records_from_lines(lines: &str) -> Vec<Value> {
    lines
        .lines()
        .map(|line| line.trim())
        .filter(|line| !line.is_empty())
        .map(|line| json!({ "url": line.trim_matches('`').trim_matches('"') }))
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
    let response = state
        .client
        .post(format!(
            "{}/api/item/drop",
            upstream.base_url.trim_end_matches('/')
        ))
        .header(reqwest::header::ORIGIN, origin.as_str())
        .header(reqwest::header::COOKIE, format!("token={session}"))
        .json(&json!({ "item_id": item_id.parse::<i64>().unwrap_or_default() }))
        .send()
        .await
        .map_err(|err| format!("释放老钱任务失败: {err}"))?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() {
        return Err(format!("释放老钱任务失败: {body}"));
    }
    Ok(body)
}

async fn check_laoqian_link_goods_id(
    state: &AppState,
    upstream: &UpstreamConfig,
    session: &str,
    item_id: &str,
    share_url: &str,
) -> Result<Value, String> {
    let origin = laoqian_origin(upstream);
    let response = state
        .client
        .post(format!(
            "{}/api/item/check_link_goods_id",
            upstream.base_url.trim_end_matches('/')
        ))
        .header(reqwest::header::ORIGIN, origin.as_str())
        .header(reqwest::header::COOKIE, format!("token={session}"))
        .json(&json!({
            "item_id": item_id.parse::<i64>().unwrap_or_default(),
            "share_url": share_url,
        }))
        .send()
        .await
        .map_err(|err| format!("老钱链接校验请求失败: {err}"))?;
    let status = response.status();
    let body = parse_response_value(response).await;
    if !status.is_success() {
        return Err(format!("老钱链接校验失败: {body}"));
    }
    Ok(body)
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
    use super::{build_laoqian_task_from_payload, extract_mock_task_items_from_url};
    use crate::models::UpstreamType;
    use crate::state::AppState;
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
}

async fn build_laoqian_capture_fields(
    state: &AppState,
    context: &IssuedTaskContext,
    item: &ClientSubmitTaskItem,
) -> Result<(Vec<String>, Vec<String>), String> {
    let mut uploaded = Vec::new();
    for capture_id in &item.capture_ids {
        let Some(capture) = state.get_capture(capture_id) else {
            continue;
        };
        uploaded
            .push(upload_laoqian_capture(state, context, &capture.path, &capture.file_name).await?);
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
    context: &IssuedTaskContext,
    path: &std::path::Path,
    file_name: &str,
) -> Result<String, String> {
    let Some(item_id) = context.item_id.as_deref() else {
        return Err("老钱任务缺少 item_id".to_string());
    };
    let token = context
        .laoqian_upload_token
        .clone()
        .or_else(|| context.laoqian_session_token.clone())
        .ok_or_else(|| "老钱任务缺少上传 token".to_string())?;
    let bytes = std::fs::read(path).map_err(|err| format!("读取本地截图失败: {err}"))?;
    let form = reqwest::multipart::Form::new()
        .part(
            "file",
            reqwest::multipart::Part::bytes(bytes.clone()).file_name(file_name.to_string()),
        )
        .text("file_name", file_name.to_string())
        .text("file_size", bytes.len().to_string())
        .text("item_id", format!("tw/{item_id}"))
        .text("token", token);
    let response = state
        .client
        .put("https://file.yqlaoqian111.com/upload")
        .header(reqwest::header::ORIGIN, LAOQIAN_DEFAULT_ORIGIN)
        .multipart(form)
        .send()
        .await
        .map_err(|err| format!("上传老钱截图失败: {err}"))?;
    if !response.status().is_success() {
        return Err(format!("上传老钱截图失败: {}", response.status()));
    }
    let payload: Value = response
        .json()
        .await
        .map_err(|err| format!("解析老钱上传响应失败: {err}"))?;
    payload
        .get("path")
        .and_then(|value| value.as_str())
        .map(|value| value.to_string())
        .ok_or_else(|| "老钱上传响应缺少 path".to_string())
}
