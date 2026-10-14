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
- 出题与练习（V3）：`QUESTION_DEFAULT_COUNT`、`QUESTION_MAX_COUNT`、`PRACTICE_QUEUE_SIZE`、`PRACTICE_JUDGE_MAX_TOKENS`
- 掌握路径（V4）：`MASTERY_LEASE_TTL`、`MASTERY_GATE_CONSECUTIVE`、`MASTERY_GATE_MIN_EVIDENCE`、`MASTERY_MAX_ACTIVE_PATHS`
- 沉浸阅读（V4）：`READING_UNIT_MAX_CHARS`、`READING_TUTOR_MAX_TOKENS`、`READING_EXTENSION_TIMEOUT`
- Memory（V4）：`MEMORY_CONFIDENCE_THRESHOLD`、`MEMORY_MIN_CORROBORATION`、`MEMORY_MIN_SURFACES`、`MEMORY_SENSITIVE_KINDS`、`MEMORY_SYNTHESIS_WINDOW_DAYS`、`MEMORY_MAX_FACTS_PER_SURFACE`
- 继续学习（V4）：`DASHBOARD_RECENT_LIMIT`

`QUESTION_MAX_COUNT` 必须**不小于** `QUESTION_DEFAULT_COUNT`——默认值大于上限时，一句没说几道的出题请求会每次都拒绝自己。`QUESTION_DEFAULT_COUNT` / `QUESTION_MAX_COUNT` 是"一次出几道题"，`PRACTICE_QUEUE_SIZE` 是"练习队列一次取几道"，`PRACTICE_JUDGE_MAX_TOKENS` 是主观题判分那一次调用的输出预算。`LLM_CAPABILITY_CONFIG_JSON` 里 `deep_question` 的条目是**必需**的：它声明的阶段名（`ideation` / `generation`）与 `chat` 的（`exploring` / `responding`）不同，按阶段名取 `max_tokens` 的查找在继承来的配置里查不到，服务会带着"缺哪几个阶段、该写哪个键"的信息拒绝启动。

V4 的两个新能力（`mastery_path` / `immersive_reading`）**都只走 `responding` 这一个阶段**，它们在 `LLM_CAPABILITY_CONFIG_JSON` 里各有一条自己的条目，而这两条只声明 `responding`——与 `chat` 同名，所以按阶段名取 `max_tokens` 的查找命中；`immersive_reading` 的 `context_budget_tokens` 比其他能力大一档（48000），因为一轮阅读动辄带进一整段材料。四个 `MASTERY_*` 与一个 `DASHBOARD_RECENT_LIMIT` 是产品参数（同时推进几条路径、连对几次升档、一次取多少条），另外九个是阅读与 Memory 的规模与阈值。`MEMORY_SENSITIVE_KINDS` 是**唯一一个逗号分隔的集合**：它点的是部署**不信任**的 kind（默认 `preference,health,relationship,credential`），列进去的观察永不自动成为事实，但用户自己写下的事实不受它影响。`MEMORY_MAX_FACTS_PER_SURFACE` 截的是整份综合而不是四个列表各自（见 `V4设计方案.md` §0.3 第 4 条）。

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
| `GET` | `/api/v1/questions` | 题库列表；`?source=&material_id=&difficulty=&resolved=&bookmarked=&question_type=&category=&q=&limit=&offset=` 逐条收窄，认不出的取值报 400 而不是忽略 |
| `GET` | `/api/v1/questions/{questionID}` | 单题：题干面、作答面、分类。**对外的 id 只有 `question_id`** |
| `PATCH` | `/api/v1/questions/{questionID}` | 收藏 / 已解决 / 改分类；body 里**出现过的**字段才被改（`{"bookmarked":true}`、`{"resolved":false}`、`{"categories":["第三章"]}`；`[]` 表示取消归类） |
| `GET` | `/api/v1/question-categories` | 分类名列表，只列出还能筛出题的那些 |
| `POST` | `/api/v1/questions/import` | 批量导入题库，body 是一个 JSON 行文件（每行一道题，与 `save_question` 参数同形）；`?dry_run=true` 只报告不写。两次请求共用同一份解析与判定（QN-05） |
| `GET` | `/api/v1/practice/queue` | 练习队列：错题插队、到期的按逾期程度、新题在后；长度是部署的 `PRACTICE_QUEUE_SIZE`，不由请求指定 |
| `POST` | `/api/v1/practice/reviews` | 提交一次复习，body 即 `practice.Review`：`request_id`、`question_id`、`user_answer`、`marker`?（`mastered` / `needs_review` / `skipped` / `bookmarked`）、`rating`?、`version`? |
| `GET` | `/api/v1/practice/reviews/{requestID}` | 读回一次提交的结果，与重发同一 `request_id` 拿到的那一份逐字相同 |
| `GET` | `/api/v1/practice/stats` | 练习趋势；`?bucket=day|week`、`?from=&to=`（日期或 RFC3339），窗口向外对齐到整桶并在响应里说明实际覆盖的范围 |
| `GET` | `/api/v1/mastery/paths` | 掌握路径列表，按最近看过排（`mastery_path_sessions.last_seen_time`）。**路径不在这里创建**，见下节 |
| `GET` | `/api/v1/mastery/paths/{pathID}` | 一条路径的状态与主题 |
| `PATCH` | `/api/v1/mastery/paths/{pathID}` | 改主题（`goal` / `description` / `emoji` / `status`），body 里出现过的字段才被改 |
| `GET` | `/api/v1/mastery/paths/{pathID}/insight` | **MP-05**：目标、证据、薄弱项、复习状态、下一步及其理由 |
| `GET` | `/api/v1/mastery/paths/{pathID}/lease` | 现在是谁在写这条路径（没有则为空） |
| `DELETE` | `/api/v1/mastery/paths/{pathID}/lease` | 主动释放租约（`mastery_leave` 的接口那一半） |
| `GET` | `/api/v1/reading/materials` | 阅读材料目录（含 `pinned` / `opened`，未表态时是缺省的 `null`） |
| `POST` | `/api/v1/reading/materials` | 从一份已上传的文件建阅读材料，body `{"file_id"}` |
| `GET` | `/api/v1/reading/materials/{materialID}` | 一份材料的 outline / units 与字符数 |
| `GET` | `/api/v1/reading/materials/{materialID}/content` | 一段文本 + 它的 locator，按 locator 取（READ-02） |
| `GET` | `/api/v1/reading/materials/{materialID}/marks` | 这份材料的书签与批注，按写入时间由早到晚 |
| `POST` | `/api/v1/reading/materials/{materialID}/marks` | 加一个书签 / 高亮 / 批注（READ-01） |
| `POST` | `/api/v1/reading/materials/{materialID}/position` | 保存阅读位置，带 `revision` 做 compare-and-swap |
| `GET` | `/api/v1/reading/materials/{materialID}/pairings` | **推荐**另一语种的那本（一次查询，不落表，§2.10） |
| `POST` | `/api/v1/reading/materials/{materialID}/pairings` | **确认**一对双语 |
| `DELETE` | `/api/v1/reading/pairings/{pairingID}` | 取消配对，成功返回 204 |
| `GET` | `/api/v1/reading/materials/{materialID}/extensions` | 扩展清单与各自的可用性（含禁用原因） |
| `POST` | `/api/v1/reading/materials/{materialID}/extensions/{name}` | 跑一个扩展，带 locator 或带选区 |
| `GET` | `/api/v1/dashboard` | **COURSE-03** 的"继续学习"：最近会话、材料、阅读、掌握、练习各取 `DASHBOARD_RECENT_LIMIT` 条 |
| `GET` | `/api/v1/memory/traces` | L1 观察，可按 surface / kind / 时间收窄 |
| `GET` | `/api/v1/memory/facts` | L2 事实，含 `status` / `confidence` / `is_sensitive` |
| `GET` | `/api/v1/memory/facts/{factID}` | 一条事实**与它的证据链**（MEM-01） |
| `PATCH` | `/api/v1/memory/facts/{factID}` | 编辑一条事实：重算 `fingerprint`，进审计 |
| `POST` | `/api/v1/memory/facts` | 用户自己写一条事实（§2.7 的第四条路，三道闸全跳过） |
| `POST` | `/api/v1/memory/facts/{factID}/retract` | 撤销 |
| `POST` | `/api/v1/memory/facts/{factID}/restore` | 取消撤销 |
| `POST` | `/api/v1/memory/facts/dedupe` | 按 `fingerprint` 合并，body `{"dry_run":true}` 只报告不写 |
| `POST` | `/api/v1/memory/cleanup` | 按时间 / 来源清理一批，同样先报告后执行 |
| `GET` | `/api/v1/memory/synthesis` | **L3**：每次读都算一遍，没有缓存，也没有表（§2.6） |
| `GET` | `/api/v1/memory/audit` | 审计流水（MEM-02） |
| `GET` | `/api/v1/memory/export` | 导出全部 Memory，含证据链（PRD §11.3） |

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

migration v11（V4）给 `practice_review_events` 加 `is_correct` 与 `idx_practice_review_events_entry_outcome`。判分的结果一直写在 `outcome_json` 里，但 JSON 里的字段走不了索引，掌握路径的档位又是第一个按对错聚合大量事件的读方。列**可空且必须可空**：跳过的题没有判断（`judgment` 是 null），回填成 `FALSE` 会把每一条历史跳过变成一次答错，而这两件事在界面上是两回事。回填语句的 `WHERE ... IS NOT NULL` 就是这条语义的执行处。

migration v12（V4）加阅读层的四张新表 `reading_positions` / `reading_marks` / `reading_pairings` / `reading_quiz_pending`。四条**指向同一个方向**的外键各自不同：位置属于**会话**（把会话删了，读到哪儿也该走，「接着上次读」是对一个还在的会话说的），批注属于**材料**而会话只是它写在哪（删一个会话不该带走别人的笔记，所以 `reading_marks.session_id` 可空并在会话消失时置空）；配对的两端都指向材料，`primary <> counterpart` 由 CHECK 钉住。位置带 `revision`，写走 `revision = revision + 1` 的 compare-and-swap，所以两个标签页同时保存时后一个拿到 409 而不是静默覆盖。

migration v13（V4）建 Memory 的四张表 `memory_traces`（L1 观察）/ `memory_facts`（L2 事实）/ `memory_fact_sources`（证据链）/ `memory_audit`（审计）。**L3 没有表**：它是一次计算（`internal/memory/synthesis.go` 的纯函数），落一张快照表就要在每条事实被改、被撤销时记得刷它，而"忘了刷"的症状正是"用户撤销了一条，综合视图里还在"。`memory_facts` 的敏感列叫 `is_sensitive` 而不是 `sensitive`——`sensitive` 是 seekdb 的关键字，DDL 会被直接拒绝（`V4设计方案.md` §0.3 第 1 条）。`owner_id` 是本仓库第一处**账号级的业务列**：Memory 按 MEM-04 是账号级的，而 V1–V3 的表都只到会话这一层，所以它由认证边界传入，没有 owner 的一轮不写 Memory。审计表**没有 UPDATE 与 DELETE 路径**，仓储层只暴露追加。

当前后端已交付的垂直切片：用户认证、文件上传与读取、会话与消息，经 WebSocket 驱动的对话回合（含工具调用、能力切换与多步进度），知识库的解析与检索，题库与练习，掌握路径，沉浸阅读，以及账号级的 Memory。尚未接上的是 `llm_calls` 的写入链路——用量表建好了，还没有调用方。现阶段没有 CORS、限流、指标、Tracing、Swagger 或消息队列。`users.password` 只允许保存密码哈希，GORM 模型不得直接作为 API 响应。

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

### 出题、题库与练习（V3）

第四个能力 `deep_question` 出的是**题库里的记录**，不是一段话。它有两个阶段，其中一个不调用模型：

1. `ideation`——向运行时问这一轮携带的材料，用**规则**把用户那句话读成一份题组规格（数量、题型、难度、分类），然后把两者作为 `observation` 摊开。规格不来自模型：它是能算出来的东西，而它的价值在于出现在屏幕上。附带的结果是**进度从第一次调用起就是真的**——「第 3 道，共 10 道」不等模型写完再回头数。
2. `generation`——调用模型，每道题一次 `save_question` 工具调用，逐题写进题库。

一轮完整的事件顺序：`stage_start(ideation)` → `sources`（**有材料才发**）→ `observation`（带 `question_plan`）→ `stage_end(ideation)` → `stage_start(generation)` → 每道题一对 `tool_call`/`tool_result`（`metadata` 是 `step` / `total` / `label`，成功时还有 `created`）→ `stage_end(generation)` → `result`。模型只讲不落库时 `saved: 0`——那是一个要画出来的状态，不是失败。

`save_question` 在写的时候拒绝的，正是判题在读的时候会拒绝的：选项少于 2 个或多于 8 个、`correct_answer` 不在 `options` 里、答案是空的、题型不在封闭集合内。拒绝是**结构化的**，带一句能让模型当场改对的话——模型还在看着这道题，这是它改得动的时刻。存储约定只写在工具 schema 的描述里：选择题答案是逗号分隔的选项键（升序、去重），填空用 `|` 分隔多个可接受写法。题目的 `material_id` 记的是**附件文件**而不是解析出来的文档（文件在解析失败时也还在）；`source_label` 只能指这一轮真正携带的材料，和多带几个附件时不猜。

**预路由（TOOL-08）**：客户端没有指定 capability 时，`Runner.Start` 在创建回合行**之前**按规则表读一遍那句话。三条同时成立才路由——话里有「出题动词 + 题目名词」、没有「考考我」这类要逐题被考的意图（`deep_question` 是出题器，不是逐题问答的运行器）、客户端确实没指定（显式指定永远优先，预路由是填空不是改写）。「给我题库」「一道题多少钱」这类含"题"字的话都不路由。命中但目标没注册时退回默认并记一条日志。决定落在该轮第一个 `session` 事件的 `metadata.routing`（`capability` / `by` / `reason` / `confidence`），断线重放也看得到。

**练习**是八条 REST 路由，都要求登录，形状见上面的接口一览：

- **判题分两支**：选择题与填空题由服务端比对（`judgment.kind = "objective"`、`model` 为 null、不产生任何模型调用）；主观题是一次模型调用（`kind = "model_judgment"`），温度 0.2，理由与采分点随判分一起回来。判不出来（密钥错、供应商不答、返回不是约定形状）报 **503 `judge_unavailable`**——它可重试，且**绝不会**被当成"答对了"。
- **提交可以安全重发**：`request_id` 是持久幂等的凭据（事件表的键），重启服务后重发同一份仍拿回同一份结果；`replayed` 只说这次是重放还是新记了一次；`version` 做乐观并发（`409 stale_review` 带回当前状态），同一个 `request_id` 换了内容则是 `409 conflicting_request_id`。重发请**沿用同一个 `request_id`**。
- **队列是一次查询**，不是后台作业：没有队列表、没有定时器，三种来源（错题插队 / 到期 / 没见过的题）在一条 JOIN 里排序。跳过只推进时钟，不发号、不计次数。
- **统计**从历次复习记录算出来，窗口按 `day` / `week` 对齐，没数据的桶也占一个点——否则图表会把隔着的两天画成相邻的两天。跳过既不算对也不算错（一天的"全跳过"与"全答错"靠旁边的计数分开）。

**已知缺口（V3）**：知识点只以标签形式落在 `extra.knowledge_points` 上，没有可跳转的对象；正确率走 `practice_review_events.outcome_json` 提取，没有索引；题目的来源会话或材料被删之后会变成"没有归属"，界面要能表达来源已删除；预路由只认中英文那几种写法，写别的说法就留在通用助手里。

### 掌握路径、沉浸阅读与 Memory（V4）

V4 加两个能力（`mastery_path`、`immersive_reading`）、一个只读的"继续学习"聚合、一个账号级的 Memory，以及 QN-05 的批量导入。三条 migration：**00011** 给复习事件补 `is_correct`（V3 留下的欠账），**00012** 加阅读层的四张表，**00013** 建 Memory 的四张表。

**掌握路径（MP-01–05）** 把一门要学的东西拆成知识点，然后按证据推进每一条的档位。它**不是一个请求-应答的接口**，是一轮对话：

- **路径在一轮 turn 里开启**，不在 REST 上（没有 `POST /mastery/paths`）。MP-01 的五个来源——目标、材料、Notebook、Reading——都要先有一次会话才谈得上"从哪个来源来"。所以 `GET /mastery/paths` 是读，建是 `mastery_path` 能力在 `Start` 时做的事。同时推进的路径数上限是 `MASTERY_MAX_ACTIVE_PATHS`，但**这一版它拦不住任何请求**——一个会话只要有活跃路径就直接回到那条路径上，要数路径得先没有路径可回（见 `.env.example` 里这个键的说明），而"上限该不该存在、该按会话还是按人算"是还没裁决的产品问题。
- **九个工具**是这一版的全部动作：`mastery_state`（读树与档位）、`mastery_question`（出题）、`mastery_grade`（判卷）、`mastery_skip`、`mastery_assess`（记一条证据）、`mastery_map` / `mastery_edit`（建图与改知识点）、`mastery_switch`、`mastery_leave`。能力本身只有一个阶段（`responding`），因为"这一轮在干什么"是**工具**说的，一轮按学习者的下一句话在九个工具之间走。
- **一题只存一处**：`mastery_question` 出的题就在 `notebook_entries` 里，只是多带了 `mastery_path_id` 与 `knowledge_point_id` 两列。掌握路径**没有自己的题表**，所以题库的每一个过滤器在它上面都照常有效。
- **档位由证据算出来，不由模型说**：连续 `MASTERY_GATE_CONSECUTIVE` 次答对升到已掌握，证据少于 `MASTERY_GATE_MIN_EVIDENCE` 时是"未知"而不是"薄弱"——`partial` 既不算对也不算错，它重置连续计数但不降档。复习间隔是一条**自己的曲线**，读的是证据序列而不是题目的连对计数。
- **租约让两个窗口不互相覆盖**：一轮开始时拿到 `mastery_path_leases` 的那一行，之后就带 `FenceToken` 写。租约过了 `MASTERY_LEASE_TTL` 可以被另一个 turn **接管**（不是续期——续期要一次心跳和它的一整套失败模式），被接管的旧 turn 退化成"只能追加证据，改不了状态"。接管与否不靠猜：冲突返回结构化失败，带上现在是谁在写。
- **`GET /mastery/paths/{pathID}/insight`** 是 MP-05 的读：目标、证据、薄弱项、复习状态、下一步及**它的理由**。

**沉浸阅读（READ-01–06）** 把一份已上传的文件变成可以停、可以标注、可以就着某一段提问的材料：

- 一份材料的正文按 `READING_UNIT_MAX_CHARS` 切成 units，目录与 units 从 `GET /reading/materials/{materialID}` 拿。
- **locator 和 V2 的引用是同一个形状**（`ordinal` / `char_start` / `char_end` / `page`，见 `internal/event/source.go` 与 `internal/reading/types.go`），所以"点开这条引用"和"跳到那一段"在两种场合是同一个动作：`GET /reading/materials/{materialID}/content?char_start=&char_end=` 取回那段文本与它的 locator。
- 位置按 `(material_id, session_id)` 存，写带 `revision` 做 compare-and-swap——两个标签页同时保存，**后一个拿到 409 `stale_position`**，而不是静默覆盖。批注分 `bookmark` / `highlight` / `note` 三种，`note` 必须有字，另外两种是纯位置。
- **问一段话是随一轮带的**：`start_turn` 的帧里有一个可选的 `selection`（`material_id` + `locator` + `text`），只属于这一轮、不落库。模型读到的是**坐标解析出来的文本**而不是客户端声称的文本——客户端可能是个陈旧的标签页。
- **扩展是服务端的**（READ-03 要的 Python 宿主这一版没有，继承的是协议）：`GET .../extensions` 列出内置的 `explain`（把一段话讲清楚）与 `vocabulary`（挑出这段里会卡住的词），各自说自己可不可用、不可用时为什么。跑扩展走 `POST .../extensions/{name}`，带 locator 或带选区；**一个扩展 panic、超时（`READING_EXTENSION_TIMEOUT`）或返回不合 schema 的结果都被跳过并带上原因**，其他扩展照常返回。
- **双语配对只存读者确认过的那一行**：`GET .../pairings` 是**推荐**（一次查询，不落表，靠语言与标题算出来），`POST .../pairings` 是**确认**，`DELETE /reading/pairings/{pairingID}` 是取消。未确认时下游一律"未启用"而不是空内容。V4 **不派生任何文本**。

**Memory（MEM-01–04）** 是账号级的三层：

- **L1 `memory_traces` 是观察**（系统听到了什么），**L2 `memory_facts` 是事实**（若干观察里活下来的断言），**L3 没有表**——`GET /memory/synthesis` 每次读都重新算一遍。所以撤销一条事实**立刻**改变综合视图：它没有第二个地方可以躲。代价是综合视图没有历史。
- **一条观察要不成为事实，要过三道闸**：置信度到 `MEMORY_CONFIDENCE_THRESHOLD`、有 `MEMORY_MIN_CORROBORATION` 条独立观察、它们来自 `MEMORY_MIN_SURFACES` 个不同的 surface；`MEMORY_SENSITIVE_KINDS` 里列的 kind 永不自动放行。没过的留在 L1（看得见），或者作为 `proposed` 的事实待佐证（也看得见）——**这两片"还没算数"是 MEM-03 的全部用户可见面**。
- **第四条路**：用户自己写的（`POST /memory/facts`）三道闸全跳过，直接 `active` 且 `confirmed_by` 是用户。这也是综合视图里 `preferences` 那一节的内容——它不是"kind 为 preference 的事实"（L2 根本没有 kind 列），是"你自己说过的"。
- 去重按归一化后的 `fingerprint`，**只归一空白不归大小写**（与 V3 的 `question_id` 同一条取舍：宁可重复，不可误并）。`POST /memory/facts/dedupe` 与 `/memory/cleanup` 都是**先报告后执行**，`dry_run` 在请求体里。
- 每一次动作在 `memory_audit` 里留一行，而审计表**没有 UPDATE 与 DELETE 路径**。`GET /memory/export` 把事实连同证据链一起导出（PRD §11.3）。
- **Memory 有 owner，别的表没有**：`owner_id` 由认证边界传入，一个没有 owner 的 turn **一行 Memory 都不写**。MEM-04 说 Memory 是账号级且不随 Workspace 切换隔离——当前后端里不存在 Workspace 切换，所以这条边界今天是空的。

**继续学习（COURSE-03/04）**：`GET /dashboard` 一个请求读三个域——最近会话、掌握路径、练习队列，各取 `DASHBOARD_RECENT_LIMIT` 条——外加一组 `next` 推荐，每条带 `reason` 与 `evidence_ids` 两个不许为空的字段。新账号拿到的是 **200 加四个空数组**，不是一个错误；一个读不通的查询是 500，**不做部分页面**。

**QN-05 批量导入**：`POST /questions/import` 的 body 是一个 JSON 行文件（每行一道题，与 `save_question` 参数同形），`?dry_run=true` 只报告不写。两次请求共用同一份解析与判定，所以 preview 不会说谎；冲突分"同一道题"（同 `question_id`）与"同一题干不同 id"（重复）两种，都进报告。两次都是 **200**——一份"写了 3 条、拒了 2 条"的报告不是失败的请求。

**已知缺口（V4）**：`MASTERY_MAX_ACTIVE_PATHS` 与它背后的 `ErrTooManyPaths` 当前到达不了（同一条判断读两次同一张表，见上）；掌握路径与阅读材料都还没有 owner 列，权限仍只到"有没有登录"；阅读的推荐要读目录里每份材料的开头一窗（材料没有语言列，语言是从正文推断的）；`mastery_interactions` 的"同一路径最多一个活跃交互"是服务层的事务内读，不是数据库唯一键；租约接管会让一个过期的长 turn 退化成只追加证据；`LLM_CAPABILITY_CONFIG_JSON` 里两个新能力各有一条条目，`immersive_reading` 的上下文预算单独大一档。

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
