---
kind: external_dependency
name: 微信支付 API
slug: wechat-pay
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/go.mod
    - goodhr5/cloud/backend/.env.example
---

### 微信支付
- 角色：GoodHR 5 云端支付能力（用户订阅、AI 钱包扣费等）的第三方支付通道。
- 集成方式：后端通过 `wechatpay-apiv3/wechatpay-go` SDK 接入；支付相关参数不再通过环境变量配置，改为在后台「系统配置」页面的「微信支付配置」中在线维护。
- 稳定约束：上线前需在微信商户平台完成配置并通过 SDK 签名流程；密钥等敏感信息应走系统配置而非代码或仓库文件。