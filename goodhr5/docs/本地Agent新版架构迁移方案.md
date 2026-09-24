# 本地 Agent 新版架构迁移方案

<!-- 文件作用：记录双目录保留前提下，从 dev 功能基线迁入 main 新版架构的实施顺序、功能清单、接口约定和验收门槛。 -->

> 执行说明：本文是迁移方案，不代表迁移已执行。后续获得实施确认后，使用 executing-plans 或 subagent-driven-development 按任务推进，逐项记录验证证据。创建分支、提交、合并、推送和真实招聘消息发送均不能由本文自动授权。

**Goal（目标）：** 保留两套源码目录，以 `dev` 的现有功能为基线，以 `main` 的 `local-agent-go-new` 为底座，最终由新版承担本地任务执行。

**Architecture（架构）：** 从确认后的 `dev` 派生改造分支，只引入 `main` 的新版目录。保留新版 Go 流程、Go 平台适配、强类型浏览器协议和 TypeScript Worker 分层，按业务能力补齐开发线的新增功能，不整体搬运老版实现。

**Tech Stack（技术）：** Go 1.25+、SQLite、Node.js 22+、TypeScript、CloakBrowser、Playwright、Next.js、Windows PowerShell、Inno Setup。

**Spec（设计依据）：** 本文第 1、3、4 节承接本轮已确认的迁移方向；具体实施细节作为待实施方案。新版目录内历史迁移文档仅供查漏，不覆盖本次用户决定。

**记录日期：** 2026-09-23。

## 1. 已确认的方向与不做的事情

### 1.1 目录和分支

1. 保留 `goodhr5/local-agent-go` 和 `goodhr5/local-agent-go-new`，不删除、不互相覆盖、不重命名合并。
2. 最终运行目标是 `local-agent-go-new`。老版是功能对照和实现参考，不再把“继续重构老目录”作为本次交付方向。
3. `dev` 开发线提供功能基线；`main` 中的新版目录提供架构与已有新版能力。
4. 先从确认后的 `dev` 创建改造分支，再引入新版目录。建议分支名：`refactor/local-agent-new`。
5. 只引入新版目录，不整体合并 `main`，不从 `main` 覆盖开发线的老版、云端或前端。
6. 实施验收通过后，经确认合回本地 `dev`；需要推送时只推同名 `dev`，不向远程 `main` 推送。

### 1.2 兼容、数据与范围

- 尚未在线上实施，不设计旧版接口兼容层、双版本平滑升级或旧 SQLite 格式兼容。
- “不兼容旧版”不等于“丢掉老版功能”，也不等于“允许删除开发数据”。
- 不从零重写整个 Agent；新版已经存在的流程、协议、平台实现、AI 和系统能力先验证再复用。
- 不同时让两套 Agent 操作同一个招聘账号或 Profile，不进行双执行发送对比。
- 不顺手修改招聘规则、会员规则、筛选阈值或升级无关依赖。
- 不把旧实现里的脚本注入、平台专用 Worker 路由、弱类型传参照搬到新版。
- Windows 是本次主要运行和验收环境；保留已有 macOS 实现，但不能用 Windows 结果宣称 macOS 已验证。

### 1.3 建议的两个交付批次

以下批次划分属于本方案建议，实施前确认，不是已获授权的开发任务。

- **A 批：架构迁移。** 开发线现有业务完整迁入新版，前后端改用新版，Windows 可安装运行；新版已有 AI 自动回复代码必须保留，但未通过页面适配验收时仍禁止启用。
- **B 批：AI 自动回复补齐。** 在既有实现上补齐平台消息配置、发送保护和端到端验证，逐平台启用。
- A 批可以独立验收，但结论必须写“架构迁移完成，自动回复尚未启用”，不能写成“所有功能全部完成”。B 批属于明确列出的后续实施范围，不因本次问答自动开始。

## 2. 已核实的代码基线

### 2.1 本地 Git 快照

编写期间存在其他开发操作，分支曾从功能分支变为 `dev`。以下是本轮后续只读核对时的状态，开工时必须重新检查：

| 用途 | 本地引用 | 提交 |
|---|---|---|
| 开发功能基线 | `dev` | `cada002a32b3e04b54f6396764d941045f6dad59` |
| 新版目录来源 | `main` | `85306063b0ec0920040186cd4b4e151c77952119` |
| main 阅读工作树 | `.worktrees/main-inspect` | 检出上述 `main` |

- 最近一次检查中 `dev..HEAD` 无提交差异；之前关于“三个功能提交尚未进入 dev”的描述已不是当前状态。
- 工作区观察到未跟踪的 `.probe/`；它不属于本次文档交付，不能加入迁移提交或自行清理。
- 这里只验证本地 Git 引用，没有执行 fetch，不能据此认定远端服务器没有后续更新。
- 本轮没有运行 Go/Worker 测试、构建安装器或启动招聘任务。历史文档的“测试通过”不能作为本次验收证据。

### 2.2 必须区分的三种 AI/回复能力

| 能力 | main 老版 | 最新开发线老版 | main 新版 |
|---|---|---|---|
| AI 评分、简历分析、通用模型请求 | 有实现 | 有实现 | 有实现 |
| 检查候选人是否回复，再索要简历 | 本轮不据 main 旧代码认定完整具备 | 已有 Boss 实现、待索要队列和休息窗口检查 | 需要按开发线行为补齐并验证 |
| 读取会话 → AI 生成文字 → 发送回复 | 未找到完整执行流程 | 未找到完整 AI 生成并发送流程 | 已有独立流程，但页面配置未完成 |

直接代码依据：

- 老版 `internal/localai/client.go` 的 `Client.Chat` 是通用模型调用；`positionrunner/decision.go` 的“AI 回复”文案主要展示评分判断，不等于自动回复候选人。
- 开发线 `internal/platforms/boss/reply.go` 的 `CheckResumeRequests` 判断页面回复状态并点击求简历，没有调用 AI 生成回答。
- 开发线 `internal/positionrunner/resumecheck.go` 维护待索要名单及结果补报；`candidate.go` 在模拟休息窗口中复用回复检查。
- 新版 `internal/flow/auto_reply/flow.go` 已串联扫描未读、读取会话、`AI.GenerateReply`、内容检查、去重、`ReplyConversation` 和结果保存。
- 新版 `internal/integration/ai/client.go` 的 `GenerateReply` 使用岗位、候选人和会话上下文生成回复。
- 新版 `internal/platform/common/reply.go` 已有会话读取、身份检查和发送动作。
- 新版四份平台配置中的 `message.unread_item`、`message.context`、`message.input`、`message.send` 仍在 `pending_selectors`。只有 Boss 配置了消息页地址，其他三个平台的 `messages_url` 为空。
- 新版 `platform.ValidateTaskConfig` 会拦截上述自动回复配置缺口。因此“已有代码”和“可直接使用”必须分开记录。

### 2.3 历史资料使用限制

新版的 `docs/migration-plan.md`、`docs/legacy-capability-matrix.md` 可以辅助盘点，但不能原样作为本次执行清单：

- “建立骨架”“新建自动回复”已经有对应实现，不从头重做。
- “稳定后删除旧实现”与本次双目录保留要求冲突，不执行。
- “Boss、猎聘企业端索要为空实现”未反映开发线后续工作。
- 多处“仅 macOS”“Windows 待实现”与已有 Windows 文件不一致，必须逐项看当前代码。
- 新版 `AGENTS.md` 中“默认分支直接开发”“自动提交推送”及旧文案风格，与当前用户要求冲突；导入后首先统一这些说明，不据此操作 `main`。

## 3. 目标结构与职责

```text
Next.js 控制台
  → 本地 Go HTTP 入口
  → Go 启动检查与任务生命周期
  → Go greeting / auto_reply 独立流程
  → Go 平台适配
  → Go Browser Client + 强类型协议
  → TypeScript Worker 封装动作
  → TypeScript 原子动作
  → CloakBrowser
```

云端提供账号、会员、岗位、运行设置、任务记录和允许同步的数据，不进入浏览器执行链路。

| 目录（相对新版根目录） | Module 职责 | 不放入的内容 |
|---|---|---|
| `internal/bootstrap` | 依赖组装、启动和统一退出 | 平台页面规则 |
| `internal/api` | 请求解析、校验、响应 | 候选人业务编排 |
| `internal/flow/preflight` | 配置、登录、权限、运行组件和锁检查 | CSS 和页面动作细节 |
| `internal/flow/lifecycle` | 任务标识、运行锁、停止、终态、资源释放 | 平台名称分支 |
| `internal/flow/greeting` | 扫描、筛选、评分、打招呼、后续动作的业务顺序 | 浏览器原子操作 |
| `internal/flow/auto_reply` | 读取会话、AI 生成、发送保护和回复结果 | 平台选择器 |
| `internal/platform/{platform}` | 平台页面行为、候选人和会话身份确认 | AI、数据库、云端业务调用 |
| `internal/browser` | Go/Worker 协议、调用和进程管理 | Go 直连 CDP |
| `internal/integration` | 云端、AI、OCR 的调用与错误处理 | 平台选择器 |
| `internal/storage` | 本地任务、动作、回复、下载摘要与队列 | 浏览器操作 |
| `internal/profile`、`internal/runtime`、`internal/system`、`internal/updater` | 账号目录、运行组件、系统能力和更新 | 候选人筛选规则 |
| `worker/src` | 通用查找、移动、点击、输入、滚轮、读取、截图和下载 | 平台名、AI、数据库、云端请求 |

设计原则：复用既有 Module 的 Interface，把重复规则集中到实现内部；不为每次转发增加一层只有一个实现的空壳。测试优先通过调用者使用的 Interface 验证行为。

### 全局约束

- 招聘页面禁止 `evaluate`、`evaluateHandle`、`$eval`、`$$eval`、`addScriptTag`、`addInitScript`、`dispatchEvent` 等脚本注入。
- 点击顺序：查找 → 移动 → 原子点击；输入顺序：查找 → 移动 → 聚焦 → 原子输入。
- 页面动作同步等待结果；只有受生命周期管理的任务运行、AI 完整响应处理和数据同步可以后台进行，不遗留脱离锁的浏览器操作。
- 平台 URL、选择器和页面规则仅存于各平台 `config.json`，统一使用 `SelectorSpec` 并经 `go:embed` 发布；修改后重新构建发布 Agent。
- 不从云端拉取、合并或覆盖平台配置；云端用户运行设置与平台选择器不是同一种配置。
- 新文件和新增方法有中文说明；数据库字段附中文备注；日志不含凭证、Cookie、代理密码和完整个人资料。
- Go 跨 Module 数据使用明确结构体；TypeScript 保持 `strict`，外部 JSON 从 `unknown` 校验后使用。
- 保留导入版本的依赖锁：Worker 的 CloakBrowser `0.5.2`、Playwright Core `1.61.1`、TypeScript `5.9.3`；本次不附带升级。

## 4. 不可丢失的功能清单

下表是迁移核对清单，不是完成声明。实施时在新版既有 `docs/legacy-capability-matrix.md` 为每项补充：旧版来源、新版落点、差异处理、自动测试、真实页面结果、提交号。

状态分别记录“有代码 / 配置完整 / 自动测试通过 / 真机通过”，不能只写一个“已迁移”。

| 编号 | 能力 | 重点来源（老版相对路径） | 新版落点与验收重点 |
|---|---|---|---|
| C01 | 启动参数、环境注入、数据与安装目录分离 | `internal/config`、`cmd`、构建脚本 | `config/bootstrap/scripts`；dev 不误连正式云端 |
| C02 | 账号绑定、会员、岗位与用户设置快照 | `app`、`cloudapi`、`positionrunner` | `preflight/integration/cloud`；按能力权限检查 |
| C03 | 任务启动、停止、异常退出与浏览器独占 | `positionrunner/lifecycle.go` | `flow/lifecycle`；一次终态、锁释放 |
| C04 | 关键词 AND/OR、排除词、详情阈值与打招呼阈值 | `positionrunner/decision.go`、`localai` | `greeting/integration/ai`；同一输入决定一致 |
| C05 | DOM、OCR、图片 AI 三种详情模式 | `positionrunner/detail.go`、`ocr` | `greeting/integration/ocr/ai`；不偷偷切换模式 |
| C06 | SSE、重试、超时、提前评分、结构化简历 | `localai/client.go` | `integration/ai`；评分先行，完整数据后台处理 |
| C07 | 列表批次、候选人去重、累计统计、打招呼上限 | `scan.go/pipeline.go/persistence.go` | `greeting/storage`；不漏算、不重复计数 |
| C08 | 随机等待、详情浏览、模拟休息、人工筛选保留 | `candidate.go`、平台实现 | `greeting/pacing.go` 与平台层；节奏和上下文不漂移 |
| C09 | 四个平台登录、选岗、筛选、详情、翻页、打招呼 | `internal/platforms` | 四份平台实现与本地配置，分别验收 |
| C10 | 聊天框复用、姓名核对、首条问候、电话/微信/简历索要 | `platforms/chatflow`、各平台 `followup.go` | `platform/common` 与平台实现；只记录真实动作结果 |
| C11 | 回复后索要简历、待索要队列、结果补报 | `resumecheck.go`、`platforms/boss/reply.go`、`localdb` | 新版受控队列及平台能力；不是 AI 自动回复 |
| C12 | 模拟休息中检查回复，检查后返回列表 | `candidate.go` | `greeting`；检查耗时计入休息，不额外延长 |
| C13 | 执行任务 `run_id`、候选人事件和索要结果归组 | `cloudapi`、`persistence.go`、`resumecheck.go` | `integration/cloud/storage`；跨任务不串记录 |
| C14 | 任务日志、运行状态小窗、评分原因、失败与完成通知 | `app/positionrunner`、前端岗位页 | `api/lifecycle/system` 与前端；不向招聘页插浮层 |
| C15 | 下载监听、目录设置、打开文件与安全校验 | `browser/app/runtime` | `flow/download/storage/system/files` |
| C16 | Profile、扩展、书签、Node/浏览器/OCR 安装更新 | `browserprofile/runtime/app` | `profile/runtime/updater`；只使用本地敏感数据 |
| C17 | Windows 环境化构建、HR+ 品牌、版本守卫、安装器 | `cmd/build-local-agent`、`scripts/packaging` | 新版构建入口；不沿用来源版默认版本号 `6` |
| C18 | AI 自动回复既有实现 | main 新版 `flow/auto_reply`、`integration/ai` | 保留并补齐，单列 B 批验收 |

特别规则：业务行为保留，违规实现不保留。例如老版 Worker 的 Boss 专用聊天路由要拆回 Go 平台层；不能因源码“已有可用逻辑”而带入第二条链路。

## 5. 实施顺序与任务依赖

```text
M0 固定基线 → M1 引入源码并验证原始状态
  → M2 配置与系统基础 → M3 云端运行标识与本地记录
  → M4 AI/OCR 与公共打招呼流程
  → M5 Boss 与回复后索要闭环
  → M6 其余三平台
  → M7 前后端统一对接 → M8 Windows 交付（A 批验收）
  → M9 AI 自动回复补齐（B 批，逐平台验收）
```

M7 的协议约定在 M3 就固定，页面接入可以提前联调；不得等安装器完成才发现字段不一致。每个 M 任务完成后更新能力矩阵。提交按主题拆分，但只有得到提交授权才执行 Git 写操作。

### M0：固定开发基线和来源

**文件：** 本方案、现有工作区状态；此步不改业务代码。

- [ ] 重新执行 `git status --short --branch`、`git branch -vv`、`git worktree list`，确认没有其他会话正在切换目标工作树。
- [ ] 列出要纳入的功能分支和未提交内容；必须确认内容已进入选定 `dev`，不按旧聊天记录自动合并。
- [ ] 记录完整 `dev` 和 `main` SHA，列出两基线之后的新增功能；后续发生新提交时以增量清单处理，不暗中更换基线。
- [ ] 确认文档本身如何进入改造分支，不让未提交文档被分支操作遗漏。
- [ ] 确认改造分支名和隔离工作树位置；优先使用独立工作树，不打断正在使用的开发目录。

**验收：** 能明确回答“取哪个 dev、取哪个 main、新增功能是否齐全、哪些文件不属于迁移”。有未归属改动时停在本步，不自动 stash、提交或覆盖。

只读核对命令（在仓库根运行）：

```powershell
git status --short --branch
git branch -vv
git worktree list
git rev-parse dev main
git log --oneline main..dev -- goodhr5/local-agent-go
git diff --name-status main dev -- goodhr5/cloud
```

### M1：引入 main 新版，建立可重复基线

**文件：** 引入 `goodhr5/local-agent-go-new/`；调整导入后的 `AGENTS.md`、`docs/migration-plan.md`、`docs/legacy-capability-matrix.md` 中与本轮要求冲突的说明。

- [ ] 经确认，从锁定的 `dev` 创建 `refactor/local-agent-new`。
- [ ] 先确认目标新版目录不存在；如存在，检查内容并停止导入，不能强行覆盖。
- [ ] 仅从锁定 main 提交引入该目录的受 Git 管理内容，不复制 `.worktrees/main-inspect` 的未提交文件、安装依赖或运行数据。
- [ ] 保留 Go module 名 `goodhr5/local-agent-go-new`，不通过全仓库改名制造旧目录替换。
- [ ] 检查内部 import、嵌入资源和构建脚本是否引用目录外缺失文件；缺失项单独登记并补最小必需资源，不整体合并 main。
- [ ] 确认镜像后运行第 8 节基线命令，记录每个失败、环境条件和缺失依赖。
- [ ] 统一历史文档：双目录保留、专门分支、Windows 验收、提交须授权；不宣称历史测试在本次重新通过。

**验收：** 导入时老目录和云端无改动；新版原始状态可复现，测试失败已分类；只有“可构建且基础测试可解释”才进入功能迁移。

导入命令示例仅供实施时使用，必须在已创建的改造分支及确认空目标后执行：

```powershell
$NewSource = '85306063b0ec0920040186cd4b4e151c77952119'
if ((git branch --show-current) -ne 'refactor/local-agent-new') { throw '当前不是改造分支' }
if (Test-Path 'goodhr5/local-agent-go-new') { throw '目标目录已存在，先核对，禁止覆盖' }
git restore --source=$NewSource --worktree -- goodhr5/local-agent-go-new
git status --short
```

### M2：配置、系统与浏览器基础可独立运行

**修改落点：** 新版 `internal/config/config.go`、`internal/bootstrap/application.go`、`internal/profile`、`internal/runtime`、`internal/system`、`internal/browser`、`scripts`。

**Interface：** 复用 `config.Load`、`bootstrap.New` 和现有 Browser Client；对外仍只有 `/api/v1/page/open` 负责打开浏览器页面。

- [ ] 先补配置回归：环境未指定、dev/prod 地址混用、数据目录覆盖顺序、端口占用、中文及空格路径。
- [ ] 迁入开发线的环境化构建校验，区分云端地址和控制台地址；不能把缺配置悄悄解释为正式环境。
- [ ] 新版默认监听 `127.0.0.1:43129`，Worker 使用 `39881`；自定义本地端口通过实际 `local_port` 传给前端。
- [ ] 本方案采用独立新版数据目录 `%APPDATA%\HRPlus\local-agent-new`，保留稳定 HRPlus 根目录，避免接触旧 SQLite 和 Profile；`--data-dir` 和环境变量仍可覆盖。
- [ ] 不自动导入旧数据库、Cookie 或 Profile；需要保留登录态时另行确认本地副本操作，否则在新版重新登录。
- [ ] 保留单实例、Profile 锁、Windows 隐藏子进程窗口、防睡眠、通知、扩展、书签、下载和安全更新能力。
- [ ] 在自建测试页面验证查找、点击、输入、真实滚轮、截图和下载。测试不能依赖招聘账号。

**验收：** 可启动、健康检查正常、Worker 可停止；浏览器不误复用旧版实例；目录和环境隔离有效；无 Go CDP、无平台业务进入 Worker。

### M3：运行标识、存储与云端事件完整贯通

**修改落点：** 新版 `integration/cloud/client.go`、`integration/cloud/types.go`、`flow/shared/types.go`、`flow/lifecycle/runner.go`、`storage`、`migrations`；云端 `position_execution.go`、`local_candidate_ingest.go`、`resume_request_ingest.go`、任务记录相关文件。

**已核实差异：** 开发线云端启动响应返回 `run_id`；main 新版 `RequestPositionStart` 当前只返回 `error`，把启动响应丢弃。该差异必须先解决。

**目标 Interface：** 修改既有方法而不是再造启动入口：

```go
// PositionStartResult 保存云端确认的本次执行任务编号。
type PositionStartResult struct {
    RunID string `json:"run_id"`
}

// RequestPositionStart 请求云端启动，并返回本次执行任务编号。
func (c *Client) RequestPositionStart(
    ctx context.Context, token, positionID, taskType, machineID, taskID string,
) (PositionStartResult, error)
```

- [ ] 先写云端假服务器测试：返回 `{"ok":true,"status":"running","run_id":"run-a"}`，新版必须读到 `run-a`；缺失或空编号应报错。
- [ ] 保留三个不同含义：`position_id` 是岗位，`task_id` 是本地一次运行，`run_id` 是云端一次执行记录，不能混用。
- [ ] 为准备快照、本地任务记录、候选人结果、索要补报和终态同步增加明确的云端运行编号字段；强类型上传字段保持 `run_id`。
- [ ] 云端启动成功、本地落盘失败时不得开始浏览器动作；补偿结束已占用的云端运行记录，补偿失败记录可恢复状态，不创建第二次运行掩盖失败。
- [ ] 同一启动请求重试不得重复创建云端运行；云端接收本地 `task_id` 作为幂等依据并绑定账号、岗位，返回同一 `run_id`。
- [ ] 候选人事件显式归属本次 `run_id`，停止后的索要补报仍使用原编号，取消“猜当前运行”作为新版的默认归组方式。
- [ ] 同步超时重试不重复统计或新增事件；晚到的上次运行终态不能覆盖新任务状态。
- [ ] SQLite 采用受控写入和事务；任务终态、本地摘要、待索要队列分别落盘，字段写中文说明。

**验收：** 同一岗位连续运行两次，分别生成 A/B 记录；A 的延迟结果只进入 A；重复补报不虚增次数；应用崩溃后遗留 running 被明确收尾。

### M4：公共打招呼流程、AI/OCR 与统计

**修改落点：** 新版 `flow/greeting/flow.go`、`flow/greeting/pacing.go`、`flow/shared`、`integration/ai/client.go`、`integration/ocr`、`flow/lifecycle`。

**Interface：** 继续使用 `Flow.Run(ctx, prepared, runtime)` 和新版强类型平台返回值；不从老版直接搬入 `map[string]any` 流程。

- [ ] 从老版测试提取固定输入：关键词 AND/OR、排除词、分数等于阈值、详情概率 0/100、重复候选人、到达打招呼上限。
- [ ] 将这些输入改写为新版测试，先跑出行为差异，再逐项修复；对于已有正确实现只补测试，不重写。
- [ ] 对同一业务使用旧版实际的比较符与阈值，不统一改成另一个 `>` 或 `>=`；主流程无法解释的差异先记录再确认。
- [ ] DOM/OCR/图片 AI 按选择的模式执行；OCR 空文本、进程退出、AI 超时、云端认证失效区分处理。
- [ ] AI 固定规则放稳定 `system` 消息，岗位、候选人、截图和会话放 `user`，保留思考开关及既有提示词配置。
- [ ] 测试 SSE：评分字段先完整、结构化简历最后才结束；主流程应先继续，完整响应再按开关异步同步，关闭开关时不得上传。
- [ ] 后台上传沿用启动时冻结的任务、岗位、`run_id` 和输出开关，不能读取之后启动的新任务上下文。
- [ ] 统计区分已发现、已处理、跳过、失败、真实打招呼成功；先保存动作结果再补报，不因补报失败重做页面动作。
- [ ] 保留安全点停止、连续同类错误停止、浏览器关闭立即中止和统一终态；运行中不偷偷启动新任务。

**验收：** 相同固定输入产生相同业务决定；评分不等待结构化响应；网络失败不引起二次打招呼；未限定批数时不因来源版默认批数过早结束。

### M5：Boss 完整闭环与回复后索要

**修改落点：** 新版 `platform/boss`、`platform/common`、`platform/model/runtime.go`、`flow/greeting`、`storage`、`integration/cloud`。

**新增文件仅在确认无同职能文件后：** `flow/greeting/resumecheck.go`、`storage/resume_request.go` 及对应测试。队列属于业务与存储，不放 Worker。

- [ ] 验证推荐页、选岗、候选人定位、详情读取、真实滚轮、打招呼、聊天框核对和收尾关闭。
- [ ] 纳入开发线聊天框等待、继续沟通重开、同名/脱敏姓名核对等后续修复，而不是仅对照 main 老版。
- [ ] 按开发线行为，打招呼成功且要求索要资料时先入待索要队列；入队不等于已索要，不能提前设置成功字段。
- [ ] 在模拟休息窗口和允许的任务收尾阶段检查回复。读取会话、判定回复和按钮操作放 Boss 平台层；何时检查、队列状态和云端补报放 Go 流程。
- [ ] 迁移消息页相关选择器到 Boss `config.json`；不新增 `/boss/chat/*` Worker 路由，复用查找、读取、滚轮、点击封装动作。
- [ ] “仅追加问候语”“索要电话”“索要微信”“索要简历”分别核对；当前只完成求简历的路径不能宣称电话和微信也已成功。
- [ ] 未回复保留待检查；会话未找到、身份不符、按钮禁用分别给出结果；只有真实完成对应动作才写成功和事件。
- [ ] 休息开始记录截止时间，回复检查使用剩余预算，检查结束回到列表后只等待剩余时长，不能额外再休息完整一轮。
- [ ] 回复检查复用同一浏览器独占锁。自然完成前的检查由当前任务受控执行；用户停止后不新发起索要，保留队列到下次运行，已开始动作先确认结果后收尾。
- [ ] 上一条是为安全停止和唯一执行链路做出的明确调整：不照搬旧版“先返回终态，再由无跟踪协程操作浏览器”的形式；实施确认时一并确认该停止语义。

**验收：** 未回复不索要；身份不符不发送；请求成功才入账；休息后列表上下文保留；停止后无遗留后台点击；云端结果属于原 `run_id`。

### M6：其他三平台逐个迁移

**修改落点：** 新版 `platform/zhaopin`、`platform/liepin`、`platform/hliepin` 和各自 `config.json`；公共差异收敛到已有 `platform/common`。

按下列次序逐个平台完成独立测试与页面验收，不一次修改四个平台后统一猜测问题来源：

| 顺序 | 平台 | 必测行为 |
|---|---|---|
| 1 | 智联招聘 `zhaopin` | 搜索并选择岗位；大/小屏打招呼按钮与详情入口分开；聊天框、索要二次确认、详情关闭 |
| 2 | 猎聘企业端 `liepin` | 选岗、详情、继续沟通、聊天框身份、电话/微信/简历动作和关闭 |
| 3 | 猎聘猎头端 `hliepin` | 保留人工筛选、不强制切岗；候选人行稳定身份；新标签详情；开聊职位选择；推广弹框与意外详情清理；翻页 |

- [ ] 每个平台先运行对应老版测试，再把业务断言改写到新版测试，不复制对旧 Worker 路径的精确断言。
- [ ] 缺选择器保持明确未配置；不能用空实现返回成功来通过验收。
- [ ] 不支持回复后索要检查的平台明确标记不支持，不冒充 Boss 的已实现范围。
- [ ] 收藏、不合适等未验证的来源版能力保持未启用；新增支持另行确认，不作为“已经迁移”的数量充数。
- [ ] 每平台记录账号类型、页面版本、测试日期、结果和本地证据位置；不把候选人截图上传项目仓库。

**验收：** 开发基线已有能力逐项通过；平台差异仍只存在对应平台目录；新增公共能力能由通用测试验证。

### M7：前端与云端统一使用新版

**修改落点：** `cloud/frontend-next/lib/admin-api.ts`、`app/admin/positions/page.tsx`、执行任务列表/详情与简历库页面、启动守卫及本地运行入口；新版 `internal/api`、`flow/lifecycle`；云端当前任务记录实现。

**接口约定：** 下表中“新增”是本方案拟定的路径，实施前先确认导入目录没有同义接口，避免重复入口。

| 用途 | 目标接口 | 处理方式 |
|---|---|---|
| 健康检查与本地发现 | `GET /health` | 复用；补充可识别新版协议的 `agent_protocol: "task-v1"` |
| 设备绑定 | `POST /api/v1/session/bind` | 复用；本地读取设备身份后请求云端 |
| 启动任务 | `POST /api/v1/tasks/start` | 复用；使用 `shared.StartRequest`，返回现有 `StartResult` |
| 停止任务 | `POST /api/v1/tasks/stop` | 复用；按 `task_id` 停止，等待安全收尾 |
| 查询一次任务 | `GET /api/v1/tasks/{task_id}` | 复用；返回任务快照和分析状态 |
| 页面刷新后恢复运行任务 | `GET /api/v1/tasks?position_id=...` | 新增；返回该岗位最新任务快照，无记录返回空列表 |
| 读取/清空本地步骤日志 | `GET/DELETE /api/v1/tasks/{task_id}/logs` | 新增；复用现有日志存储，不清除候选人动作记录 |
| 打开浏览器页面 | `POST /api/v1/page/open` | 复用；唯一对外打开入口 |
| 运行组件、诊断、更新、下载 | 新版既有对应接口 | 保留可用字段与错误提示，逐入口联调 |
| 云端执行记录列表 | `GET /api/task-runs` | 复用当前云端路由，不能因前端页面名另造接口 |

- [ ] 先写请求响应样例与失败测试：缺 `task_id`、未知任务、权限不足、缺配置、重复启动、停止等待中、Worker 不可用。
- [ ] `localRequest` 继续统一解包；前端不把 `{ok,data}` 当成旧版平铺状态读取，不丢弃错误的 `code/message/preflight`。
- [ ] 每次新运行产生独立本地 `task_id`；重复点击和请求超时重试使用同一编号，刷新页面通过查询恢复。
- [ ] 前端默认发现端口改为 `43129`，仍支持实际 `local_port`；检查 `agent_protocol`，旧端口缓存和任意 200 响应不能误认成新版。
- [ ] 开始、停止、状态、日志、小窗全部改走任务接口；移除对旧版 `/local/positions/...` 和无对应实现的强停调用的依赖。
- [ ] 不把“云端强制结束状态”当成本地停止成功。停止超时继续查询并提示，不偷偷杀进程或启动第二个任务。
- [ ] 保留执行任务双名单、候选人事件时间线、简历库进度、岗位模板标签等开发线已有界面；不从 main 覆盖这些页面。
- [ ] 入口全部替换并测试通过后，才清理新版内仅为旧版兼容存在的别名；不删除老源码目录。

**验收：** 登录 → 绑定 → 开始 → 实时分析 → 停止 → 查看本次云端记录全过程无旧版请求；页面刷新能恢复；只启动旧 Agent 时前端不假报新版已连接。

### M8：Windows 安装包与 A 批交付

**修改落点：** 新版 `scripts/package-windows.ps1`、`scripts/package-windows.bat`、`packaging/HRPlusLocalAgent.iss`、`internal/version`、`README.md`；开发启动脚本及前端下载/版本守卫配置。

- [ ] 先确认 Go、Node、Worker 编译产物、生产依赖、浏览器与 OCR 的路径契约，安装包不依赖开发目录。
- [ ] 吸收开发线环境化构建与 HR+ 品牌资源；正式版本沿开发线版本策略，在发布时确认具体版本，不沿用 main 新版默认 `6`。
- [ ] 安装程序写入独立新版程序目录与第 M2 节数据目录；不覆盖旧安装目录、不删除旧 Profile/SQLite。
- [ ] 安装前识别自身进程与程序路径，不因两版可执行文件同名而误结束旧版或其他进程。
- [ ] 检查 dev/prod 包的云端地址、控制台地址、版本、Node/CloakBrowser 平台和 SHA256；正式包不指向 localhost。
- [ ] Windows 11 真机验证首次安装、重复安装新版、中文路径、启动、绑定、浏览器、下载、OCR、提示音、防睡眠和安全退出。
- [ ] 修改开发启动入口为新版；老目录仍可供查阅，但不被默认启动、打包或自动探测。
- [ ] 经人工确认后更新下载地址、组件清单、版本守卫；上传安装包、修改远端配置另需发布授权。

**验收：** 干净 Windows 环境可安装运行，开发机路径不泄漏进发布包；A 批 C01–C17 完成或有用户确认的排除项；C18 保留且缺配置时受阻，不误宣传已可用。

### M9：保留并补齐 AI 自动回复（B 批）

**修改落点：** 新版 `flow/auto_reply/flow.go`、`integration/ai/client.go`、`platform/common/reply.go`、各平台 `reply.go/config.json`、`storage`、`preflight`；前端任务类型选择与运行状态。

**复用 Interface：** `GenerateReply(ctx, cfg, position, conversation, history) (string, error)`；`ScanUnreadConversations`、`ReadConversation`、`ReplyConversation`；不再新建一套聊天执行器。

- [ ] 保留来源版实现和测试，先用假 AI 与假浏览器验证“读取 → 生成 → 校验 → 发送 → 保存”的完整路径。
- [ ] 先接 Boss，再按智联、猎聘企业端、猎聘猎头端逐个接入；每个平台具备真实消息地址与选择器才允许自动回复启动。
- [ ] 把已确认的配置从 `pending_selectors` 转入正式 `selectors`，补 `conversation_fields` 的稳定身份与消息标识；不能只移除拦截而没有实际定位能力。
- [ ] 区分候选人消息、我方消息、系统提示、空消息与附件；没有新的候选人消息时不调用 AI、不发送。
- [ ] 使用现有岗位回复提示词 → AI 配置回复提示词 → 默认提示词的优先级；不擅自增加承诺薪资、自动约面等新产品规则。
- [ ] 保留空白及超过 1000 字回复的拦截；AI 失败不得发送占位文案。
- [ ] 发送前重新确认当前会话身份与最后一条候选人消息；发生人工发送、会话切换或新消息到达时丢弃旧生成结果，下一轮重新读取。
- [ ] 去重依据改为本地 Profile + 平台 + 稳定会话身份 + 候选人入站消息标识/指纹；回复文本哈希只作为附加审计信息，不能是唯一依据。
- [ ] 发送意图先持久化；明确发送成功后再标记成功。点击后超时或进程崩溃留下的未知结果先从页面核对，禁止盲目再发。
- [ ] 启动新任务、重启 Agent、AI 换一种措辞，均不能对同一入站消息再次回复；不同会话使用相同回复不应误去重。
- [ ] 与 greeting 共用浏览器独占管理、停止、权限和统计；不运行两个独立浏览器循环，不把回复逻辑塞进 Worker。
- [ ] 单独验证云端自动回复权限和前端任务类型入口；无权限或配置缺失时在浏览器动作前阻止启动。

**验收：** 一个新消息最多触发一次已确认回复；人工接管后不发送旧答案；错误会话不发送；异常结果不盲目重试；四个平台分别记录启用状态，不将单平台通过算成全部通过。

## 6. 数据、隐私与恢复策略

### 6.1 数据去向

| 数据 | 本地 | 云端 |
|---|---|---|
| Cookie、Profile、截图、OCR 临时文件 | 仅本地，按生命周期处理 | 禁止同步 |
| 任务状态、累计统计、不含敏感详情的动作摘要 | 保存并用于恢复 | 可同步 |
| 完整候选人详情、结构化简历 | 仅按任务需要处理 | 仅岗位明确开启 `output_structured_resume` 后允许结构化结果异步同步 |
| 聊天原文和 AI 回复原文 | 当前执行所需的内存上下文；去重落哈希/标识 | 不因迁移而新增完整聊天正文上传 |
| 待索要队列和回复发送状态 | 本地保存，按 Profile/任务关联 | 只同步允许的动作结果 |

现有简历库事件与进度必须保留，但不能为了展示名字或时间线绕过结构化输出开关。关闭开关时保留任务级统计及非敏感摘要；不能自动创建含完整详情的云端简历记录。

### 6.2 失败处理

- 浏览器动作成功、云端失败：保存本地结果，补同步；不重新点击。
- AI 评分已完成、结构化简历失败：主流程继续，记录同步失败，不撤销打招呼。
- SQLite 写入失败：涉及防重复发送记录时停止新的发送，不能在不知道是否记录成功时继续。
- Worker、浏览器或 OCR 退出：返回明确步骤错误，由生命周期处理终态和资源释放。
- 自动回复发送结果不确定：保持未知结果，先核对页面；不能一律算成功或自动重试。
- 开发回退：停新版，保留新版诊断数据，经确认恢复此前构建或原开发入口；这不是旧版兼容承诺，也不通过删除任一源码目录实现。
- 数据回退：使用明确的数据副本，不让旧 Agent 打开新版数据库；不自动恢复覆盖用户原数据。

## 7. 自动测试与页面验收清单

每个代码任务遵循：新增行为测试 → 确认在缺能力时失败 → 最小实现 → 定向测试 → 相关全量测试。仅修改文档、原样导入和验证既有正确行为不要求人为制造失败。

| 测试编号 | 场景 | 必须断言 |
|---|---|---|
| T01 | 同时启动两个任务 | 只有一个获得浏览器和 Profile，另一个明确被拒绝 |
| T02 | 云端启动返回 `run_id` | 本地保存并全链路传递；缺失编号不执行页面动作 |
| T03 | 同一任务启动重试 | 返回同一云端运行记录，不增加第二条 |
| T04 | 旧任务晚到补报 | 只写原 `run_id`，不污染新任务 |
| T05 | SSE 评分先到、完整简历延后 | 主流程先继续；输出开关关闭时上传次数为零 |
| T06 | 分数等于阈值/关键词边界 | 与选定旧基线的业务决定一致 |
| T07 | 已打招呼成功但云端超时 | 同一候选人点击次数仍为一次 |
| T08 | 回复后求简历 | 未回复/身份不符/按钮禁用不计成功，成功才补报 |
| T09 | 休息检查耗时接近预算 | 总时长不叠加，返回列表后仅等待剩余时间 |
| T10 | 停止、取消、浏览器关闭、进程退出 | 无遗留点击，锁释放，一次终态 |
| T11 | 自动回复缺配置 | 启动被拦截，greeting 不因缺消息配置被误拦截 |
| T12 | 同一消息二次扫描/重启/AI 换措辞 | 不再次回复 |
| T13 | 生成期间人工发消息或切换会话 | 原回复不发送 |
| T14 | 点击后超时/成功后落盘前崩溃 | 进入结果核对，不盲目重发 |
| T15 | 下载越界、损坏包、SHA256 不符 | 不打开越界文件、不安装错误包 |
| T16 | 前端缓存旧端口或错误程序返回 200 | 不误识别新版，不把凭证发给未知目标 |
| T17 | 打包后离开开发目录运行 | 无源码路径依赖，Worker 和平台配置齐全 |

针对已核实的自动回复配置缺口，可在新版 `internal/platform/config_template_test.go` 复用现有测试结构增加以下回归。示例只用于测试配置，不是招聘网站选择器：

```go
// TestAutoReplyMissingSelectorsBlocked 验证缺失消息配置始终被拦截。
func TestAutoReplyMissingSelectorsBlocked(t *testing.T) {
    for _, id := range []string{"boss", "zhaopin", "liepin", "hliepin"} {
        t.Run(id, func(t *testing.T) {
            cfg, err := LoadConfig(id)
            if err != nil {
                t.Fatal(err)
            }
            cfg.MessagesURL = "https://example.com/messages"
            cfg.Selectors = make(map[string]contract.SelectorSpec)
            if err := ValidateTaskConfig(cfg, "auto_reply"); err == nil {
                t.Fatal("缺少消息选择器时不能启动自动回复")
            }
            if err := ValidateTaskConfig(cfg, "greeting"); err != nil {
                t.Fatalf("消息配置缺失不应阻止打招呼：%v", err)
            }
        })
    }
}
```

真实页面验证分级：

1. 自建页面：所有 Worker 基础动作和错误场景，不需要招聘账号。
2. 招聘页面只读与导航：登录态、列表、详情、消息身份和选择器；进入页面仍须用户提供可用环境。
3. 真实打招呼/索要/AI 回复：先确认测试账号、候选人、内容和数量上限，再执行，不能默认为批量发送。
4. 长时测试：至少覆盖一个完整运行上限、两次模拟休息、一次安全停止；重复启动/停止 20 次检查资源释放；另做一次断网和一次 Worker 中断恢复验证。

缺账号或缺页面条件时记录“阻塞、未验证”，不改成空实现通过，也不使用脚本注入补位。

## 8. 验证命令

以下均是实施时的命令清单，本次文档编写没有运行。PowerShell 从仓库根执行，路径指向引入后的新版，不能误跑旧目录。

先确认环境和镜像：

```powershell
go version
node --version
npm --version
go env GOPROXY
npm config get registry
```

需要安装依赖时，先按项目规则确认 Go/npm 镜像，再使用锁文件安装；不在本次文档任务下载依赖。

```powershell
go -C goodhr5/local-agent-go-new test ./...
go -C goodhr5/local-agent-go-new build ./...
npm --prefix goodhr5/local-agent-go-new/worker run typecheck
npm --prefix goodhr5/local-agent-go-new/worker test
go -C goodhr5/cloud/backend test ./...
go -C goodhr5/cloud/backend build ./...
npm --prefix goodhr5/cloud/frontend-next run build
```

定向验证示例：

```powershell
go -C goodhr5/local-agent-go-new test ./internal/platform/... -count=1
go -C goodhr5/local-agent-go-new test ./internal/flow/... -count=1
go -C goodhr5/local-agent-go-new test ./internal/integration/... -count=1
go -C goodhr5/local-agent-go-new test ./internal/storage/... -count=1
```

- Windows `-race` 需要额外工具链时，只记录条件，不为本次迁移擅自安装编译器；普通测试通过不等于竞争检查通过。
- 前端当前只有 `dev/build/start` 脚本，不写不存在的 `npm test` 或 `npm run lint` 作为验收证据；需要的交互测试复用项目实际测试设施。
- Windows 打包脚本会写构建目录并下载依赖，须在构建授权及环境检查后执行；正式发布号由发布环节确认。
- 每次运行记录命令、提交号、退出码、失败用例、环境条件。基线既有失败可以单列，但不能跳过后宣称全量通过。

## 9. 阶段验收与交付

### A 批完成条件

- [ ] 两个源码目录同时存在，老目录未被覆盖或重命名。
- [ ] 默认开发启动、控制台连接和打包入口全部指向新版。
- [ ] C01–C17 有逐项验证证据，开发线后续新增功能已完成增量核对。
- [ ] `position_id/task_id/run_id` 关联明确，运行记录、索要补报和事件不串任务。
- [ ] 四平台既有打招呼能力分别通过测试；缺账号的平台明确列为阻塞，不能宣布整体通过。
- [ ] 回复后索要、休息窗口检查和停止语义按本方案确认后实现。
- [ ] AI 自动回复源码保留，未配置平台不能启动。
- [ ] Windows 安装包真机验收通过；无旧数据覆盖，无招聘页面脚本注入。
- [ ] 经确认合入本地 dev；仅在另行授权后推送远程 dev。

### B 批完成条件

- [ ] 每个拟启用平台均补齐消息页和选择器，并有真实页面证据。
- [ ] AI 回复链路、跨运行去重、人工接管、未知发送结果处理均通过。
- [ ] 前端可明确区分主动打招呼、回复后索要与 AI 自动回复。
- [ ] 会员限制、隐私约束、任务独占、停止和结果同步全部生效。
- [ ] 不把 B 批未完成项藏在 A 批“迁移完成”的结论里。

### 实施交接记录

每个 M 任务完成时，在既有能力矩阵记录以下字段：

| 字段 | 记录要求 |
|---|---|
| 基线 | dev SHA、新版来源 SHA、当前实施 SHA |
| 范围 | 本阶段能力编号和实际文件清单 |
| 差异 | 保留旧行为、复用新实现、明确改动的原因 |
| 自动验证 | 命令、退出码、关键用例和既有失败 |
| 页面验证 | 平台、账号类型、动作范围、是否真实发送、结果 |
| 阻塞 | 缺少的账号/配置/环境、影响的交付批次 |
| 下一步 | 一个明确的下一阶段，不跨过未通过门槛 |

## 10. 下一次开工前的确认清单

本次已经确定迁移方向，尚未授权实际改造。实施开始前仅需落实以下操作性事项：

1. 使用重新核实并锁定的 dev/main 提交，确认没有遗漏正在开发的能力。
2. 确认改造分支、独立工作树和现有未提交文件的处理方式。
3. 确认本方案的新版独立数据目录，以及 M5“停止后不再新发起索要、保留到下次检查”的安全停止调整。
4. 确认先执行 A 批，还是同时安排 B 批的真实页面适配；两批始终分别验收。
5. 真实账号测试前单独确认可发送范围；发布、远端配置、提交和推送按当次授权执行。

建议首次实施只推进 M0–M1：固定基线、创建专门分支、引入 main 新版并取得基线测试结果。此时不改业务规则，不切换现有默认运行入口。
