---
kind: external_dependency
name: AI 大模型服务：通义千问 DashScope OpenAI 兼容端点
slug: dashscope-qwen
category: external_dependency
category_hints:
    - vendor_identity
    - sdk_real_api
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/internal/httpapi/system_config_store.go
    - goodhr5/cloud/backend/internal/httpapi/ai_wallet.go
    - goodhr5/cloud/frontend-next/app/admin/ai-config/page.tsx
---

### 身份与角色
- 项目默认的 AI 大模型供应商，通过 DashScope 的 OpenAI 兼容 API 接入。
- 前端 AI 配置页面默认 base URL 指向千问的 OpenAI 兼容地址，默认模型为 `qwen3.7-plus`。

### 集成点
- 后端通过 `AIConfigStore` 管理多模态大模型配置（base URL、model、API Key），默认内置模型 ID 为 `qwen3.7-plus`，显示名为“通义千问 Plus”。
- 前端 AI 配置页提供“前往千问模型页面”链接，引导用户申请多模态模型和 API Key。

### 稳定约束
- 要求支持 OpenAI 兼容的 `/chat/completions` 接口；多模态能力需要模型具备图片输入能力。
- 历史代理地址会被规范化为千问默认地址，迁移时注意保留旧 Key。