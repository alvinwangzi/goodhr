---
kind: logging_system
name: GoodHR 5 日志系统：Go/Node 双端结构化输出与本地任务日志持久化
category: logging_system
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/cmd/server/main.go
    - goodhr5/cloud/backend/internal/httpapi/agent_ws.go
    - goodhr5/cloud/backend/internal/httpapi/ai_config.go
    - goodhr5/cloud/backend/internal/httpapi/ai_wallet.go
    - goodhr5/cloud/backend/internal/httpapi/auth.go
    - goodhr5/local-agent-go-new/worker/src/logging/logger.ts
    - goodhr5/local-agent-go-new/worker/src/main.ts
    - goodhr5/local-agent-go-new/internal/flow/lifecycle/logger.go
    - goodhr5/local-agent-go-new/internal/storage/task_log.go
    - goodhr5/local-agent-go-new/internal/bootstrap/application.go
---

## 1. 使用的系统与框架

仓库包含两个独立进程的日志子系统，没有引入第三方日志库（如 logrus、zap、slog）。

- **云端后端（Go）**：使用 Go 标准库 `log`，通过 `setupLogger()` 将 `os.Stdout` 与一个可配置文件（默认 `logs/backend.log`）合并输出，并启用微秒级时间戳。所有业务代码直接使用 `log.Printf` 以带前缀的字符串形式记录，例如 `[云端WS]`、`[AI配置测试]`、`[内置AI]` 等。
- **本地 Agent（Go + Node Worker）**：采用“结构化 JSON 行 + 管道”模式。Node 侧 Browser Worker 通过 `WorkerLogger` 把日志写向 `process.stdout`；Go 主进程通过 `lifecycle.TaskLogger.WorkerLine` 解析这些 JSON 行，再写入 SQLite `task_logs` 表，供本地控制台按岗位查看。

## 2. 关键文件与位置

| 组件 | 路径 | 职责 |
|---|---|---|
| 云端后端启动与日志初始化 | `goodhr5/cloud/backend/cmd/server/main.go` | 创建 `logs/backend.log`，`log.SetOutput(io.MultiWriter(os.Stdout, file))` |
| 云端业务日志（示例） | `goodhr5/cloud/backend/internal/httpapi/agent_ws.go`、`ai_config.go`、`ai_wallet.go`、`auth.go` | 用 `log.Printf("[模块] ...")` 记录请求、连接、扣费等 |
| Worker 结构化日志器 | `goodhr5/local-agent-go-new/worker/src/logging/logger.ts` | `WorkerLogger` 输出统一 JSON 行，过滤敏感字段 |
| Worker 入口 | `goodhr5/local-agent-go-new/worker/src/main.ts` | 启动时输出一条 `action=worker.start` 的结构化日志 |
| Go 侧 Worker 日志消费 | `goodhr5/local-agent-go-new/internal/flow/lifecycle/logger.go` | `TaskLogger` 解析 JSON 行、去重、转中文用户文案、落库 |
| 任务日志存储 | `goodhr5/local-agent-go-new/internal/storage/task_log.go` | `task_logs` 表读写、按岗位裁剪至最近 1000 条 |
| 应用组装（注入 Logger） | `goodhr5/local-agent-go-new/internal/bootstrap/application.go` | 创建 `TaskLogger`，并通过 `workerProcess.SetLogSink(logger.WorkerLine)` 接入 |

## 3. 架构与约定

### 3.1 云端后端（Go）
- 启动阶段调用 `setupLogger()`，日志路径由环境变量 `GOODHR_CLOUD_LOG_FILE` 控制，默认 `logs/backend.log`；目录不存在时自动创建。
- 输出目标为 `stdout + 文件` 的双写，便于容器环境收集 stdout，也保留本地文件。
- 日志格式为 Go 标准库 `log.LstdFlags | log.Lmicroseconds`，即 `YYYY/MM/DD HH:MM:SS.mmmmmm` 前缀 + 自定义消息。
- 业务层未抽象统一 logger，直接 `import "log"` 后 `log.Printf`，但通过固定前缀（如 `[云端WS]`、`[AI配置测试]`、`[内置AI]`）区分来源模块。

### 3.2 本地 Agent（Go + Node）
#### Node 侧：`WorkerLogger`
- 每个浏览器动作类（click/find/input/scroll/read/screenshot/move/keyboard/session/download-manager 等）通过依赖注入持有同一个 `WorkerLogger` 实例。
- 输出结构体字段固定为：`timestamp`、`level`、`trace_id`、`action`、`step`、`status`、`duration_ms`，以及业务扩展字段（如 `page_url`、`target_description`、`error_code`、`error_message`、`poll_attempts`、`timeout_ms` 等）。
- 敏感字段清洗：键名匹配 `/cookie|authorization|token|password|proxy.*(user|pass)|secret/i` 的值会被替换为 `[已隐藏]`；单个字符串超过 1000 字符会被截断并追加 `…`。
- 失败去抖：`failure()` 方法基于 `trace_id|action|step|code|message` 组合键在 500ms 内去重，内存 Map 超过 200 项会清理 10 秒前的条目。
- 输出方式：`process.stdout.write(JSON.stringify(payload) + '\n')`，每行一条 JSON。

#### Go 侧：`TaskLogger`
- 作为 `shared.Logger` 实现，被注入到 preflight/greeting/auto-reply/lifecycle 各流程中，同时承担两件事：
  1. 把流程步骤（start/running/success/warning/failed/skipped）写入 SQLite `task_logs` 表，并更新当前任务悬浮窗显示的“当前步骤”。
  2. 通过 `SetLogSink` 接收 Node Worker 的 JSON 行，解析后同样写入 `task_logs`，并按 `keepWorkerLog` 规则只保留封装操作的首尾日志和全部失败，避免刷屏。
- 用户可见文案：内部 step/action 会通过 `userStepLabel` / `workerActionLabel` 映射成中文（如 `打开页面`、`查找页面元素`、`正在滚动到目标`），错误码还会附加建议提示（如 `VIEWPORT_TOO_SMALL` → “请把浏览器窗口放大后再试”）。
- 级别转换：`storage.logLevel` 把 `failed` 转为 `error`、`warning` 保持 `warning`，其余默认 `info`。

### 3.3 数据流
```
Browser Worker (Node)
  → process.stdout 输出 JSON 行
  → Go 主进程 workerProcess.SetLogSink(TaskLogger.WorkerLine)
  → TaskLogger.WorkerLine 解析 + 过滤 + 转中文
  → storage.SaveTaskLog 写入 task_logs
  → 本地控制台按 position_id 查询展示
```

## 4. 约定与约束

| 约定 | 说明 | 依据 |
|---|---|---|
| 云端后端日志必须经 `setupLogger` | 启动时创建 `logs/backend.log` 并双写 stdout | `cmd/server/main.go` 强制调用，失败则 `log.Fatalf` |
| 云端日志不入库 | 仅通过标准库 `log` 输出到文件和 stdout | 全仓未发现对数据库写日志的代码 |
| Node Worker 日志必须是单行 JSON | 字段包括 timestamp/level/trace_id/action/step/status/duration_ms | `logging/logger.ts` 的 `write` 方法 |
| 禁止在日志中泄露 cookie/token/password/proxy 密码 | 键名命中正则即替换为 `[已隐藏]` | `sanitizeFields` 中的 `sensitiveKeyPattern` |
| 字符串字段最大 1000 字符 | 超长会被截断并加 `…` | `sanitizeValue` |
| 同一失败 500ms 内去抖 | 基于 trace_id+action+step+code+message 组合键 | `WorkerLogger.failure` 的 `recentFailures` |
| Worker 日志只保留首尾和失败 | 非 start/结束/failed 的中间过程（如反复 find）被丢弃 | `keepWorkerLog` 白名单 |
| 任务日志按岗位最多保留 1000 条 | 超出部分删除最早的 | `maxPositionTaskLogs` 常量及 `trimPositionTaskLogs` |
| 日志级别由 status 推导 | `failed→error`、`warning→warning`、其他→`info` | `storage.logLevel` |
| 用户文案使用中文 | 步骤标签、错误提示均映射为中文 | `userStepLabel`、`workerActionLabel`、`workerLogSuggestion` |