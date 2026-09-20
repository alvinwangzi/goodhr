---
kind: external_dependency
name: PostgreSQL 云端数据库
slug: postgresql
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

### PostgreSQL
- 角色：GoodHR 5 云端后端（Go）的持久化存储，保存账号、岗位、任务、配置等数据。
- 集成方式：通过 `lib/pq` 驱动连接，DSN 由环境变量 `GOODHR_PG_DSN` 注入；服务器部署使用 `docker-compose.server.yml`，后端容器以 host 网络直接访问宿主机上的 PostgreSQL。
- 稳定约束：生产环境要求外部 PostgreSQL 实例可用，本地开发需自行提供 PG 服务并设置对应 DSN。