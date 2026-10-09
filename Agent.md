# Agent.md

## 项目概览

这是一个以 Go 和 Gin 为基础的后端服务项目，主要代码集中在 `backend/` 目录。

当前项目提供可运行的 HTTP 服务骨架，包含系统路由、配置读取、请求 ID、访问日志、panic 恢复、统一错误响应和优雅关闭。业务接口、数据库和认证能力应在需求明确后再逐步接入。

## 技术栈

- Go 1.22
- Gin 1.10.0
- 标准库 `net/http`
- 标准库 `log/slog`
- Go modules

## 目录结构说明

- `backend/main.go`：服务启动入口、配置加载、日志初始化、监听和优雅关闭
- `backend/internal/config/`：环境变量读取、默认值和配置校验
- `backend/internal/server/`：Gin 路由注册和 `net/http.Server` 构造
- `backend/internal/handler/`：HTTP handler，当前包含根路径和健康检查
- `backend/internal/middleware/`：request ID、panic recovery、访问日志
- `backend/internal/httpx/`：HTTP 错误响应和 request ID 辅助函数
- `backend/*_test.go`、`backend/internal/**/_test.go`：与源码同目录的 Go 测试
- `backend/.env.example`：本地环境变量示例，不会被程序自动加载
- `README.md`：backend 的运行和接口说明
- `.gitignore`：仓库根目录的 Git 忽略规则
- `frontend/`：前端预留目录，当前没有可用业务代码
- `.product/`：产品资料目录，与代码实现无关，除非用户明确要求，否则不查阅、不修改
- `.claude/skills/`：项目级 Agent skills；所有 skill 仅允许用户显式手动调用
- `.claude/skills/UPSTREAM.md`：Matt Pocock skills 的来源、版本和更新约定

## 常用命令

默认在 `backend/` 目录下执行：

- `go mod tidy`：整理并下载 Go 依赖
- `go run .`：启动开发服务
- `go build -o bin/server .`：构建服务二进制文件
- `go test ./...`：运行全部测试
- `go vet ./...`：运行 Go 静态检查
- `gofmt -w .`：格式化 Go 文件；实际使用时应限制到项目源码文件

默认服务监听 `:8080`。可以通过环境变量覆盖：

- `HTTP_ADDR`：HTTP 监听地址，默认 `:8080`
- `APP_ENV`：运行环境，默认 `development`
- `SHUTDOWN_TIMEOUT`：优雅关闭超时时间，默认 `10s`

## 当前系统接口

- `GET /`：返回服务欢迎信息
- `GET /health`：兼容的基础健康检查
- `GET /healthz`：liveness 检查
- `GET /readyz`：readiness 检查
- 未知路径：返回统一格式的 404 JSON
- 已注册路径使用不支持的方法：返回统一格式的 405 JSON

所有请求都会返回 `X-Request-ID`。客户端传入合法 request ID 时复用该值，否则由服务生成。错误响应不得泄露 panic 详情、堆栈、SQL、配置值或其他内部信息。

## 代码规范

- 修改前先读取相关文件，确认真实代码和调用关系，再决定改法。
- 遵循 Go 官方格式和习惯，提交前运行 `gofmt`、`go test ./...` 和 `go vet ./...`。
- 公共函数、类型和复杂行为使用简洁准确的注释。
- 错误要保留上下文；对外 HTTP 响应使用稳定的错误 code 和面向用户的 message。
- handler 只负责 HTTP 输入输出、状态码和响应映射，不承载复杂业务规则。
- 配置通过 `internal/config` 读取，不在业务代码中直接散落读取环境变量。
- 日志使用 `log/slog`；禁止记录请求体、Authorization、Cookie、密钥和其他敏感信息。
- request ID 应贯穿响应、错误和访问日志，便于排查请求。
- 每个代码文件尽量不超过 500 行；接近或超过限制时按职责拆分。
- 避免 `any`、全局可变状态、隐式依赖和无必要的反射。
- 不修改 `bin/`、测试输出、编辑器缓存等生成内容。
- 不把 `.env`、密钥、token、数据库密码或本地配置提交到仓库。

## 架构约束

- `backend/main.go` 只负责进程组装和生命周期管理，不直接编写业务路由或业务规则。
- 路由统一在 `internal/server/` 注册，handler 放在 `internal/handler/`。
- 中间件顺序保持为 Request ID → Recovery → Access Log，除非有明确理由并补充测试。
- 服务使用 `net/http.Server`，不要恢复为直接调用 `gin.Engine.Run`；保留读取、写入、空闲连接和请求头超时。
- 使用 `signal.NotifyContext` 处理 `SIGINT`/`SIGTERM`，关闭过程必须有上限，不使用 panic 处理正常退出。
- 未出现真实业务需求前，不预先创建空的 `service`、`repository`、`model` 或 `api/v1` 层。
- 首个业务功能按垂直切片设计：handler 负责 HTTP 边界，service 负责业务规则，repository adapter 负责外部存储；只有实际存在替换实现或测试 seam 时才抽象接口。
- 当前没有数据库、认证、CORS、限流、指标、Tracing、Swagger、消息队列或 Docker/CI 约定，不得在没有需求时自行引入。
- 当前 readiness 没有外部依赖，返回 200；未来接入数据库或队列后，只由 readiness 检查依赖，liveness 不因依赖故障失败。
- 默认不信任 `X-Forwarded-For` 等代理头；只有部署环境明确配置可信代理时才调整。

## 测试要求

- 配置测试覆盖默认值、环境变量覆盖、空值和非法值。
- 路由测试使用 `httptest`，覆盖正常响应、404、405、content type 和 request ID。
- 中间件测试覆盖 request ID 复用与生成、panic 恢复以及错误信息不泄露。
- 新增业务时优先测试领域纯函数、service 规则和关键外部依赖 seam。
- 测试不得依赖固定的本机端口、真实数据库或开发者本地状态，除非明确是集成测试。
- 修复 bug 时先增加能复现问题的测试，再修改实现。

## 外部数据库参考

当前 `backend/` 尚未接入数据库或 ORM。若明确需要参考已有 CRM 数据库实现，再查阅：

`C:\Users\鄭昊\Desktop\internship\Egooai-CRM-SDK-Py`

不要因为该目录存在就擅自引入数据库连接、迁移、模型或认证逻辑；先确认具体业务需求和数据契约。

## Git 工作流

- 每次修改尽量只解决一个明确目标，保持 diff 小而容易审查。
- 用户未要求时不要提交、推送、创建分支或修改远程仓库。
- 删除、覆盖、重命名或大范围重构前先确认影响范围；不要无提示删除用户已有内容。
- 修改前后检查 `git diff` 和 `git status`，确认没有生成物、密钥或无关文件。
- 新增依赖前说明用途，优先使用标准库和已有 Gin 能力。
- 代码、测试、文档和临时验证文件要分开管理；临时日志和构建产物不得留在仓库中。

## 行为要求

- 默认使用中文回复。
- 先理解项目结构，再开始修改；先读相关文件，再判断方案。
- 如果文档、代码和实际行为不一致，以实际代码为准，并主动说明差异。
- 优先复用现有结构、命名和工具，不随意发明新模式。
- 涉及删除或覆盖内容时先确认；用户已明确要求的删除除外。
- 完成修改后必须如实报告验证结果；测试失败时说明失败命令和原因，不得声称已通过。
- 不把历史文件、Git dangling object 或外部参考项目当作当前有效契约，除非用户明确确认。
- 业务术语、API 字段和错误码保持一致；新增接口时同步更新 `backend/README.md`。
- 如果后续要扩展技能或仓库约定，优先更新 CLAUDE.md 和对应说明文档，而不是散落在业务代码里
- .product里的文件与项目无关，不需要查阅和修改
- 禁止在页面写入描述类小字文本
- 项目每个代码文件不超过500行，如果发现超过500行代码的文件立刻拆解
- 数据库已搭建完成，位置在"C:\Users\鄭昊\Desktop\internship\Egooai-CRM-SDK-Py"，可用于参考
- 修改代码是不需要兼容旧逻辑，旧逻辑直接移除就可以
- 每次修改代码如果超过50行，在git仓库commit一次

