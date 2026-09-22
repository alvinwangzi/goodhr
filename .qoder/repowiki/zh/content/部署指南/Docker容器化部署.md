# Docker容器化部署

<cite>
**本文引用的文件**
- [docker-compose.yml](file://goodhr5/docker-compose.yml)
- [docker-compose.local.yml](file://goodhr5/docker-compose.local.yml)
- [docker-compose.server.yml](file://goodhr5/docker-compose.server.yml)
- [docker-compose.local.windows.yml](file://goodhr5/docker-compose.local.windows.yml)
- [README_DOCKER.md](file://goodhr5/README_DOCKER.md)
- [Dockerfile（后端）](file://goodhr5/cloud/backend/Dockerfile)
- [Dockerfile.dev（后端开发）](file://goodhr5/cloud/backend/Dockerfile.dev)
- [Dockerfile（前端生产）](file://goodhr5/cloud/frontend-next/Dockerfile)
- [Dockerfile.dev（前端开发）](file://goodhr5/cloud/frontend-next/Dockerfile.dev)
</cite>

## 更新摘要
**所做更改**
- 更新了本地开发环境配置，现在只运行PostgreSQL容器，Go后端和Next.js前端在本地直接运行
- 优化了开发效率，减少了容器开销和资源消耗
- 更新了Windows本地开发配置，支持热重载和缓存优化
- 增强了健康检查和网络配置说明

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构总览](#架构总览)
5. [详细组件分析](#详细组件分析)
6. [依赖关系分析](#依赖关系分析)
7. [性能与可观测性](#性能与可观测性)
8. [故障排查指南](#故障排查指南)
9. [结论](#结论)
10. [附录：本地开发与测试环境搭建](#附录本地开发与测试环境搭建)

## 简介
本文件面向 GoodHR 5 的容器化部署，覆盖镜像构建、容器编排、服务间通信、环境变量、端口映射、数据卷挂载、网络与健康检查、日志收集方案，以及开发/生产环境的差异化配置。文档基于仓库中提供的 docker-compose 与 Dockerfile 进行说明，帮助开发者快速搭建本地与服务器环境。

**更新** 本地开发环境已优化为仅运行PostgreSQL容器，Go后端和Next.js前端直接在宿主机运行，显著提升开发效率和资源利用率。

## 项目结构
GoodHR 5 采用前后端分离的容器化编排：
- 后端：Go 服务，提供 REST API 与 WebSocket 能力
- 前端：Next.js 应用，生产模式使用 standalone 输出
- 数据库与缓存：PostgreSQL 与 Redis（可通过外部网络复用或自行启动）
- 多套 compose 文件：默认、本地开发、Windows 本地开发、Linux 服务器部署

```mermaid
graph TB
A["浏览器"] --> B["前端 Next.js<br/>本地:5173"]
B --> C["后端 Go API<br/>本地:8084"]
C --> D["PostgreSQL<br/>容器:25432"]
```

图示来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.yml:1-32](file://goodhr5/docker-compose.yml#L1-L32)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.yml:1-32](file://goodhr5/docker-compose.yml#L1-L32)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [README_DOCKER.md:1-46](file://goodhr5/README_DOCKER.md#L1-L46)

## 核心组件
- 后端服务（cloud/backend）
  - 生产镜像：基于 golang:1.24-alpine，直接运行 go run ./cmd/server
  - 开发镜像：安装 air 热重载，监听 .go/.html/.sql 变更自动重启
  - **更新** 本地开发时直接在宿主机运行，无需容器化
- 前端服务（cloud/frontend-next）
  - 生产镜像：Node 22 Alpine，npm ci 安装依赖，构建后以 standalone 模式运行
  - 开发镜像：Node 22 Alpine，启用 Next.js 开发服务器并暴露 3000 端口
  - **更新** 本地开发时直接在宿主机运行，享受更快的热更新体验
- 编排文件
  - docker-compose.yml：基础编排，包含后端与前端
  - docker-compose.local.yml：**已优化** 仅启动 PostgreSQL 数据库容器
  - docker-compose.local.windows.yml：Windows 本地开发，支持热重载和缓存优化
  - docker-compose.server.yml：Linux 服务器部署，后端使用 host 网络直连宿主机 PG/Redis

章节来源
- [Dockerfile（后端）:1-8](file://goodhr5/cloud/backend/Dockerfile#L1-L8)
- [Dockerfile.dev（后端开发）:1-14](file://goodhr5/cloud/backend/Dockerfile.dev#L1-L14)
- [Dockerfile（前端生产）:1-23](file://goodhr5/cloud/frontend-next/Dockerfile#L1-L23)
- [Dockerfile.dev（前端开发）:1-12](file://goodhr5/cloud/frontend-next/Dockerfile.dev#L1-L12)
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

## 架构总览
下图展示典型本地/服务器部署的服务拓扑与通信路径：

```mermaid
graph TB
subgraph "本地开发环境"
FE["前端 Next.js<br/>本地:5173 (直接运行)"]
BE["后端 Go API<br/>本地:8084 (直接运行)"]
PG["PostgreSQL<br/>容器:25432"]
end
subgraph "服务器部署环境"
FE2["前端 Next.js<br/>端口 5173:3000"]
BE2["后端 Go API<br/>host 网络"]
PG2["PostgreSQL<br/>data-services 网络"]
RD["Redis<br/>data-services 网络"]
end
FE --> |"HTTP 请求"| BE
BE --> |"连接字符串 DSN"| PG
FE2 --> |"HTTP 请求"| BE2
BE2 --> |"连接字符串 DSN"| PG2
BE2 --> |"TCP 地址"| RD
```

图示来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

## 详细组件分析

### 后端服务（Go）
- 镜像构建
  - 生产：golang:1.24-alpine，设置 GOPROXY，复制 go.mod/go.sum 并下载依赖，再复制源码，运行 cmd/server
  - 开发：安装 air 热重载工具，通过 .air.local.toml 监听代码变化自动重启
- **更新** 本地开发模式
  - 直接在宿主机运行 `go run ./cmd/server`，无需容器化
  - 利用本地开发工具链获得更好的调试体验
  - 支持热重载，修改 .go 文件后自动重启服务
- 运行时
  - 暴露 8084 端口
  - 通过环境变量配置数据库与缓存连接信息（DSN、Redis 地址等）
  - 支持挂载 /app/tmp 用于临时文件存储

```mermaid
flowchart TD
Start(["本地开发启动"]) --> DirectRun["直接运行<br/>go run ./cmd/server"]
DirectRun --> AirWatch["Air 监听代码变更"]
AirWatch --> AutoRestart["自动重启服务"]
AutoRestart --> Ready["服务就绪"]
```

图示来源
- [Dockerfile.dev（后端开发）:1-14](file://goodhr5/cloud/backend/Dockerfile.dev#L1-L14)
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)

章节来源
- [Dockerfile（后端）:1-8](file://goodhr5/cloud/backend/Dockerfile#L1-L8)
- [Dockerfile.dev（后端开发）:1-14](file://goodhr5/cloud/backend/Dockerfile.dev#L1-L14)
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

### 前端服务（Next.js）
- 镜像构建
  - 生产：Node 22 Alpine，使用国内 npm 镜像安装依赖，构建产物为 standalone 模式，运行 server.js
  - 开发：Node 22 Alpine，启用 Next.js 开发服务器，监听 0.0.0.0:3000
- **更新** 本地开发模式
  - 直接在宿主机运行 `npm run dev`，获得更快的热更新体验
  - 支持文件系统监听，修改前端代码即时刷新
  - 使用 WATCHPACK_POLLING 解决 Windows 下的文件监听问题
- 运行时
  - 暴露 3000 端口，compose 映射到宿主 5173
  - 通过环境变量指定后端 API 基址与站点 URL

```mermaid
sequenceDiagram
participant U as "用户浏览器"
participant FE as "前端 Next.js<br/>本地 : 5173"
participant BE as "后端 Go API<br/>本地 : 8084"
U->>FE : 访问 http : //localhost : 5173
FE->>BE : 调用 http : //localhost : 8084
BE-->>FE : 返回 JSON/页面数据
FE-->>U : 渲染页面/交互
```

图示来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [Dockerfile.dev（前端开发）:1-12](file://goodhr5/cloud/frontend-next/Dockerfile.dev#L1-L12)

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [Dockerfile（前端生产）:1-23](file://goodhr5/cloud/frontend-next/Dockerfile#L1-L23)
- [Dockerfile.dev（前端开发）:1-12](file://goodhr5/cloud/frontend-next/Dockerfile.dev#L1-L12)

### 数据与缓存（PostgreSQL 与 Redis）
- **更新** 本地开发：仅运行 PostgreSQL 容器，端口 25432 映射到容器 5432
- 服务器部署：后端使用 host 网络，直接访问宿主机暴露的 PG/Redis
- 环境变量：
  - GOODHR_PG_DSN：PostgreSQL 连接串（含用户名、密码、主机、库名、SSL 模式）
  - GOODHR_REDIS_ADDR：Redis 地址（IP:端口）
- **新增** 健康检查：PostgreSQL 容器内置 pg_isready 健康检查，确保数据库可用性
- 首次启动：根据 README 提示，首次构建约需数分钟，后续秒级启动

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)
- [README_DOCKER.md:1-46](file://goodhr5/README_DOCKER.md#L1-L46)

### 端口映射与服务发现
- **更新** 本地开发：
  - 前端：本地 5173 端口（直接运行）
  - 后端：本地 8084 端口（直接运行）
  - 数据库：容器 25432 -> 内部 5432
- 服务器部署：
  - 前端：容器内 3000 -> 宿主 5173
  - 后端：host 网络直连
- 服务发现：
  - 同一 compose 网络下，前端通过服务名 backend 访问后端
  - 本地/Windows 开发通过 external 网络 data-services 访问 PG/Redis
  - 服务器部署使用 host 网络直连宿主机服务

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

### 数据卷与文件存储
- **更新** 本地开发：
  - 后端：直接在宿主机运行，无需卷挂载
  - 前端：直接在宿主机运行，享受原生文件系统性能
  - 数据库：postgres_data 卷持久化 PostgreSQL 数据
- 服务器部署：
  - 后端：挂载 ./cloud/backend:/app 实现源码热更新
  - 前端：命名卷保存 node_modules 与 .next 缓存加速构建
- 注意：当前 compose 未定义持久化的业务数据卷；如需持久化，请按需添加 PG/Redis 的数据卷映射

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

### 环境变量与配置项
- **更新** 本地开发环境：
  - 前端：NEXT_PUBLIC_CLOUD_API_BASE="http://localhost:8084"
  - 后端：GOODHR_PG_DSN="postgres://goodhr5_dev:goodhr5_dev@localhost:25432/goodhr5_dev?sslmode=disable"
- 服务器部署：
  - 前端：CLOUD_API_BASE="http://backend:8084"
  - 后端：GOODHR_PG_DSN 指向 data-services 网络中的 PG
- 建议：将敏感信息放入 .env 并通过 env_file 引用

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)
- [README_DOCKER.md:1-46](file://goodhr5/README_DOCKER.md#L1-L46)

### 网络配置
- **更新** 本地开发：
  - 前端和后端直接在宿主机运行，通过 localhost 通信
  - PostgreSQL 容器加入 data-services 外部网络
  - 端口映射：25432:5432
- 服务器部署：
  - 默认网络：frontend 与 backend 在同一 compose 网络中，通过服务名互通
  - data-services：外部网络，复用已有 PG/Redis 容器
  - host 网络：服务器部署时后端直接使用宿主机网络，便于访问本机 PG/Redis

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

### 健康检查与自恢复
- **更新** PostgreSQL 容器：
  - 内置 pg_isready 健康检查，每 5 秒检查一次
  - 超时时间 5 秒，重试 5 次
  - 自动重启策略：restart: unless-stopped
- 其他服务：
  - 本地开发的前端和后端直接运行，无需健康检查
  - 服务器部署的后端和前端可通过 HTTP 接口进行健康检查
  - 建议结合 restart: unless-stopped 提升可用性

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)

### 日志收集方案
- **更新** 本地开发环境：
  - 后端：air 控制台输出，实时查看日志
  - 前端：Next.js 开发服务器控制台输出
  - 数据库：PostgreSQL 容器标准输出
- 生产环境：
  - 建议使用 Docker 日志驱动（如 json-file、journald、fluentd、loki）集中收集
  - 可在 compose 中为各服务配置 logging 驱动与选项
  - 结合系统日志采集器（如 Filebeat/Fluent Bit）统一汇聚

## 依赖关系分析
- **更新** 本地开发：
  - 前端直接调用后端 API（http://localhost:8084）
  - 后端直接连接 PostgreSQL 容器（localhost:25432）
- 服务器部署：
  - 前端依赖后端 API（CLOUD_API_BASE）
  - 后端依赖 PostgreSQL（GOODHR_PG_DSN）与 Redis（GOODHR_REDIS_ADDR）
  - 本地/Windows 开发通过 external 网络 data-services 复用 PG/Redis
  - 服务器部署后端使用 host 网络直连宿主机 PG/Redis

```mermaid
graph LR
FE["前端 Next.js<br/>本地:5173"] --> BE["后端 Go API<br/>本地:8084"]
BE --> PG["PostgreSQL<br/>容器:25432"]
```

图示来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)

章节来源
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

## 性能与可观测性
- **更新** 构建优化
  - 前端：使用 npm ci 与分层缓存，standalone 输出减少体积
  - 后端：先下载依赖再复制源码，利用 Go 模块缓存
  - **新特性** 本地开发直接运行，避免容器化开销
- 开发体验
  - 后端：air 热重载，修改 .go 文件自动重启
  - 前端：Next.js 开发服务器 + WATCHPACK_POLLING（Windows）
  - **改进** 本地开发获得更快的热更新速度和更好的调试体验
- 可观测性
  - 日志：按"日志收集方案"一节配置
  - 指标：可在后端引入 Prometheus 指标端点，配合采集器上报
  - 追踪：可按需接入 OpenTelemetry 链路追踪

## 故障排查指南
- **更新** 本地开发常见问题：
  - 端口冲突：确保 5173、8084、25432 端口未被占用
  - 数据库连接：检查 GOODHR_PG_DSN 是否正确指向 localhost:25432
  - 热更新无效：确认使用了正确的开发命令和配置文件
- 无法连接数据库
  - 检查 GOODHR_PG_DSN 是否正确（主机、端口、库名、认证）
  - 确认 data-services 网络可达或 host 网络已正确配置
- 无法连接 Redis
  - 检查 GOODHR_REDIS_ADDR 是否指向可用实例
- 前端无法访问后端
  - 核对 CLOUD_API_BASE/NEXT_PUBLIC_CLOUD_API_BASE 与实际端口
  - 确认端口映射 5173:3000 与 8084:8084 生效
- 热更新无效（Windows）
  - 确保设置了 WATCHPACK_POLLING=true
  - 确认 volumes 挂载路径正确
- 首次构建慢
  - 参考 README 提示，首次构建需要时间，后续会显著加快

章节来源
- [README_DOCKER.md:1-46](file://goodhr5/README_DOCKER.md#L1-L46)
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)

## 结论
GoodHR 5 提供了完善的容器化方案：通过多份 compose 文件适配本地与服务器部署，前后端均具备开发/生产两套镜像，支持热更新与缓存加速。**最新更新** 本地开发环境已优化为仅运行必要的数据库容器，Go 后端和 Next.js 前端直接在宿主机运行，显著提升开发效率和资源利用率。借助 external 网络与 host 网络，灵活对接现有数据库与缓存。建议在生产环境补充健康检查与日志收集策略，进一步提升稳定性与可观测性。

## 附录：本地开发与测试环境搭建
- **更新** 前置条件
  - 安装 Docker（仅用于运行 PostgreSQL 容器）
  - 安装 Go 开发环境（用于本地运行后端）
  - 安装 Node.js 开发环境（用于本地运行前端）
  - 准备 data-services 网络（包含 PostgreSQL），或在服务器上直连宿主机 PG/Redis
- **更新** 启动步骤
  - 进入 goodhr5 目录
  - 启动 PostgreSQL 容器：`docker compose -f docker-compose.local.yml up -d`
  - 本地运行后端：`cd cloud/backend && go run ./cmd/server`
  - 本地运行前端：`cd cloud/frontend-next && npm run dev`
  - 访问前端 http://localhost:5173，后端 http://localhost:8084
- **更新** 停止与清理
  - 停止数据库容器：`docker compose -f docker-compose.local.yml down`
  - 停止本地服务：Ctrl+C 或直接关闭终端
  - 删除数据库数据：`docker compose -f docker-compose.local.yml down -v`
- **更新** 开发注意事项
  - Windows：使用 docker-compose.local.windows.yml 启动数据库，本地运行前后端
  - Linux/macOS：使用 docker-compose.local.yml 启动数据库，本地运行前后端
  - 服务器部署：使用 docker-compose.server.yml，后端 host 网络直连 PG/Redis
  - 热重载：后端使用 air，前端使用 Next.js 开发服务器
  - 缓存优化：Windows 版本支持 Go 模块缓存和构建缓存

章节来源
- [README_DOCKER.md:1-46](file://goodhr5/README_DOCKER.md#L1-L46)
- [docker-compose.local.yml:1-32](file://goodhr5/docker-compose.local.yml#L1-L32)
- [docker-compose.local.windows.yml:1-58](file://goodhr5/docker-compose.local.windows.yml#L1-L58)
- [docker-compose.server.yml:1-12](file://goodhr5/docker-compose.server.yml#L1-L12)