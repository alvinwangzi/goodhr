---
kind: external_dependency
name: Redis 缓存/会话
slug: redis
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/go.mod
    - goodhr5/cloud/backend/.env.example
    - goodhr5/docker-compose.server.yml
---

### Redis
- 角色：云端后端的缓存与状态中间件（如会话、限流、临时状态），由 Go 端通过 `go-redis` 客户端接入。
- 集成方式：地址由环境变量 `GOODHR_REDIS_ADDR` 注入；服务器部署时后端以 host 网络直连宿主机上的 Redis。
- 稳定约束：运行云端服务时必须提供可连接的 Redis 实例，否则后端无法启动或关键功能不可用。