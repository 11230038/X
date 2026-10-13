# X

X 是一个前后端分离项目。当前阶段的开发只针对后端：`backend/` 是完整的 Go/Gin 服务，`frontend/` 只保留一个不含任何功能的 Vite 脚手架。

## 项目结构

```text
X/
├── backend/       # Go + Gin HTTP 服务
├── frontend/      # Vue 3 + Vite 脚手架，当前不含功能代码
├── data/upload/   # 本地上传运行数据（由应用创建，Git 忽略）
├── Agent.md       # 项目开发约定
└── .gitignore
```

## 后端

### 技术栈

- Go 1.23
- Gin 1.10.0
- 标准库 `net/http`
- 标准库 `log/slog`
- `github.com/coder/websocket`（WebSocket 升级与读写）
- seekdb v1.4.0.0（MySQL 协议兼容）
- GORM
- Goose（版本化 SQL migration）
- `golang.org/x/crypto/bcrypt`（密码哈希）
- `github.com/golang-jwt/jwt/v5`（JWT 签发与校验）

对话模型与嵌入模型都走 OpenAI 兼容的 HTTP 接口，客户端用标准库 `net/http` 自己写，没有引入供应商 SDK：`internal/llm` 接 `/chat/completions`，`internal/embed` 接 `/embeddings`。

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

仅开发环境需要测试数据时，可执行 `go run ./cmd/seed`。该命令幂等写入 `test_admin`、`test_user`，一套固定 ID 的完整会话数据，一套 quiz/practice fixture，以及完整的 Mastery Path 与 Reading Workspace fixture；密码只以 bcrypt 哈希保存。

监听地址由 `.env` 中的 `HTTP_ADDR` 决定。构建、测试和静态检查：

```bash
go build -o bin/server .
go test ./...
go vet ./...
```

`go test ./...` **以本地 seekdb 为前提**：存储契约（排序、唯一性、级联、CHECK、错误码映射）由真库证明，所以测试会读 `backend/.env`，为每个包建一个 `<DB_NAME>_test_<包名>` 的库（如 `x_test_kb`）并自行跑完 migration，然后只碰这些库——开发库 `x` 不会被打开，所以**测试不需要先迁移开发库**，起容器就够了：

```bash
docker start seekdb
```

没有可用数据库时这些用例**失败**而不是跳过——不连库的绿灯没有意义。只跑纯逻辑的包不受这个前提影响。

### 配置

服务只读取 `backend/.env`，不会从进程环境补值或覆盖。文件缺失、键缺失、空值或非法值都会阻止启动；[backend/.env.example](backend/.env.example) 是完整的非秘密模板，真实 `.env` 不得提交。

必填配置包括：

- HTTP：`HTTP_ADDR`、`APP_ENV`、`SHUTDOWN_TIMEOUT`、`HTTP_READ_HEADER_TIMEOUT`、`HTTP_READ_TIMEOUT`、`HTTP_WRITE_TIMEOUT`、`HTTP_IDLE_TIMEOUT`
- 文件上传：`UPLOAD_ROOT`、`UPLOAD_MAX_FILE_BYTES`、`UPLOAD_MAX_REQUEST_BYTES`
- seekdb：`DB_HOST`、`DB_PORT`、`DB_NAME`、`DB_USER`、`DB_PASSWORD`、`DB_CHARSET`、`DB_COLLATION`、`DB_TLS`、`DB_TIMEZONE`、`DB_PING_TIMEOUT`
- 连接池：`DB_MAX_OPEN_CONNS`、`DB_MAX_IDLE_CONNS`、`DB_CONN_MAX_LIFETIME`、`DB_CONN_MAX_IDLE_TIME`
- 认证：`AUTH_JWT_SECRET`、`AUTH_JWT_ISSUER`、`AUTH_TOKEN_TTL`
- 对话模型：`LLM_PROVIDER`、`LLM_BASE_URL`、`LLM_MODEL`、`LLM_API_KEY`、`LLM_TIMEOUT`、`LLM_TASK_MODEL`、`LLM_TASK_MAX_TOKENS`、`LLM_CAPABILITY_CONFIG_JSON`
- 一轮的边界：`TURN_INPUT_TIMEOUT`、`TURN_MAX_ROUNDS`、`TURN_MAX_ATTACHMENT_BYTES`
- 嵌入模型（V2）：`EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`、`EMBEDDING_API_KEY`、`EMBEDDING_DIMENSION`、`EMBEDDING_TIMEOUT`、`EMBEDDING_BATCH_SIZE`
- 解析（V2）：`PARSER_DEFAULT_ENGINE`、`PARSER_DOCLING_BASE_URL`、`PARSER_TIMEOUT`
- 知识库（V2）：`KB_CHUNK_SIZE`、`KB_CHUNK_OVERLAP`、`KB_RETRIEVAL_TOP_K`、`KB_RETRIEVAL_CANDIDATES`、`KB_RRF_K`、`KB_MAX_CHARS_PER_DOCUMENT`、`KB_MAX_CHARS_PER_TURN`、`KB_VECTOR_LEASE`、`KB_INDEX_WORKERS`

`LLM_API_KEY`、`EMBEDDING_API_KEY` 与 `AUTH_JWT_SECRET` 同级：不写日志、不进错误信息、不进响应体，占位值不替换会让服务停在启动阶段。

`AUTH_JWT_SECRET` 至少 32 字节，必须使用随机值且不得提交：

```bash
openssl rand -base64 48
```

[backend/.env.example](backend/.env.example) 中的占位值故意短于 32 字节，照抄模板而不替换会让服务在监听端口前启动失败。`AUTH_TOKEN_TTL` 是 Go duration 字面量（如 `24h`）。密钥值不会出现在任何日志或错误信息中。

API 启动不会自动修改 schema：必须先通过 migration 命令升级到代码要求的版本。`cmd/migrate` 会先用一条不选库的连接执行 `CREATE DATABASE IF NOT EXISTS`（运行 migration 的连接无法创建自己指向的库），服务本身不会建库。数据库配置和密码不会写入日志。

### seekdb 约束

`DB_NAME` 只接受小写标识符，因为 seekdb 运行在 `lower_case_table_names=1` 下，混合大小写会被折叠。`DB_CHARSET`、`DB_COLLATION` 和 `DB_TIMEZONE` 也做格式限制，避免任意文本进入连接串。

三个 seekdb 默认值与本服务的约定冲突，全部在连接串或 DDL 中显式钉死，不依赖服务端默认：

| 默认行为 | 后果 | 处理方式 |
| --- | --- | --- |
| 默认排序规则 `utf8mb4_general_ci` | `Test` 与 `test` 会被当成同一个用户名 | 所有表 `COLLATE=utf8mb4_bin`；`DB_COLLATION` 必须指定二进制排序规则 |
| 会话时区默认取宿主偏移（本地为 `+08:00`） | `CURRENT_TIMESTAMP` 按该偏移求值，`creat_time` / `update_time` 全部偏移 | DSN 固定 `time_zone='+00:00'`；`DB_TIMEZONE` 只接受 `UTC` |
| 没有部分索引（partial index） | 无法表达「仅对未删除行唯一」这类约束 | 删除 5 处部分索引，改为普通索引，语义在应用层保证 |

`DB_PORT` 默认 `2881`。`DB_TLS` 是严格的 `true` / `false`：seekdb 走 MySQL 协议，没有 PostgreSQL 的 `sslmode` 档位；置为 `true` 时使用驱动内置的 `true` TLS 配置。schema 里也不再用 `TEXT` 作为主键、唯一键或外键——seekdb 不支持这类用法（`ERROR 1167` / `1215`），相应列改为带显式长度的 `VARCHAR`。

`UPLOAD_ROOT` 的相对路径以 `backend/.env` 所在目录为基准解析；示例值 `../data/upload` 指向仓库根目录的 `data/upload/`。应用启动时创建 `.staging`、`images`、`pdf`、`ppt`、`doc`、`md`、`mp3` 子目录，目录不可创建时启动失败。

### 接口一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/` | 服务欢迎信息 |
| `GET` | `/health` | 基础健康检查 |
| `GET` | `/healthz` | liveness 检查 |
| `GET` | `/readyz` | readiness 检查；seekdb 可连接时返回 200，否则返回 503 |
| `POST` | `/api/v1/library/files` | 上传单个文件；`multipart/form-data` 字段固定为 `file`，成功返回 201。**唯一不需要认证的业务路由** |
| `POST` | `/api/v1/auth/register` | 注册并直接返回登录态，成功返回 201 |
| `POST` | `/api/v1/auth/login` | 用户名密码登录，成功返回 200 |
| `GET` | `/api/v1/auth/me` | 返回当前登录用户 |
| `POST` | `/api/v1/sessions` | 建会话，成功返回 201 |
| `GET` | `/api/v1/sessions` | 会话列表；`?deleted=true` 看回收站，`?limit=&offset=` 分页 |
| `GET` | `/api/v1/sessions/{id}` | 单个会话 |
| `PATCH` | `/api/v1/sessions/{id}` | 改名，body `{"title"}` |
| `DELETE` | `/api/v1/sessions/{id}` | 软删除（进回收站），成功返回 204 |
| `POST` | `/api/v1/sessions/{id}/restore` | 从回收站恢复 |
| `DELETE` | `/api/v1/sessions/{id}/permanent` | 永久删除，成功返回 204 |
| `GET` | `/api/v1/sessions/{id}/messages` | 会话的消息，按时间由早到晚 |
| `GET` | `/api/v1/capabilities` | 这个 build 注册的能力：名字、说明、阶段名、工具名 |
| `GET` | `/api/v1/ws` | WebSocket 入口，token 在升级前校验 |
| `GET` | `/api/v1/library/files/{id}` | 文件的 metadata 与解析状态（ATT-01） |
| `GET` | `/api/v1/library/files/{id}/text` | 模型实际看到的抽取文本；`?offset=&limit=` 按字符取窗口，供引用深链（ATT-02、AC-06） |
| `POST` | `/api/v1/knowledge-bases` | 建知识库 |
| `GET` | `/api/v1/knowledge-bases` | 列表，含每个库的文档数与向量就绪数 |
| `GET` | `/api/v1/knowledge-bases/{id}` | 详情，含逐份文档的解析与向量状态（KB-03） |
| `DELETE` | `/api/v1/knowledge-bases/{id}` | 删库，成功返回 204 |
| `POST` | `/api/v1/knowledge-bases/{id}/documents` | 加文档，body `{"file_id"}`；同步抽取分块，异步算向量，成功返回 201 |
| `DELETE` | `/api/v1/knowledge-bases/{id}/documents/{documentID}` | 移除一份文档，成功返回 204 |
| `POST` | `/api/v1/knowledge-bases/{id}/rebuild` | 重建一份文档，body `{"document_id"}` |
| `POST` | `/api/v1/knowledge-bases/{id}/search` | 检索调试面，body `{"query","top_k"?}` |
| `POST` | `/api/v1/knowledge-bases/{id}/default` | 把某个库标为默认（只是标记，不改检索范围） |

除 `/`、`/health*` 与上传外，全部路由要求 `Authorization: Bearer <token>`；缺 token 返回 401 `authorization_required`。

### 响应封装

每个响应都是同一个 envelope，`request_id` 一定存在，且 `data` 与 `error` 恰好出现一个：

```json
{ "data": { "status": "ok" }, "request_id": "..." }
```

```json
{ "error": { "code": "not_found", "message": "route not found" }, "request_id": "..." }
```

handler 只提供自己的 payload，`request_id` 由 `httpx` 统一附加，成功走 `httpx.WriteSuccess`、失败走 `httpx.WriteError`，两者共用同一份约定。**成功和失败共用这一套信封**：业务错误（400/401/403/404/405/409/413）、readiness 失败（503）和 panic 恢复（500）都是同一个 `error` 形状。HTTP 状态码仍然表达语义（201、400、409……），`error.code` 是前端做分支判断的稳定契约。

服务不做任何重定向：尾斜杠（如 `/health/`）不会被改写成 301，而是走统一 404，因此客户端拿到的每个响应都是 JSON 信封，不会出现 HTML 或空 body。

文件库允许图片（JPG/JPEG/PNG/GIF/WebP）、PDF、PPT/PPTX、DOC/DOCX、Markdown 和 MP3，并按类型写入 `data/upload/` 的固定子目录。服务端流式限制大小、计算 SHA-256、验证扩展名与实际内容、先写 staging 再原子移动；数据库写入失败会补偿删除文件。SVG、宏 Office 文件以及未列出的格式会被拒绝。响应只返回逻辑 metadata，不暴露 `library_path` 或绝对路径，也没有静态目录或下载接口。

当前上传接口没有认证、用户归属、限流、磁盘总配额或恶意文件扫描，只适用于本机或可信网络环境，不应直接暴露到互联网。文件系统与 seekdb 无法共享事务；普通失败会补偿清理，但进程在最终移动后立即崩溃仍可能留下 orphan 文件，当前版本尚无后台 reconciliation。

未知路径返回统一格式的 404 JSON，不支持的方法返回 405。每个请求都会返回 `X-Request-ID`；服务端不会向客户端泄露 panic 详情、堆栈或内部错误。

### 认证

注册与登录使用 `users` 表已有的 `username` / `password` 列，本次没有新增 migration，schema 版本仍为 5。密码使用 bcrypt（`bcrypt.DefaultCost`）哈希后写入 `users.password`，该列永远只保存哈希，接口响应只返回 `user_id`、`username`、`role`。

**用户名严格区分大小写**：不做 trim、不做大小写折叠，`Test` 与 `test` 是两个不同账号。注册要求 `^[A-Za-z0-9_-]{3,32}$`，密码按字节长度要求 8–72（72 是 bcrypt 的输入上限）。登录不校验用户名格式，只做长度上界，这样历史数据仍可登录。

登录成功返回：

```json
{
  "data": {
    "user": { "user_id": 1, "username": "alice", "role": "user" },
    "token": "<JWT>",
    "token_type": "Bearer",
    "expires_at": "2026-10-11T08:00:00Z"
  },
  "request_id": "..."
}
```

`GET /api/v1/auth/me` 返回同样的 envelope，`data` 为 `{ "user": { ... } }`，不包含 token。

token 是 HS256 JWT，`iss` 为 `AUTH_JWT_ISSUER`，`sub` 是 `user_id`，有效期由 `AUTH_TOKEN_TTL` 决定。受保护接口通过 `Authorization: Bearer <token>` 携带；除了校验签名、签发者与过期时间（含 30 秒宽限），服务端每次请求都会按 `sub` 重新读取用户，因此删除或禁用账号会立即生效，无需等待 token 过期。当前没有刷新 token、吊销列表或登出接口：登出只是客户端丢弃 token，该 token 在过期前仍然有效。

为防用户名枚举，未知用户名与错误密码返回完全相同的 401 `invalid_credentials`，且未知用户名也会执行一次等成本的 bcrypt 比较；账号禁用状态只在密码验证通过后才返回。

错误响应：

| 情况 | 状态码 | code |
| --- | --- | --- |
| 请求体非法 JSON / 多余字段 | 400 | `invalid_request` |
| 请求体超过 4 KiB | 413 | `request_too_large` |
| 用户名不符合规则（注册） | 400 | `invalid_username` |
| 密码长度不在 8–72 字节（注册） | 400 | `invalid_password` |
| 用户名已存在（注册） | 409 | `username_taken` |
| 用户名或密码错误（登录） | 401 | `invalid_credentials` |
| 账号已禁用（登录 / 受保护接口） | 403 | `account_disabled` |
| 缺少 Bearer token | 401 | `authorization_required` |
| token 签名错误 / 过期 / 格式错误 / 用户已删除 | 401 | `invalid_token` |
| 其他注册、登录、鉴权失败 | 500 | `registration_failed` / `login_failed` / `authentication_failed` |

已知缺口：没有刷新 token、token 吊销、登出接口、改密与找回密码、邮箱验证、限流与账号锁定、2FA/CAPTCHA；没有基于 `role` 的授权判定（`role` 只作为信息返回）；`/api/v1/library/files` 因为 `library_files` 还没有 owner 列，仍然不需要认证。

当前后端已接入 seekdb 数据基础设施。版本化 SQL 除 `users` 外，conversation migration v2 还创建 `turn_event_types`、`sessions`、`messages`、`summaries`、`turns`、`turn_events`，对应 GORM 映射位于 `internal/data/models`。`turn_event_types` 是 ID 1–15 的稳定整数查找表；summary 按 revision 保留历史，`summary_up_to_msg_id` 是由字符串 message ID 组成的 JSON 数组，不是外键；`turns.assistant_message_id` 是指向 `messages.message_id` 的字符串外键。删除 session 会级联删除其 messages、summaries、turns，删除 turn 会级联删除其 events。

migration v3 新增 quiz/practice 持久化表 `notebook_entries`、`notebook_categories`、`notebook_entry_categories`、`reading_quiz_pending`、`practice_review_state`、`practice_review_events`。`notebook_entries.notebook_entries_id` 是主键，`question_id` 在全表全局唯一；可空的 `session_id`、`turn_id` 分别引用 conversation 表，并使用 `ON DELETE CASCADE`。`reading_quiz_pending` 刻意只保存 `question_id`、`question`、`creat_time`。

migration v4 新增七张 Mastery Path 表和四张 Reading Workspace 表。Mastery 根表使用 `mastery_paths.mastery_path_id`，子表使用 `path_id` 外键；`mastery_path_sessions`、`reading_workspace_sessions`、`reading_workspace_materials` 使用复合主键。删除 mastery path 会级联清理其子记录；删除 reading workspace 只清理映射，不删除独立 session/material；删除 material 会清理 workspace-material 映射，并把 workspace 与 workspace-session 的 `active_material_id` 置空。`notebook_entries.mastery_path_id` 仍是应用层 ID，不追补数据库外键。

migration v5 新增 `library_files` 与 `llm_calls`。文件本体位于本地磁盘，`library_files.library_path` 只保存相对 `UPLOAD_ROOT` 的路径；SHA-256 用于完整性和查询，不限制相同内容重复上传。`library_files` 的路径约束用 `REGEXP` 加三层 `SUBSTRING_INDEX` 复刻了原来的「目录前缀与文件名中的 `lib_<id>` 必须与该行 `id` 一致」，因为 seekdb 没有部分索引也没有等价于 `split_part` 的函数。`llm_calls.usage_json` 是 seekdb `JSON` 且必须是 object；`session_id` 与 `turn_id` 是历史关联标识，刻意不建立外键，因此 conversation 删除不会删除用量历史。本次不 seed 这两张表：文件 metadata 不能脱离真实文件，LLM 用量也不应伪造。字段、约束、索引和删除规则以 migration SQL 为唯一事实来源。

migration v6 给 `sessions` 加 `is_deleted` / `deleted_at`，让 CHAT-06 的「软删 / 恢复 / 永久删」三步可以被表达，并用一条 `CHECK` 钉住两列的一致性；seekdb 没有部分索引，所以活跃行的筛选条件放在索引键的最前面。

migration v7（V2）新增 `knowledge_bases`、`documents`、`document_chunks`。三张表一条链：文件变成文档，文档变成分块，检索读的是分块。没有引入 seekdb 以外的队列或索引——待嵌入的队列就是 `documents` 上的一个谓词（「还没就绪」），检索索引就是 `document_chunks` 上的 `FULLTEXT`（ngram）与 `VECTOR(1024)` 两个索引。`documents.kb_id` 可空，空表示这份文档只作为某一轮对话的附件存在，不属于任何库；`documents.active_revision` + 分块的主键前缀让「重建后原子切换版本」成为一次指针移动，并发检索永远只看见一个版本。

当前后端已交付的垂直切片：用户认证、文件上传与读取、会话与消息，以及经 WebSocket 驱动的对话回合（含工具调用、能力切换与多步进度）、知识库的解析与检索。quiz/practice、Mastery Path 与 Reading Workspace 只有 schema 与 GORM 映射，没有 repository、service 或 API；`llm_calls` 的写入链路同样尚未接上。现阶段没有 CORS、限流、指标、Tracing、Swagger 或消息队列。`users.password` 只允许保存密码哈希，GORM 模型不得直接作为 API 响应。

### 对话回合与 WebSocket

一轮对话在 `GET /api/v1/ws` 上跑。一条连接可以做多件事：开始一轮、订阅任意一轮的事件流、取消、回答模型的反问。**回合在服务端独立于连接运行**，事件按序号落库，所以断线之后可以带着 `after_seq` 回来接着看。

客户端帧是一个扁平 JSON 对象，`type` 是唯一的分发字段，必须带 `protocol_version: "2.0"`：版本不符会以终止性错误关闭连接，未知字段被忽略，未知 `type` 回非终止的 `unsupported_command`。

| type | 字段 | 做什么 |
| --- | --- | --- |
| `start_turn` / `message` | `session_id`（空 = 新建）、`capability`（空 = 默认 `chat`）、`content`、`attachments[]`、`knowledge_bases[]` | 开始一轮，回 `command_ack` 并自动订阅这一轮 |
| `subscribe` / `resume` | `turn_id` 或 `session_id`、`after_seq` | 从某个序号起重放事件，断线重连就是它 |
| `unsubscribe` | `turn_id` 或 `session_id` | 停止接收 |
| `cancel` | `turn_id` | 请求取消；收尾（写终止状态并发 `done`）仍由那一轮自己做 |
| `submit_reply` | `turn_id`、`content` | 回答 `wait_for_input` 提出的问题 |
| `check_active_turn` | `session_id` | 问这个会话有没有在跑的回合 |
| `ping` | 无 | 回 `pong` |
| `regenerate` | —— | 只留了名字，**永远回 `unsupported_command`** |

服务端帧有五种（`frame` 字段）：`event`、`command_ack`、`error`（带 `terminal` 布尔）、`active_turn`、`pong`。

事件类型走线时用名字，数字 id 只用于落库（`turn_event_types` 是 1–15 的字典表）：

| 事件 | 载荷 | 什么时候发 |
| --- | --- | --- |
| `session` | `content` = session id | **每轮的第一个事件** |
| `stage_start` / `stage_end` | `stage`，`source` = 能力名 | 能力在阶段之间走动时 |
| `thinking` | `content` = 推理文本 | 模型产出了推理内容时 |
| `observation` | `metadata.grounding` | 带材料的 `chat` 预调查结束之后（CHAT-09） |
| `content` | `content` = 答案片段 | 模型边写边发；同一阶段里连续的片段会在服务端合并成一个事件（最多攒 50ms 或 1KB）再写库、再发出，所以一段长回答是若干行而不是每个 token 一行 |
| `tool_call` / `tool_result` | 工具名，`metadata.args` / `call_id`、`success` | 模型调用工具时 |
| `progress` | `metadata.{step,total,label}` | `deep_solve` 每走一步（多步进度条的数据源） |
| `sources` | `metadata.sources[]` | 这一轮带着材料作答、而且检索有结果时 |
| `result` | `content` = 完整答案；`metadata.usage_summary`、`.grounding`、`.unresolved_citations` | 一轮成功结束时 |
| `wait_for_input` | `content` = 问题 | 能力调用 `ask_user` 反问用户时 |
| `error` | `content` = 面向用户的一句话；`metadata.error_code`、`retryable`、`partial_response` | **只在失败时**出现 |
| `done` | `metadata.status`、`partial_response` | 每轮恰好一个 |
| `session_meta` | `content` = 会话标题 | **总是最后一个事件**，也是流的结束标记 |

失败一轮的结尾是 `error` → `done` → `session_meta`；取消是 `done(status=cancelled)` → `session_meta`，没有 `error`。回合状态是 `queued → running → (waiting_input → running) → completed | failed | cancelled`。对一个已经不在跑的回合发 `cancel`，只会把它记成 `abandoned` 失败，不再补发任何事件。

连接与协议的上限：入站帧 1 MiB（非文本帧忽略），出站队列 64 帧，积压满会以终止性 `slow_consumer` 关闭（close code 1008）；带 `command_id` 的命令在单条连接内幂等去重，缓存 256 条。流意外提前结束时客户端会收到终止性 `internal_error`，带上最后收到的 `seq` 重连即可续看。

### 材料、解析与引用（V2）

一轮可以带材料：`attachments` 是已上传文件（`library_files.id`）的列表，`knowledge_bases` 是知识库的列表。前者只对**这一轮**可见，后者是**可检索**的语料库——这个区别就是 `documents.kb_id` 可空的含义。

**从文件到模型看到的文字**：

1. 上传把字节写进 `data/upload/`，并按类型登记 `library_files`。
2. 解析：`PARSER_DEFAULT_ENGINE=text` 用本进程把文件当文本读（md、txt）；`docling` 把 PDF / Office 交给一个独立服务。没人能读的类别（图片）会以 `unsupported_format` 明确拒绝，并在上下文里告诉模型"这个附件读不了"，而不是让它假装看到了。
3. 分块按**字符**切（`KB_CHUNK_SIZE=512`、`KB_CHUNK_OVERLAP=50`），因为引用指向的就是字符。超过 `KB_MAX_CHARS_PER_DOCUMENT` 的文档会被截断存储并标记 `truncated`，不会静默丢弃。
4. 向量是**异步**算的：抽取和分块同步完成（HTTP 请求返回时文档已经可读），索引 worker 认领 `pending` 的文档、批量嵌入、写回向量。认领带租约（`KB_VECTOR_LEASE`），所以 worker 崩掉不会让文档永远卡住。这段时间里 `GET /knowledge-bases/{id}` 会显示每份文档的 `vector_state`，而**全文那一路已经能检索出结果**（KB-01）。
5. 检索是混合的：ngram 全文 + 向量，两路各自取 `KB_RETRIEVAL_CANDIDATES` 条，再用 RRF（`KB_RRF_K`）融合成 `KB_RETRIEVAL_TOP_K` 条。只有一路有结果时，融合就等于那一路的排序。

**引用怎么落到界面上**：`sources` 事件里每条来源带 `index`、`document_id`、`file_id`、`title`、`engine`、`score`、`locator` 和 `snippet`；`locator` 里是 `chunk_id` 与 `char_start` / `char_end`（加上解析器给出的 `page`，如果有）。模型写出的编号要么在 `sources` 里对上，要么进 `result.metadata.unresolved_citations`——**不会**凭模型的说法造一条来源。把 `file_id` 和 `char_start` 交给 `GET /api/v1/library/files/{id}/text?offset=&limit=`，就是"点开这条引用"。`result.metadata.grounding` 给出这一轮的来源数与不可用材料数，界面据此区分"有依据的回答"和"库中未找到依据"（AIQ-04）。

**降级与失败**：

- 外部解析引擎不在（`docling` 没起）→ 加文档返回 **503**，文案是给上传者看的那句；应用的其余部分照常（AC-14）。
- 单份文档解析失败 → 该文档以 `parse_failed` 结束，其余文档不受影响，重建可以重试。
- 嵌入失败 → 文档的 `vector_state` 变成 `failed`，原因留在 `vector_error` 里；全文检索仍然可用，`rebuild` 是重试路径。
- 删库就是删库：带着已删库的 id 开一轮会报来源不可用，不会悄悄落到别的库（KB-07）。

**启动期的三处一致性校验**（不一致就拒绝启动，不回退）：`EMBEDDING_DIMENSION` 必须等于 schema 里 `VECTOR(N)` 的 N；`PARSER_DEFAULT_ENGINE` 必须在解析器注册表里；库里已有知识库的 `embedding_model` 必须等于 `EMBEDDING_MODEL`（换模型要重建，重建之后才启动得起来）。

**已知缺口**：`docling` 需要自己跑起来（默认 `http://127.0.0.1:5001`），本仓库不含它；嵌入用独立供应商，DeepSeek 没有 embedding 接口，所以 `EMBEDDING_*` 与 `LLM_*` 是两套配置；检索参数是全局的，不按库配置；模型目前只读文本，`VISION_*` 配了也没有消费方；上传与知识库都还没有属主列，权限只到"有没有登录"这一层。

## 前端

`frontend/` 目前只是 Vite 官方脚手架：`src/App.vue` 渲染一个静态标题，没有路由、页面、API 调用或认证状态。此前实现的登录、注册、登录态校验等前端功能已全部移除，后端接口暂时没有前端消费方。

```bash
cd frontend
npm install
npm run dev          # 默认 http://localhost:5173
npm run type-check
npm run build
```

当前阶段只做后端改动，前端保持为空壳；后续阶段再按当时的后端契约重新实现。

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
gofmt -l .
```

`go test ./...` 会自建 `<DB_NAME>_test_*` 库并在其中跑 migration，所以需要本地 seekdb 在跑（`docker start seekdb`）。测试库用完留在那里即可，`x` 与之无关——`go run ./cmd/migrate up` 是给应用用的，测试不依赖它。

前端：

```bash
cd frontend
npm run type-check
npm run build
```
