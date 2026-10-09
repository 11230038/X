# Backend

这是一个基于 Go 1.22 和 Gin 的 HTTP 服务骨架。当前提供系统级路由，业务模块可在需求明确后按垂直切片扩展。

## 快速开始

在 `backend` 目录执行：

```bash
go mod tidy
go run .
```

默认监听 `:8080`。构建和测试：

```bash
go build -o bin/server .
go test ./...
go vet ./...
```

## 配置

服务从环境变量读取配置，不会自动加载 `.env` 文件。可复制 `.env.example` 作为本地配置参考。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听地址，例如 `127.0.0.1:8080` |
| `APP_ENV` | `development` | `development` 使用 Gin debug 和文本日志；其他值使用 Gin release 和 JSON 日志 |
| `SHUTDOWN_TIMEOUT` | `10s` | 收到 SIGINT/SIGTERM 后等待在途请求完成的最长时间 |

配置非法时服务会记录错误并以非零状态退出。

## 系统路由

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/` | 兼容现有的欢迎响应 |
| `GET` | `/health` | 兼容现有的健康响应 |
| `GET` | `/healthz` | liveness 检查；仅表示进程存活 |
| `GET` | `/readyz` | readiness 检查；当前无外部依赖，因此进程运行时返回 200 |

未知路径返回 404，已注册路径使用不支持的方法返回 405。错误响应统一为：

```json
{
  "error": {
    "code": "not_found",
    "message": "route not found"
  },
  "request_id": "..."
}
```

每个请求都有 `X-Request-ID` 响应头。客户端提供合法的 `X-Request-ID` 时服务会复用，否则生成随机 ID。服务端不会把 panic 详情、堆栈或内部错误返回给客户端。

## 项目结构

```text
backend/
├── main.go              # 进程入口、配置组装和优雅关闭
├── internal/config/     # 环境配置读取与校验
├── internal/handler/    # 系统 HTTP handler
├── internal/httpx/      # HTTP 错误响应和 request ID 辅助函数
├── internal/middleware/ # request ID、panic recovery、访问日志
└── internal/server/     # Gin 路由和 net/http.Server 构造
```

目前没有预置数据库、认证、CORS、限流、指标、Tracing、Swagger 或空的 service/repository 层；首个真实业务需求出现后再引入对应的深模块边界。

## 运行时行为

服务使用 `net/http.Server`，并设置请求头、读取、写入和空闲连接超时。收到 `SIGINT` 或 `SIGTERM` 后停止接受新连接，在 `SHUTDOWN_TIMEOUT` 内等待现有请求完成，然后退出。访问日志写入 stderr：开发环境为文本，其他环境为 JSON；日志只包含方法、路径、状态、耗时、request ID 和客户端 IP，不记录请求体或敏感请求头。
