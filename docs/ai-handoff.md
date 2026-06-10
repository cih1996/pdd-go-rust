# pdd-go-rust AI 接力文档

## 1. 目标

- 旧架构:
  - `workspace/frontend`: Vue + Electron 前端
  - `workspace/backend`: Python 业务后端
  - `Interface-adapter-server`: Python 适配器
  - `ocr-server` / `opencv` 走本地 API
- 新架构:
  - `frontend`: 复用旧 Vue 前端
  - `unified-server`: Go 业务端，最终内置 ADB / OpenCV / OCR / 业务循环
  - `adapter-rs`: Rust 适配器，独立隔离上游协议

设计原则:

- 前端尽量复用旧 `workspace/frontend`
- Go 后端尽量先兼容旧前端接口，再逐步迁移实际执行逻辑
- Rust 适配器只做上游协议适配，不承载 OCR/OpenCV

## 2. 当前状态

### 2.1 已完成

- 新仓库已可独立作为 Git 仓库使用
- 已新增仓库根 `.gitignore`
- 已清理 `adapter-rs/target`
- `frontend` 已不再是说明页，已经开始复用旧业务前端
- `unified-server` 已可启动，并可把 `frontend/dist` 挂到 `http://127.0.0.1:8080/`
- `unified-server` 已有基础兼容接口:
  - `GET /api/health`
  - `GET /api/state`
  - `GET /api/runtime/summary`
  - `GET/POST /api/upstreams`
  - `PUT /api/upstreams/{id}`
  - `POST /api/upstreams/{id}/toggle`
  - `DELETE /api/upstreams/{id}`
  - `GET /api/templates`
  - `GET /api/devices`
  - `POST /api/tasks/start` 占位
  - `GET /api/debug/vision` 占位
- `adapter-rs` 已有首版接口:
  - `GET /api/health`
  - `GET /api/state`
  - `GET/POST /api/upstreams`
  - `PUT /api/upstreams/{id}`
  - `POST /api/upstreams/{id}/toggle`
  - `DELETE /api/upstreams/{id}`
  - `POST /api/client/fetch-task`
  - `POST /api/client/submit-task`
- `adapter-rs` 已内置两个 provider:
  - `mock_local`
  - `laoqian_worker`
- `mock_local` 已支持内置样例任务，不依赖外部 mock 服务

### 2.2 未完成

- `adapter-rs` 还没有持久化
- `adapter-rs` 还没有 `upload-capture`
- `adapter-rs` 老钱逻辑还是首版骨架，未完全迁移 Python 版全部细节
- `unified-server` 还没有真正的:
  - 设备连接
  - 任务启动/停止
  - 模板 CRUD 完整实现
  - 单步调试
  - WebSocket 状态推送
  - Go 内置 OpenCV/OCR 执行链路
  - 平台账号管理
  - 系统配置持久化
  - 明细与日志持久化

## 3. 启动与调试

### 3.1 当前脚本

- `start-unified-server.bat`
  - 设计为前台调试脚本
  - 再次执行时，如果 `8080` 上占用的是旧 `unified-server`，会自动杀掉旧进程并前台重启
- `stop-unified-server.bat`
- `restart-unified-server.bat`
- `start-adapter.bat`
- `start-frontend.bat`
- `start-dev-all.bat`

### 3.2 环境依赖

- Go 业务端:
  - 需要 `go`
- Rust 适配器:
  - 需要 `cargo`
  - Windows 还需要 Visual Studio Build Tools 的 C++ 工具链
  - 关键缺失报错是 `link.exe not found`

### 3.3 端口

- Go: `8080`
- Rust 适配器: `8091`
- 前端 dev: `5173` 起，若被占用会自动换端口

## 4. 目录说明

- `frontend`
  - 新前端，正在复用旧 `workspace/frontend`
- `internal/httpapi`
  - Go HTTP 接口兼容层
- `internal/upstream`
  - Go 侧上游配置存储
- `internal/device`
  - Go 侧设备服务骨架
- `internal/task`
  - Go 侧任务服务骨架
- `internal/template`
  - Go 侧模板存储骨架
- `adapter-rs/src/routes`
  - Rust 适配器路由
- `adapter-rs/src/state`
  - Rust 适配器运行时状态
- `docs/adapter-apipost.md`
  - Apipost 调试说明
- `docs/architecture.md`
  - 新架构草案

## 5. 旧 Python 适配器必须迁移的协议

参考旧实现:

- `Interface-adapter-server/app/models.py`
- `Interface-adapter-server/app/services.py`

### 5.1 上游类型

- `mock_upstream`
- `laoqian_worker`
- `custom_http`

### 5.2 通用协议模型

旧模型关键字段:

- `ClientTask`
  - `task_id`
  - `upstream_task_ref`
  - `source_code`
  - `source_name`
  - `account_id`
  - `account_name`
  - `task_items[]`
- `ClientTaskItem`
  - `goods_id`
  - `sku_id`
  - `step_index`
- `ClientSubmitRequest`
  - `task_id`
  - `type`: `success|failure|cancelled`
  - `device_id`
  - `message`
  - `task_items[]`
- `ClientSubmitTaskItem`
  - `goods_id`
  - `sku_id`
  - `recognition`
  - `message`
  - `capture_ids`
  - `capture_urls`

### 5.3 老钱 token 格式

- 格式: `account|secret_key`
- 可选第三段上传 token: `account|secret_key|upload_token`

Python 旧逻辑:

- `fetch-task` 强制要求 token
- 对老钱 token 先拆成:
  - `account`
  - `secret_key`
  - `upload_token`

### 5.4 老钱登录流程

1. `POST /api/user/login`
2. 请求体:
   - `account`
   - `secret_key`
3. 返回里取 `token`
4. 将其保存为:
   - `session_token`
   - `upload_token`，优先使用显式第三段，否则回退为 `session_token`

旧 Python 版还做了:

- 认证信息缓存到 runtime `auth`
- 失败后识别 `401` 或 `message == "请登录"` 时自动重登一次
- 请求头携带 `Cookie: token=<session_token>`

### 5.5 老钱取任务流程

接口:

- `POST /api/item/fetch`

请求体:

```json
{
  "item_type": "ds_detail"
}
```

返回关键结构:

- `item`
  - `item_id`
  - `item_name`
  - `content`，其中是 JSON 字符串

`content` 解析后关注:

- `spu_id`
- `spu_name`
- `skus[]`
  - `source_sku_id`
  - `source_url`

取任务后的本地构建规则:

- `goods_id` 优先取 `spu_id`
- 否则从第一个 `source_url` 里解析 `goods_id`
- 每个 `sku` 生成一个 step:
  - `index`
  - `sku_id = source_sku_id`
- 最终转成统一 `ClientTask.task_items[]`

### 5.6 老钱任务去重与上下文

旧 Python 适配器维护:

- `active_jobs`
- `issued_tasks`
- `account_locks`

核心规则:

- 同一个 `item_id` 只允许一个活动任务
- 同一个 `item_id` 不允许重复生成多个私有 `task_id`
- 同一个平台账号同时只能绑定一个进行中的老钱任务
- 提交时依赖 `task_id -> issued_task_context` 回查

Rust 版目前只做了:

- `issued_tasks`
- 无持久化
- 无 `active_jobs`
- 无 `account_locks`

这是后续必须补齐的重点，否则老钱多 SKU/多账号场景会继续丢上下文。

### 5.7 老钱失败与取消

取消接口:

- `POST /api/item/drop`

请求体:

```json
{
  "item_id": 123456
}
```

旧逻辑:

- `cancelled`:
  - 直接 `drop`
  - 清理任务上下文
- `failure`:
  - 先 `drop`
  - 再把 `goods_id` 加入黑名单
  - 防止同货品被反复领取

### 5.8 老钱成功前的链接校验

接口:

- `POST /api/item/check_link_goods_id`

请求体:

```json
{
  "item_id": 123456,
  "share_url": "..."
}
```

旧逻辑:

- 仅在多 SKU 任务时触发
- 如果校验失败，不提交 work，直接走异常处理

### 5.9 老钱截图上传

上传地址不是主站，而是:

- `https://file.yqlaoqian111.com/upload`

方法:

- `PUT`

表单字段:

- `file`
- `file_name`
- `file_size`
- `item_id = tw/<item_id>`
- `token`

返回中要取:

- `path`

旧逻辑:

- 上传 token 优先级:
  1. 显式第三段 `upload_token`
  2. `session_token`
- 若上传返回需要登录，则重新登录一次后重试

### 5.10 老钱成功提交 work

接口:

- `POST /api/item/work`

请求体实际结构:

```json
{
  "item_id": 123456,
  "work_result": "{\"share_url\":\"...\",\"skus\":[...]}"
}
```

注意:

- `work_result` 是 JSON 字符串，不是对象

`work_result.skus[]` 旧逻辑结构:

```json
{
  "sku_id": "xxx",
  "status": "款式存在",
  "youhuiquan": ["uploaded/path.png"],
  "xiangqingjietu": ["uploaded/path.png"]
}
```

截图规则:

- 如果只有 1 张成功图:
  - `xiangqingjietu` 和 `youhuiquan` 都复用同一张
- 如果有 2 张:
  - 第一张当 `xiangqingjietu`
  - 最后一张当 `youhuiquan`

### 5.11 老钱错误触发拉黑

旧逻辑里，出现以下关键字时会拉黑 `goods_id`:

- `题目已失效`
- `不匹配`
- `已风控`
- `已失效`

## 6. 旧 Python 业务逻辑必须迁移的规则

参考旧实现:

- `workspace/backend/app/services.py`

### 6.1 任务来源与候选区

旧后端不是设备自己单独抢任务，而是:

1. 后端收集所有可用上游候选:
   - 上游配置
   - 平台账号
2. 后端轮询账号并发预取任务
3. 预取到的任务先进入 `pending_task_queue`
4. 空闲设备再从候选区拿任务

关键规则:

- 每个账号同时只能有一个 inflight task
- 业务重复任务根据:
  - `source_code + upstream_task_ref`
  - 若无则回退 `task_id`
- `desired_pending_task_count = running_devices * 2`

### 6.2 系统配置中的 SKU 数限制

旧后端有:

- `max_task_sku_count`

规则:

- `0` 表示不限
- 如果预取任务的 SKU 数超过限制:
  - 立即取消上游任务
  - 不进入候选区

### 6.3 多 SKU 执行方式

统一任务结构:

- 一个上游任务对应一组 `task_items`
- 业务执行时按 SKU 顺序逐个跑
- 每个 SKU:
  - 生成 URL
  - 打开链接
  - 循环识别直到终态
- 只有全部 SKU 成功，整个 submit 才是 `success`
- 任意 SKU 失败，整个任务 submit 为 `failure`

### 6.4 终态识别顺序

每轮截图后的模板识别顺序固定:

1. `account_risk`
2. `fail_release`
3. `click_image`
4. `success_image`

含义:

- `account_risk`
  - 命中后:
    - 当前任务失败
    - 停止该设备后续执行
- `fail_release`
  - 命中后:
    - 当前任务失败
    - 设备继续运行下一任务
- `click_image`
  - 命中后:
    - 立刻点击
    - 等待
    - 重新截图进入下一轮
- `success_image`
  - 命中后:
    - 当前 SKU 成功

### 6.5 OCR 规则

旧后端已经做过一轮重要优化，必须保留:

1. 只有本轮模板集里存在 OCR 模板，才触发 OCR
2. 同一轮截图只做一次 OCR
3. OCR 结果缓存到 `ocr_cache["full_scan"]`
4. 本轮所有 OCR 模板复用同一个 OCR 结果

对应规则:

- 不允许每个 OCR 模板都单独请求一次 OCR
- 这点迁到 Go 内置 OCR 时也必须保持

### 6.6 OCR `&` 规则

`expected_text` 支持:

- `店铺优惠&立即支付`

语义:

- 按 `&` 拆分成多个 token
- 所有 token 都必须在 OCR 结果里命中
- 每个 token 都要满足阈值过滤

### 6.7 OCR crop 规则

OCR 不是简单全文 contains，而是:

- 对 OCR 返回候选框先做 crop 区域过滤
- 只在选区内匹配文本
- 然后再做 token 匹配和 confidence 过滤

### 6.8 成功图截图规则

- 正常成功:
  - 保存成功图
- 如果此前命中过 `click_image`
  - 最终成功时保留两张图:
    - 点击前的图
    - 成功后的图

这也是后续老钱上传时 1 张图/2 张图分流的依据。

### 6.9 适配器 submit 规则

旧业务端成功提交时会先把本地截图传给适配器:

1. 调 `upload-capture`
2. 得到 `capture_id`
3. 再把 `capture_id` 带到 `submit-task`

当前 Rust 版还没有这个接口，所以 Go 版真实提交链路还没法完全迁完。

## 7. Rust 适配器当前实现与差距

### 7.1 当前已实现

- 内存态上游配置
- 内存态 `issued_tasks`
- mock 内置任务
- 老钱基本登录/取任务/提交成功/失败骨架

### 7.2 与旧 Python 版差距

- 缺 runtime JSON 持久化
- 缺 `upload-capture`
- 缺 detail/log/capture 存储
- 缺 `active_jobs`
- 缺 `account_locks`
- 缺黑名单 `blocked_goods_ids`
- 缺老钱自动重登与上传 token 的完整策略
- 缺老钱 `check_link_goods_id`
- 缺成功前的截图上传逻辑
- 缺失败后 goods_id 拉黑
- 缺基于 `item_id` 的 submit 上下文兜底

## 8. Go 业务端当前实现与差距

### 8.1 当前已实现

- 基础路由
- 旧前端首层兼容
- 上游配置内存 CRUD
- 前端静态文件托管
- 调试脚本

### 8.2 需要尽快补的接口

高优先级:

- `/api/devices/connect`
- `/api/tasks/start`
- `/api/tasks/stop`
- `/api/templates` 的新增/更新/删除/启停/排序
- `/api/debug/vision`
- `/api/debug/ocr-selection`
- `/ws/events`

中优先级:

- `/api/platform-accounts`
- `/api/system-config`
- `/api/details`
- `/api/detail-records/...`

### 8.3 最终必须迁入 Go 的业务能力

- ADB:
  - 设备枚举
  - 连接
  - 打开链接
  - 点击
  - 截图
- 任务循环:
  - 候选区预取
  - 多账号轮询
  - 设备派发
  - 多 SKU 顺序执行
- OCR/OpenCV:
  - 单轮共享 OCR 结果
  - 本地图像匹配
  - 调试选区
- 持久化:
  - templates
  - runtime
  - details
  - accounts
  - system config

## 9. 推荐迁移顺序

### 第一阶段: 先把前端页面都喂起来

1. 补齐 Go 兼容接口，让旧前端不报错
2. 保证页面能浏览、能增删改查基础配置

### 第二阶段: 先迁控制面，不迁执行链

1. 设备连接
2. 模板管理
3. 平台账号
4. 系统配置
5. WebSocket 基础推送

### 第三阶段: 迁适配器完整协议

1. `upload-capture`
2. 老钱 `check_link_goods_id`
3. 老钱 `upload`
4. 老钱 `work`
5. `active_jobs / issued_tasks / account_locks / blocked_goods_ids`

### 第四阶段: 迁业务执行主循环到 Go

1. 候选区预取
2. 多账号共同抢任务
3. 多 SKU 串行执行
4. OCR/OpenCV 内置
5. 成功/失败/风控终态上报

## 10. 建议优先待办

### P0

- 给 `adapter-rs` 增加 `upload-capture`
- 给 `adapter-rs` 增加老钱完整 submit 流程
- 给 `adapter-rs` 增加 `active_jobs / account_locks / blocked_goods_ids`
- 给 Go 端补 `/api/tasks/stop`
- 给 Go 端补 `/api/devices/connect`

### P1

- 给 Go 端补模板 CRUD
- 给 Go 端补调试接口
- 给 Go 端补平台账号接口
- 给 Go 端补系统配置接口

### P2

- Go 内置 OCR/OpenCV
- Go 实现候选区预取
- Go 实现多账号轮询与任务派发

## 11. 调试建议

- Rust 适配器优先用 Apipost 单独打通
- Go 业务端优先用浏览器 + `/api/state` 看兼容情况
- 每迁一段协议，就保持:
  - mock_local 单独可测
  - laoqian_worker 单独可测
  - 前端页面能看到当前状态

## 12. 本次整理 Git 的结果

- 新增仓库根 `.gitignore`
- 已忽略:
  - `adapter-rs/target`
  - `frontend/node_modules`
  - `frontend/dist`
- 已删除:
  - `adapter-rs/target`

后续如再出现 Git 脏文件，优先检查是否属于真实源码，避免误删:

- `frontend/package-lock.json`
- `adapter-rs/Cargo.lock`

这两个锁文件建议保留并纳入版本控制。
