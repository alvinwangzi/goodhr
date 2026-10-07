---
kind: logging_system
name: Go 标准库 log 日志系统（云端后端 + 本地 Agent）
category: logging_system
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/cmd/server/main.go
    - goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go
    - goodhr5/cloud/backend/internal/httpapi/agent_ws.go
    - goodhr5/cloud/backend/internal/httpapi/ai_config.go
    - goodhr5/cloud/backend/internal/httpapi/ai_wallet.go
---

## 1. 使用的框架/方案

HRPlus 的 Go 代码（`goodhr5/cloud/backend` 与 `goodhr5/local-agent-go`）统一使用 **Go 标准库 `log`**，没有引入 zap、logrus、zerolog、slog 等第三方日志框架。所有业务模块直接调用 `log.Printf` / `log.Fatal` / `log.Println`。

前端 Next.js 部分未发现专门的日志框架引用，未纳入本卡片范围。

## 2. 关键文件

- `goodhr5/cloud/backend/cmd/server/main.go` — 云端后端日志初始化：输出到 `logs/backend.log`，同时写入 stdout。
- `goodhr5/local-agent-go/cmd/goodhr-local-agent/main.go` — 本地 Agent 日志初始化：输出到 `<data_dir>/local-agent.log`，Windows 下仅写文件，非 Windows 下同时写 stderr + 文件。
- `goodhr5/cloud/backend/internal/httpapi/agent_ws.go` — 大量 `[云端WS]` 前缀的业务日志示例。
- `goodhr5/cloud/backend/internal/httpapi/ai_config.go`、`ai_wallet.go` — `[AI配置测试]`、`[内置AI]` 前缀日志。
- `goodhr5/cloud/backend/logs/backend.log` — 默认日志落盘路径。

## 3. 架构与约定

### 3.1 启动时全局初始化

两个可执行入口都在 `main()` 中尽早完成日志设置：

- 云端后端 (`cmd/server/main.go`)：
  - 通过环境变量 `GOODHR_CLOUD_LOG_FILE` 控制日志路径，默认 `logs/backend.log`；目录不存在则自动创建。
  - `os.OpenFile(logPath, O_CREATE|O_APPEND|O_WRONLY, 0644)` 打开文件。
  - `log.SetOutput(io.MultiWriter(os.Stdout, file))` 同时输出到 stdout 和文件。
  - `log.SetFlags(log.LstdFlags | log.Lmicroseconds)` 启用标准时间戳 + 微秒精度。

- 本地 Agent (`cmd/goodhr-local-agent/main.go`)：
  - 日志路径由 `cfg.LogsDir` 决定，固定文件名 `local-agent.log`。
  - Windows 平台：`log.SetOutput(file)` 仅写文件。
  - 非 Windows 平台：`io.MultiWriter(os.Stderr, file)` 同时写 stderr 和文件。
  - 同样使用 `log.LstdFlags | log.Lmicroseconds`。

### 3.2 日志级别策略

仓库中没有自定义日志级别枚举或分级函数。所有日志均通过 `log.Printf` 输出，**没有 Info/Warn/Error/Debug 级别的区分**。业务语义通过消息前缀来区分来源，例如：

- `[云端WS]` — WebSocket 连接与消息收发
- `[AI配置测试]` — AI 配置连通性检测
- `[内置AI]` — 内置 AI 钱包与流式响应
- `[migrate]` — 数据库迁移
- `本地程序进程启动` / `本地程序更新...` — 本地 Agent 生命周期

### 3.3 结构化字段约定

日志采用 `fmt.Printf` 风格的键值对拼接，而非 JSON 结构化日志。常见字段包括：

- `user=%s` / `email=%s` — 用户标识
- `model=%s` — AI 模型名
- `target=%s` / `host=%s` / `port=%s` — 目标地址
- `elapsed=%s` — 耗时（通常用 `time.Since(start).Round(time.Millisecond)`）
- `pid=%d` / `args=%v` — 进程信息
- `position=%s` / `message_id=%s` / `type=%s` — 业务上下文

敏感信息（如 URL）会经过 `safeAIURLForLog` / `normalizeAIChatCompletionsURL` 处理后再入日志。

### 3.4 多组件日志隔离

每个 Go 子项目独立管理自己的日志文件：

| 组件 | 默认日志路径 | 输出目标 |
|---|---|---|
| 云端后端 | `logs/backend.log` | stdout + 文件 |
| 本地 Agent | `<data_dir>/local-agent.log` | Windows: 文件; 其他: stderr + 文件 |

## 4. 约定与约束

- **唯一日志实现**：整个 Go 代码库只使用 `log` 包，未见任何第三方日志库 import。
- **无日志级别**：不区分 Info/Warn/Error/Debug，全部以 `log.Printf` 输出，业务来源靠字符串前缀区分。
- **非结构化格式**：日志是纯文本，键值对通过空格分隔拼接，不是 JSON 或 key=value 解析友好的格式。
- **微秒级时间戳**：`log.SetFlags(log.LstdFlags | log.Lmicroseconds)` 在所有入口生效。
- **日志文件可追加**：使用 `O_APPEND` 打开，避免覆盖历史日志。
- **敏感字段脱敏**：AI URL 在日志前经 `safeAIURLForLog` 过滤，体现对敏感信息的处理约定。
- **Docker 环境**：云端后端同时输出到 stdout，便于容器编排收集；本地 Agent 在非 Windows 下输出到 stderr，也符合容器最佳实践。

## 5. 局限

- 没有集中 logger 抽象层，各模块直接依赖全局 `log`，无法按模块切换输出或注入 mock。
- 没有日志轮转（rotation），长期运行可能产生大文件。
- 没有结构化字段查询能力，不适合接入 ELK/Loki 等结构化日志分析平台。
- 没有统一的 trace/correlation ID 机制，跨请求关联困难。