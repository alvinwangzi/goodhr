<!-- 本文件用于跟踪"自动回复 FAQ 语料 + 候选人扫描记录表 + 任务多选"功能的开发进度与验证结果。 -->

# 自动回复 FAQ 与候选人扫描记录开发清单

更新时间：2026-09-23

## 背景

- 当前 AI 自动回复只有一套简单的提示词（岗位回复提示词 > 全局回复规则 > 硬编码兜底），没有参考语料，候选人问上下班时间、社保等常见问题时 AI 无法据实回答。
- 自动回复不区分候选人状态：不管是系统打过招呼的还是主动投递的，一律用同一套逻辑回复。缺少"已筛过的要简历、没筛过的先筛选、不合格礼貌拒绝"的分流能力。
- 打招呼流程和自动回复流程之间没有共享的候选人状态，互相不知道对方处理过谁。
- 启动岗位时"打招呼"和"AI 自动回复"是二选一，不能同时跑。

## 功能目标

1. **岗位 FAQ 语料**：岗位编辑里允许配置常见问答（最多 10 条，问题 20 字，回答 50 字），AI 回复时作为参考注入提示词。
2. **拒绝话术**：岗位可自定义拒绝模板，留空用系统默认。
3. **候选人扫描记录表**：云端新建表，作为打招呼与自动回复的共享状态。打招呼流程写入 >= 50 分的候选人；自动回复流程写入所有遇到的人。
4. **自动回复分流**：查表 → 有记录按状态处理 → 无记录先评分再处理。
5. **启动多选**：打招呼和 AI 自动回复从二选一改为可多选，选中后先跑打招呼再跑自动回复。

## 入库口径

| 来源 | 入库条件 | 原因 |
|------|----------|------|
| 打招呼流程（候选人列表） | AI 评分 >= 50 才入库 | 列表扫描量大，控制表大小 |
| 自动回复流程（消息页会话） | 所有人必须入库，不管几分 | 会话列表会反复扫描，不入库会反复重评浪费 AI 调用 |

---

## A. 云端数据库

- [ ] 新建 migration 文件（编号接当前最大），创建 `candidate_screenings` 表：

```sql
CREATE TABLE IF NOT EXISTS candidate_screenings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    position_id UUID NOT NULL REFERENCES positions(id) ON DELETE CASCADE,
    platform VARCHAR(50) NOT NULL,                    -- 平台标识（boss/liepin/zhaopin/hliepin）
    platform_candidate_id VARCHAR(255) NOT NULL,      -- 平台侧候选人唯一标识
    candidate_name VARCHAR(100) NOT NULL DEFAULT '',  -- 候选人姓名
    score INTEGER NOT NULL DEFAULT 0,                 -- AI 评分
    status VARCHAR(20) NOT NULL DEFAULT 'screened',   -- passed（过打招呼阈值）/ screened（过入库线但没过打招呼阈值）
    resume_status VARCHAR(20) NOT NULL DEFAULT 'none',-- none / requested / received
    source VARCHAR(20) NOT NULL DEFAULT 'greeting',   -- greeting（打招呼流程）/ auto_reply（自动回复流程）
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),    -- 首次扫描时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()     -- 最后更新时间
);

COMMENT ON TABLE candidate_screenings IS '候选人扫描记录表，打招呼与自动回复共享的筛选状态';
```

- [ ] 建唯一索引：`UNIQUE (position_id, platform, platform_candidate_id)`，同一岗位同一平台同一候选人只有一条记录
- [ ] 建查询索引：`(position_id, platform, platform_candidate_id)` 用于自动回复时按岗位+平台+候选人快速查找
- [ ] 岗位 `positions` 表的 `ai_config` JSONB 新增两个可选键（无需 migration，JSONB 自由扩展）：
  - `reply_faq`：`[{ "q": "...", "a": "..." }]`，最多 10 条
  - `reply_reject_template`：`string`，岗位自定义拒绝话术，留空用系统默认

## B. 云端后端接口

- [ ] `candidate_screening_store.go`（新建）：`CandidateScreeningStore` 接口 + PG 实现
  - `FindScreening(ctx, positionID, platform, platformCandidateID) (*CandidateScreening, error)`：按岗位+平台+候选人查，找不到返回 `ErrNotFound`
  - `UpsertScreening(ctx, screening *CandidateScreening) error`：插入或更新（ON CONFLICT 更新 score/status/resume_status/updated_at）
  - `ListScreeningsByPosition(ctx, positionID string, limit, offset int) ([]CandidateScreening, int, error)`：按岗位分页列表（后台查看用）
- [ ] `candidate_screening.go`（新建 handler）：
  - `POST /api/positions/{id}/screenings`：本地程序上报扫描结果（批量，payload 为数组），去重写入
  - `GET /api/positions/{id}/screenings?platform=...&candidate_id=...`：本地程序查询单个候选人扫描记录
  - `GET /api/positions/{id}/screenings?page=1&page_size=20`：后台分页列表
- [ ] `server.go` 注册路由：在 `positionRoute` 下追加 `/screenings` 分发
- [ ] 岗位保存接口（`POST /api/positions`）：`ai_config` 里的 `reply_faq` 和 `reply_reject_template` 随 JSONB 整体保存，无需额外处理，但前端保存时需校验格式

## C. 前端岗位编辑

- [ ] 岗位编辑表单新增"AI 回复配置"区域（在已有的"AI 回复提示词"下方）：
  - **FAQ 语料编辑**：动态列表，每行两个输入框（问题 + 回答），带添加/删除按钮
    - 最多 10 条
    - 问题字段 maxLength=20，回答字段 maxLength=50
    - 显示字数计数器（如 "12/20"）
  - **拒绝话术输入**：多行文本框
    - placeholder 显示系统默认话术："感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。"
    - helperText 说明："候选人不符合岗位要求时发送此消息，留空使用系统默认"
- [ ] 保存逻辑：`reply_faq` 和 `reply_reject_template` 写入 `ai_config`，随现有 `mergeReplyPrompt` 一起提交
- [ ] 编辑回填：`formFromItem` 读取 `ai_config.reply_faq` 和 `ai_config.reply_reject_template`

## D. 前端启动弹窗

- [ ] `ChoiceCards` 组件检查是否支持多选模式；如不支持，新增 `multiSelect` 属性
- [ ] `startTaskType` 状态从 `string` 改为 `string[]`（如 `["greeting", "auto_reply"]`）
- [ ] 启动确认按钮文案按选中数量调整：全选显示"开始运行"，单选保持原逻辑
- [ ] 启动请求：`task_type` 传数组或逗号分隔字符串，本地程序解析
- [ ] 自动回复选项的禁用逻辑保持不变（平台支持 + 会员权限）

## E. 本地 Go 打招呼流程改造

- [ ] `scan.go` / `candidate.go`：每个候选人处理完成后，如果 AI 评分 >= 50，调用 `cloudapi.ReportScreening` 上报扫描记录
  - 上报数据：`position_id`、`platform`、`platform_candidate_id`（从候选人数据取）、`candidate_name`、`score`、`status`（passed=过了打招呼阈值 / screened=没过阈值但 >= 50）、`source="greeting"`
  - 上报失败不阻塞打招呼流程（记日志，下次再补）
- [ ] `cloudapi/client.go` 新增 `ReportScreenings(ctx, positionID string, screenings []ScreeningRecord) error`：批量上报

## F. 本地 Go 自动回复流程改造

- [ ] `auto_reply.go` 的 `replyFlow.process` 改造：对每个未读会话，先查云端扫描记录
  - 调用 `GET /api/positions/{id}/screenings?platform=...&candidate_id=...`
  - 找到记录 → 按 status 决定动作：
    - `passed` → 正常回复（FAQ + 上下文 + 要简历判断）
    - `screened` → 回复但不主动要简历（没过打招呼阈值）
  - 没找到记录 → 需要现场筛选：
    - 读取对话面板可见的候选人简单信息
    - 调用 AI 首次评分（复用现有评分能力）
    - 上报扫描记录到云端（不管多少分都写）
    - 评分 >= 打招呼阈值 → 标记 passed → 正常回复 + 要简历
    - 评分 >= 50 但 < 打招呼阈值 → 标记 screened → 回复但不要简历
    - 评分 < 50 → 入库（source=auto_reply）→ 发拒绝模板
- [ ] 拒绝话术发送：直接发固定文本，不走 AI 生成
  - 优先用岗位 `reply_reject_template`
  - 留空用系统默认："感谢你的关注，我们看了你的信息，跟我们的岗位要求不匹配。下次有机会再合作。"
- [ ] `replyFlow.request` 扩展：增加 `FAQ` 字段（`[]FAQEntry`）和 `RejectTemplate` 字段
- [ ] 读取岗位配置时从 `ai_config` 取 `reply_faq` 和 `reply_reject_template`

## G. 本地 Go 提示词组装改造

- [ ] `reply.go` 的 `GenerateReply` 改造：用户消息里注入 FAQ
  ```
  岗位：XX
  岗位要求：XX
  参考问答（候选人可能问到，请据此回答；没有对应内容的不要编造）：
  - 上下班时间 → 9:00-18:00
  - 社保 → 五险一金
  当前目标：根据对话推进，必要时索要简历
  会话：XX
  ```
- [ ] FAQ 为空时不注入"参考问答"段落，保持原逻辑
- [ ] 补充测试：FAQ 注入验证、空 FAQ 兼容、拒绝模板优先级

## H. 本地 Go 任务多选编排

- [ ] `runner.go` / `lifecycle.go`：`TaskType` 从 `string` 改为 `[]string`，支持 `["greeting", "auto_reply"]`
- [ ] 执行顺序：先跑打招呼流程，跑完不停浏览器，接着跑自动回复流程
- [ ] 打招呼结束 → 自动回复启动之间：记录日志"打招呼完成，正在切换到自动回复"
- [ ] 停止逻辑：用户点停止时，不管在跑哪个流程都正常中断
- [ ] 统计分开：打招呼统计和自动回复统计独立展示

## I. 测试

- [ ] 云端：`candidate_screening_store_test.go` — CRUD、唯一约束冲突、分页
- [ ] 云端：handler 测试 — 上报去重、查询匹配、权限校验
- [ ] 本地：`reply_test.go` — FAQ 注入提示词、拒绝模板发送、查表分流三种场景
- [ ] 本地：`auto_reply_test.go` — 有记录 passed/screened、无记录现场筛选入库
- [ ] 本地：打招呼流程上报扫描记录的测试
- [ ] 前端：岗位编辑 FAQ 增删改、字数限制、拒绝模板保存

## J. 文档与提交

- [ ] 本清单按开发进度逐项勾选
- [ ] 提交功能分支（分支名待定，如 `feat/auto-reply-faq-screening`）
- [ ] 涉及文件超过 6 个，按项目规则签新分支开发

---

## 验证记录

（开发完成后填写）
