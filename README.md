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

### 启动

在 `backend/` 目录执行：

```bash
go mod tidy
go run .
```

默认监听 `:8080`。构建、测试和静态检查：

```bash
go build -o bin/server .
go test ./...
go vet ./...
```

### 配置

服务从环境变量读取配置，不会自动加载 `.env` 文件。可参考 [backend/.env.example](backend/.env.example)。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址，例如 `127.0.0.1:8080` |
| `APP_ENV` | `development` | `development` 使用 Gin debug 和文本日志；其他值使用 Gin release 和 JSON 日志 |
| `SHUTDOWN_TIMEOUT` | `10s` | 收到 SIGINT/SIGTERM 后等待在途请求完成的最长时间 |

### 系统接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/` | 服务欢迎信息 |
| `GET` | `/health` | 基础健康检查 |
| `GET` | `/healthz` | liveness 检查 |
| `GET` | `/readyz` | readiness 检查；当前无外部依赖，运行时返回 200 |

未知路径返回统一格式的 404 JSON，不支持的方法返回 405。每个请求都会返回 `X-Request-ID`；服务端不会向客户端泄露 panic 详情、堆栈或内部错误。

当前后端尚未接入数据库、认证、CORS、限流、指标、Tracing、Swagger、消息队列或业务 service/repository 层，真实业务需求明确后再按垂直切片扩展。

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
