---
kind: build_system
name: GoodHR 5 构建与部署体系（Docker Compose + Shell/PowerShell 脚本）
category: build_system
scope:
    - '**'
source_files:
    - goodhr5/docker-compose.yml
    - goodhr5/docker-compose.server.yml
    - goodhr5/docker-compose.local.yml
    - goodhr5/docker-compose.local.windows.yml
    - goodhr5/cloud/backend/Dockerfile
    - goodhr5/cloud/frontend-next/Dockerfile
    - goodhr5/cloud/backend/.air.toml
    - goodhr5/auto_deploy.sh
    - goodhr5/local-agent-go/scripts/build_go_binary.sh
    - goodhr5/local-agent-go/scripts/build_go_binary.ps1
    - goodhr5/local-agent-go/cmd/build-local-agent/main.go
    - goodhr5/local-agent-go/packaging/environments/prod.json
    - goodhr5/local-agent-go/packaging/GoodHRLocalAgentGo.iss
    - goodhr5/local-agent-go/build_windows_installer.bat
---

## 1. 使用的系统与工具

- **后端**：Go 1.24，通过 `go build` / `go run` 编译，使用 [air](https://github.com/cosmtrek/air)（`.air.toml`）做本地热重载。
- **前端**：Next.js 16 + React 19，依赖管理使用 npm（`package-lock.json`），同时存在 `pnpm-workspace.yaml` 和 `pnpm-lock.yaml`（可能为历史或子模块遗留）。
- **容器化**：每个服务独立 `Dockerfile`，编排统一由 `docker-compose.yml` / `docker-compose.server.yml` / `docker-compose.local.yml` / `docker-compose.local.windows.yml` 管理。
- **服务器自动部署**：仓库根 `auto_deploy.sh`，基于 Git diff 的增量构建 + Docker Compose 重建/重启。
- **本地 Agent（桌面端）**：Go 程序，通过自研构建入口 `cmd/build-local-agent` 注入配置并跨平台交叉编译；打包器使用 Inno Setup（`packaging/*.iss`）。
- **Windows 安装器**：`build_windows_installer.bat` + PowerShell 辅助脚本。

## 2. 关键文件

| 用途 | 路径 |
|---|---|
| 本地开发编排 | `goodhr5/docker-compose.yml` |
| 服务器生产编排 | `goodhr5/docker-compose.server.yml` |
| 仅数据库本地环境 | `goodhr5/docker-compose.local.yml` |
| Windows 本地编排 | `goodhr5/docker-compose.local.windows.yml` |
| 后端容器镜像 | `goodhr5/cloud/backend/Dockerfile` |
| 前端容器镜像 | `goodhr5/cloud/frontend-next/Dockerfile` |
| 后端热重载配置 | `goodhr5/cloud/backend/.air.toml` |
| 服务器自动部署脚本 | `goodhr5/auto_deploy.sh` |
| Go 二进制构建脚本（Linux/macOS） | `goodhr5/local-agent-go/scripts/build_go_binary.sh` |
| Go 二进制构建脚本（Windows） | `goodhr5/local-agent-go/scripts/build_go_binary.ps1` |
| 统一构建入口（Go） | `goodhr5/local-agent-go/cmd/build-local-agent/main.go` |
| 生产环境嵌入配置 | `goodhr5/local-agent-go/packaging/environments/prod.json` |
| Windows 安装包定义 | `goodhr5/local-agent-go/packaging/GoodHRLocalAgentGo.iss` |
| Windows 安装器构建 | `goodhr5/local-agent-go/build_windows_installer.bat` |

## 3. 架构与约定

### 3.1 云端服务（backend + frontend-next）

- **后端镜像**：基于 `golang:1.24-alpine`，设置 `GOPROXY=https://goproxy.cn,direct`，直接 `go run ./cmd/server` 运行（开发模式）。
- **前端镜像**：三阶段构建（dependencies → builder → runner），输出 Next.js standalone 产物，`node server.js` 启动，暴露 3000 端口。
- **Compose 编排**：
  - `docker-compose.yml`：本地开发，frontend 用 `Dockerfile.dev`，挂载源码目录，端口映射 5173:3000、8084:8084。
  - `docker-compose.server.yml`：生产部署，后端使用 `network_mode: host` 以直连宿主机上的 PostgreSQL/Redis。
  - `docker-compose.local.yml`：仅拉起 PostgreSQL 17.5-alpine，供前后端在宿主机直接运行。

### 3.2 服务器自动部署（`auto_deploy.sh`）

脚本实现“按变更范围只构建受影响服务”的策略：

1. 通过 `mkdir $LOCK_DIR` 实现互斥锁，防止并发部署。
2. 检查当前目录是 Git 仓库且无未提交改动。
3. `git fetch` 后比较本地与远端 commit，要求可快进合并（`merge-base --is-ancestor`）。
4. 对 `git diff --name-only` 中的文件分类：
   - 修改 `Dockerfile` / `.dockerignore` / `go.mod` / `go.sum` → 需要重新构建 backend 镜像。
   - 其他 backend 源码变化 → 仅需重启 backend（生产 compose 挂载源码）。
   - frontend-next 下任何变化 → 重建 frontend 镜像。
   - `docker-compose.*.yml` 变化 → 完整部署。
5. 若核心容器不存在则回退到完整 `docker compose build up -d`。
6. 可选清理缓存：`DEPLOY_PRUNE=1` 时执行 `docker image/builder/container prune`，默认保留 168h 内缓存。

环境变量约定：`DEPLOY_BRANCH`、`DEPLOY_REMOTE`、`DEPLOY_COMPOSE_FILE`、`DEPLOY_PRUNE`、`DEPLOY_PRUNE_AGE`。

### 3.3 本地 Agent 构建系统

- 所有构建最终调用 `cmd/build-local-agent`，它负责：
  - 校验 `-env` 必须为 `dev` 或 `prod`。
  - 校验版本号格式（`^[0-9A-Za-z._-]+$`）、目标 OS/Arch（`^[a-z0-9]+$`）。
  - 读取 `packaging/environments/<env>.json`，base64 编码后通过 `-X` ldflags 注入 `internal/config.EmbeddedBuildConfig` 与 `internal/version.Value`。
  - Windows 目标追加 `-H windowsgui` 隐藏控制台窗口。
  - 强制 `CGO_ENABLED=0`，输出命名形如 `hrplus-agent-<env>-<os>-<arch>[.exe]`，默认落 `dist/bin/<env>/`。
- `scripts/build_go_binary.sh` / `build_go_binary.ps1` 是对该入口的薄封装，分别支持 bash 与 PowerShell，并在调用前将 GOOS/GOARCH 设为宿主值。
- 发布包通过 Inno Setup（`GoodHRLocalAgentGo.iss`）打包，配套 PowerShell 脚本 `build_windows_installer.ps1`。

### 3.4 版本与构建环境

- 本地 Agent 默认版本号硬编码为 `0.1.1`（见 `build_go_binary.sh` 与 `build_go_binary.ps1`），可通过 `VERSION` 环境变量覆盖。
- 构建环境通过 `GOODHR_APP_ENV` 区分 `dev` / `prod`，对应不同的 `packaging/environments/*.json` 配置文件。
- 云端后端没有独立的 Makefile 或构建脚本，直接依赖 `go run` / `go build` 与 Docker 镜像构建。

## 4. 约定与约束

- **云端服务容器化**：每个服务提供独立 `Dockerfile`，并通过顶层 `docker-compose*.yml` 组合，禁止在仓库根新增未纳入 compose 的服务。
- **生产部署走 `auto_deploy.sh`**：脚本要求仓库处于干净工作区（`git status --porcelain` 为空），且远端分支必须是本地分支的可快进祖先，否则终止部署。
- **后端源码变更不触发镜像重建**：生产 compose 将 `./cloud/backend:/app` 挂载进容器，因此普通 Go 源码变化仅 `restart backend`；只有 `Dockerfile` / `.dockerignore` / `go.mod` / `go.sum` 变化才触发 `build backend`。
- **本地 Agent 构建必须显式指定环境**：`build_go_binary.sh` 与 `build_go_binary.ps1` 均拒绝非 `dev` / `prod` 的 `BUILD_ENV` / `Environment`，并以非零退出码终止。
- **本地 Agent 交叉编译禁用 CGO**：`cmd/build-local-agent/main.go` 强制 `CGO_ENABLED=0`，确保产物静态链接、可跨平台分发。
- **版本号与构建配置注入**：版本号与 `packaging/environments/*.json` 内容通过 ldflags 注入二进制，运行时不可篡改。
- **开发热重载**：后端开发使用 air，排除 `_test.go`、`tmp`、`vendor`，编译产物写入 `./tmp/main`。
- **数据库初始化**：后端使用 SQL migration 文件（`db/migrations/0001_initial_schema.sql` … `0083_rename_subscription_plan_names.sql`），迁移文件成对提供 `.down.sql` 用于回滚。