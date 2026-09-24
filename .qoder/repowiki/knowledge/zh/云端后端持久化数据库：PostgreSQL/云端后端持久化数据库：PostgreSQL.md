---
kind: external_dependency
name: 云端后端持久化数据库：PostgreSQL
slug: postgresql
category: external_dependency
category_hints:
    - vendor_identity
    - client_constraint
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/.env.example
    - goodhr5/cloud/backend/internal/httpapi/config.go
    - goodhr5/cloud/backend/db/migrations
---

### 身份与角色
- 云端后端（Go）的持久化数据库，通过 `lib/pq` 驱动连接。
- 开发环境默认通过 Docker 启动，端口映射到宿主机的 25432；生产环境需自行部署 PostgreSQL 并配置 DSN。

### 集成点
- 环境变量：`GOODHR_PG_DSN`（`.env.example` / `.env`）。
- 启动时调用 `RunMigrations` 自动执行 `db/migrations/*.sql`。
- 未配置 DSN 时回退为内存实现（仅本地调试用）。

### 稳定约束
- 必须使用支持 `sslmode=disable` 的 PostgreSQL 实例；DSN 格式遵循 lib/pq 约定。
- 迁移文件按数字前缀顺序执行，新增表/字段需追加新的 migration 文件。