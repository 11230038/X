# X

X 是一个前后端分离项目，当前包含一个 Go/Gin 后端服务和一个 Vue 3 前端基础应用。

## 项目结构

```text
X/
├── backend/       # Go + Gin HTTP 服务
├── frontend/      # Vue 3 + Vite + TypeScript 前端
├── Agent.md       # 项目开发约定
└── .gitignore
```

## 后端

### 技术栈

- Go 1.22
- Gin 1.10.0
- 标准库 `net/http`
- 标准库 `log/slog`
- PostgreSQL
- GORM
- Goose（版本化 SQL migration）

### 启动

在 `backend/` 目录复制并完整填写本地配置，然后先迁移数据库再启动：

```bash
cp .env.example .env
go mod tidy
go run ./cmd/migrate up
go run .
```

如需查看或单步回退 migration：

```bash
go run ./cmd/migrate status
go run ./cmd/migrate down
```

仅开发环境需要两条测试用户时，可执行 `go run ./cmd/seed`。该命令幂等写入 `test_admin` 和 `test_user`，密码只以 bcrypt 哈希保存。

监听地址由 `.env` 中的 `HTTP_ADDR` 决定。构建、测试和静态检查：

```bash
go build -o bin/server .
go test ./...
go vet ./...
```

### 配置

服务只读取 `backend/.env`，不会从进程环境补值或覆盖。文件缺失、键缺失、空值或非法值都会阻止启动；[backend/.env.example](backend/.env.example) 是完整的非秘密模板，真实 `.env` 不得提交。

必填配置包括：

- HTTP：`HTTP_ADDR`、`APP_ENV`、`SHUTDOWN_TIMEOUT`
- PostgreSQL：`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_SCHEMA`、`DB_SSLMODE`、`DB_TIMEZONE`、`DB_PING_TIMEOUT`
- 连接池：`DB_MAX_OPEN_CONNS`、`DB_MAX_IDLE_CONNS`、`DB_CONN_MAX_LIFETIME`、`DB_CONN_MAX_IDLE_TIME`

API 启动不会自动修改 schema：必须先通过 migration 命令升级到代码要求的版本。数据库配置和密码不会写入日志。

### 系统接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/` | 服务欢迎信息 |
| `GET` | `/health` | 基础健康检查 |
| `GET` | `/healthz` | liveness 检查 |
| `GET` | `/readyz` | readiness 检查；PostgreSQL 可连接时返回 200，否则返回 503 |

未知路径返回统一格式的 404 JSON，不支持的方法返回 405。每个请求都会返回 `X-Request-ID`；服务端不会向客户端泄露 panic 详情、堆栈或内部错误。

当前后端已接入 PostgreSQL 数据基础设施，并通过版本化 SQL 创建 `users` 表；GORM 映射位于 `internal/data/models`。现阶段没有用户业务 API、认证、CORS、限流、指标、Tracing、Swagger、消息队列或业务 service/repository 层，首个真实用例明确后再按垂直切片扩展。`users.password` 只允许保存密码哈希，GORM 模型不得直接作为 API 响应。

## 前端

### 技术栈

- Vue 3
- Vite
- TypeScript
- vue-tsc
- npm

### 安装和启动

在 `frontend/` 目录执行：

```bash
npm install
npm run dev
```

默认访问 `http://localhost:5173`。

其他命令：

```bash
npm run type-check
npm run build
npm run preview
```

### 当前范围

前端当前是一个可启动的基础应用壳，根页面显示 `Vue 3 + Vite`。暂未引入 Vue Router、Pinia、Axios/API client、UI 组件库、认证或业务页面，避免在接口和业务需求明确前建立空的抽象层。

## 开发约定

- 修改前先读取相关文件，确认实际结构和调用关系。
- 默认使用中文回复，代码和文档中的业务术语保持一致。
- Go 代码修改后运行 `gofmt`、`go test ./...` 和 `go vet ./...`。
- 前端代码修改后运行 `npm run type-check` 和 `npm run build`。
- 每个代码文件不超过 500 行，接近限制时按职责拆分。
- 不提交 `.env`、密钥、token、`node_modules/`、构建产物或本地缓存。
- `.product/` 与项目代码无关，除非用户明确要求，否则不查阅、不修改。
- 项目级 Agent skills 位于 `.claude/skills/`，仅允许用户显式手动调用；详见 `.claude/skills/UPSTREAM.md`。

## 验证命令

后端：

```bash
cd backend
go test ./...
go vet ./...
```

前端：

```bash
cd frontend
npm run type-check
npm run build
```
