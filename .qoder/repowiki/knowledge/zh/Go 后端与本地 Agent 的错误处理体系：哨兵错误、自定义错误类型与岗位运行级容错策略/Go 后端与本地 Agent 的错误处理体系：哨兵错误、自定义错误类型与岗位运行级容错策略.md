---
kind: error_handling
name: Go 后端与本地 Agent 的错误处理体系：哨兵错误、自定义错误类型与岗位运行级容错策略
category: error_handling
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/internal/httpapi/auth_store.go
    - goodhr5/cloud/backend/internal/httpapi/agent_store.go
    - goodhr5/cloud/backend/internal/httpapi/admin_user.go
    - goodhr5/cloud/backend/internal/httpapi/agent_ws.go
    - goodhr5/cloud/backend/internal/httpapi/ai_config.go
    - goodhr5/cloud/backend/internal/httpapi/activation_code.go
    - goodhr5/cloud/backend/internal/httpapi/agent_store_pg.go
    - goodhr5/cloud/backend/internal/httpapi/ai_config_store_pg.go
    - goodhr5/local-agent-go/internal/positionrunner/error_policy.go
    - goodhr5/local-agent-go/internal/positionrunner/error_policy_test.go
    - goodhr5/local-agent-go/internal/localai/reply.go
    - goodhr5/local-agent-go/internal/localdb/positions.go
    - goodhr5/local-agent-go/internal/positionrunner/candidate.go
    - goodhr5/local-agent-go/internal/positionrunner/pipeline.go
    - goodhr5/local-agent-go/internal/positionrunner/resumecheck.go
---

## 1. 总体方案

HRPlus 仓库包含两个 Go 子系统（`goodhr5/cloud/backend` HTTP API 服务、`goodhr5/local-agent-go` 本地 Agent），两者均使用 Go 标准库 `errors`/`fmt` 进行错误处理，没有引入第三方错误库。错误处理分为三层：

- **HTTP API 层**：通过包级哨兵错误（如 `ErrNotFound`）和少量自定义 error 结构体表达业务语义；调用方用 `errors.Is` / `errors.As` 判断。
- **本地 Agent 平台执行层**：在 `internal/positionrunner` 中定义候选人级错误包装器 `candidateOperationError` 与连续错误跟踪器 `consecutiveOperationErrorTracker`，实现“同一平台环节连续三次相同错误自动停止整个岗位运行”的策略。
- **AI 集成层**：`localai.ServiceError` 携带 `StatusCode`、`Body`、`Fatal` 字段，并通过 `IsPositionStoppingError` 暴露给上层判定是否立即终止岗位运行。

仓库中没有全局 panic/recover 中间件；panic 仅用于隔离不可恢复的底层崩溃（见第 4 节）。

## 2. 关键文件与包

| 路径 | 职责 |
|---|---|
| `goodhr5/cloud/backend/internal/httpapi/auth_store.go` | 定义包级哨兵错误 `ErrNotFound = errors.New("not found")`，被多个 store/handler 复用 |
| `goodhr5/cloud/backend/internal/httpapi/agent_store.go` | 定义 `AgentBindingConflictError` 结构体错误 |
| `goodhr5/cloud/backend/internal/httpapi/admin_user.go` | 定义 `adjustmentNoticeError` 结构体错误 |
| `goodhr5/cloud/backend/internal/httpapi/agent_ws.go` | WebSocket 相关运行时错误（消息类型缺失、连接断开、超时等） |
| `goodhr5/cloud/backend/internal/httpapi/ai_config.go` | AI 配置校验错误（Key/模型/公网地址/重定向次数等） |
| `goodhr5/local-agent-go/internal/positionrunner/error_policy.go` | 候选人操作错误包装、连续错误跟踪、OCR 致命错误分类 |
| `goodhr5/local-agent-go/internal/positionrunner/error_policy_test.go` | 验证“连续 3 次相同错误停止岗位运行”“AI 余额不足立即停止”“成功重置计数”“OCR 组件未安装为致命错误”等规则 |
| `goodhr5/local-agent-go/internal/localai/reply.go` | AI 回复决策校验错误（格式不完整、内容超长、冲突决策等） |
| `goodhr5/local-agent-go/internal/localdb/positions.go` | 数据库层 recover 保护 |
| `goodhr5/local-agent-go/internal/positionrunner/candidate.go` | 候选人处理 goroutine 内 recover |
| `goodhr5/local-agent-go/internal/positionrunner/pipeline.go` | 流水线入口 recover |
| `goodhr5/local-agent-go/internal/positionrunner/resumecheck.go` | 简历检查 goroutine 内 recover |

## 3. 架构与约定

### 3.1 哨兵错误（Sentinel Errors）

`auth_store.go` 中声明 `var ErrNotFound = errors.New("not found")`，随后被 `activation_code.go`、`agent.go`、`agent_store_pg.go`、`ai_config_store_pg.go` 等文件统一返回并配合 `errors.Is(err, ErrNotFound)` 判断。这是仓库中最统一的业务错误标记方式。

### 3.2 自定义错误结构体

仓库中显式定义的结构体错误只有三个：

- `AgentBindingConflictError`（`agent_store.go`）：绑定冲突场景。
- `adjustmentNoticeError`（`admin_user.go`）：管理员调整通知错误。
- `candidateOperationError`（`error_policy.go`）：候选人级平台操作错误，包含 `Operation` 与 `Err` 字段，实现 `Error()` 与 `Unwrap()`，以便 `errors.As` 识别。

其余错误以 `errors.New(...)` 或 `fmt.Errorf(...)` 直接返回，不封装类型。

### 3.3 候选人级错误与连续错误策略

`error_policy.go` 是本地 Agent 错误处理的核心：

- `candidateOperationError` 包装任何平台操作错误，使调用方可区分“可跳过当前候选人的错误”和“必须停止整个岗位运行的错误”。
- `consecutiveOperationErrorTracker` 按 `Operation` 维度记录最近一次错误的指纹（经 `normalizeOperationError` 归一化数字与空白后比较），当同一环节连续出现相同错误达到 3 次时，返回包裹原错误的终止信号：`"同一平台环节连续%d个候选人出现相同错误，岗位运行已自动停止"`。
- `stopAfterCandidateOperationError` 先检查 `shouldStopPositionImmediately`（即 `localai.IsPositionStoppingError` 或浏览器关闭类错误），命中则立即停止，无需累计。
- OCR 致命错误由 `isFatalOCRError` 通过中文关键字匹配判定：`ocr 组件未配置`、`ocr 组件未安装`、`ocr 模型文件不完整`、`启动 ocr 组件失败`、`ocr 组件已退出`、`ocr 组件没有返回结果并已关闭输出`。

该策略由 `error_policy_test.go` 明确断言：

- 同一错误连续 3 次触发停止；
- `localai.ServiceError{Fatal: true}`（如余额不足）立即停止；
- 一次成功调用会 `Reset` 对应操作的连续计数；
- OCR 组件未安装视为致命错误，单张图片未识别到文字只跳过当前候选人。

### 3.4 AI 错误分类

`localai` 模块通过 `ServiceError` 结构体承载远程 AI 调用的结构化错误（含 HTTP 状态码、响应体、`Fatal` 标志），并由 `IsPositionStoppingError` 向上暴露，供 positionrunner 决定是否立即终止岗位运行。`reply.go` 中对 AI 返回 JSON 的解析失败、文本为空或超过 1000 字、索要简历与状态冲突等情况，统一返回 `fmt.Errorf("...，已跳过发送")` 形式的错误，由上层决定跳过而非继续。

### 3.5 HTTP API 层错误模式

HTTP handler/store 层普遍采用“返回 `(value, error)` + `errors.Is(err, ErrNotFound)`”的模式，例如：

- `activation_code.go` 对 `sql.ErrNoRows` 转换为 `ErrNotFound`。
- `agent_store_pg.go`、`ai_config_store_pg.go` 同样将 `sql.ErrNoRows` 映射为 `ErrNotFound`。
- 参数校验错误直接使用 `errors.New("请填写 AI Key" / "AI 接口必须是有效的公网 HTTPS 地址" / ...)` 返回。

## 4. Panic/Recover 策略

仓库没有全局 panic 捕获中间件。panic 仅在以下位置被 recover，目的是防止单个 goroutine 或子流程崩溃影响主流程：

- `internal/localdb/positions.go:L321`：数据库查询 goroutine 内 `recover()`。
- `internal/positionrunner/candidate.go:L356`：候选人处理 goroutine 内 `defer func() { _ = recover() }()`。
- `internal/positionrunner/pipeline.go:L30`：流水线入口 `if recovered := recover(); recovered != nil`。
- `internal/positionrunner/resumecheck.go:L46`：简历检查 goroutine 内 `recover()`。

这些 recover 块均不打印堆栈也不向上传播 panic，属于“吞掉异常以保证自动化流程继续”的设计选择。

## 5. 约定与约束（基于代码观察）

- 业务“不存在”场景统一返回包级哨兵 `ErrNotFound`，调用方使用 `errors.Is(err, ErrNotFound)` 判断（来源：`auth_store.go` 定义及多处 `errors.Is` 用法）。
- 数据库 `sql.ErrNoRows` 在 store 层被转换为 `ErrNotFound`，避免调用方感知底层驱动细节（来源：`activation_code.go`、`agent_store_pg.go`、`ai_config_store_pg.go`）。
- 候选人级平台操作错误需包装为 `*candidateOperationError`，以便 `consecutiveOperationErrorTracker.Record` 通过 `errors.As` 识别（来源：`error_policy.go` 的 Record 逻辑）。
- 同一平台环节连续出现相同错误达 3 次时，岗位运行自动停止（来源：`error_policy.go` 中 `count >= 3` 分支，并由 `error_policy_test.go` 断言）。
- 标记为 `Fatal: true` 的 AI 错误（如余额不足）立即停止岗位运行，不受连续计数限制（来源：`error_policy_test.go` 中 `TestPositionStoppingAIErrorStopsImmediately`）。
- 一次成功的平台操作会 `Reset` 对应环节的连续错误计数，打断连续计数（来源：`error_policy.go` 的 Reset 方法及测试）。
- OCR 致命错误通过中文关键字匹配判定，非致命 OCR 错误（如未识别到文字）只跳过当前候选人（来源：`error_policy.go` 的 `isFatalOCRError` 及测试）。
- 所有 goroutine 级别的 UI/浏览器/AI/DB 子流程都使用 `recover()` 隔离崩溃，不向上传播（来源：`pipeline.go`、`candidate.go`、`resumecheck.go`、`positions.go`）。