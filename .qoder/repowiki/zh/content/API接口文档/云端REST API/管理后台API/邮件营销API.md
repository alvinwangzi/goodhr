# 邮件营销API

<cite>
**本文引用的文件**   
- [admin_email.go](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go)
- [mailer.go](file://goodhr5/cloud/backend/internal/httpapi/mailer.go)
- [email_campaign_store.go](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go)
- [email_campaign_store_pg.go](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go)
- [0053_admin_email_campaigns.sql](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql)
- [0064_flow_reminder_email_config.sql](file://goodhr5/cloud/backend/db/migrations/0064_flow_reminder_email_config.sql)
- [flow-reminder-api.md](file://goodhr5/docs/flow-reminder-api.md)
- [subscription_reward.html](file://goodhr5/cloud/backend/templates/email/subscription_reward.html)
</cite>

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构总览](#架构总览)
5. [详细接口说明](#详细接口说明)
6. [依赖关系分析](#依赖关系分析)
7. [性能与可靠性](#性能与可靠性)
8. [故障排查指南](#故障排查指南)
9. [结论](#结论)
10. [附录：模板、数据模型与工作流示例](#附录模板数据模型与工作流示例)

## 简介
本文件为 HRPlus 云后端“邮件营销”相关能力的完整接口文档。系统提供以下能力：
- 超级管理员自定义邮件批量发送
- 自动流程提醒与挽回邮件任务
- 邮件批次创建、进度查询、收件人明细查看
- 打开追踪统计
- 公共定时任务触发器，用于外部调度平台调用
- 邮件内容渲染、SMTP 发送、TLS 安全通道支持
- 基于数据库的幂等控制、失败记录与基础统计

需要特别说明的是：当前实现以“超管后台邮件 + 自动流程提醒”为主，未提供独立的“邮件模板管理 API”。模板通过系统配置和 HTML 文件管理；模板变量替换由服务端在发送前完成。

## 项目结构
邮件营销功能主要分布在以下位置：
- HTTP 服务层：`internal/httpapi/admin_email.go`
- 邮件发送抽象与 SMTP 实现：`internal/httpapi/mailer.go`
- 邮件批次与收件人存储接口及内存实现：`internal/httpapi/email_campaign_store.go`
- PostgreSQL 持久化实现：`internal/httpapi/email_campaign_store_pg.go`
- 数据库迁移：`db/migrations/0053_admin_email_campaigns.sql`、`db/migrations/0064_flow_reminder_email_config.sql`
- 公共定时任务使用说明：`docs/flow-reminder-api.md`
- 内置邮件模板：`templates/email/...`、`templates/automatic_emails/...`

```mermaid
graph TB
AdminUI["前端管理页面<br/>admin/mail/page.tsx"] --> AdminAPI["超管邮件接口<br/>admin_email.go"]
Scheduler["外部调度平台<br/>Cron / 计划任务"] --> PublicJob["公共定时任务接口<br/>admin_email.go"]
AdminAPI --> Store["邮件批次存储接口<br/>email_campaign_store.go"]
Store --> PGStore["PostgreSQL 实现<br/>email_campaign_store_pg.go"]
AdminAPI --> Mailer["邮件发送抽象<br/>mailer.go"]
Mailer --> SMTP["SMTP 服务器"]
AdminAPI --> OpenPixel["打开追踪接口<br/>admin_email.go"]
```

**图表来源**
- [admin_email.go:59-142](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L59-L142)
- [admin_email.go:204-236](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L204-L236)
- [email_campaign_store.go:57-66](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L57-L66)
- [email_campaign_store_pg.go:21-58](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L21-L58)
- [mailer.go:19-25](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L19-L25)

**章节来源**
- [admin_email.go:1-778](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L1-L778)
- [mailer.go:1-463](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L1-L463)
- [email_campaign_store.go:1-219](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L1-L219)
- [email_campaign_store_pg.go:1-277](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L1-L277)
- [0053_admin_email_campaigns.sql:1-94](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L1-L94)
- [0064_flow_reminder_email_config.sql:1-24](file://goodhr5/cloud/backend/db/migrations/0064_flow_reminder_email_config.sql#L1-L24)
- [flow-reminder-api.md:1-343](file://goodhr5/docs/flow-reminder-api.md#L1-L343)

## 核心组件
- `AdminEmailService`：负责超管邮件列表、详情、发送、图片上传、打开追踪、公共定时任务处理、自动流程提醒与挽回邮件调度。
- `Mailer` 接口与 `SMTPMailer`：定义多种邮件发送方法，并提供 SMTP 明文与 TLS 通道实现。
- `EmailCampaignStore` 接口：定义邮件批次、收件人、目标用户筛选、幂等键检查等能力。
- `MemoryEmailCampaignStore`：内存实现，适合测试或轻量场景。
- `PostgresEmailCampaignStore`：PostgreSQL 实现，生产环境使用。
- 数据库表：`email_batches` 保存批次元信息与统计；`email_recipients` 保存每个收件人的状态、错误信息、打开时间与发送时间。

**章节来源**
- [admin_email.go:20-57](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L20-L57)
- [mailer.go:19-25](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L19-L25)
- [email_campaign_store.go:14-66](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L14-L66)
- [email_campaign_store_pg.go:12-19](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L12-L19)
- [0053_admin_email_campaigns.sql:2-54](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L2-L54)

## 架构总览
邮件营销工作流分为三类：
1. 超管手动批量发送
2. 公共定时任务触发的自动流程提醒
3. 系统内部每日定时挽回提醒

```mermaid
sequenceDiagram
participant Admin as "超级管理员"
participant API as "超管邮件接口"
participant Store as "邮件批次存储"
participant Worker as "异步发送协程"
participant Mailer as "邮件发送器"
participant DB as "PostgreSQL"
participant Recipient as "收件人邮箱客户端"
Admin->>API : POST /api/admin/emails
API->>Store : CreateBatch(主题, 摘要, 创建者, 收件人列表)
Store-->>API : 返回批次与收件人
API-->>Admin : 返回批次ID
API->>Worker : 启动 sendBatch
Worker->>Mailer : SendCustomHTML(每封邮件)
Mailer->>DB : 更新发送成功/失败
Note over Worker,DB : 每条收件人独立标记状态
Recipient->>API : GET /api/public/mail/open?id=xxx
API->>DB : MarkRecipientOpened
```

**图表来源**
- [admin_email.go:106-142](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L106-L142)
- [admin_email.go:523-534](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L523-L534)
- [admin_email.go:193-202](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L193-L202)
- [email_campaign_store_pg.go:116-141](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L116-L141)

## 详细接口说明

### 超管邮件批次接口

#### 获取邮件批次列表
- **路径**：`GET /api/admin/emails`
- **鉴权**：超级管理员会话
- **响应字段**：
  - `ok`：布尔值
  - `batches`：最近邮件批次数组
- **用途**：查看最近创建的邮件批次，便于监控发送进度。

**章节来源**
- [admin_email.go:59-72](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L59-L72)
- [admin_email.go:96-104](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L96-L104)

#### 获取指定邮件批次详情
- **路径**：`GET /api/admin/emails/{batch_id}`
- **鉴权**：超级管理员会话
- **路径参数**：
  - `batch_id`：邮件批次ID
- **响应字段**：
  - `ok`：布尔值
  - `batch`：批次信息
  - `recipients`：收件人列表
- **错误码**：
  - 400：缺少批次ID
  - 404：批次不存在
  - 500：加载失败

**章节来源**
- [admin_email.go:74-94](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L74-L94)

#### 创建并发送邮件批次
- **路径**：`POST /api/admin/emails`
- **鉴权**：超级管理员会话
- **请求体字段**：
  - `subject`：邮件标题，必填
  - `html`：HTML 正文，必填
  - `mode`：可选模式标识
  - `emails`：指定邮箱列表，支持逗号、换行、分号分隔
  - `tags`：画像标签过滤条件
  - `flows`：流程节点过滤条件
  - `last_login_before_days`：至少多少天未登录
  - `meta`：扩展元数据
- **业务规则**：
  - 标题与正文不能为空
  - 收件人必须至少匹配一个邮箱
  - 会自动追加统一页脚
  - 创建成功后立即异步发送
- **响应字段**：
  - `ok`：布尔值
  - `batch`：创建的批次信息

**章节来源**
- [admin_email.go:27-36](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L27-L36)
- [admin_email.go:106-142](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L106-L142)
- [admin_email.go:548-593](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L548-L593)

#### 上传富文本图片
- **路径**：`POST /api/admin/emails/upload-image`
- **鉴权**：超级管理员会话
- **表单字段**：
  - `file`：图片文件
- **限制**：
  - 最大 8MB
  - 仅支持 png、jpg、jpeg、gif、webp
- **响应字段**：
  - `ok`：布尔值
  - `url`：相对路径
  - `absolute_url`：完整公网地址

**章节来源**
- [admin_email.go:144-191](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L144-L191)

### 打开追踪接口

#### 标记邮件被打开
- **路径**：`GET /api/public/mail/open?id={recipient_id}`
- **鉴权**：无需鉴权
- **行为**：
  - 根据 `recipient_id` 标记对应收件人已打开
  - 返回 1×1 GIF 像素图
- **用途**：嵌入邮件 HTML 中，当收件人加载图片时记录打开事件。

**章节来源**
- [admin_email.go:193-202](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L193-L202)
- [admin_email.go:644-649](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L644-L649)

### 公共定时任务接口

#### 通用自动邮件任务
- **路径**：`POST /api/public/email-jobs/{job}` 或 `GET /api/public/email-jobs/{job}`
- **鉴权**：环境变量 `GOODHR_EMAIL_JOB_TOKEN`，可通过 `Authorization: Bearer` 或 URL 参数 `token` 传递
- **支持的 job**：
  - `yesterday-incomplete`
  - `inactive-3-days`
  - `inactive-7-days`
  - `inactive-30-days`
- **响应字段**：
  - `ok`：布尔值
  - `result`：自动邮件结果对象，包含批次、跳过原因、预览数量等

**章节来源**
- [admin_email.go:204-236](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L204-L236)
- [admin_email.go:369-385](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L369-L385)

#### 流程提醒任务
- **路径**：`GET /api/public/email-jobs/flow-reminder` 或 `POST /api/public/email-jobs/flow-reminder`
- **鉴权**：同公共定时任务
- **查询参数**：
  - `flows`：流程节点列表，支持逗号、中文逗号、分号、中文分号分隔
  - `stalled_hours`：流程停滞小时数，默认 24，范围 1~8760
  - `limit`：最大匹配用户数，默认 1000，范围 1~5000
  - `created_day`：注册日期，格式 YYYY-MM-DD
  - `dry_run`：预览模式，不实际发送邮件
- **响应字段**：
  - `ok`：布尔值
  - `result.job`：任务名
  - `result.batches`：创建的批次
  - `result.skipped`：跳过原因
  - `result.preview`：按流程节点分组的预览数量

**章节来源**
- [admin_email.go:238-307](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L238-L307)
- [admin_email.go:309-356](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L309-L356)
- [flow-reminder-api.md:1-343](file://goodhr5/docs/flow-reminder-api.md#L1-L343)

### 自动挽回提醒调度
- 系统会按配置的每日小时启动定时任务
- 对注册满 1、3、7、30 天且未完成流程的用户发送提醒
- 每次提醒会根据用户当前流程节点选择不同模板
- 使用幂等键避免重复发送

**章节来源**
- [admin_email.go:358-396](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L358-L396)
- [admin_email.go:404-451](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L404-L451)
- [admin_email.go:663-778](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L663-L778)

## 依赖关系分析

```mermaid
classDiagram
class AdminEmailService {
+Collection(w, r)
+Detail(w, r)
+List(w, r)
+Send(w, r)
+UploadImage(w, r)
+OpenPixel(w, r)
+PublicJob(w, r)
+sendFlowReminder(req, baseURL)
+SendAutomaticJob(job, baseURL)
+StartRecoveryScheduler()
+SendScheduledRecovery()
}
class EmailCampaignStore {
<<interface>>
+CreateBatch(subject, targetSummary, sourceKey, createdBy, emails)
+GetBatch(id)
+ListBatches(limit)
+MarkRecipientSent(id)
+MarkRecipientFailed(id, message)
+MarkRecipientOpened(id)
+FindTargetUsers(filter)
+SourceKeyExists(sourceKey)
}
class MemoryEmailCampaignStore {
+CreateBatch(...)
+GetBatch(...)
+ListBatches(...)
+MarkRecipientSent(...)
+MarkRecipientFailed(...)
+MarkRecipientOpened(...)
+FindTargetUsers(...)
+SourceKeyExists(...)
}
class PostgresEmailCampaignStore {
+CreateBatch(...)
+GetBatch(...)
+ListBatches(...)
+MarkRecipientSent(...)
+MarkRecipientFailed(...)
+MarkRecipientOpened(...)
+FindTargetUsers(...)
+SourceKeyExists(...)
}
class Mailer {
<<interface>>
+SendLoginCode(email, code)
+SendSubscriptionReward(email, notice)
+SendAIBalanceNotice(email, notice)
+SendPositionStatus(email, notice)
+SendCustomHTML(email, subject, htmlBody, plainText)
}
class SMTPMailer {
+Host
+Port
+Username
+Password
+From
+SendCustomHTML(...)
+sendMessage(...)
+renderHTML(...)
}
AdminEmailService --> EmailCampaignStore : "依赖"
AdminEmailService --> Mailer : "依赖"
MemoryEmailCampaignStore ..|> EmailCampaignStore
PostgresEmailCampaignStore ..|> EmailCampaignStore
SMTPMailer ..|> Mailer
```

**图表来源**
- [admin_email.go:20-57](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L20-L57)
- [email_campaign_store.go:57-66](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L57-L66)
- [email_campaign_store.go:68-77](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L68-L77)
- [email_campaign_store_pg.go:12-19](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L12-L19)
- [mailer.go:19-25](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L19-L25)
- [mailer.go:104-110](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L104-L110)

**章节来源**
- [admin_email.go:20-57](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L20-L57)
- [email_campaign_store.go:57-66](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L57-L66)
- [email_campaign_store_pg.go:12-19](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L12-L19)
- [mailer.go:19-25](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L19-L25)

## 性能与可靠性

### 发送队列与并发
- 超管发送接口创建批次后，立即启动 goroutine 异步发送
- 每条收件人独立调用邮件发送器，失败不影响其他收件人
- 没有全局队列管理器，也没有重试机制

**章节来源**
- [admin_email.go:133-142](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L133-L142)
- [admin_email.go:523-534](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L523-L534)

### 重试机制
- 当前实现未实现自动重试
- 失败收件人会被标记为 `failed`，并记录错误信息
- 如需重试，建议外部调度系统重新触发相同任务，或通过幂等键控制

**章节来源**
- [email_campaign_store.go:140-143](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L140-L143)
- [email_campaign_store_pg.go:125-132](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L125-L132)

### 失败处理策略
- 单条收件人发送失败不会中断整批发送
- 失败原因写入 `error_message`
- 批次统计会重算成功、失败、已打开数量
- 所有收件人状态完成后，批次标记为已完成

**章节来源**
- [email_campaign_store.go:167-207](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store.go#L167-L207)
- [email_campaign_store_pg.go:209-230](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L209-L230)

### 内容审核与防垃圾邮件
- 图片上传限制类型与大小
- 公共定时任务需要令牌鉴权
- 流程提醒任务有参数范围校验与上限限制
- 未实现反垃圾邮件评分、退订列表、频率限制策略

**章节来源**
- [admin_email.go:144-191](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L144-L191)
- [admin_email.go:651-661](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L651-L661)
- [admin_email.go:273-307](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L273-L307)

### 模板变量替换
- 模板变量在服务端渲染阶段替换
- 自动邮件模板从代码目录或系统配置读取
- 超管自定义邮件直接传入 HTML，不再进行模板变量替换
- 打开追踪图片由发送前统一追加

**章节来源**
- [mailer.go:314-335](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L314-L335)
- [admin_email.go:721-736](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L721-L736)
- [admin_email.go:523-534](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L523-L534)

## 故障排查指南

### 无法发送邮件
- 检查 SMTP 主机、端口、用户名、密码是否配置正确
- 检查端口是否为 465，若是则使用 TLS 通道
- 检查邮件模板是否能正常读取与解析

**章节来源**
- [mailer.go:278-312](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L278-L312)
- [mailer.go:314-348](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L314-L348)
- [mailer.go:410-449](file://goodhr5/cloud/backend/internal/httpapi/mailer.go#L410-L449)

### 公共定时任务返回 401
- 检查环境变量 `GOODHR_EMAIL_JOB_TOKEN` 是否配置
- 检查请求头 `Authorization: Bearer YOUR_EMAIL_JOB_TOKEN` 是否正确
- 检查 URL 参数 `token` 是否与配置一致

**章节来源**
- [admin_email.go:651-661](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L651-L661)
- [flow-reminder-api.md:27-45](file://goodhr5/docs/flow-reminder-api.md#L27-L45)

### 打开追踪未生效
- 检查邮件 HTML 是否包含追踪图片
- 检查 `baseURL` 是否为完整公网地址
- 检查 `/api/public/mail/open` 是否可访问

**章节来源**
- [admin_email.go:536-546](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L536-L546)
- [admin_email.go:644-649](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L644-L649)
- [admin_email.go:193-202](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L193-L202)

### 自动提醒重复发送
- 检查 `source_key` 是否唯一
- 检查系统配置中的模板是否启用
- 检查幂等键生成逻辑是否符合预期

**章节来源**
- [email_campaign_store_pg.go:143-151](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L143-L151)
- [admin_email.go:453-460](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L453-L460)
- [0053_admin_email_campaigns.sql:16-18](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L16-L18)

## 结论
HRPlus 邮件营销能力以“超管批量发送 + 自动流程提醒”为核心，具备完整的批次管理、收件人状态跟踪、打开追踪与基础统计能力。系统在安全性上提供了图片类型限制、公共任务令牌鉴权与参数范围校验；在可靠性上实现了失败记录与批次完成判断。当前版本未提供独立的模板管理 API、全局发送队列、自动重试与反垃圾邮件策略，这些可作为后续演进方向。

## 附录：模板、数据模型与工作流示例

### 数据模型

```mermaid
erDiagram
EMAIL_BATCHES {
uuid id PK
text subject
text target_summary
text source_key
text created_by_email
int total_count
int sent_count
int failed_count
int opened_count
timestamptz created_at
timestamptz finished_at
}
EMAIL_RECIPIENTS {
uuid id PK
uuid batch_id FK
text email
text status
text error_message
boolean opened
timestamptz opened_at
timestamptz created_at
timestamptz sent_at
}
EMAIL_BATCHES ||--o{ EMAIL_RECIPIENTS : "包含"
```

**图表来源**
- [0053_admin_email_campaigns.sql:2-34](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L2-L34)

**章节来源**
- [0053_admin_email_campaigns.sql:1-94](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L1-L94)

### 模板管理方式
- 自动邮件模板位于 `templates/automatic_emails/`，包括流程帮助模板与统一页脚
- 普通业务邮件模板位于 `templates/email/`，例如会员奖励通知
- 模板变量在服务端渲染时替换
- 系统配置 `system.email_recovery` 可覆盖默认模板内容与作者信息

**章节来源**
- [admin_email.go:676-736](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L676-L736)
- [0053_admin_email_campaigns.sql:56-93](file://goodhr5/cloud/backend/db/migrations/0053_admin_email_campaigns.sql#L56-L93)
- [0064_flow_reminder_email_config.sql:1-24](file://goodhr5/cloud/backend/db/migrations/0064_flow_reminder_email_config.sql#L1-L24)
- [subscription_reward.html:22-32](file://goodhr5/cloud/backend/templates/email/subscription_reward.html#L22-L32)

### 完整工作流示例：从模板设计到效果分析

```mermaid
flowchart TD
Start["开始"] --> Design["设计或选择邮件模板<br/>HTML 模板或系统配置模板"]
Design --> Prepare["准备收件人列表<br/>指定邮箱、标签、流程节点、登录状态"]
Prepare --> Validate["校验标题、正文、收件人"]
Validate --> CreateBatch["创建邮件批次<br/>记录总数与幂等键"]
CreateBatch --> SendAsync["异步逐条发送邮件"]
SendAsync --> TrackState["记录发送成功或失败"]
TrackState --> OpenTrack["收件人打开邮件<br/>加载追踪图片"]
OpenTrack --> UpdateOpen["标记已打开并更新时间"]
UpdateOpen --> Analyze["查看批次统计<br/>发送成功率、打开率"]
Analyze --> End["结束"]
```

**图表来源**
- [admin_email.go:106-142](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L106-L142)
- [admin_email.go:523-534](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L523-L534)
- [admin_email.go:193-202](file://goodhr5/cloud/backend/internal/httpapi/admin_email.go#L193-L202)
- [email_campaign_store_pg.go:116-141](file://goodhr5/cloud/backend/internal/httpapi/email_campaign_store_pg.go#L116-L141)