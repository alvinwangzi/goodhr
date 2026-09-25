# HRPlus｜AI 招聘自动化助手

HRPlus 是面向企业 HR、猎头顾问和招聘团队的招聘自动化工具。它由云端管理后台和 Windows 本地程序组成：你在云端配置岗位、平台账号和任务，本地程序负责操作招聘网站、读取候选人并执行筛选与沟通。

官网：[https://www.xx.com/](https://www.xx.com/)

> 当前主要代码位于 `goodhr5/`。仓库根目录下的早期浏览器扩展代码属于历史版本，不再代表 HRPlus 的产品形态。

## HRPlus 能做什么

- 统一管理招聘平台账号、岗位和任务。
- 使用关键词或 AI 分析候选人与岗位的匹配程度。
- 并发完成候选人基础预评分，减少等待时间。
- 达到设定分数后读取候选人详情并打招呼，未达到阈值则自动跳过。
- 自动去重候选人，降低重复查看和重复沟通的概率。
- 保存任务进度、候选人处理结果和运行日志，方便复盘与排查。
- 按招聘平台分别实现岗位切换、候选人读取、翻页和沟通流程。

## 当前平台适配

### Boss 直聘

- 切换任务对应岗位。
- 提取候选人列表并去重。
- 支持关键词筛选和 AI 预评分。
- 按阈值打开候选人详情并执行打招呼。
- 处理候选人可见区域、列表滚动和详情关闭。

### 猎聘猎头端

- 根据岗位配置的猎聘搜索关键词查找候选人。
- 从“正在发布的职位”中选择任务岗位。
- 自动启用“隐藏已查看、隐藏已沟通、隐藏已获取联系方式”。
- 支持列表翻页以及新页面形式的候选人详情。
- 通过 DOM 读取候选人信息并执行后续判断。

智联招聘、猎聘企业端等平台采用相同的平台接口组织代码，相关能力会持续完善。

## 一次任务如何运行

1. 在云端创建招聘平台账号并完成平台登录。
2. 在岗位管理中选择平台，填写岗位名称和筛选要求。
3. AI 筛选模式可以使用“AI 分析岗位”整理岗位要求；猎聘猎头端还会生成候选人搜索关键词。
4. 创建任务并点击开始。
5. 本地程序打开对应招聘页面，确认岗位和筛选条件。
6. 程序提取并去重候选人，同时进行第一轮关键词或 AI 分析。
7. 通过阈值的候选人继续读取详情并进行最终判断，符合要求后才会打招呼。
8. 任务可以随时停止；停止时会尽量关闭已打开的候选人详情。

## 产品架构

```text
goodhr5/
├─ cloud/
│  ├─ backend/          Go 云端 API、数据与任务配置
│  └─ frontend-next/    Next.js 云端管理后台
├─ local-agent-go/      Windows 本地程序与任务主流程
│  ├─ internal/platforms/  各招聘平台实现
│  └─ worker-node/      CloakBrowser 浏览器原子操作
└─ docs/                架构、流程和开发说明
```

各部分的职责保持清晰：

- 云端后端负责账号、岗位、任务和配置数据。
- 云端前端负责配置与查看任务。
- 本地 Go 程序负责任务主流程和平台调度。
- 各平台目录负责实现自身页面流程。
- Node Worker 只提供点击、输入、滚动、截图、DOM 提取等浏览器基础操作和组合操作。

## 隐私与数据

候选人详情、招聘平台登录状态、截图、OCR 结果和本地任务数据优先保存在用户电脑中。云端主要保存账号、岗位配置、任务摘要和本地程序状态，减少敏感招聘数据在不同环境之间流转。

## 开发环境上手

### 前置依赖

- **Docker**：用于启动 PostgreSQL 数据库
- **Go 1.25+**：云端后端和本地程序（`local-agent-go/go.mod` 要求 1.25.0；本机装 1.24 时构建会自动下载 1.25 工具链）
- **Node.js 18+**：云端前端（Next.js）和本地 Worker

### 一键启动

```powershell
git clone https://github.com/alvinwangzi/goodhr.git
cd goodhr
git checkout dev
cd goodhr5
.\scripts\dev.ps1
```

脚本会自动完成以下步骤：

1. 用 `docker-compose.local.yml` 启动 PostgreSQL（端口 25432）
2. 检查 `cloud/backend/.env` 是否存在，不存在则自动生成默认开发配置
3. 启动后端 Go 服务（端口 8084）
4. 检查前端 `node_modules` 是否存在，不存在则自动 `npm install`
5. 启动 Next.js 前端（端口 3000）

启动完成后访问：

- 前端：http://localhost:3000
- 后端：http://localhost:8084
- 数据库：localhost:25432

按 `Ctrl+C` 停止所有服务并清理 Docker 容器。

### 国内代理配置（推荐）

国内网络拉取依赖可能很慢或失败，建议在首次启动前配置以下代理：

```powershell
# Go 模块代理
go env -w GOPROXY=https://goproxy.cn,direct

# npm 镜像源
npm config set registry https://registry.npmmirror.com

# Docker 镜像加速（编辑 Docker Desktop 设置里的 daemon.json，或手动写入）
# 在 %USERPROFILE%\.docker\daemon.json 的顶层 JSON 对象里加入：
# "registry-mirrors": ["https://docker.1ms.run", "https://docker.xuanyuan.me"]
# 保存后重启 Docker Desktop
```

### 环境变量说明

后端环境变量保存在 `cloud/backend/.env`（已被 gitignore 忽略），脚本首次启动时会自动生成。如需自定义配置，可参考 `cloud/backend/.env.example` 模板。

前端开发时脚本已硬编码注入 `NEXT_PUBLIC_CLOUD_API_BASE` 和 `NEXT_PUBLIC_SITE_URL`，通常不需要手动配置 `.env`。

### 本地程序打包

本地程序位于 `goodhr5/local-agent-go/`，开发环境可以直接运行，也可以打包成 Windows 安装程序。

**直接运行（开发调试）：**

```powershell
cd goodhr5/local-agent-go
go run ./cmd/goodhr-local-agent --open-console=false
```

启动后监听 `http://127.0.0.1:55271`。

**打包 Windows 安装程序：**

前置条件：

1. 安装 [Inno Setup 6](https://jrsoftware.org/isdl.php)（`choco install innosetup --yes`）
2. 安装 Worker 依赖：`cd goodhr5/local-agent-go/worker-node && npm install`

一键打包：

```powershell
cd goodhr5/local-agent-go
.\packaging\build_windows_installer.ps1 -Version "0.1.1" -Environment dev
```

参数说明：

- `-Environment dev`：开发环境，连接 `localhost:8084` 后端和 `localhost:3000` 前端
- `-Environment prod`：生产环境，连接线上地址
- `-Version`：安装包版本号，需要和当前本地程序版本一致

产物位于 `goodhr5/local-agent-go/dist/installers/dev/HRPlusSetup-dev-{version}.exe`。

## 部署与使用

### 云端服务

云端包含 Go 后端、Next.js 前端和 PostgreSQL，可通过 `goodhr5/docker-compose.server.yml` 部署。详细说明请查看 [HRPlus Docker 部署文档](goodhr5/README_DOCKER.md)。

### Windows 本地程序

本地程序位于 `goodhr5/local-agent-go/`，正式使用时安装与云端版本匹配的 Windows 安装包。开发环境的启动、测试和打包方式请查看 [本地程序说明](goodhr5/local-agent-go/README.md)。

## 使用提醒

- 招聘平台页面会不定期更新，页面结构变化后可能需要同步更新平台适配。
- 自动化操作应合理设置速度和任务数量，并遵守对应招聘平台的规则。
- AI 判断用于辅助筛选，重要招聘决定仍建议由招聘人员复核。
- 使用前请确认本地程序、云端和运行组件版本一致。

## 相关文档

- [HRPlus 项目说明](goodhr5/README.md)
- [系统架构](goodhr5/docs/architecture.md)
- [业务流程](goodhr5/docs/goodhr5-flow.md)
- [本地程序重构说明](goodhr5/local-agent-go/docs/REFACTOR_PLAN.md)

---

**HRPlus**：把重复操作交给程序，把判断时间留给真正值得聊的候选人。

## 🔍 HR搜索关键词

以下是企业HR在寻找招聘插件时可能搜索的50个关键词：

1. Boss直聘自动打招呼插件
2. 猎聘自动打招呼插件
3. 招聘自动打招呼工具
4. Boss直聘批量打招呼插件
5. 猎聘批量打招呼工具
6. 招聘网站自动打招呼插件
7. HR自动打招呼助手
8. 招聘自动化打招呼插件
9. Boss直聘简历下载插件
10. 猎聘简历下载插件
11. 招聘简历批量下载工具
12. Boss直聘助手插件
13. 猎聘助手插件
14. 招聘网站助手插件
15. HR招聘助手插件
16. 招聘自动化工具插件
17. Boss直聘自动沟通插件
18. 猎聘自动沟通工具
19. 招聘自动沟通插件
20. HR招聘自动化插件
21. 招聘流程自动化插件
22. Boss直聘关键词筛选插件
23. 猎聘关键词筛选工具
24. 招聘关键词筛选插件
25. HR招聘筛选插件
26. 招聘候选人筛选插件
27. Boss直聘自动打招呼软件
28. 猎聘自动打招呼软件
29. 招聘自动打招呼软件
30. HR招聘自动化软件
31. Boss直聘招聘插件
32. 猎聘招聘插件
33. 招聘网站插件
34. HR招聘工具插件
35. 招聘助手浏览器扩展
36. Boss直聘辅助插件
37. 猎聘辅助插件
38. 招聘辅助工具插件
39. HR招聘辅助插件
40. 招聘效率提升插件
41. Boss直聘效率插件
42. 猎聘效率插件
43. 招聘效率工具插件
44. HR招聘效率插件
45. 招聘自动化浏览器扩展
46. Boss直聘浏览器插件
47. 猎聘浏览器插件
48. 招聘浏览器插件
49. HR招聘浏览器扩展
50. 招聘插件免费下载

## 🎯 猎头搜索关键词

以下是猎头顾问在寻找招聘插件时可能搜索的50个关键词：

1. 猎头自动打招呼插件
2. 猎聘猎头自动打招呼插件
3. Boss直聘猎头插件
4. 猎头批量打招呼插件
5. 猎头找人插件
6. 猎头挖人插件
7. 猎头助手插件
8. 猎头招聘插件
9. 猎头浏览器扩展
10. 猎头自动沟通插件
11. 猎聘猎头插件
12. 猎头简历下载插件
13. 猎头批量下载插件
14. 猎头候选人筛选插件
15. 猎头关键词筛选插件
16. 猎头自动化插件
17. 猎头招聘工具插件
18. 猎头效率插件
19. 猎头自动化打招呼工具
20. 猎头批量打招呼工具
21. 猎头找人工具
22. 猎头挖人工具
23. 猎头助手工具
24. 猎头招聘工具
25. 猎头浏览器工具
26. 猎头自动沟通工具
27. 猎聘猎头工具
28. 猎头简历下载工具
29. 猎头批量下载工具
30. 猎头候选人筛选工具
31. 猎头关键词筛选工具
32. 猎头自动化工具
33. 猎头招聘工具
34. 猎头效率工具
35. 猎头自动打招呼软件
36. 猎头批量打招呼软件
37. 猎头找人软件
38. 猎头挖人软件
39. 猎头助手软件
40. 猎头招聘软件
41. 猎头浏览器软件
42. 猎头自动沟通软件
43. 猎聘猎头软件
44. 猎头简历下载软件
45. 猎头批量下载软件
46. 猎头候选人筛选软件
47. 猎头关键词筛选软件
48. 猎头自动化软件
49. 猎头招聘软件
50. 猎头效率软件
