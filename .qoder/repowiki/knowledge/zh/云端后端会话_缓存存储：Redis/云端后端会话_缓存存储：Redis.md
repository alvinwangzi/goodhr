---
kind: external_dependency
name: 云端后端会话/缓存存储：Redis
slug: redis
category: external_dependency
category_hints:
    - vendor_identity
    - client_constraint
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/.env.example
    - goodhr5/cloud/backend/internal/httpapi/config.go
---

### 身份与角色
- 云端后端的可选依赖，用于认证会话、AI 配置等共享状态。
- 通过 `github.com/redis/go-redis/v9` 接入。

### 集成点
- 环境变量：`GOODHR_REDIS_ADDR`、`GOODHR_REDIS_PASSWORD`、`GOODHR_REDIS_DB`。
- 未配置 Redis 时回退为内存实现（AuthStore、AgentStore、UserFlowStore 等均如此）。

### 稳定约束
- 生产环境应提供可连接的 Redis 实例；地址/密码/DB 三件套需同时配置才能启用。