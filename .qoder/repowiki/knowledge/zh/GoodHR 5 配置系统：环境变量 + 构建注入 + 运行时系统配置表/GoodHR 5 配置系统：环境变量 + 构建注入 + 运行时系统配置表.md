---
kind: configuration_system
name: GoodHR 5 配置系统：环境变量 + 构建注入 + 运行时系统配置表
category: configuration_system
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/internal/httpapi/config.go
    - goodhr5/cloud/backend/cmd/server/main.go
    - goodhr5/cloud/backend/.env.example
    - goodhr5/cloud/backend/internal/httpapi/runtime_config.go
    - goodhr5/cloud/backend/internal/httpapi/system_config_store.go
    - goodhr5/local-agent-go/internal/config/config.go
    - goodhr5/local-agent-go/internal/config/build_config.go
    - goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go
---

## 1. 总体方案

GoodHR 5 是一个多组件 Go/Node 项目，包含云端后端（`goodhr5/cloud/backend`）、本地 Agent（`goodhr5/local-agent-go`）和 Next.js 控制台前端。配置系统按“启动期配置”和“运行时业务配置”两条线组织：

- **启动期配置**：通过 `os.Getenv` 读取环境变量，配合少量命令行 flag。
- **运行时业务配置**：通过云端后端的 `system_configs` 数据库表（JSONB），由 `SystemConfigStore` 抽象提供内存/PostgreSQL 双实现。
- **构建期配置**：本地 Agent 通过 Base64 嵌入的 JSON 注入 `ConsoleURL`、`CloudAPIBase`、`Environment` 等公开地址，构建时校验环境一致性，不携带密钥。

没有使用统一的配置框架（如 Viper、envconfig），所有加载逻辑都是手写 `os.Getenv` + 自定义 helper。

## 2. 关键文件与包

| 组件 | 路径 | 职责 |
|---|---|---|
| 云端后端配置加载 | `goodhr5/cloud/backend/internal/httpapi/config.go` | 从环境变量加载 `Config`，工厂方法根据是否连接 PG/Redis 返回内存或持久化 Store |
| 云端 main | `goodhr5/cloud/backend/cmd/server/main.go` | 解析 `GOODHR_CLOUD_ADDR`、日志路径，启动 HTTP 服务 |
| 云端 .env 示例 | `goodhr5/cloud/backend/.env.example` | 列出全部环境变量键名及说明 |
| 运行时组件配置 API | `goodhr5/cloud/backend/internal/httpapi/runtime_config.go` | 暴露 `/runtime-config`，返回本地程序与运行组件下载地址 |
| 系统配置存储抽象 | `goodhr5/cloud/backend/internal/httpapi/system_config_store.go` | `SystemConfigStore` 接口 + Memory/Postgres 实现，默认值含 onboarding_config、subscription_plans 等 |
| 本地 Agent 配置结构 | `goodhr5/local-agent-go/internal/config/config.go` | `Config` 结构体，负责数据目录、端口、浏览器 profile 目录等路径计算与目录创建 |
| 本地 Agent 构建配置 | `goodhr5/local-agent-go/internal/config/build_config.go` | `BuildConfig` + `RuntimeBuildConfig()`，支持嵌入式 Base64 JSON 或进程环境变量回退 |
| 本地 Agent 入口 | `goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go` | 解析 `-host/-port/-data-dir/-open-console/-restart/-print-config` 等 flag |

## 3. 架构与约定

### 3.1 环境变量命名规范
所有环境变量统一以 `GOODHR_` 前缀命名，例如：
- `GOODHR_APP_ENV`：`dev` / `prod`，决定开发/生产行为（邮件降级、Redis/PG 可选等）。
- `GOODHR_PG_DSN`、`GOODHR_REDIS_ADDR`、`GOODHR_SMTP_HOST` 等：外部依赖连接串。
- `GOODHR_CLOUD_ADDR`：云端监听地址，默认 `:8084`。
- `GOODHR_CONSOLE_URL`、`GOODHR_CLOUD_API_BASE`、`GOODHR_CONSOLE_MANIFEST_URL`：本地 Agent 在源码模式下使用的云端地址。
- `GOODHR_DATA_DIR`、`GOODHR_AUTO_OPEN_CONSOLE`：本地 Agent 的数据目录与自动打开控制台开关。

云端 `LoadConfigFromEnv` 集中读取这些变量，并提供 `envInt` / `envBool` / `envList` / `envString` 四个类型安全的 helper；布尔值接受 `true/false/1/0/yes/no/on/off` 多种写法。

### 3.2 构建期配置（本地 Agent）
`internal/config/build_config.go` 定义 `BuildConfig`（`environment`、`console_url`、`cloud_api_base`、`console_manifest_url`、`dev_scan_limit`）。运行时优先读取 `EmbeddedBuildConfig`（构建工具注入的 Base64 JSON），否则回退到进程环境变量。`parseBuildConfig` 使用 `json.Decoder.DisallowUnknownFields()` 拒绝未知字段，并强制要求 `environment` 为 `dev` 或 `prod`，`console_url` / `cloud_api_base` 必须是不含账号密码的完整 http/https URL，且 `cloud_api_base` 不允许查询参数或锚点。

### 3.3 运行时系统配置（云端）
`SystemConfigStore` 是云端运行时配置的抽象，键形如 `system.*`，值以 JSONB 字符串持久化。默认内存实现 `defaultMemorySystemConfigs` 内置了以下 key：
- `system.app_config`：公共系统配置（公告、管理员横幅、邮箱域名白名单等）。
- `system.subscription_plans`：订阅套餐定义。
- `system.onboarding_config`：本地程序版本、运行组件下载地址、试用天数等。
- `system.invite_config`、`system.guide`、`system.payment_wechat`。

`runtime_config.go` 的 `RuntimeConfigService.Current` 读取 `system.onboarding_config` 返回给前端；开发环境下 `applyDevComponentURLs` 会把组件下载 URL 覆盖为 `http://localhost:8084/uploads/<filename>`，避免依赖外部 OSS。

### 3.4 目录与数据布局（本地 Agent）
`Config.EnsureDirs()` 在启动时创建 `DataDir`、`RuntimeDir`、`LogsDir`、`OCRDir`、`FrontendDir`、`ProfilesDir`、`DownloadsDir`、`ScreenshotsDir` 八个目录，权限 `0755`。默认数据目录位于 `os.UserConfigDir()/HRPlus`，下载目录优先使用系统 Downloads 目录，失败则回退到 `os.TempDir()/GoodHR/Downloads`。

### 3.5 存储层切换策略
云端的几乎所有 Store（Auth、Agent、UserFlow、AIConfig、Subscription、Position、Cookie、Tenant、Payment 等）都遵循同一模式：`Config.XxxStore(db)` 根据 `db != nil` 选择 Postgres 实现，否则返回 Memory 实现。`PostgresDB()` 会先 Ping 再执行迁移，失败直接报错；未配置 DSN 时返回 nil，使整个应用可在无 PG 环境下运行。

## 4. 约定与约束

- **环境变量必须以 `GOODHR_` 开头**：仓库中所有环境变量（`.env.example`、`config.go`、`main.go`、`build_config.go`）均遵循该命名空间，未见其他前缀的环境变量。
- **`GOODHR_APP_ENV` 只允许 `dev` 或 `prod`**：`build_config.go` 的 `validate()` 显式检查，非两者即报错；`httpapi.Config.Env()` 对未识别值回退为 `dev`。
- **构建配置禁止携带密钥**：`build_config.go` 注释明确“构建时只将公开地址写入 EXE，不包含任何密钥”，且 `Validate` 拒绝 URL 中包含 userinfo。
- **构建配置必须单对象 JSON**：`parseBuildConfig` 二次解码空结构体并断言 EOF，拒绝多个 JSON 对象。
- **运行时系统配置键以 `system.` 前缀分组**：`SystemConfigStore.List(prefix)` 使用 `strings.HasPrefix(key, prefix)` 过滤，调用方统一传 `"system."`。
- **系统配置仅启用项可见**：`Get` 和 `List` 都附加 `enabled = true` 条件，禁用配置不会返回。
- **系统配置值存为 JSONB**：Postgres 实现的 `Save` 使用 `VALUES ($1, $2::jsonb, ...)`，插入时使用 `ON CONFLICT (config_key) DO UPDATE` 做 upsert。
- **微信支付配置不再走环境变量**：`.env.example` 注释明确“微信支付参数已改为在后台「系统配置」页面的「微信支付配置」里在线维护”。
- **本地 Agent 启动参数优先级**：flag > 环境变量 > 默认值。例如 `data-dir` flag 优先于 `GOODHR_DATA_DIR`，后者优先于 `os.UserConfigDir()/HRPlus`。
- **本地 Agent 端口探测范围**：`DefaultPort=55271`，`MaxPort=55279`，用于端口自动探测。
- **开发环境邮件降级**：`Mailer()` 在 dev 下直接返回 `DevMailer{}`，prod 下若 SMTP host/username/password 不全则降级并打印警告。
- **本地 Agent 日志输出**：Windows 下写文件，非 Windows 下同时写 stderr 和文件；云端日志通过 `MultiWriter(os.Stdout, file)` 同时输出 stdout 和文件。

## 5. 未观察到的内容

- 未发现 YAML/TOML/INI 等结构化配置文件作为运行时配置源（`boss.json`、`boss.normalized.json` 是平台页面选择器数据，不是通用配置）。
- 未发现 feature flag 框架；功能开关通过 `system.onboarding_config`、`system.app_config` 等 JSON 配置项控制。
- 未发现 `.env` 文件被 git 提交（`.gitignore` 排除），仅 `.env.example` 作为文档存在。
- 未发现统一的配置校验库（如 go-playground/validator），校验逻辑内联在 `BuildConfig.validate()` 和各 Store 构造处。