---
kind: external_dependency
name: SMTP 邮件发送
slug: smtp
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/.env.example
---

### SMTP
- 角色：云端发送邮件（邀请码、通知、营销邮件等）的传输通道。
- 集成方式：通过环境变量 `GOODHR_SMTP_HOST` 指定 SMTP 服务器地址；具体端口、认证凭据按所用 SMTP 服务商约定注入。
- 稳定约束：部署时需配置可用的 SMTP 服务（如企业邮箱、SendGrid、阿里云邮件推送等），否则邮件类功能不可用。