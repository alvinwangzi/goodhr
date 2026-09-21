---
kind: dependency_management
name: 多语言模块依赖管理（Go modules + npm/pnpm + 镜像代理）
category: dependency_management
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/go.mod
    - goodhr5/local-agent-go/go.mod
    - goodhr5/local-agent-go/go.mod
    - goodhr5/cloud/frontend-next/package.json
    - goodhr5/cloud/frontend-next/.npmrc
    - goodhr5/cloud/frontend-next/pnpm-lock.yaml
    - goodhr5/local-agent-go/worker-node/package.json
    - goodhr5/local-agent-go/worker/package.json
    - goodhr5/local-agent-go/scripts/build.sh
    - goodhr5/local-agent-go/scripts/package-release.sh
    - goodhr5/cloud/backend/Dockerfile
    - goodhr5/cloud/backend/Dockerfile.dev
---

## 1. 使用的系统/工具

仓库采用**多语言、多子模块**的依赖管理方式：
- **Go 后端与本地 Agent**：使用 Go Modules（`go.mod` / `go.sum`），每个 Go 工程独立维护依赖。
- **前端 Next.js**：使用 npm 包管理器，通过 `package.json` + `package-lock.json` + `pnpm-workspace.yaml` + `pnpm-lock.yaml` 共同锁定版本；同时存在 `.npmrc` 指定 npm registry。
- **Node Worker（浏览器自动化）**：分别位于 `local-agent-go/worker-node/package.json` 和 `local-agent-go/worker/package.json`，各自独立声明 Playwright/CloakBrowser 等运行时依赖。
- **私有/国内镜像**：Go 侧通过 `GOPROXY=https://goproxy.cn,direct` 加速下载；npm 侧通过 `.npmrc` 将 registry 指向 `https://registry.npmmirror.com`。
- **无 vendor 目录**：未启用 `go mod vendor`，所有第三方库通过 Go Module Cache 拉取。

## 2. 关键文件

| 组件 | 关键文件 | 作用 |
|---|---|---|
| 云端后端 | `goodhr5/cloud/backend/go.mod` | 声明 `lib/pq`、`redis/go-redis/v9`、`wechatpay-apiv3/wechatpay-go` 等核心依赖 |
| 旧版本地 Agent | `goodhr5/local-agent-go/go.mod` | 仅依赖 `modernc.org/sqlite`、`google/uuid`，保持极简 |
| 新版本地 Agent | `goodhr5/local-agent-go/go.mod` | 同样仅依赖 `modernc.org/sqlite`，与旧版保持一致 |
| Next 前端 | `goodhr5/cloud/frontend-next/package.json` | 声明 React 19、Next 16、MUI 9、CodeMirror、WangEditor 等 |
| 前端缓存/镜像 | `goodhr5/cloud/frontend-next/.npmrc` | 设置 npmmirror registry 及本地缓存路径 |
| 旧版 Node Worker | `goodhr5/local-agent-go/worker-node/package.json` | 依赖 `cloakbrowser ^0.3.27`、`playwright-core ^1.53.0` |
| 新版 Node Worker | `goodhr5/local-agent-go/worker/package.json` | 依赖 `cloakbrowser 0.5.2`、`playwright-core 1.61.1`、`mmdb-lib 3.0.2`，并声明 `engines.node >= 22` |
| Go 构建脚本 | `goodhr5/local-agent-go/scripts/build.sh`、`package-release.sh` | 通过 `${GOPROXY:-https://goproxy.cn,direct}` 注入 GOPROXY |
| Dockerfile | `goodhr5/cloud/backend/Dockerfile`、`Dockerfile.dev` | `ENV GOPROXY=https://goproxy.cn,direct` 确保容器内构建走国内代理 |

## 3. 架构与约定

- **按子模块隔离依赖**：每个 Go module（backend、local-agent-go、local-agent-go）和每个 Node 工程（frontend-next、worker-node、worker）都有独立的 `go.mod` / `package.json`，互不共享，避免“幽灵依赖”跨工程传播。
- **Go 工程保持最小依赖面**：两个本地 Agent 的 `go.mod` 仅引入 SQLite 与 UUID，其余能力（浏览器控制、平台适配、AI 调用）通过进程间通信交给 Node Worker 完成，从而降低 Go 二进制体积与攻击面。
- **Node Worker 与主程序解耦**：Worker 通过 HTTP API 被 Go 主进程反向代理调用（见 `internal/app/server.go` 中的 `proxyWorkerPost`），因此 Worker 的依赖升级不影响主服务编译。
- **统一使用国内镜像**：Go 侧统一 `goproxy.cn`，npm 侧统一 `npmmirror.com`，保证在境内网络环境下可稳定拉取依赖。
- **Lockfile 双保险**：前端同时生成 `package-lock.json` 与 `pnpm-lock.yaml`，说明团队可能在 CI 中混用 `npm install` 与 `pnpm install`，但两者都用于锁定精确版本。

## 4. 约定与约束

- **Go 依赖必须通过 `go mod tidy` 同步**：仓库未使用 vendor，所有依赖由 `go.mod` + `go.sum` 共同描述，新增/删除依赖后需提交两份文件。
- **Go 构建必须设置 GOPROXY**：`scripts/build.sh`、`scripts/package-release.sh` 以及 `Dockerfile*` 均显式设置 `GOPROXY=https://goproxy.cn,direct`，本地开发或 CI 若覆盖该变量可能导致拉取失败。
- **npm 构建必须使用 npmmirror**：`.npmrc` 固定了 registry 为 `https://registry.npmmirror.com`，直接访问 `registry.npmjs.org` 会被忽略。
- **Node Worker 要求 Node ≥ 22**：新版 worker 通过 `engines.node >= 22` 声明最低版本，旧版 worker 未声明 engines，但两者均使用 ESM (`"type": "module"`)。
- **禁止随意升级主框架版本**：`frontend-next` 的 `next`、`react`、`react-dom`、`@mui/*` 均使用精确版本号（如 `next: 16.2.9`、`react: 19.2.0`、`@mui/material: 9.1.1`），而非语义化范围，表明升级需人工评估兼容性。
- **无私有 Go 模块**：所有 require 均来自 `github.com` 公共仓库，未发现 `GOPRIVATE` 或私有 GOPROXY 配置，也未发现自定义 `go.sum` 校验策略。