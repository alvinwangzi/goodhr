---
kind: error_handling
name: Go/TS 多模块错误处理体系：哨兵错误、HTTP 响应封装与任务生命周期 panic 兜底
category: error_handling
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/internal/httpapi/auth_store.go
    - goodhr5/cloud/backend/internal/httpapi/server.go
    - goodhr5/cloud/backend/internal/httpapi/activation_code.go
    - goodhr5/local-agent-go/internal/positionrunner/error_policy.go
    - goodhr5/local-agent-go/internal/positionrunner/pipeline.go
    - goodhr5/local-agent-go/internal/localdb/positions.go
    - goodhr5/local-agent-go-new/internal/flow/lifecycle/runner.go
    - goodhr5/local-agent-go-new/internal/integration/cloud/types.go
---

## 1. 整体方案

GoodHR5 仓库包含三个主要 Go 子项目（云端后端 `cloud/backend`、本地 Agent v1 `local-agent-go`、本地 Agent v2 `local-agent-go-new`）以及一个 Next.js 前端，每个部分采用不同的错误处理策略，但共享“业务错误用哨兵值 + HTTP 层统一写响应”的约定。

- **云端后端**：使用包级哨兵错误 `ErrNotFound`，配合 `errors.Is` 在 handler/service/store 之间传递；所有 HTTP 响应通过 `writeError` / `writeJSON` 两个 helper 统一写出，避免分散的 `json.NewEncoder`。外部依赖错误（如 `sql.ErrNoRows`）被转换为业务哨兵错误再向上传播。
- **本地 Agent v1 (`positionrunner`)**：定义自定义错误类型 `candidateOperationError`（实现 `Error()` 和 `Unwrap()`），并通过 `consecutiveOperationErrorTracker` 统计同一平台环节连续出现的相同错误，达到阈值后自动停止岗位运行；同时集中判断哪些错误属于“必须立即停止整个岗位运行”的致命错误（AI 停止信号、浏览器关闭、OCR 组件损坏等）。
- **本地 Agent v2 (`lifecycle.Runner`)**：在主流程入口用 `defer recover()` 捕获未捕获 panic，记录 stack trace 并转为统一任务失败错误；最终状态由 `finish` 方法根据 `stopped` / `context.Canceled` / 云端认证过期等条件决定写入 `completed` / `stopped` / `failed` 三种状态，并将 `ErrorCode`、`ErrorMessage`、`Summary` 持久化到任务记录。
- **前端 (Next.js)**：未发现统一的错误类型或全局异常处理中间件，主要通过 TypeScript 运行时抛出错误并由调用方 catch；本仓库没有发现专门的 `errors/` 目录用于前端错误模型。

## 2. 关键文件与位置

| 模块 | 文件 | 作用 |
|---|---|---|
| 云端后端 | `goodhr5/cloud/backend/internal/httpapi/auth_store.go` | 定义包级哨兵错误 `var ErrNotFound = errors.New("not found")` |
| 云端后端 | `goodhr5/cloud/backend/internal/httpapi/server.go` | 定义 `writeJSON` / `writeError` 两个 HTTP 响应 helper |
| 云端后端 | `goodhr5/cloud/backend/internal/httpapi/activation_code.go` | 展示 store 返回 `ErrNotFound` → handler 用 `errors.Is` 映射为 404 的典型链路 |
| 本地 Agent v1 | `goodhr5/local-agent-go/internal/positionrunner/error_policy.go` | 定义 `candidateOperationError`、`consecutiveOperationErrorTracker`、`shouldStopPositionImmediately` |
| 本地 Agent v1 | `goodhr5/local-agent-go/internal/positionrunner/pipeline.go` | `withOperationTimeout` 中用 `recover()` 包裹单个候选人操作，超时/panic 都转为 error |
| 本地 Agent v1 | `goodhr5/local-agent-go/internal/localdb/positions.go` | 对底层 DB 操作加 `recover()` 防止单条记录崩溃影响整批 |
| 本地 Agent v2 | `goodhr5/local-agent-go-new/internal/flow/lifecycle/runner.go` | 主流程 `run` 中 `defer recover()` 兜住 panic，`finish` 统一落盘最终状态 |
| 本地 Agent v2 | `goodhr5/local-agent-go-new/internal/integration/cloud/types.go` | 定义 `APIError` 强类型表示云端 HTTP 错误，支持 `errors.As` 匹配 |

## 3. 架构与约定

### 3.1 云端后端：哨兵错误 + HTTP 层统一输出

- 业务层（store / service）只返回 `error`，不直接写 HTTP 响应。
- 当数据库查询无行时，store 将 `sql.ErrNoRows` 包装成包级哨兵 `ErrNotFound`；handler 层用 `errors.Is(err, ErrNotFound)` 判断并返回 404。
- 所有 handler 通过 `writeError(w, status, message)` 写出 JSON `{"error": "..."}`，通过 `writeJSON(w, status, payload)` 写出成功响应；禁止在 handler 内直接使用 `w.Write` 或 `json.Encode`。
- 参数校验失败统一返回 `http.StatusBadRequest`，鉴权失败返回 `http.StatusUnauthorized` / `Forbidden`，业务资源不存在返回 `http.StatusNotFound`，内部错误返回 `http.StatusInternalServerError`。

### 3.2 本地 Agent v1：候选人类错误 + 连续错误熔断

- 平台操作错误统一包装为 `*candidateOperationError`，携带 `Operation` 字段标识当前步骤（如 “打开详情页”、“点击打招呼”）。
- `consecutiveOperationErrorTracker.Record` 对同一操作的错误进行指纹归一化（去掉数字、空白），连续出现相同错误达 3 次时返回“岗位运行已自动停止”的错误，上层据此终止该岗位运行。
- `shouldStopPositionImmediately` 识别两类致命错误：AI 服务返回的“停止岗位”信号、浏览器被关闭；这类错误不会进入连续计数，而是立即让上层停止运行。
- OCR 相关错误通过字符串关键字匹配（“ocr 组件未配置/未安装/模型文件不完整/启动失败/已退出/没有返回结果并已关闭输出”）判定为致命错误。

### 3.3 本地 Agent v2：panic 兜底 + 任务生命周期状态机

- `Runner.run` 在分发具体流程前注册 `defer func() { if recovered := recover(); recovered != nil { ... } r.finish(active, stats, err) }()`，把 panic 转为 `panicTaskError` 错误，再由 `finish` 统一保存任务状态。
- `finish` 根据以下规则决定最终状态：
  - 用户主动停止（`active.stopped == true`）或 `context.Canceled` 且非中断场景 → `status=stopped`，`summary="任务已按你的要求停下来了"`。
  - 云端认证过期（`cloud.IsAuthExpired`）→ 同样视为 stopped。
  - 其他 error → `status=failed`，`errorCode="TASK_FLOW_FAILED"`，`errorMessage` 为错误原文。
  - 无 error → `status=completed`，`summary="任务已经处理完成"`。
- 任务失败时会触发失败提示音播放和云端失败通知（`SendFailNotice`），通知失败本身不影响任务最终状态。

### 3.4 云端 API 错误模型

- `cloud.APIError` 是强类型结构体，包含 `StatusCode`、`Code`、`Message`，实现 `Error()` 接口。
- 调用方使用 `errors.As(err, &apiErr)` 提取结构化错误码，例如在 `StartTask` 中将云端返回的 `apiErr.Code` 写入任务记录的 `ErrorCode` 字段。

## 4. 约定与约束

- **云端后端**：
  - 所有 store 在找不到记录时必须返回 `ErrNotFound`，不得返回空值 + nil；调用方必须用 `errors.Is` 判断。
  - 所有 HTTP handler 必须通过 `writeError` / `writeJSON` 写出响应，禁止直接 `w.Write`。
  - 数据库层 `sql.ErrNoRows` 必须在 store 边界转换为业务错误（通常是 `ErrNotFound`），不得泄漏到 handler。
  - 参数校验失败一律返回 400，鉴权失败返回 401/403，业务不存在返回 404，内部错误返回 500。
- **本地 Agent v1**：
  - 平台操作错误必须包装为 `*candidateOperationError`，以便连续错误追踪器识别。
  - 对可能崩溃的浏览器/OCR/AI 调用必须用 `withOperationTimeout` 包裹，确保 panic 和超时都能转为 error 并记录日志。
  - 连续 3 次相同平台环节错误即视为岗位级故障，应停止运行。
- **本地 Agent v2**：
  - 主流程入口必须保留 `defer recover()`，不允许 panic 逃逸出 Runner。
  - 任务最终状态必须由 `finish` 唯一写入，禁止在流程各处直接修改任务状态。
  - 任务失败必须同步云端（`SyncSummary` / `SendFailNotice`），即使通知失败也不影响任务落盘。
- **通用**：
  - 仓库未使用 `panic` 作为正常控制流（仅用于极端不可恢复场景），也未使用 `recover` 作为常规错误恢复手段。
  - 未发现全局中间件式错误处理器（如 gin middleware），错误处理以函数级 `defer` 和显式 `if err != nil` 分支为主。
  - 前端 Next.js 部分未发现统一的错误类型或全局异常捕获逻辑，错误处理主要由各页面/组件自行处理。

## 5. 适用性说明

本仓库存在明确的错误处理体系：云端后端使用哨兵错误 + HTTP 响应封装，本地 Agent v1 使用自定义错误类型 + 连续错误熔断，本地 Agent v2 使用 panic 兜底 + 任务生命周期状态机。因此该类别适用于此仓库。
