# HRPlus 生产部署手册（Ubuntu 服务器）

本手册面向第一次把 HRPlus 部署到正式线上环境的人，按顺序照做即可上线。
所有命令都在 Ubuntu 服务器的终端里执行（用 SSH 登录服务器后操作）。

---

## 一、先搞清楚要部署什么

HRPlus 分成两大块，**只有"云端"需要部署到服务器**：

| 组件 | 位置 | 是否上服务器 | 作用 |
| --- | --- | --- | --- |
| Go 后端 | `goodhr5/cloud/backend` | ✅ 部署 | 存数据、发接口，监听 `8084` |
| Next.js 前端 | `goodhr5/cloud/frontend-next` | ✅ 部署 | 官网 + 管理后台界面，容器内 `3000` |
| PostgreSQL | Docker 容器 | ✅ 部署 | 主数据库 |
| Redis | Docker 容器 | ✅ 部署 | 会话、缓存 |
| Nginx + HTTPS | 服务器本机安装 | ✅ 部署 | 对外统一入口、加密 |
| 本地 Agent（Go+Node） | HR 自己的 Windows 电脑 | ❌ 不部署 | 实际操作招聘页面的程序，做成安装包发给 HR |

**部署后的访问链路：**

```
用户浏览器
   │  https://你的域名
   ▼
Nginx（服务器 80/443）
   ├── /api/、/uploads/  →  后端 127.0.0.1:8084
   └── 其它所有路径        →  前端 127.0.0.1:5173
                                   │
                          后端 → PostgreSQL / Redis（Docker 内网）
```

数据库表结构和初始数据**不需要手动导入**：后端第一次启动时会自动执行
`cloud/backend/db/migrations/` 下的全部 `.sql` 迁移文件，建好所有表并写入初始配置，
并记录哪些已执行过，之后重启不会重复执行。

---

## 二、准备工作

### 2.1 服务器要求

- 系统：Ubuntu 20.04 / 22.04 / 24.04（64 位）
- 配置：至少 2 核 4G 内存、40G 硬盘（前端构建较吃内存）
- 能用 root 或 sudo 权限
- 已开放安全组/防火墙端口：`80`、`443`（`8084`、`5173` **不要**对公网开放，只给本机 Nginx 用）

### 2.2 域名解析

把你的域名（例如 `goodhr.example.com`）解析到服务器公网 IP，并等生效。
验证方式（在自己电脑上执行）：

```bash
ping 你的域名
```

能 ping 到你的服务器 IP 即可。**HTTPS 证书申请要求域名已经解析到本机**，所以这一步必须先做。

---

## 三、安装 Docker

Ubuntu 上执行（官方脚本，最省事）：

```bash
# 安装 Docker
curl -fsSL https://get.docker.com | sudo sh

# 启动并设置开机自启
sudo systemctl enable --now docker

# 验证：能看到版本信息就成功了
docker --version
docker compose version
```

> 国内服务器如果拉取镜像很慢，可给 Docker 配置镜像加速。编辑 `/etc/docker/daemon.json`：
>
> ```json
> {
>   "registry-mirrors": [
>     "https://docker.1ms.run",
>     "https://docker.m.daocloud.io"
>   ]
> }
> ```
>
> 改完执行 `sudo systemctl restart docker`。

---

## 四、获取代码

```bash
# 装 git（已装可跳过）
sudo apt update && sudo apt install -y git

# 克隆仓库到家目录
cd ~
git clone https://github.com/alvinwangzi/goodhr.git
cd goodhr/goodhr5
```

> 后续所有命令都默认在 `~/goodhr/goodhr5` 目录下执行。
> 如果你的正式代码在 `dev` 分支，执行 `git checkout dev`。

---

## 五、配置环境变量

生产环境的所有配置集中在一个文件：`goodhr5/.env`。

```bash
# 从模板复制一份
cp .env.prod.example .env

# 编辑填写真实值
nano .env
```

**必须改的几项：**

| 配置项 | 说明 | 示例 |
| --- | --- | --- |
| `POSTGRES_PASSWORD` | 数据库密码，改成强密码 | `Xk9$mP2vL7qR` |
| `SITE_URL` | 你的正式域名（前端 sitemap/邀请链接用），结尾不带斜杠 | `https://goodhr.example.com` |
| `GOODHR_SUPER_ADMINS` | 超级管理员邮箱，多个用英文逗号隔开 | `you@example.com` |
| `GOODHR_SMTP_*` | 邮件服务，用于发登录验证码/通知，四项都要填 | 见下方说明 |

关于邮件：生产环境（`GOODHR_APP_ENV=prod`）下如果 SMTP 没配齐，系统会降级为"不发真实邮件"，
用户就收不到验证码、无法注册登录。所以正式对外服务前，**SMTP 一定要配好**。
`GOODHR_SMTP_PASSWORD` 一般填邮箱服务商给的"授权码"，不是邮箱登录密码。

`GOODHR_PG_DSN` 和 `GOODHR_REDIS_ADDR` 不用填，`docker-compose.prod.yml` 会根据上面的
账号密码自动拼好。

填完 `Ctrl+O` 回车保存，`Ctrl+X` 退出。

---

## 六、启动服务

```bash
# 构建镜像并后台启动全部服务（第一次构建约 3-8 分钟）
docker compose -f docker-compose.prod.yml up -d --build
```

查看运行状态：

```bash
docker compose -f docker-compose.prod.yml ps
```

四个服务（postgres、redis、backend、frontend）的 `STATUS` 都应是 `Up` 或 `healthy`。

### 6.1 验证后端和数据库初始化

看后端启动日志，确认数据库自动迁移成功：

```bash
docker compose -f docker-compose.prod.yml logs backend
```

日志里应能看到类似：

```
[migrate] 0001_initial_schema.sql
[migrate] 0002_add_system_configs.sql
...
[migrate] done
HRPlus cloud backend listening on :8084
```

看到 `[migrate] done` 和 `listening on :8084` 就说明数据库建好、后端起来了。

在服务器本机验证接口能通：

```bash
curl -i http://127.0.0.1:8084/health
curl -i http://127.0.0.1:5173
```

两条命令都应返回 HTTP 状态（200 或正常的 JSON），不是"连接被拒绝"。

---

## 七、安装 Nginx 并配置 HTTPS

### 7.1 安装 Nginx 和 certbot

```bash
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```

### 7.2 放置站点配置

仓库里已经准备好完整模板 `nginx/goodhr5.example.conf`：

```bash
# 复制模板到 Nginx 站点目录
sudo cp nginx/goodhr5.example.conf /etc/nginx/sites-available/goodhr5.conf

# 把配置里的占位域名全部替换成你的真实域名
sudo sed -i 's/your-domain.com/你的域名/g' /etc/nginx/sites-available/goodhr5.conf

# 启用站点
sudo ln -sf /etc/nginx/sites-available/goodhr5.conf /etc/nginx/sites-enabled/goodhr5.conf

# 删除默认站点，避免抢占
sudo rm -f /etc/nginx/sites-enabled/default

# 准备证书验证目录
sudo mkdir -p /var/www/certbot
```

### 7.3 申请 HTTPS 证书

因为上面的配置里已经写好了证书路径，但证书还没生成，直接 reload 会报错。
先用 certbot 的临时 webroot 方式签发证书：

```bash
# 先临时只保留 80 端口的验证配置：用 certbot 自带 nginx 插件自动签发最简单
sudo certbot certonly --webroot -w /var/www/certbot -d 你的域名 --agree-tos -m 你的邮箱 --no-eff-email
```

> 如果 `--webroot` 方式因为 Nginx 配置未生效而失败，改用 nginx 插件让 certbot 自动改配置：
> `sudo certbot --nginx -d 你的域名`

证书签发成功后，会生成在 `/etc/letsencrypt/live/你的域名/` 下，正好对应配置里的路径。

### 7.4 校验并重载

```bash
sudo nginx -t          # 语法检查，显示 ok / successful 才继续
sudo systemctl reload nginx
```

### 7.5 验证上线

浏览器打开 `https://你的域名`，应能看到 HRPlus 官网/登录页，地址栏是小锁（HTTPS 正常）。

### 7.6 证书自动续期

Let's Encrypt 证书 90 天到期，certbot 安装时已自动加了续期定时任务。验证一下：

```bash
sudo certbot renew --dry-run
```

显示成功即代表会自动续期，无需人工干预。

---

## 八、后台初始配置

用 `GOODHR_SUPER_ADMINS` 里填的邮箱，通过验证码登录（`https://你的域名/login`），
登录后进入管理后台（`https://你的域名/admin`）。首次上线需要检查/配置：

1. **系统配置 → 本地程序 / 运行组件下载地址**
   这里填 HR 要下载的本地 Agent 安装包、以及 CloakBrowser、Node 运行组件的下载地址。
   详见下一节"本地 Agent 安装包分发"。

2. **订阅套餐、公告、使用指南**等按需维护。

3. **微信支付配置**（如需付费功能）：在后台"系统配置 → 微信支付配置"里在线填写，
   不走环境变量。

> 提示：这些配置存在数据库 `system_configs` 表里，改了立即生效（前端页面刷新一下即可），
> 不需要重启后端。

---

## 九、本地 Agent 安装包分发

本地 Agent 是 HR 装在自己 Windows 电脑上的程序，**不在服务器运行**。分发流程：

### 9.1 打包（在开发机上做，不在服务器）

本地 Agent 的生产地址由 `goodhr5/local-agent-go/packaging/environments/prod.json` 决定。
**打包前先把里面的地址改成你的正式域名**：

```json
{
  "environment": "prod",
  "console_url": "https://你的域名/admin",
  "cloud_api_base": "https://你的域名",
  "console_manifest_url": "https://你的域名/downloads/goodhr-console-manifest.json"
}
```

然后在 Windows 开发机上用现成脚本打安装包（详见 `goodhr5/local-agent-go/README.md`）：

```powershell
# 在 goodhr5/local-agent-go 目录下
.\build_windows_installer.bat
```

产物是 Inno Setup 生成的 `.exe` 安装包。

> 打包会按 dev/prod 环境产出不同的 EXE，正式分发一定用 prod 环境的产物。

### 9.2 提供下载

把安装包放到一个 HR 能下载的地方，两种方式：

- **方式 A（推荐）：对象存储 / OSS**
  上传到你的 OSS，拿到公网下载链接。

- **方式 B：放在后端 uploads 目录**
  把文件放到服务器 `goodhr5/cloud/backend/uploads/` 下，
  下载地址就是 `https://你的域名/uploads/文件名.exe`
  （Nginx 已把 `/uploads/` 转发到后端，后端会把这个目录作为静态文件提供）。

### 9.3 在后台填地址

回到第八节的后台"系统配置 → 本地程序/运行组件下载地址"，把安装包和各运行组件的
下载链接、版本号填进去。HR 登录后就能在引导页看到下载入口。

> 注意版本号要和安装包实际的版本一致，填高了会触发"版本过低"拦截、且没有对应产物可下。

---

## 十、后续更新与运维

### 10.1 更新代码（手动方式，最直观）

```bash
cd ~/goodhr/goodhr5
git pull                                   # 拉最新代码

# 只重启后端（改了后端 Go 代码，因为源码是挂载的，重启即生效）
docker compose -f docker-compose.prod.yml restart backend

# 前端有改动时需要重新构建前端镜像
docker compose -f docker-compose.prod.yml up -d --build frontend

# 依赖（go.mod / package.json / Dockerfile）有变化时，整体重建
docker compose -f docker-compose.prod.yml up -d --build
```

> 数据库迁移在后端每次启动时自动执行，`git pull` 后重启 backend 就会应用新增的迁移文件，
> 不用手动跑 SQL。

### 10.2 更新代码（自动方式，可选）

仓库根有 `auto_deploy.sh`，基于 git diff 只重建/重启有变化的服务，适合配成定时任务：

```bash
# 生产用 prod 编排文件、dev 分支时，这样调用
DEPLOY_BRANCH=dev DEPLOY_COMPOSE_FILE=docker-compose.prod.yml sh auto_deploy.sh
```

它会检查：工作区是否干净、远端能否快进合并，然后按变更范围更新。
日志写在 `deploy.log`。

> `auto_deploy.sh` 要求服务器上的仓库没有未提交改动，否则会终止部署（防止覆盖文件）。

### 10.3 常用运维命令

```bash
cd ~/goodhr/goodhr5

# 查看各服务状态
docker compose -f docker-compose.prod.yml ps

# 实时看日志
docker compose -f docker-compose.prod.yml logs -f backend
docker compose -f docker-compose.prod.yml logs -f frontend

# 重启某个服务
docker compose -f docker-compose.prod.yml restart backend

# 停止全部（保留数据）
docker compose -f docker-compose.prod.yml down

# 启动全部
docker compose -f docker-compose.prod.yml up -d
```

后端还会把日志写到文件：`goodhr5/cloud/backend/logs/backend.log`。

---

## 十一、数据库备份与恢复

### 11.1 手动备份

仓库已准备好备份脚本：

```bash
cd ~/goodhr/goodhr5
sh scripts/backup-db.sh
```

备份文件生成在 `goodhr5/backups/goodhr5_goodhr5_时间戳.sql.gz`，脚本会自动删除 14 天前的旧备份。

### 11.2 定时备份（推荐）

用 crontab 每天凌晨 3 点自动备份：

```bash
crontab -e
```

加入一行（把路径换成你服务器上的实际路径）：

```cron
0 3 * * * cd /home/你的用户名/goodhr/goodhr5 && sh scripts/backup-db.sh >> backups/backup.log 2>&1
```

### 11.3 恢复

```bash
# 先解压再灌回数据库容器
gunzip -c backups/goodhr5_goodhr5_时间戳.sql.gz | \
  docker exec -i goodhr5-prod-postgres-1 psql -U goodhr5 -d goodhr5
```

> 恢复会覆盖现有数据，操作前务必确认目标库正确、并先做一次当前库的备份。

---

## 十二、常见故障排查

| 现象 | 可能原因 | 处理办法 |
| --- | --- | --- |
| `up -d --build` 时 postgres 起不来，报 `POSTGRES_PASSWORD` 相关错误 | `.env` 没设密码 | 检查 `goodhr5/.env` 里 `POSTGRES_PASSWORD` 是否填了 |
| 后端日志一直重连数据库失败 | 数据库还没就绪或密码不对 | `docker compose -f docker-compose.prod.yml logs postgres` 看数据库日志；确认 `.env` 密码一致 |
| 用户收不到验证码邮件 | `GOODHR_APP_ENV` 不是 prod，或 SMTP 没配齐 | 后端日志会打印 `[Mailer]` 提示；核对 `.env` 里 SMTP 四项 |
| 浏览器打开是 502 Bad Gateway | 后端/前端容器没起来 | `docker compose -f docker-compose.prod.yml ps` 看状态，再看对应服务日志 |
| HTTPS 打不开、证书报错 | 证书没签发或域名不匹配 | 重新执行第七节 certbot 步骤，确认 `/etc/letsencrypt/live/你的域名/` 存在 |
| 前端页面能开但接口 404/跨域 | Nginx 的 `/api/` 转发没生效 | 检查 `/etc/nginx/sites-enabled/goodhr5.conf`，`sudo nginx -t && sudo systemctl reload nginx` |
| 改了域名后 sitemap/邀请链接还是旧的 | 前端是构建期注入域名 | 改 `.env` 的 `SITE_URL` 后重新 `up -d --build frontend` |
| 后台提示"读不到本地程序版本" | 后台系统配置里没填本地程序下载地址/版本 | 到后台"系统配置"补齐，版本号与安装包实际版本一致 |

**排查通用第一步**：先看日志。

```bash
docker compose -f docker-compose.prod.yml logs --tail=100 backend
docker compose -f docker-compose.prod.yml logs --tail=100 frontend
sudo tail -n 50 /var/log/nginx/error.log
```

---

## 十三、附录

### 13.1 端口清单

| 端口 | 服务 | 是否对公网开放 |
| --- | --- | --- |
| 80 | Nginx（跳 HTTPS + 证书验证） | ✅ 开放 |
| 443 | Nginx（HTTPS 正式入口） | ✅ 开放 |
| 8084 | Go 后端（绑 127.0.0.1） | ❌ 不开放 |
| 5173 | Next.js 前端（绑 127.0.0.1） | ❌ 不开放 |
| 5432 | PostgreSQL（仅 Docker 内网） | ❌ 不开放 |
| 6379 | Redis（仅 Docker 内网） | ❌ 不开放 |

### 13.2 本次新增/涉及的部署文件

| 文件 | 作用 |
| --- | --- |
| `goodhr5/docker-compose.prod.yml` | 生产编排：PG + Redis + 后端 + 前端 |
| `goodhr5/.env.prod.example` | 生产环境变量模板，复制成 `.env` 使用 |
| `goodhr5/nginx/goodhr5.example.conf` | 完整 Nginx + HTTPS 站点配置示例 |
| `goodhr5/scripts/backup-db.sh` | 数据库备份脚本 |
| `goodhr5/cloud/frontend-next/Dockerfile` | 已支持构建期注入域名（`NEXT_PUBLIC_SITE_URL`） |
| `goodhr5/cloud/backend/db/migrations/*.sql` | 数据库初始化脚本，后端启动时自动执行 |
| `goodhr5/auto_deploy.sh` | 后续增量自动部署脚本（可选） |

### 13.3 完整环境变量清单

见 `goodhr5/.env.prod.example`，其中：

- 数据库：`POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`
- 站点：`SITE_URL` / `PUBLIC_API_BASE`
- 后端：`GOODHR_APP_ENV` / `GOODHR_CLOUD_ADDR`
- 管理员：`GOODHR_SUPER_ADMINS`
- 邮件：`GOODHR_SMTP_HOST` / `PORT` / `USERNAME` / `PASSWORD` / `FROM`
- 绑定：`GOODHR_AGENT_BINDING_ENABLED`

### 13.4 上线检查清单

- [ ] 域名已解析到服务器 IP
- [ ] 安全组只开放 80/443
- [ ] Docker 安装成功，`docker compose version` 有输出
- [ ] `.env` 已填数据库密码、域名、超管邮箱、SMTP
- [ ] `docker compose -f docker-compose.prod.yml ps` 四个服务都 Up
- [ ] 后端日志出现 `[migrate] done` 和 `listening on :8084`
- [ ] `https://你的域名` 能打开且是小锁
- [ ] 证书续期 `certbot renew --dry-run` 成功
- [ ] 用超管邮箱能收到验证码并登录后台
- [ ] 后台已配置本地程序下载地址
- [ ] 已配置数据库定时备份
