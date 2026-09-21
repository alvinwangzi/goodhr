---
kind: configuration_system
name: GoodHR5 配置系统：环境变量 + 数据库运行时配置 + 内嵌平台配置
category: configuration_system
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/cmd/server/main.go
    - goodhr5/cloud/backend/internal/httpapi/config.go
    - goodhr5/cloud/backend/internal/httpapi/runtime_config.go
    - goodhr5/cloud/backend/.env.example
    - goodhr5/local-agent-go/internal/config/config.go
    - goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go
    - goodhr5/local-agent-go/internal/config/config.go
    - goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go
    - goodhr5/local-agent-go/internal/platform/config.go
    - goodhr5/docker-compose.yml
---

## 1. 总体方案

GoodHR5 采用**分层配置**策略，按“进程启动参数 → 环境变量 → 默认值”的优先级加载；运行期可变的业务开关则通过数据库中的 `system_configs` 表以键值对形式持久化，由前端管理页面在线维护。

- **云端后端（Go）**：仅依赖环境变量与数据库，无配置文件。
- **本地 Agent（Go v1/v2）**：命令行 flag + 环境变量 + 用户数据目录，并自动创建所需子目录。
- **前端 Next.js**：通过 `docker-compose.yml` 注入 `CLOUD_API_BASE`、`NEXT_PUBLIC_*` 等变量。
- **平台行为配置**：通过 `//go:embed` 将各招聘平台的选择器/URL 配置编译进二进制，随程序发布。

## 2. 关键文件与位置

| 组件 | 核心文件 | 作用 |
|---|---|---|
| 云端后端入口 | `goodhr5/cloud/backend/cmd/server/main.go` | 读取 `GOODHR_CLOUD_ADDR`、日志路径 |
| 云端后端配置 | `goodhr5/cloud/backend/internal/httpapi/config.go` | `LoadConfigFromEnv()` 解析所有 PG/Redis/SMTP/SuperAdmins 等环境变量 |
| 云端运行时配置 API | `goodhr5/cloud/backend/internal/httpapi/runtime_config.go` | 从 `system.onboarding_config` 返回给已登录用户的本地程序/组件配置 |
| 云端 .env 模板 | `goodhr5/cloud/backend/.env.example` | 列出部署所需的环境变量 |
| 旧版本地 Agent 配置 | `goodhr5/local-agent-go/internal/config/config.go` | `NewWithDataDir` 解析 host/port/data-dir/env，自动建目录 |
| 新版本地 Agent 配置 | `goodhr5/local-agent-go/internal/config/config.go` | `Load` 解析更多字段（WorkerPort、NodePath、OCRExecutable、DatabasePath 等） |
| 平台内置配置加载 | `goodhr5/local-agent-go/internal/platform/config.go` | `//go:embed boss/config.json` 等，提供 `LoadConfig` / `ValidateTaskConfig` |
| Docker Compose | `goodhr5/docker-compose.yml` | 为 backend/frontend 注入环境变量 |

## 3. 架构与约定

### 3.1 加载顺序（统一模式）

```text
命令行 flag / 函数参数 > 环境变量 > 代码默认值
```

- 云端后端：`main.go` 中 `envOrDefault("GOODHR_CLOUD_ADDR", ":8084")`，日志路径 `GOODHR_CLOUD_LOG_FILE`。
- 旧版本地 Agent：`config.NewWithDataDir(host, port, customDataDir)`，再读 `GOODHR_DATA_DIR`、`GOODHR_CONSOLE_MANIFEST_URL`、`GOODHR_CLOUD_API_BASE`、`GOODHR_AUTO_OPEN_CONSOLE`。
- 新版本地 Agent：`config.Load(host, port, dataDir)`，再读 `GOODHR_WORKER_PORT`、`GOODHR_CLOUD_API_BASE`、`GOODHR_CONSOLE_URL`、`GOODHR_NODE_PATH`、`GOODHR_OCR_EXECUTABLE`、`GOODHR_AUTO_OPEN_CONSOLE`。

### 3.2 数据类型解析工具

每个模块都实现自己的轻量 helper：
- `envOrDefault(key, fallback string)` — 字符串
- `envInt(key, fallback int)` — 端口/数字，非法时回退
- `envBool(key, fallback bool)` — 支持 `1/true/yes/on` 与 `0/false/no/off`
- `envList(key, fallback []string)` — 逗号分隔列表（云端后端超级管理员）

### 3.3 数据目录约定

两个本地 Agent 均遵循：
- 优先使用 flag `-data-dir`；否则 `GOODHR_DATA_DIR`；否则 OS 用户配置目录下的 `GoodHR`（新版追加 `local-agent-new`）。
- 启动时调用 `EnsureDirs()` / `EnsureDirectories()` 自动创建 `runtime`、`logs`、`profiles`、`downloads`、`screenshots`、`extensions` 等子目录。
- 下载目录优先走系统 Downloads，失败回退到 `os.TempDir()/GoodHR/Downloads`。

### 3.4 运行时配置（数据库驱动）

云端后端通过 `SystemConfigStore` 读写 `system_configs` 表（见 migrations `0021_system_app_config.sql` 起），典型 key：
- `system.onboarding_config` — 本地程序与运行组件的配置，由 `/api/runtime-config` 暴露给已登录用户。
- 微信支付等敏感配置明确注释“不再走环境变量”，改为后台「系统配置」页面在线维护。

### 3.5 平台行为配置（内嵌 JSON）

新版本地 Agent 使用 Go `//go:embed` 将 `boss/config.json`、`zhaopin/config.json`、`liepin/config.json`、`hliepin/config.json` 编译进二进制，通过 `platform.LoadConfig(platformID)` 加载，并用 `ValidateTaskConfig` 校验自动回复所需的 selector 是否完整。这使平台选择器变更无需重新部署 Agent，只需更新内嵌资源或后续迁移到数据库。

### 3.6 构建期注入

- 新版本地 Agent 的 `DefaultCloudURL`、`DefaultConsoleURL` 注释写明“正式打包时通过 ldflags 固定为线上地址”，开发环境分别指向 `http://127.0.0.1:8084` 和 `http://localhost:5173`。
- 版本信息通过 `-version` flag 在 `cmd/goodhr-local-agent/main.go` 中注入。

## 4. 约定与约束

1. **所有外部依赖（PG、Redis、SMTP、OCR、Node）必须通过环境变量启用**，未配置时回退到内存实现或默认路径，保证单机开发可用。
2. **敏感配置不放入代码仓库**：`.env.example` 仅列占位符；微信支付等已迁移至数据库配置页。
3. **数据目录非空校验**：`EnsureDirs` 中对空目录显式返回错误，防止误用。
4. **端口范围校验**：`envInt` 对端口做 `<=0 || >65535` 检查，非法值回退默认。
5. **布尔环境变量多值兼容**：`true/false/yes/no/on/off/1/0` 均可识别。
6. **SuperAdmins 列表**：通过逗号分隔环境变量 `GOODHR_SUPER_ADMINS` 传入，空值回退到硬编码默认邮箱。
7. **日志输出**：云端后端同时写 stdout 与文件；本地 Agent 在 Windows 上只写文件，Unix 上双写 stderr+文件。
8. **容器化**：`docker-compose.yml` 通过 `env_file` 与 `environment` 注入配置，backend 默认监听 `:8084`，frontend 通过 `NEXT_PUBLIC_CLOUD_API_BASE` 暴露给浏览器。

## 5. 适用性判断

该仓库存在完整的配置体系，覆盖进程启动、环境变量、数据库运行时配置、内嵌平台配置、构建期注入等多个层面，因此本类别适用。
