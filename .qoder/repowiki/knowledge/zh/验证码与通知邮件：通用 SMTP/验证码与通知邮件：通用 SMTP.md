---
kind: external_dependency
name: 验证码与通知邮件：通用 SMTP
slug: smtp-mailer
category: external_dependency
category_hints:
    - framework_behavior
    - client_constraint
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/.env.example
    - goodhr5/cloud/backend/internal/httpapi/config.go
---

### 身份与角色
- 通用的 SMTP 发信通道，用于发送登录验证码、激活码、任务完成通知等。
- 不绑定特定厂商；生产环境通过 SMTPHost/Port/Username/Password/From 配置。

### 行为约束
- 开发环境（`GOODHR_APP_ENV=dev`）强制走 DevMailer，只打印日志不发真实邮件。
- 生产环境若 SMTP 配置不完整（host/username/password 缺一），降级为 DevMailer 并输出警告。
- 默认端口 465。

### 稳定约束
- 上线时必须配置完整的 SMTP 五件套；否则所有邮件功能静默降级。