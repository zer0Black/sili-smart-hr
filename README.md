# sili-smart-hr

综合人才测评平台，用 AI 完成对人的多维度能力评估。

核心数据来自兄弟系统 sili-smart-api（LLM 网关）记录的用户 AI 对话原文，通过对对话的分析加上主动测试，评估人员的 AI 使用能力和通用素质，用于能力盘点和培训提升。

本仓库为前后端骨架（B 档可运行脚手架）：单进程内嵌的后端基座 + 手动初始化的前端骨架，外加一条 account 登录端到端链路作为可运行证据。规格依据见 [context/03_architecture/architecture.md](context/03_architecture/architecture.md)（第 4 章承载运行时约定与 B 档验收口径）。

## 技术栈

后端 Go 1.25：Gin、GORM（SQLite/MySQL/PostgreSQL 按 SQL_DSN 切换）、Asynq（基于 Redis 的异步任务与定时调度）、Wire、Viper、JWT、slog。

前端 React 19 + TypeScript：Rsbuild 构建、TanStack Router/Query、Zustand、axios、shadcn/ui 加 @base-ui/react、Tailwind 4、i18next 双语。

数据库多库可切换：生产推荐 PostgreSQL 或 MySQL，SQLite 仅开发测试。Redis 是 Asynq 的硬依赖，无免起方案。

## 目录结构

```
sili-smart-hr/
├── hr-backend/         # Go 后端，单进程内嵌 Gin + Asynq server + scheduler
├── hr-frontend/        # React 前端骨架
├── docker-compose.yml  # PostgreSQL + Redis（生产联调）
└── context/            # 产品与架构文档
```

## 快速开始

### 1. 启动 Redis（必选）

Redis 是 Asynq 的硬依赖，后端启动时必须可达。

```bash
# 方式一：用仓库编排（同时起 PostgreSQL + Redis）
docker compose up -d

# 方式二：仅起 Redis，数据库回退 SQLite 免起库
docker run -d --name sili-redis -p 6379:6379 redis:7-alpine
```

### 2. 启动后端

```bash
cd hr-backend
# 默认走 SQLite（data/sili-smart-hr.db），Redis 用 localhost:6379
go run ./cmd/server
```

需要 PostgreSQL 时设置 `SQL_DSN`：

```bash
SQL_DSN="postgres://sili:sili@localhost:5432/sili_smart_hr?sslmode=disable" go run ./cmd/server
```

### 3. 启动前端

```bash
cd hr-frontend
pnpm install
pnpm dev
```

dev 模式经 Rsbuild devServer.proxy 把 `/api` 与 `/health` 转发到后端 `http://localhost:8080`，同源免跨域。

### 4. 登录验证

系统无预置账号，所有库（含 SQLite）首启 accounts 与 system_initializations 均空。浏览器打开 `http://localhost:3000`，未初始化时 `__root` beforeLoad 探针据 `system_initializations` 记录存在性自动重定向到 `/setup` 向导，在向导页创建首个管理员账号后跳转 `/login`。

登录成功后 token 落地，占位首页挂载时调用 `GET /api/me` 探测 token 有效性，闭合鉴权链路。

## 关键环境变量

| 变量 | 用途 | 默认 |
|------|------|------|
| `SQL_DSN` | 数据库连接串，按前缀切库（local 空走 SQLite，postgres:// 走 PostgreSQL，其余 MySQL） | `local`（SQLite） |
| `JWT_SECRET` | JWT 签名密钥，生产必须覆盖 | config.yaml 开发默认值 |
| `SQL_MAX_IDLE_CONNS` / `SQL_MAX_OPEN_CONNS` / `SQL_MAX_LIFETIME` | 连接池（仅 MySQL/PostgreSQL） | 库默认 |
| `REDIS_ADDR` | Redis 地址 | `localhost:6379` |
| `SERVER_PORT` | HTTP 端口 | `8080` |
| `CONFIG_PATH` | 配置文件路径 | `configs/config.yaml` |

## 验收口径

要点：后端 `GET /health` 与 `POST /api/login`、带 token `GET /api/me`、OPTIONS 预检与带 Authorization 请求的 CORS 头、Asynq no-op 健康任务按 cron 触发写日志；前端登录跳首页、`/api/me` 放通、语言切换中英、token 失效自动跳登录。
