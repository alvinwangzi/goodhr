---
kind: build_system
name: GoodHR5 构建与发布体系：Docker Compose + Shell/PowerShell 脚本 + Inno Setup
category: build_system
scope:
    - '**'
source_files:
    - goodhr5/docker-compose.yml
    - goodhr5/docker-compose.server.yml
    - goodhr5/auto_deploy.sh
    - goodhr5/cloud/backend/Dockerfile
    - goodhr5/cloud/backend/Dockerfile.dev
    - goodhr5/cloud/backend/.air.toml
    - goodhr5/cloud/frontend-next/Dockerfile
    - goodhr5/cloud/frontend-next/Dockerfile.dev
    - goodhr5/cloud/frontend-next/package.json
    - goodhr5/local-agent-go/scripts/build_go_binary.sh
    - goodhr5/local-agent-go/scripts/build_go_binary.ps1
    - goodhr5/local-agent-go/scripts/package_worker.sh
    - goodhr5/local-agent-go/packaging/GoodHRLocalAgentGo.iss
    - goodhr5/local-agent-go-new/scripts/build.sh
    - goodhr5/local-agent-go-new/scripts/package-release.sh
    - goodhr5/local-agent-go-new/scripts/package-windows.ps1
    - goodhr5/local-agent-go-new/packaging/GoodHRLocalAgent.iss
    - goodhr5/local-agent-go-new/worker/package.json
---

## 1. 整体方案

GoodHR5 是一个多语言、多组件的仓库，包含云端 Go 后端、Next.js 前端、Go+TypeScript 本地 Agent（两套实现 `local-agent-go` 与 `local-agent-go-new`）。构建与发布采用以下组合：

- **云端服务**：使用 Docker + Docker Compose 进行开发编排与服务器部署；生产通过根目录 `auto_deploy.sh` 脚本基于 Git diff 做增量构建与滚动更新。
- **本地 Agent**：使用 Go 交叉编译 + Node/TypeScript Worker 预编译产物打包，Windows 端用 Inno Setup 生成安装器，macOS/Linux 端输出 ZIP 发布包。
- **依赖源**：统一配置国内镜像加速 Go 模块 (`goproxy.cn`) 与 npm (`npmmirror.com`)，保证在受限网络环境下可稳定构建。

## 2. 关键文件与职责

### 云端后端 (cloud/backend)
- `Dockerfile`：基于 `golang:1.24-alpine`，仅 `go run ./cmd/server`，用于 CI/CD 或最小运行环境。
- `Dockerfile.dev`：安装 `air-verse/air@v1.62.0`，监听 `.go/.tpl/.html` 变更自动重启，配合 `.air.toml` 排除 `_test.go`。
- `.air.toml`：定义 dev 热重载规则，`include_ext = ["go", "tpl", "tmpl", "html"]`，`exclude_regex = ["_test.go"]`。
- `go.mod/go.sum`：Go 模块清单，由 Dockerfile 先 `COPY go.mod go.sum` 再 `go mod download` 以利用层缓存。

### 云端前端 (cloud/frontend-next)
- `Dockerfile`：三阶段构建（dependencies → builder → runner），最终产出 Next.js standalone 模式 (`node server.js`)，端口 3000。
- `Dockerfile.dev`：直接 `npm run dev -- --hostname 0.0.0.0 --port 3000`，挂载源码与 node_modules 卷。
- `package.json`：scripts 暴露 `dev`/`build`/`start`，Next.js 版本锁定为 `16.2.9`，React 19。

### 编排与部署
- `docker-compose.yml`：开发环境，backend 端口 8084，frontend 端口 5173，挂载源码目录并设置 `CLOUD_API_BASE`。
- `docker-compose.server.yml`：生产部署，backend 使用 `network_mode: host` 以便访问宿主机 PostgreSQL/Redis。
- `auto_deploy.sh`：**核心生产部署脚本**。逻辑包括：
  - 通过 `mkdir .deploy.lock` 实现互斥锁，防止并发部署。
  - 检查工作树干净、远端分支可快进合并后才执行。
  - 计算 `git diff --name-only`，按文件前缀分类到 `BACKEND_CHANGED` / `FRONTEND_CHANGED` / `FULL_DEPLOY_REQUIRED`。
  - 若 `Dockerfile`/`.dockerignore`/`go.mod`/`go.sum` 变化则触发 backend 重建；否则仅重启进程。
  - 支持 `DEPLOY_PRUNE=1` 清理过期镜像/容器/BuildKit 缓存。

### 本地 Agent v1 (`local-agent-go`)
- `scripts/build_go_binary.sh` / `build_go_binary.ps1`：交叉编译 Go 主程序，输出至 `dist/bin/goodhr-local-agent-{os}-{arch}.exe?`，Windows 使用 `-H windowsgui`，CGO 关闭。
- `scripts/package_worker.sh`：将 `worker-node` 的 `src/node_modules` 打包为 `goodhr-browser-worker-{platform}-{version}.zip`，同时输出 sha256。
- `packaging/GoodHRLocalAgentGo.iss`：Inno Setup 脚本，安装到 `{localappdata}\Programs\GoodHRLocalAgent`，升级时删除旧 `worker-node` 与 `runtime/browser-worker` 目录，安装后启动主程序。
- `scripts/install_local_worker_dev.sh` / `scripts/package_node_runtime.sh`：辅助脚本，用于开发环境与运行时打包。

### 本地 Agent v2 (`local-agent-go-new`)
- `scripts/build.sh`：macOS 专用，先 `cd worker && npm ci && npm run build` 编译严格 TypeScript Worker，再 `go build -o bin/goodhr-local-agent`。
- `scripts/package-release.sh`：macOS 发布流程，校验版本号格式，按 `uname -m` 判断 arm64/x64，构建 ZIP 包 `goodhr-local-agent-v{ver}-darwin-{arch}`，并通过 `ditto -c -k` 压缩。
- `scripts/package-windows.ps1`：Windows 完整发布流水线，依次执行：清理 → 编译 TypeScript Worker → 安装生产依赖 → 交叉编译 Go (`CGO_ENABLED=0, GOOS=windows, GOARCH=amd64`) → 生成 ZIP → 调用 Inno Setup 编译器 `ISCC.exe` 生成安装器。
- `packaging/GoodHRLocalAgent.iss`：新版 Inno Setup 配置，安装到 `{localappdata}\GoodHR\local-agent-new`，升级前删除旧 `worker` 目录，避免新旧 TypeScript 产物混用。
- `worker/package.json`：严格 TypeScript Worker 的依赖与脚本，`engines.node >= 22`，使用 `playwright-core`、`cloakbrowser`、`mmdb-lib`。

## 3. 架构与约定

| 维度 | 约定 |
|---|---|
| **镜像基础** | 后端固定 `golang:1.24-alpine`，前端固定 `node:22-alpine`，确保构建可重现。 |
| **依赖缓存** | Dockerfile 先 COPY `go.mod`/`go.sum`/`package.json` 再 install，利用 Docker 层缓存加速重复构建。 |
| **国内镜像** | Go 通过 `GOPROXY=https://goproxy.cn,direct`，npm 通过 `registry https://registry.npmmirror.com`，均作为环境变量注入。 |
| **二进制裁剪** | 发布构建统一使用 `-trimpath -s -w` 去除调试信息，Windows 额外加 `-H windowsgui` 隐藏控制台窗口。 |
| **版本注入** | 通过 `go build -ldflags="-X ...internal/version.Value=<version>"` 将版本号嵌入二进制，供运行时 `--version` 查询。 |
| **Worker 隔离** | 浏览器自动化逻辑剥离为独立 Node/TypeScript Worker，与 Go 主程序解耦，分别打包、分别升级。 |
| **增量部署** | `auto_deploy.sh` 根据 git diff 前缀决定只重建/重启哪个服务，减少停机时间。 |
| **幂等性** | 部署脚本开头检测未提交改动、不可快进合并时直接退出，避免覆盖本地工作区。 |

## 4. 约束与规则

- **生产部署必须走 `auto_deploy.sh`**：脚本强制要求当前目录是 Git 仓库、无未提交改动、远端分支可 fast-forward 合并，否则终止部署（见 `auto_deploy.sh` 第 47–73 行）。
- **后端源码变更不重建镜像**：生产 compose 挂载 `./cloud/backend:/app`，Go 源码变化仅触发 `docker compose restart backend`，因为 air 不在生产使用，但脚本仍会区分 `BACKEND_BUILD_REQUIRED` 与 `BACKEND_CHANGED`（见 `auto_deploy.sh` 第 153–157 行）。
- **Windows 安装器升级前必须杀掉旧进程**：Inno Setup `[Code]` 段在 `ssInstall` 步骤调用 `taskkill /IM goodhr-local-agent.exe /T /F`，防止文件被占用（两个版本的 `.iss` 均有此逻辑）。
- **Worker 升级必须删除旧目录**：两个 Inno Setup 均在 `[InstallDelete]` 中删除旧 `worker-node`/`worker` 目录，避免新旧 TypeScript 产物混用导致运行时错误。
- **版本号格式受校验**：`package-release.sh` 与 `package-windows.ps1` 都要求版本号匹配 `^[0-9A-Za-z._-]+$`，非法版本直接退出。
- **Node 版本约束**：前端与 Worker 均声明 `engines.node >= 22`，Dockerfile 也使用 `node:22-alpine`，禁止使用低于 22 的 Node 环境构建。
- **CGO 禁用**：所有发布构建显式设置 `CGO_ENABLED=0`，确保生成纯静态 Go 二进制，便于跨平台分发。

## 5. 缺失项说明

仓库中未发现 GitHub Actions / GitLab CI / Jenkins 等 CI 配置文件；构建与发布目前完全由本地 shell/PowerShell 脚本驱动，`auto_deploy.sh` 充当“类 CI”的服务器端自动部署入口。