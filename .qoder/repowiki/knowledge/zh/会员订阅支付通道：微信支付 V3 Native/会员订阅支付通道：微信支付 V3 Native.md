---
kind: external_dependency
name: 会员订阅支付通道：微信支付 V3 Native
slug: wechatpay-v3
category: external_dependency
category_hints:
    - vendor_identity
    - auth_protocol
    - sdk_real_api
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/internal/httpapi/payment_wechat.go
    - goodhr5/cloud/backend/internal/httpapi/payment.go
---

### 身份与角色
- 会员订阅（免费版/Plus 包月/Pro 包年）的支付提供方，采用微信支付 V3 Native 下单 + 回调验签模式。
- SDK：`github.com/wechatpay-apiv3/wechatpay-go`。

### 集成点
- 商户配置保存在系统配置表 `system.payment_wechat`（键名 `system.payment_wechat`），由超级管理员在后台维护，运行时每次请求前从该表读取，配置变化自动重建客户端。
- 对外暴露统一支付接口，内部 Provider 名称为 `wechat`。

### 协议与参数
- 必需字段：app_id、mch_id、merchant_serial_no、private_key_base64、api_v3_key、public_key_id、public_key_base64、notify_url。
- 私钥/公钥支持 Base64 或 PEM 文本两种格式。
- 回调通过 RSA-SHA256 验签，交易类型限定为 NATIVE，币种限定为 CNY。
- 订单金额单位为分（cents），超时时间固定 30 分钟。

### 方向
- 新增其他支付方式需实现统一的 PaymentProvider 接口，并在系统配置中注册新 key。