# GoodHR 5

GoodHR 5 是面向 HR 和猎头的招聘自动化工具，由云端管理后台和 Windows 本地程序组成。

## 目录结构

```text
goodhr5/
├─ cloud/
│  ├─ backend/          Go 云端 API（端口 8084）
│  └─ frontend-next/    Next.js 云端管理后台（端口 3000）
├─ local-agent-go/      Windows 本地程序与任务主流程
│  ├─ internal/platforms/  各招聘平台实现
│  └─ worker-node/      CloakBrowser 浏览器原子操作
├─ scripts/             开发环境启动脚本
└─ docs/                架构、流程和开发说明
```

各部分职责：

- **云端后端**：账号、岗位、任务、配置数据，提供 REST API
- **云端前端**：配置与查看任务的管理后台
- **本地程序**：任务主流程、平台调度、候选人处理
- **平台目录**：各招聘平台（Boss 直聘、猎聘等）的具体页面流程实现
- **Node Worker**：点击、输入、滚动、截图、DOM 提取等浏览器基础操作

## 快速开始

详见 [根目录 README](../README.md#开发环境上手) 的「开发环境上手」章节。

简要步骤：

```powershell
cd goodhr5
.\scripts\dev.ps1
```

脚本会自动启动 PostgreSQL、后端和前端，首次运行会自动生成 `.env` 和安装前端依赖。

## 当前状态

项目处于活跃开发阶段，核心功能已可用：

- 云端管理后台（账号、岗位、任务管理）
- Boss 直聘自动打招呼
- 猎聘猎头端候选人查找与沟通
- AI 候选人评分与筛选
- 本地程序任务执行与平台调度

## 相关文档

- [系统架构](docs/architecture.md)
- [业务流程](docs/goodhr5-flow.md)
- [本地程序说明](local-agent-go/README.md)
- [Docker 部署](README_DOCKER.md)
