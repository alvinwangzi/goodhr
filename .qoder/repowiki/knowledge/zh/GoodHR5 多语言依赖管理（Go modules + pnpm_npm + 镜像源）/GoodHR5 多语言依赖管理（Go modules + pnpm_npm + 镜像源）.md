---
kind: dependency_management
name: HRPlus5 多语言依赖管理（Go modules + pnpm/npm + 镜像源）
category: dependency_management
scope:
    - '**'
source_files:
    - goodhr5/cloud/backend/go.mod
    - goodhr5/cloud/backend/go.sum
    - goodhr5/local-agent-go/go.mod
    - goodhr5/local-agent-go/go.sum
    - goodhr5/cloud/frontend-next/package.json
    - goodhr5/cloud/frontend-next/pnpm-lock.yaml
    - goodhr5/cloud/frontend-next/pnpm-workspace.yaml
    - goodhr5/cloud/frontend-next/.npmrc
    - goodhr5/local-agent-go/worker-node/package.json
    - goodhr5/local-agent-go/worker-node/package-lock.json
---

## 2. 关键文件与包

| 子模块 | 依赖声明文件 | 主要第三方依赖 |
|---|---|---|
| cloud/backend | `go.mod` | gorilla/websocket、lib/pq、redis/go-redis/v9、wechatpay-apiv3/wechatpay-go、golang.org/x/crypto |
| local-agent-go | `go.mod` | google/uuid、modernc.org/sqlite（含 modernc.org/libc/mathutil/memory 等间接依赖） |
| cloud/frontend-next | `package.json` + `pnpm-lock.yaml` | next、react/react-dom、@mui/*、@emotion/*、@codemirror、@wangeditor、qrcode.react |
| worker-node | `package.json` + `package-lock.json` | cloakbrowser、playwright-core |

## 3. 架构与约定

### Go 模块
- 每个 Go 子项目独立 `module` 路径：`goodhr5/cloud/backend` 与 `goodhr5/local-agent-go`，互不引用，各自维护自己的 `go.mod` / `go.sum`。
- 两个模块的 Go 主版本不同（backend 用 1.24，local-agent-go 用 1.25.0），说明它们不是共享同一 workspace。
- 未发现 `replace` 指令、GOPRIVATE、GONOSUMDB 或自定义 proxy 配置；依赖全部来自公共 Go Proxy。

### Node.js 依赖
- 前端 Next.js 项目同时保留 `pnpm-lock.yaml` 与 `package-lock.json`，且 `.npmrc` 将 npm registry 指向国内镜像 `https://registry.npmmirror.com`，缓存目录硬编码为 `/Users/Zhuanz/Downloads/goodHR/goodhr5/cloud/frontend-next/.npm-cache`。
- `pnpm-workspace.yaml` 仅包含 `allowBuilds` 中对 `es5-ext` 和 `sharp` 的构建开关，未见 `packages:` 字段，因此当前并非真正的 monorepo workspace，只是启用 pnpm 的工作区能力。
- worker-node 是独立 npm 包，`package-lock.json` 中所有 resolved URL 同样走 `registry.npmmirror.com`。

### 锁定策略
- Go：通过 `go.sum` 校验依赖完整性。
- Node：pnpm 使用 `pnpm-lock.yaml`，npm 使用 `package-lock.json`，两者并存于前端项目。

## 4. 观察到的约定与约束

- **按子模块隔离依赖**：Go 后端与本地 Agent 各自拥有独立 `go.mod`，Node 前端与 worker-node 各自拥有独立 `package.json`，不存在跨子模块共享依赖。
- **Go 依赖版本精确到次版本**：backend 的依赖如 `gorilla/websocket v1.5.3`、`lib/pq v1.12.3`、`redis/go-redis/v9 v9.19.0` 均为固定版本号（非 `^`/`~` 范围）。
- **Node 依赖使用语义化版本范围**：frontend-next 的依赖普遍使用 `^` 前缀（如 `next: "16.2.9"` 除外，该依赖为精确版本；`react: "19.2.0"` 也为精确版本），devDependencies 则广泛使用 `^`。
- **npm 镜像源统一配置在 `.npmrc`**：`registry=https://registry.npmmirror.com`，并关闭 `fund` 与 `audit`。
- **无 vendoring、无私有 registry**：仓库中未发现 `vendor/` 目录、GOPRIVATE 环境变量或私有 Go module proxy 配置。
- **Go 主版本差异**：backend 与 local-agent-go 分别声明 `go 1.24` 与 `go 1.25.0`，二者不共享 Go workspace。