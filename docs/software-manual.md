# pdd-go-rust 软件使用说明书

## 1. 适用范围

本说明书面向日常使用人员，用于说明：

- 软件由哪些服务组成
- 启动前需要准备什么
- Windows 下如何配置 ADB
- 常见启动地址和访问方式

本说明书不涉及代码实现细节。

## 2. 软件组成

本项目由三部分组成：

- 前端页面：用于查看设备、任务、模板、日志和运行状态
- Go 业务端：负责设备接入、任务调度、识别执行和状态推送
- Rust 适配器：负责对接上游平台接口

默认端口如下：

- Go 业务端：`18080`
- Rust 适配器：`8091`
- 前端开发端口：`5173`

## 3. 使用前准备

启动前请确保本机已安装：

- Go
- Rust / Cargo
- ADB
- SQLite 数据文件由软件自动创建，无需单独安装数据库服务

如果是 Windows 系统，建议优先准备好 Android SDK 中的 `platform-tools`，确保可以正常使用 `adb`。

## 4. Windows 下 ADB 配置说明

Windows 环境下，软件支持以下几种 ADB 配置方式。

推荐优先级如下：

1. 直接配置 `ADB_PATH`
2. 配置 `ANDROID_SDK_ROOT`
3. 配置 `ANDROID_HOME`

### 4.1 推荐方式一：配置 ADB_PATH

如果你已经知道 `adb.exe` 的完整路径，推荐直接设置：

```text
ADB_PATH=C:\Android\platform-tools\adb.exe
```

适用场景：

- 已单独安装 `platform-tools`
- 想明确指定某一个 adb 版本
- 不希望依赖系统 PATH

### 4.2 推荐方式二：配置 ANDROID_SDK_ROOT

如果你已安装完整 Android SDK，推荐设置：

```text
ANDROID_SDK_ROOT=C:\Android\Sdk
```

软件会自动使用：

```text
C:\Android\Sdk\platform-tools\adb.exe
```

### 4.3 兼容方式三：配置 ANDROID_HOME

如果你的机器上仍在使用旧环境变量，也可以设置：

```text
ANDROID_HOME=C:\Android\Sdk
```

软件会自动使用：

```text
C:\Android\Sdk\platform-tools\adb.exe
```

### 4.4 建议

三种方式不要混乱配置。

建议按下面规则使用：

- 已知 adb 完整路径：用 `ADB_PATH`
- 已装 Android SDK：用 `ANDROID_SDK_ROOT`
- 老环境兼容：用 `ANDROID_HOME`

## 5. 启动方式

Windows 下可使用现有批处理脚本：

- `start-adapter.bat`：启动 Rust 适配器
- `start-unified-server.bat`：启动 Go 业务端
- `start-frontend.bat`：启动前端开发服务
- `start-dev-all.bat`：一次启动全部服务

## 6. SQLite 数据说明

软件已支持使用 SQLite 记录运行数据，便于后续做：

- 任务明细留存
- 统计分析
- 系统配置持久化
- 模板、账号、上游配置持久化

默认数据库文件位置：

```text
./.runtime/unified-server.db
```

如果需要自定义数据库文件位置，可通过环境变量设置：

```text
SQLITE_PATH=自定义数据库文件路径
```

### 6.1 自动建表与字段补齐

软件启动时会自动检查 SQLite 中的表结构。

如果后续版本新增了：

- 新表
- 新字段

系统会在启动时自动补齐缺失结构，无需手工执行 SQL 脚本。

这意味着后续需求变化时，数据库可随着版本自动演进。

## 7. 访问地址

启动成功后，可通过以下地址访问：

- Go 业务端健康检查：`http://127.0.0.1:18080/api/health`
- Go 业务端状态接口：`http://127.0.0.1:18080/api/state?range_key=today`
- Rust 适配器健康检查：`http://127.0.0.1:8091/api/health`

如果前端已构建并由 Go 业务端托管，也可以直接访问：

- `http://127.0.0.1:18080/`

## 8. 设备使用说明

设备接入依赖 ADB。

在 ADB 配置正确时，业务端可支持：

- 设备枚举
- 设备连接
- 打开链接
- 点击屏幕
- 设备截图

如果设备列表为空，请优先检查：

- ADB 环境变量是否配置正确
- `adb devices` 是否能在系统命令行中正常执行
- 设备是否已授权
- USB 或无线连接是否正常

## 9. 常见问题

### 9.1 设备列表为空

优先检查：

- ADB 是否安装完成
- 环境变量是否设置正确
- 命令行执行 `adb devices` 是否有返回

### 9.2 前端提示跨域错误

如果浏览器控制台出现 `CORS error`，需要确认：

- Go 业务端是否已经重启到最新版本
- 当前访问的前端地址是否与业务端地址不同

### 9.3 页面无数据

如果接口返回 200，但列表仍为空，通常表示：

- 当前还没有连接设备
- 还没有启动任务
- 还没有导入账号或模板
