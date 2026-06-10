# adapter-rs Apipost 调试说明

## 基础地址

- Rust 适配器：`http://127.0.0.1:8091`
- Go 业务端：`http://127.0.0.1:18080`

## 启动说明

- Rust 适配器目录：`pdd-go-rust/adapter-rs`
- Go 业务端目录：`pdd-go-rust`
- 当前这台机器还没安装 `cargo` 和 `go`，所以这里只先给你保留调试命令：

```bash
cd pdd-go-rust/adapter-rs
cargo run
```

```bash
cd pdd-go-rust
go run .
```

## 适配器接口

### 1. 健康检查

- `GET /api/health`

### 2. 查看适配器状态

- `GET /api/state`

返回里会带：

- `upstreams`
- `recent_logs`
- `recent_reports`
- `summary`

### 3. 查看上游列表

- `GET /api/upstreams`

### 4. 新增上游

- `POST /api/upstreams`

请求体：

```json
{
  "name": "本地模拟上游测试",
  "upstream_type": "mock_upstream",
  "enabled": true,
  "priority": 10,
  "base_url": "http://127.0.0.1:8102",
  "fetch_path": "/api/mock/fetch-task",
  "report_success_path": "/api/mock/report-success",
  "report_failure_path": "/api/mock/report-failure",
  "notes": "Apipost 测试"
}
```

字段说明：

- `code` 可不传，不传时自动生成
- `base_url` 可不传
- `fetch_path/report_success_path/report_failure_path` 现在都支持自定义
- `mock_local` 默认已经内置一套适配器本地模拟任务；如果 `base_url/fetch_path` 留空，`fetch-task` 会直接返回内置样例任务

支持类型：

- `mock_upstream`
- `laoqian_worker`
- `custom_http`

### 5. 更新上游

- `PUT /api/upstreams/{upstream_id}`

请求体同新增。

### 6. 切换启用状态

- `POST /api/upstreams/{upstream_id}/toggle`

```json
{
  "enabled": false
}
```

### 7. 删除上游

- `DELETE /api/upstreams/{upstream_id}`

### 8. 客户端拉任务

- `POST /api/client/fetch-task`

内置 mock 直接测：

```json
{
  "device_id": "emulator-5554",
  "source_code": "mock_local",
  "token": "mock"
}
```

如果 `mock_local` 没被你改成外部地址，这个请求会直接返回一组内置的 2 个 SKU 样例任务。

```json
{
  "device_id": "emulator-5554",
  "source_code": "mock_local",
  "token": "mock"
}
```

老钱测试时：

```json
{
  "device_id": "emulator-5554",
  "source_code": "laoqian_worker",
  "token": "account|secret_key"
}
```

### 9. 客户端提交任务

- `POST /api/client/submit-task`

成功：

```json
{
  "task_id": "mock_local-task-1",
  "type": "success",
  "device_id": "emulator-5554",
  "message": "Apipost success",
  "task_items": [
    {
      "goods_id": "10001",
      "sku_id": "sku-1",
      "recognition": "success_image",
      "message": "命中成功图",
      "capture_ids": [],
      "capture_urls": []
    }
  ]
}
```

如果你当前测的是内置 mock，没有配置外部回调地址，适配器会直接回显一份 `builtin_mock` 结果，方便你单机验证 submit 链路。

失败或取消：

```json
{
  "task_id": "mock_local-task-1",
  "type": "failure",
  "device_id": "emulator-5554",
  "message": "Apipost failure",
  "task_items": []
}
```

## Go 业务端接口

### 1. 查看业务端上游列表

- `GET /api/upstreams`

### 2. 新增业务端上游配置

- `POST /api/upstreams`

```json
{
  "name": "业务端联调上游",
  "code": "mock_local",
  "upstream_type": "mock_upstream",
  "enabled": true,
  "priority": 10,
  "base_url": "http://127.0.0.1:8091",
  "notes": "给 Go 业务端联调 Rust 适配器"
}
```

说明：

- `enabled` 可不传，默认 `true`
- `priority` 可不传，默认 `100`
- `base_url` 可不传，默认 `http://127.0.0.1:8091`
