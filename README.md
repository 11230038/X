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
- 认证：`AUTH_JWT_SECRET`、`AUTH_JWT_ISSUER`、`AUTH_TOKEN_TTL`、`AUTH_LOCKOUT_THRESHOLD`（V6）、`AUTH_LOCKOUT_WINDOW`（V6）、`AUTH_LOCKOUT_DURATION`（V6）
- 对话模型：`LLM_PROVIDER`、`LLM_BASE_URL`、`LLM_MODEL`、`LLM_API_KEYS`、`LLM_KEY_FAILURE_THRESHOLD`、`LLM_KEY_COOLDOWN`、`LLM_TIMEOUT`、`LLM_TASK_MODEL`、`LLM_TASK_MAX_TOKENS`、`LLM_CAPABILITY_CONFIG_JSON`
- 一轮的边界：`TURN_INPUT_TIMEOUT`、`TURN_MAX_ROUNDS`、`TURN_MAX_ATTACHMENT_BYTES`
- 嵌入模型（V2）：`EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`、`EMBEDDING_API_KEY`、`EMBEDDING_DIMENSION`、`EMBEDDING_TIMEOUT`、`EMBEDDING_BATCH_SIZE`
- 解析（V2）：`PARSER_DEFAULT_ENGINE`、`PARSER_DOCLING_BASE_URL`、`PARSER_TIMEOUT`
- 知识库（V2）：`KB_CHUNK_SIZE`、`KB_CHUNK_OVERLAP`、`KB_RETRIEVAL_TOP_K`、`KB_RETRIEVAL_CANDIDATES`、`KB_RRF_K`、`KB_MAX_CHARS_PER_DOCUMENT`、`KB_MAX_CHARS_PER_TURN`、`KB_VECTOR_LEASE`、`KB_INDEX_WORKERS`
- 出题与练习（V3）：`QUESTION_DEFAULT_COUNT`、`QUESTION_MAX_COUNT`、`PRACTICE_QUEUE_SIZE`、`PRACTICE_JUDGE_MAX_TOKENS`
- 掌握路径（V4）：`MASTERY_LEASE_TTL`、`MASTERY_GATE_CONSECUTIVE`、`MASTERY_GATE_MIN_EVIDENCE`
- 沉浸阅读（V4）：`READING_UNIT_MAX_CHARS`、`READING_TUTOR_MAX_TOKENS`、`READING_EXTENSION_TIMEOUT`
- Memory（V4）：`MEMORY_CONFIDENCE_THRESHOLD`、`MEMORY_MIN_CORROBORATION`、`MEMORY_MIN_SURFACES`、`MEMORY_SENSITIVE_KINDS`、`MEMORY_SYNTHESIS_WINDOW_DAYS`、`MEMORY_MAX_FACTS_PER_SURFACE`
- 继续学习（V4）：`DASHBOARD_RECENT_LIMIT`
- 授权（V5）：`AUTHZ_DEFAULT_PRESET`、`AUTHZ_LEARNER_CAPABILITIES`、`AUTHZ_LEARNER_SURFACES`
- LLM key 池（V5）：`LLM_KEY_FAILURE_THRESHOLD`、`LLM_KEY_COOLDOWN`（`LLM_API_KEYS` 是第二版那个键改的名，已列在上一行的对话模型里）
- 审计（V5）：`AUDIT_RETENTION_DAYS`、`AUDIT_MAX_PAGE_SIZE`
- 用量（V5）：`USAGE_RETENTION_DAYS`、`USAGE_DAILY_CALLS_PER_ACCOUNT`、`USAGE_MONTHLY_TOKENS_PER_ACCOUNT`
- 限速（V5）：`RATE_LIMIT_MODEL_PER_MINUTE`、`RATE_LIMIT_RETRIEVAL_PER_MINUTE`、`RATE_LIMIT_UPLOAD_PER_MINUTE`、`RATE_LIMIT_BURST`
- 导入导出（V5）：`WORKSPACE_ARCHIVE_ROOT`、`WORKSPACE_MAX_ARCHIVE_BYTES`、`WORKSPACE_JOB_TIMEOUT`

`QUESTION_MAX_COUNT` 必须**不小于** `QUESTION_DEFAULT_COUNT`——默认值大于上限时，一句没说几道的出题请求会每次都拒绝自己。`QUESTION_DEFAULT_COUNT` / `QUESTION_MAX_COUNT` 是"一次出几道题"，`PRACTICE_QUEUE_SIZE` 是"练习队列一次取几道"，`PRACTICE_JUDGE_MAX_TOKENS` 是主观题判分那一次调用的输出预算。`LLM_CAPABILITY_CONFIG_JSON` 里 `deep_question` 的条目是**必需**的：它声明的阶段名（`ideation` / `generation`）与 `chat` 的（`exploring` / `responding`）不同，按阶段名取 `max_tokens` 的查找在继承来的配置里查不到，服务会带着"缺哪几个阶段、该写哪个键"的信息拒绝启动。

V4 的两个新能力（`mastery_path` / `immersive_reading`）**都只走 `responding` 这一个阶段**，它们在 `LLM_CAPABILITY_CONFIG_JSON` 里各有一条自己的条目，而这两条只声明 `responding`——与 `chat` 同名，所以按阶段名取 `max_tokens` 的查找命中；`immersive_reading` 的 `context_budget_tokens` 比其他能力大一档（48000），因为一轮阅读动辄带进一整段材料。四个 `MASTERY_*` 与一个 `DASHBOARD_RECENT_LIMIT` 是产品参数（同时推进几条路径、连对几次升档、一次取多少条），另外九个是阅读与 Memory 的规模与阈值。`MEMORY_SENSITIVE_KINDS` 是**唯一一个逗号分隔的集合**：它点的是部署**不信任**的 kind（默认 `preference,health,relationship,credential`），列进去的观察永不自动成为事实，但用户自己写下的事实不受它影响。`MEMORY_MAX_FACTS_PER_SURFACE` 截的是整份综合而不是四个列表各自（见 `V4设计方案.md` §0.3 第 4 条）。

V5 的十七个新键同样没有代码默认值——**缺键是启动失败，不是程序替部署编一个数**（`LLM_API_KEYS` 是第二版改名的那个旧键，不重复计）。它们分成六组：两个 `LLM_KEY_*` 是 key 池的两个阈值——连续失败几次把一把 key 打进冷却、冷却多久（冷却以 429 为准：500 是上游不舒服、401 是键配错了，都不该用一次 60 秒的冷却把配置错误伪装成暂时故障）；三个 `AUTHZ_*` 说的不是规则而是**基线**（规则在 `internal/authz` 里：`AUTHZ_DEFAULT_PRESET` 只决定新账号落在哪个 preset 上，两个 `AUTHZ_LEARNER_*` 只能在 Learner 的地板之上**往上加**，写错只会让它够得着更多、不会让它够不着）；两个 `AUDIT_*` 与三个 `USAGE_*` 各含一个保留期，而**保留期只被一条按窗口删的语句读到，不起定时作业**（清理由调用方触发，与"租约过期是一次比较"同一条；**V6 给了那个调用方**——`go run ./cmd/cleanup`，见下）；四个 `RATE_LIMIT_*` 里三个是每账号每类动作的令牌桶速率、`BURST` 是三个桶共用的容量（速率写 `0` 表示这一类不限，而 `BURST` 不能是 `0`——那是三个桶一个令牌都装不下）；三个 `WORKSPACE_*` 里的 `WORKSPACE_ARCHIVE_ROOT` 与 `UPLOAD_ROOT` 是**两个目录而不是一个的两种叫法**，`MAX_ARCHIVE_BYTES` 同时管读和写，`JOB_TIMEOUT` 是作业的墙钟上限。每一个键的取值理由都写在 [backend/.env.example](backend/.env.example) 那一行上面。

V6 只加了三个键，都在认证那一组：`AUTH_LOCKOUT_THRESHOLD` / `AUTH_LOCKOUT_WINDOW` / `AUTH_LOCKOUT_DURATION`——**窗口内错够几次就锁、窗口多长、锁多久**，是三个可以被分别质疑的量，所以是三个键而不是一个。它们与其余键同级：没有代码默认值，缺一个就是启动失败。**阈值必须是正整数**：写成 `0` 会让第一次密码错误就锁定，没有哪种读法是一个部署想要的，所以 `Validate` 直接拒它（`internal/config/auth.go:48`）。三个键与 `.env.example` 里的注释都写明了一件事：**锁定只挡登录，不挡已经拿到的 token**——要踢掉已登录的会话得走 `POST /auth/logout` 或管理员那条吊销。

`LLM_API_KEYS`、`EMBEDDING_API_KEY` 与 `AUTH_JWT_SECRET` 同级：不写日志、不进错误信息、不进响应体，占位值不替换会让服务停在启动阶段。

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
| `POST` | `/api/v1/library/files` | 上传单个文件；`multipart/form-data` 字段固定为 `file`，成功返回 201。**重复上传同一份字节返回同一个 id**（见下节） |
| `POST` | `/api/v1/auth/register` | 注册并直接返回登录态，成功返回 201 |
| `POST` | `/api/v1/auth/login` | 用户名密码登录，成功返回 200 |
| `GET` | `/api/v1/auth/me` | 返回当前登录用户 |
| `POST` | `/api/v1/auth/logout` | 登出：撤回该账号的全部 token（**全端下线**），204 |
| `PUT` | `/api/v1/me/password` | 改密，body `{"old_password","new_password"}`；成功后**全部 token 作废，包括发起这次请求的那个**，204 |
| `POST` | `/api/v1/sessions` | 建会话，成功返回 201 |
| `GET` | `/api/v1/sessions` | 会话列表；`?deleted=true` 看回收站，`?limit=&offset=` 分页 |
| `GET` | `/api/v1/sessions/{id}` | 单个会话 |
| `PATCH` | `/api/v1/sessions/{id}` | 改名，body `{"title"}` |
| `DELETE` | `/api/v1/sessions/{id}` | 软删除（进回收站），成功返回 204 |
| `POST` | `/api/v1/sessions/{id}/restore` | 从回收站恢复 |
| `DELETE` | `/api/v1/sessions/{id}/permanent` | 永久删除，成功返回 204 |
| `POST` | `/api/v1/sessions/{id}/claim` | **认领**一条没有主人的会话（V5 的存量数据，见下节） |
| `GET` | `/api/v1/sessions/{id}/messages` | 会话的消息，按时间由早到晚 |
| `GET` | `/api/v1/capabilities` | 这个 build 注册的能力：名字、说明、阶段名、工具名 |
| `GET` | `/api/v1/me/permissions` | 我这一份**生效权限**——四层合成的结果，界面不必自己拼 |
| `GET` | `/api/v1/usage` | 用量聚合；`?since=&until=`（RFC3339）、`?model=&provider=&source=&session_id=` 逐条收窄、`?group_by=`（`model` / `provider` / `source` / `session` / `day`） |
| `GET` | `/api/v1/system/version` | 版本、构建时间、Go 版本、migration 版本 |
| `GET` | `/api/v1/system/status` | 运行模式、依赖的逐项 readiness、活跃回合数、索引 worker、**未收尾的调用数** |
| `GET` | `/api/v1/ws` | WebSocket 入口，token 在升级前校验 |
| `GET` | `/api/v1/library/files` | **文件库列举与搜索**；`?filename=&category=&since=&until=&deleted=&limit=&offset=`，`deleted=true` 看回收站 |
| `GET` | `/api/v1/library/files/{id}/content` | **下载**；**唯一不走 JSON 信封的一条**，带 `Content-Disposition` 与 `nosniff` |
| `GET` | `/api/v1/library/files/{id}` | 文件的 metadata 与解析状态（ATT-01） |
| `GET` | `/api/v1/library/files/{id}/text` | 模型实际看到的抽取文本；`?offset=&limit=` 按字符取窗口，供引用深链（ATT-02、AC-06） |
| `DELETE` | `/api/v1/library/files/{id}` | **软删除**（进回收站，字节还在），成功返回 204 |
| `POST` | `/api/v1/library/files/{id}/restore` | 从回收站恢复 |
| `DELETE` | `/api/v1/library/files/{id}/purge` | **彻底删除**；仍被引用时 409 `file_in_use` 并说出是谁挡着（AC-15） |
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
| `GET` | `/api/v1/audit` | 审计流水，管理员专属；`?actor_id=&subject_id=&action=&target_kind=&target_id=&since=&until=&limit=` 逐条收窄，认不出的取值报 400 而不是忽略 |
| `GET` | `/api/v1/admin/users` | 账号列表（`role` / `preset` / 是否禁用） |
| `PATCH` | `/api/v1/admin/users/{userID}` | 改 `preset`；**会把权限清空的改动被拒**（400 `policy_would_be_empty`） |
| `DELETE` | `/api/v1/admin/users/{userID}/sessions` | **吊销**：把该账号的全部 token 作废（写 `token_valid_after`），204，写审计 |
| `DELETE` | `/api/v1/admin/users/{userID}/lock` | **解锁**：清掉失败计数与锁定，204，写审计 |
| `GET` / `POST` / `DELETE` | `/api/v1/admin/users/{userID}/grants` | 逐项授权的读 / 开 / 关；写成 body `{"kind","id","allowed"}`，关掉一项时 `DELETE` 也带 body |
| `GET` / `POST` / `DELETE` | `/api/v1/admin/guardians` | Guardian 关系的列表 / 建立 / 撤销；建立带 `{"guardian_id","learner_id","permissions"?}`，撤销带 `{"link_id"}` |
| `GET` / `POST` / `DELETE` | `/api/v1/admin/guardians/{linkID}/permissions` | 逐项 permission 的读 / 授 / 撤；带 `{"permission"}` |
| `GET` | `/api/v1/guardian/learners` | Guardian 视角：我能看的那些人 |
| `GET` | `/api/v1/guardian/learners/{userID}/reports` | 学习报告；**需要 `view_reports`，而且每一次读都写一行审计**（AC-11） |
| `POST` | `/api/v1/workspace/exports` | 导出一个包，body `{"refs":[{"kind","id"}]}`；`kind` 是 `session` / `material` / `question` / `knowledge_base` |
| `GET` | `/api/v1/workspace/exports` | 导出作业列表 |
| `GET` | `/api/v1/workspace/exports/{jobID}/content` | **下载包**；`application/zip` 流式返回，带 `Content-Disposition` 与 `nosniff`。**第二处不走 JSON 信封**，与文件库那条下载同一个套路 |
| `POST` | `/api/v1/workspace/imports/preview` | 预览一个包，`multipart/form-data` 字段固定为 `file`；**零写入** |
| `POST` | `/api/v1/workspace/imports` | 提交一次导入，body `{"job_id"}`（预览给的那个） |
| `GET` | `/api/v1/workspace/jobs/{jobID}` | 作业状态：state / 清单 / 计数 / 已经落了哪些 |
| `POST` | `/api/v1/workspace/jobs/{jobID}/recover` | 恢复：把这次作业已经落的行按记录删掉 |

除 `/`、`/health*` 外，全部路由要求 `Authorization: Bearer <token>`；缺 token 返回 401 `authorization_required`。**这里只有一层的分离**：要求 token 与要求权限是两件事，见下节。

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

文件库允许图片（JPG/JPEG/PNG/GIF/WebP）、PDF、PPT/PPTX、DOC/DOCX、Markdown 和 MP3，并按类型写入 `data/upload/` 的固定子目录。服务端流式限制大小、计算 SHA-256、验证扩展名与实际内容、先写 staging 再原子移动；数据库写入失败会补偿删除文件。SVG、宏 Office 文件以及未列出的格式会被拒绝。响应只返回逻辑 metadata，不暴露 `library_path` 或绝对路径，也没有静态目录；下载只有 `GET /api/v1/library/files/{id}/content` 这一条，它自己校验归属。

上传接口从 V5 起**要登录、要有归属、要限速**（见下节）。它仍然没有磁盘总配额与恶意文件扫描，只适用于本机或可信网络环境。文件系统与 seekdb 无法共享事务；普通失败会补偿清理，但进程在最终移动后立即崩溃仍可能留下 orphan 文件，当前版本尚无后台 reconciliation。

未知路径返回统一格式的 404 JSON，不支持的方法返回 405。每个请求都会返回 `X-Request-ID`；服务端不会向客户端泄露 panic 详情、堆栈或内部错误。

### 认证

注册与登录使用 `users` 表已有的 `username` / `password` 列。密码使用 bcrypt（`bcrypt.DefaultCost`）哈希后写入 `users.password`，该列永远只保存哈希，接口响应只返回 `user_id`、`username`、`role`。

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

token 是 HS256 JWT，`iss` 为 `AUTH_JWT_ISSUER`，`sub` 是 `user_id`，有效期由 `AUTH_TOKEN_TTL` 决定。受保护接口通过 `Authorization: Bearer <token>` 携带；除了校验签名、签发者与过期时间（含 30 秒宽限），服务端每次请求都会按 `sub` 重新读取用户，因此删除或禁用账号会立即生效，无需等待 token 过期。**从 V6 起 token 可以被撤回**：`users.token_valid_after` 存一个时刻，签发时间早于它（`iat <= token_valid_after`，含同一秒）的 token 一律作废——`POST /auth/logout` 会把它写成一刻，`PUT /me/password` 改密成功后也一样，管理员的 `DELETE /admin/users/{userID}/sessions` 也一样。**登出是全端下线**：本仓库没有"设备"这个概念，所以拿不出只踢一台机器的答案（见下）。

V6 的四个路由：

| method | path | 面 | 说明 |
| --- | --- | --- | --- |
| `POST` | `/api/v1/auth/logout` | `authenticated`，**无面** | 撤回该账号的全部 token，204。**不补发新 token**——客户端重新登录 |
| `PUT` | `/api/v1/me/password` | `authenticated`，**无面** | body `{"old_password","new_password"}`；要旧密码，成功后撤回全部 token（含自己），204 |
| `DELETE` | `/api/v1/admin/users/{userID}/sessions` | `administrative` | 撤回目标账号的全部 token，204，写审计 `account.sessions_revoked` |
| `DELETE` | `/api/v1/admin/users/{userID}/lock` | `administrative` | 清掉目标账号的失败计数与锁定，204，写审计 `account.unlocked` |

两条管理员路由**按账号的属性命名而不是按动作命名**——锁是账号有的一样东西，会话是它持有的一样东西，所以两条都是账号下的 `DELETE`；两条都写一行审计，`subject` 是那个账号，因为**动手的管理员与被动到的账号是两个人**。

`PUT /me/password` 是这套 API 的第一个 `PUT`。**改密之后不补发 token**是有意的：补发要求新 token 的 `iat` 晚于刚写入的 `token_valid_after`，而在秒级精度下那意味着"写入与签发落在同一秒"，把一条安全规则压在一个时间巧合上不值得；改密要求重新登录本身也是常见且诚实的产品行为。这条裁定有一条用例钉着（改密后两个 token 都得是 401）。

登录失败会被锁定：窗口内错满 `AUTH_LOCKOUT_THRESHOLD` 次之后，**即使密码正确也是 403 `account_locked`**，直到 `AUTH_LOCKOUT_DURATION` 过去。锁定**只挡登录，不挡已经拿到的 token**——要踢掉已登录的会话，走上面那四条。代价要说清楚：**失败计数按用户名计，所以任何人都可以拿一个已知的用户名把那个人锁在门外**；这是这个机制的固有属性而不是实现缺陷，缓解手段是阈值 + 窗口 + 管理员解锁（上表最后一条）。

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
| 账号被失败锁定（登录） | 403 | `account_locked` |
| 改密的旧密码不对 | 403 | `invalid_credentials` |
| 缺少 Bearer token | 401 | `authorization_required` |
| token 签名错误 / 过期 / 格式错误 / 用户已删除 | 401 | `invalid_token` |
| 其他注册、登录、鉴权失败 | 500 | `registration_failed` / `login_failed` / `authentication_failed` |

已知缺口：没有刷新 token、找回密码、邮箱验证、单端登出、2FA/CAPTCHA。V6 补齐的是**token 生命周期与账号锁定**这一半：登出、改密、管理员吊销、失败锁定都有了，但它们背后是一个 `token_valid_after` 时刻而不是一张吊销表——**登出是全端下线**，因为本仓库没有"设备"这个概念（没有设备注册、没有设备凭据），要做单端登出得先有 `jti` 与一张按 token 的吊销表。**"有没有登录"之外的判定从 V5 起有了**：`role` / `preset` / grants / Guardian 四层由 `internal/authz` 合成，中间件与 handler 只问它，见下节。限流按账号、按动作，不按设备（本仓库没有设备这个概念）。

当前后端已接入 seekdb 数据基础设施。版本化 SQL 除 `users` 外，conversation migration v2 还创建 `turn_event_types`、`sessions`、`messages`、`summaries`、`turns`、`turn_events`，对应 GORM 映射位于 `internal/data/models`。`turn_event_types` 是 ID 1–15 的稳定整数查找表；summary 按 revision 保留历史，`summary_up_to_msg_id` 是由字符串 message ID 组成的 JSON 数组，不是外键；`turns.assistant_message_id` 是指向 `messages.message_id` 的字符串外键。删除 session 会级联删除其 messages、summaries、turns，删除 turn 会级联删除其 events。

migration v3 新增 quiz/practice 持久化表 `notebook_entries`、`notebook_categories`、`notebook_entry_categories`、`reading_quiz_pending`、`practice_review_state`、`practice_review_events`。`notebook_entries.notebook_entries_id` 是主键，`question_id` 在全表全局唯一；可空的 `session_id`、`turn_id` 分别引用 conversation 表，并使用 `ON DELETE CASCADE`。`reading_quiz_pending` 刻意只保存 `question_id`、`question`、`creat_time`。

migration v4 新增七张 Mastery Path 表和四张 Reading Workspace 表。Mastery 根表使用 `mastery_paths.mastery_path_id`，子表使用 `path_id` 外键；`mastery_path_sessions`、`reading_workspace_sessions`、`reading_workspace_materials` 使用复合主键。删除 mastery path 会级联清理其子记录；删除 reading workspace 只清理映射，不删除独立 session/material；删除 material 会清理 workspace-material 映射，并把 workspace 与 workspace-session 的 `active_material_id` 置空。`notebook_entries.mastery_path_id` 仍是应用层 ID，不追补数据库外键。

migration v5 新增 `library_files` 与 `llm_calls`。文件本体位于本地磁盘，`library_files.library_path` 只保存相对 `UPLOAD_ROOT` 的路径；SHA-256 用于完整性和查询，不限制相同内容重复上传。`library_files` 的路径约束用 `REGEXP` 加三层 `SUBSTRING_INDEX` 复刻了原来的「目录前缀与文件名中的 `lib_<id>` 必须与该行 `id` 一致」，因为 seekdb 没有部分索引也没有等价于 `split_part` 的函数。`llm_calls.usage_json` 是 seekdb `JSON` 且必须是 object；`session_id` 与 `turn_id` 是历史关联标识，刻意不建立外键，因此 conversation 删除不会删除用量历史。本次不 seed 这两张表：文件 metadata 不能脱离真实文件，LLM 用量也不应伪造。字段、约束、索引和删除规则以 migration SQL 为唯一事实来源。

migration v6 给 `sessions` 加 `is_deleted` / `deleted_at`，让 CHAT-06 的「软删 / 恢复 / 永久删」三步可以被表达，并用一条 `CHECK` 钉住两列的一致性；seekdb 没有部分索引，所以活跃行的筛选条件放在索引键的最前面。

migration v7（V2）新增 `knowledge_bases`、`documents`、`document_chunks`。三张表一条链：文件变成文档，文档变成分块，检索读的是分块。没有引入 seekdb 以外的队列或索引——待嵌入的队列就是 `documents` 上的一个谓词（「还没就绪」），检索索引就是 `document_chunks` 上的 `FULLTEXT`（ngram）与 `VECTOR(1024)` 两个索引。`documents.kb_id` 可空，空表示这份文档只作为某一轮对话的附件存在，不属于任何库；`documents.active_revision` + 分块的主键前缀让「重建后原子切换版本」成为一次指针移动，并发检索永远只看见一个版本。

migration v11（V4）给 `practice_review_events` 加 `is_correct` 与 `idx_practice_review_events_entry_outcome`。判分的结果一直写在 `outcome_json` 里，但 JSON 里的字段走不了索引，掌握路径的档位又是第一个按对错聚合大量事件的读方。列**可空且必须可空**：跳过的题没有判断（`judgment` 是 null），回填成 `FALSE` 会把每一条历史跳过变成一次答错，而这两件事在界面上是两回事。回填语句的 `WHERE ... IS NOT NULL` 就是这条语义的执行处。

migration v12（V4）加阅读层的四张新表 `reading_positions` / `reading_marks` / `reading_pairings` / `reading_quiz_pending`。四条**指向同一个方向**的外键各自不同：位置属于**会话**（把会话删了，读到哪儿也该走，「接着上次读」是对一个还在的会话说的），批注属于**材料**而会话只是它写在哪（删一个会话不该带走别人的笔记，所以 `reading_marks.session_id` 可空并在会话消失时置空）；配对的两端都指向材料，`primary <> counterpart` 由 CHECK 钉住。位置带 `revision`，写走 `revision = revision + 1` 的 compare-and-swap，所以两个标签页同时保存时后一个拿到 409 而不是静默覆盖。

migration v13（V4）建 Memory 的四张表 `memory_traces`（L1 观察）/ `memory_facts`（L2 事实）/ `memory_fact_sources`（证据链）/ `memory_audit`（审计）。**L3 没有表**：它是一次计算（`internal/memory/synthesis.go` 的纯函数），落一张快照表就要在每条事实被改、被撤销时记得刷它，而"忘了刷"的症状正是"用户撤销了一条，综合视图里还在"。`memory_facts` 的敏感列叫 `is_sensitive` 而不是 `sensitive`——`sensitive` 是 seekdb 的关键字，DDL 会被直接拒绝（`V4设计方案.md` §0.3 第 1 条）。`owner_id` 是本仓库第一处**账号级的业务列**：Memory 按 MEM-04 是账号级的，而 V1–V3 的表都只到会话这一层，所以它由认证边界传入，没有 owner 的一轮不写 Memory。审计表**没有 UPDATE 与 DELETE 路径**，仓储层只暴露追加。

migration v14（V5）是这一版最宽的一条：给八张表加 `owner_id`（`sessions` / `notebook_entries` / `notebook_categories` / `mastery_paths` / `library_files` / `knowledge_bases` / `reading_materials` / `llm_calls`），重建 `notebook_categories` 的唯一键为 `(owner_id, name)`，把 `turns.own_id` 改名成 `worker_id`，并给 `users.preset` 加一条 `CHECK`。**判据不是"这张表看起来是不是用户的东西"，是"这一行的生命周期挂不挂在会话上"**：挂在（`messages`、`turns`、`turn_events`、`documents`……）就顺着外键走，不挂才自己带一列。`owner_id` 全是 NULL 起步，**迁移一条都不指派**——NULL 在这里读作"V5 之前的历史，不属于任何账号"（见下节）。`notebook_categories` 的唯一键重建是这一版唯一可能失败的语句，它的 Down 也会失败：库里已有两个同名分类时那条语句**必须报错**，静默删行会删掉别人的分类。`turns.own_id` 存的是 worker 的标识，而 `turn.StartRequest.OwnerID` 指的是账号，两个东西同名不同义，所以改掉而不是留着。

migration v15（V5）把 `llm_calls.usage_json` 里的数值提到列上：`provider`、`model`、`prompt_tokens`、`completion_tokens`、`total_tokens`、`duration_ms`、`ttft_ms`、`finished_at`、`outcome`。理由与 v11 一模一样（JSON 里的字段走不了索引），纪律也一样（写的时候同源，读的时候只读列）。**不加 `base_url`**：用量记录不能回答"这次请求发去了哪里"，那是 endpoint 信息。`outcome` 的闭集（`''` / `ok` / `error` / `cancelled`）写在 CHECK 里，`finished_at IS NULL` 就是"这次调用没有回来"——一个可查的状态，不是一个缺失的行。

migration v16（V5）建治理的五张表 `user_grants` / `guardian_links` / `guardian_permissions` / `audit_events` / `workspace_jobs`，它们是一条决定从五个面看过去。**这几张表都没有触发器，也没有为它们写 UPDATE 路径**：撤销一条 grant 就是删行，不持有的 permission 就是不存在的那一行，审计只追加。`guardian_permissions` 一行一条 permission，四个名字写在 CHECK 里（加第五个是一次 `ALTER`，那是刻意的代价）。`audit_events.actor_id` **故意不加外键**：CASCADE 会让删账号删掉他的审计，RESTRICT 会让留下过审计的账号永远删不掉——记录要活得比它记录的事更久。`user_grants.item_id` 是空串而不是 NULL，因为 NULL 在唯一索引里可以重复。这三张表第一次用了 `ON DELETE SET NULL`（`granted_by` / `created_by` / `workspace_jobs.owner_id`）。

migration v17（V5）把 `llm_calls.session_id` / `turn_id` 放开成可空。00005 假设每一次调用都属于某一轮，而 v9 的装饰器记的是进程发出的**每一次**调用——判卷与阅读扩展都是从 HTTP 路由发起的，身后没有会话。**NULL 而不是空串或 `-`**：编出来的值与 NULL 读起来一样、数起来不一样。仍需非空的是"谁付钱、从哪来"（`owner_id` 与 `source`），一次没有账号的调用是"调用点忘了说"，recorder 拒绝制造它。

migration v18（V6）第一次改 `users` 的列（00001 建好之后只被 00014 加过一条 CHECK），加四列回答"这个账号现在还能不能用"：`token_valid_after`（撤销，NULL = 从未撤销）、`failed_attempts`（当前这一轮的连续失败数，写锁时清零）、`failed_window_start`（这一轮什么时候开始的）、`locked_until`（NULL = 没锁）。**四列都不建索引**，因为读它们的就是每请求已经在读的那一行（按主键或按用户名的唯一键），而且**今天没有任何查询问"哪些账号被锁着"**——等那个问题出现时，跟它一起到的才是它的索引。`token_valid_after` 的值**由 Go 写而不是 SQL 的 `UTC_TIMESTAMP()`**：跟它比的是 token 自己的 `iat`，两个时刻必须来自同一个时钟。Down 有一个不对称的后果：**删掉这一列会让所有撤销过的 token 复活**——凭据复活与一列消失不是同一种损失。

migration v19（V6）把"一个掌握路径最多一个活跃交互"交给 schema，形状与 00009 同源：生成列 `open_scope = IF(result_json IS NULL, path_id, NULL)` + 唯一键 `mastery_interactions_one_open`。前面有一段 repair-first 的删除（**预料删 0 行**：正确的写入路径产不出重复，这些行只可能是两个调用者在旧代码下赛出来的），子查询要包一层派生表——从子查询读的那张表里删会被引擎拒（ERROR 1093），而多表 DELETE 那种写法这个引擎不认。Down 是两条语句而不是一条，因为它拒绝在同一条语句里改表又删索引。

当前后端已交付的垂直切片：用户认证、文件上传与读取、会话与消息，经 WebSocket 驱动的对话回合（含工具调用、能力切换与多步进度），知识库的解析与检索，题库与练习，掌握路径，沉浸阅读，账号级的 Memory，V5 的归属、四层授权、Guardian、审计、用量与配额、限速、key 池、文件库回收站、导入导出与两条只读的运行状态，以及 V6 的 token 生命周期（登出 / 改密 / 吊销）、账号锁定与出站边界的接线。现阶段没有 CORS、指标、Tracing、Swagger 或消息队列。`users.password` 只允许保存密码哈希，GORM 模型不得直接作为 API 响应。

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

**已知缺口**：`docling` 需要自己跑起来（默认 `http://127.0.0.1:5001`），本仓库不含它；嵌入用独立供应商，DeepSeek 没有 embedding 接口，所以 `EMBEDDING_*` 与 `LLM_*` 是两套配置；检索参数是全局的，不按库配置；模型目前只读文本，`VISION_*` 配了也没有消费方；~~上传与知识库都还没有属主列，权限只到"有没有登录"这一层~~（V5 的 00014 给它们补了属主列，四层授权见「治理与运维（V5）」）。

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

**已知缺口（V3）**：知识点只以标签形式落在 `extra.knowledge_points` 上，没有可跳转的对象；~~正确率走 `practice_review_events.outcome_json` 提取，没有索引~~（**V6 已改**：正确率改读 00011 的 `is_correct` 列，不再逐行解文档；注意服务这个窗口的是 `idx_practice_review_events_entry_time` 而不是 00011 那个 `..._entry_outcome`，因为窗口的谓词是 `review_time`）；题目的来源会话或材料被删之后会变成"没有归属"，界面要能表达来源已删除；预路由只认中英文那几种写法，写别的说法就留在通用助手里。

### 掌握路径、沉浸阅读与 Memory（V4）

V4 加两个能力（`mastery_path`、`immersive_reading`）、一个只读的"继续学习"聚合、一个账号级的 Memory，以及 QN-05 的批量导入。三条 migration：**00011** 给复习事件补 `is_correct`（V3 留下的欠账），**00012** 加阅读层的四张表，**00013** 建 Memory 的四张表。

**掌握路径（MP-01–05）** 把一门要学的东西拆成知识点，然后按证据推进每一条的档位。它**不是一个请求-应答的接口**，是一轮对话：

- **路径在一轮 turn 里开启**，不在 REST 上（没有 `POST /mastery/paths`）。MP-01 的五个来源——目标、材料、Notebook、Reading——都要先有一次会话才谈得上"从哪个来源来"。所以 `GET /mastery/paths` 是读，建是 `mastery_path` 能力在 `Start` 时做的事。同时推进的路径数**没有上限**：曾经有一个配置键想当这个上限（`MASTERY_MAX_ACTIVE_PATHS`），但它拦不住任何请求——一个会话只要有活跃路径就直接回到那条路径上，要数路径得先没有路径可回，而这两件事读的是同一张表——于是整条链连同那个键一起删掉了（见 [backend/.env.example](backend/.env.example)）。要再定一条上限，得先说清它数的是什么、拦的是什么。
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

**已知缺口（V4）**：掌握路径同时推进的条数没有上限，因为那个上限从没被回答过（`MASTERY_MAX_ACTIVE_PATHS` 与它背后的 `ErrTooManyPaths` 已随定义一起删除，见上）；~~掌握路径与阅读材料都还没有 owner 列，权限仍只到"有没有登录"~~（V5 补上了）；阅读的推荐要读目录里每份材料的开头一窗（材料没有语言列，语言是从正文推断的）；~~`mastery_interactions` 的"同一路径最多一个活跃交互"是服务层的事务内读，不是数据库唯一键~~（**V6 已改**：00019 把它变成生成列 + 唯一键，代码跟着把撞键映射回"已经有一个开着的"，见「收口（V6）」）；租约接管会让一个过期的长 turn 退化成只追加证据；`LLM_CAPABILITY_CONFIG_JSON` 里两个新能力各有一条条目，`immersive_reading` 的上下文预算单独大一档。

### 治理与运维（V5）

V5 加的不是"能做什么"，是**"不能做什么"**和**"做过的每一件被记下来"**：归属、四层授权、Guardian、审计、用量与配额、限速、key 池、出站边界、文件库回收站、导入导出，以及两条只读的运行状态。它没有新 capability、没有新事件类型（15 种已被 V1–V4 用满），零新依赖。

**四条 migration**（00014 归属列 / 00015 用量列 / 00016 五张治理表 / 00017 会话之外的调用）见上节。

#### 权限是四层叠在一个人身上，不是一条"权限等级"

四套东西看起来都像"权限"，它们回答的是**四个不同的问题**：

| 层 | 存在哪 | 回答的问题 | 谁改 |
| --- | --- | --- | --- |
| `role` | `users.role` | 你是不是**这个实例的管理员** | 注册时定，今天只有 `user` 一个值 |
| `preset` | `users.preset` | 你这一类账号**开局**是什么样（`standard` / `learner` / `custom`） | 管理员 |
| grants | `user_grants` | 管理员**单独给这个人**开了/关了什么 | 管理员 |
| Guardian | `guardian_links` + `guardian_permissions` | 这个人能不能**看另一个人的**东西 | 管理员建，Guardian 自己也能撤 |

**这四层不能合成一条等级线**，合成之后就回答不了其中任何一个问题：一个 Learner 可以被 grants 单独开一个工具（第二层说不要、第三层说要），而"他能不能看孩子的学习报告"与他是 learner 还是 standard **完全无关**——第四层是横向的，不是纵向的。把这两件事合成"升职"，要么让"给 Learner 开一个工具"变成不可能，要么让"给一个 standard 用户 Guardian 权限"变成把他升级成管理员。

合成只有一处实现：`authz.Decide(actor, action, target)`。中间件与 handler **只问它**，`internal/middleware` 与各 handler 里不出现 `user.Role == "admin"` 这样的比较（有一条用例遍历 `.go` 文件钉住这件事）。界面只需要读 `GET /api/v1/me/permissions` 一处，不必自己拼。

**生效权限 = 基线(preset) ⊕ grants 的逐项加减**，而基线里有一层**地板**：

- 地板是**代码里的具名常量**（`internal/authz/policy.go`：只有 `chat`），不是一行数据——所以它不能被一条手滑的 SQL 删掉，也不能被一次导入覆盖。部署配置只能往上加，不能往下减。
- **"清空"这个动作没有对象**：删掉全部 grants 只能回到基线，而基线不在库里。所以 AUTH-03 的"管理员不得清空"落到这里是一个**类型问题**而不是一条产品规则；真去做一件会把权限清空的事，回答是 400 `policy_would_be_empty`。
- `preset = ''` 是 V5 之前注册的账号，**按 `standard` 读**——它们今天能做的事就是 standard 的所有事，读成"受限"是一次凭空收紧。
- 一个 grant 既能**开**也能**关**（`allowed` 布尔），先有"关"才有"基线之上加减"这句话。

**Guardian** 是唯一一条"读别人的"的合法路径。一行 = 一条关系，**撤销就是删行**（历史在审计里，表里没有第二个状态），所以"表里有一行"直接等于"关系还在"。逐项 permission 一行一条，四个名字是闭集：`view_reports`、`assign_materials`、`manage_restrictions`、`reset_credentials`。两条跨表判断（被监护的必须**真的是** learner、监护人不能是 learner）落在服务层的事务里，因为 seekdb 的 CHECK 不能跨表。`GET /api/v1/guardian/learners/{userID}/reports` **每一次读都写一行审计**（AC-11）。

**401 与 403 是两件事**，不许折叠：没登录是 401 `authorization_required`（→ 去登录），登录了但不许是 403 `permission_denied`（→ 去找管理员）。界面要能分别画这两件事。**拒绝也留痕**：中间件是拒绝产生的地方，所以"拒绝写审计"接在中间件上（`surface.denied`），接在每个 handler 上就变成"每个 handler 漏记一次"，而漏记不会失败——它会让审计说没事发生。

#### 无主数据是一个要画的状态

`owner_id IS NULL` 不是"忘了填"，是**定义好的**：**V5 之前的历史，不属于任何账号**。00014 一行都不指派——猜错一次的代价是 A 的会话出现在 B 的列表里，而这没有任何东西会提醒任何人。

- **只有管理员看得见**无主的行。这不是特权，是把一批无主的行交给唯一有权力处置它们的人。
- **出口是认领**：`POST /api/v1/sessions/{id}/claim` 把一条会话收归自己，连带它的 messages / turns / events；认领是一次显式动作，所以它总会留下一条审计（`session.claimed`）。
- **`go run ./cmd/seed` 写出来的行也没有主人**：seed 不指派 `owner_id`，所以全新部署里 `test_user` **看不到**那条种子会话，只有管理员看得到（一次 `claim` 就归自己）。这是"新库里一条无主行都没有"这条设计意图与当前实现的一处出入，收尾时按代码记。
- 界面上**只画成空列表会让用户以为数据丢了**（§7.3 第 10 条）。

#### 审计是一根指针，不是一个载荷

`audit_events` 回答"谁、何时、做了什么、对象是谁"：`actor_id`（**故意不加外键**——CASCADE 会让删账号删掉他的审计，RESTRICT 会让留下过审计的账号永远删不掉）、`actor_name`（写入时的用户名**快照**：改名之后回看三个月的记录应该看到当时的名字）、`action`（闭集）、`target_kind` / `target_id`（**一个指向**）、`subject_id`（这一行说的那个账号）、`result`（`ok` / `denied` / `failed`）、`detail_json`（**只放 id、计数与原因码**，不放自由文本、不放用户输入）、`request_id`。

**没有列能装下 prompt、token 或密码**——所以"审计里没有这些"不靠过滤，靠没有地方放。表**只有追加**，仓储层没有 UPDATE、没有 delete-by-id，`Prune` 按年龄且只按年龄。保留期 `AUDIT_RETENTION_DAYS` 只被这一条语句读到，**不起定时作业**（清理由调用方触发）。页大小 `AUDIT_MAX_PAGE_SIZE` 是**上限不是默认值**：要得比它多的请求被拒绝，而不是被悄悄截短。

#### 用量是"装饰"出来的，配额从它算出来，限速在内存里

**用量不是各处上报，是装饰**：装配处把真的 `llm.Provider` 包一层，写行发生在装饰器里。忘了一个调用方就永远不会出现在用量里——不报错、不告警，只是数字偏小，而数字偏小看起来像"这个月用得少"。**一次调用写两段**：开始前写一行（身份与归属），结束后补数值列。所以**一次没有回来的调用是一行 `finished_at IS NULL` 的行，不是一个缺失的行**——那正是最需要看见的一种失败。`GET /api/v1/usage` 分 `model` / `provider` / `source` / `session` / `day` 五种 breakdown，**只读列**（同一个值存了两处，纪律是写的时候同源、读的时候只读列），响应里**没有 base URL**（AC-13）。`internal/embed` 的嵌入调用不记用量：它是另一个客户端（走 `EMBEDDING_*`），没有为它准备配置节。

**配额 = 一次聚合查询**（`llm_calls` 上 `owner_id` + 时间窗），不另建表——累计量已经在库里，抄一份就会漂，而且被拒的那个数与用量屏上读到的那个数必须是同一次读。`0` 表示不限，**也是默认**（阈值未定，"默认把所有人限死"比"默认不限"坏得多）。超限是**结构化的拒绝**：429 `quota_exceeded` + `details{item, limit, used, resets_at}`，**不给 `Retry-After`**——那是要向用户解释的事，不是要睡过去的事。窗口是 **UTC 自然日 / 自然月**，不是"最近 24 小时"：滚动窗口没有清零的那一刻。日额度数"发起"、月额度数"花掉"：没回来的调用照样是一次被发出去的调用，否则一个挂起 20 次调用的账号还能再被放行 20 次。**被拒的调用不花一个 id、不写一行、不发一次请求**。

**限速与配额是两个问题**：限速问"你多快"（秒/分钟，**进程内存**，重启从头开始是正确行为），配额问"你多少"（天/月，**在库里**，必须活过重启）。限速是按账号 + 动作的令牌桶，**只挂三类路由**：模型调用（`/ws`、`POST /practice/reviews`、`POST /reading/materials/{id}/extensions/{name}`）、外部检索（`POST /knowledge-bases/{id}/search`）、文件上传（`POST /library/files`）。429 `rate_limited` + `details{action, retry_after}` + 标准 `Retry-After` 头；这个数字**向上取整且最小是 1 秒**（一个被告知"0 秒后重试"的客户端会立刻重试然后再次被拒）。挂哪几条路由**由一条遍历路由表的用例钉住**，以后有人加一条昂贵路由而没读设计，报的是"未被限流的路由"而不是让它悄悄溜过。**多实例会静默变成每实例限速**（实际额度是 N 倍），本仓库单实例部署。

**key 池**：`LLM_API_KEYS` 是逗号分隔的列表（一把也行，行为与从前一样），轮换 + 每把的连续失败计数 + 冷却。**只有 429 算在一把 key 头上**（500 是上游不舒服、401 是键配错了，都不该用一次 60 秒的冷却把配置错误伪装成暂时故障）；一次成功清零计数；全部在冷却时仍然发**冷得最久**的那把（"没有 key"会把上游的背压变成我们这边的一次故障）。冷却是每把 key 一个"到什么时候为止"，判断它就是一次时间比较——**没有后台协程去解除冷却**。错误信息里只有序号，没有 key 的值。`LLM_API_KEY` → `LLM_API_KEYS` 是唯一一次改已有的键名，**不做兼容**：升级前先改 `.env`。

**出站边界**（`internal/outbound`）判的是"**谁给的地址**"，不是"地址私不私"：同一个 `192.168.1.10`，在 `ExternalMediaPolicy`（外部内容里的媒体 URL）下必须拒，在 `LANServicePolicy`（局域网里由使用者配置的服务）下必须放——两套策略是两个类型，不是同一个函数的两个参数。用 `net/netip` 而不是 `net`，因为 `net.IPNet.Contains` 会把 `::ffff:127.0.0.1` 当成普通 IPv6 地址放过去，那正好是 SSRF 最常用的一种绕法。

**V6 把它接上了三处**，接线点选在 `DialContext` 而不是调用点上——"记得调用"不是一种机制，而拨号是每一次出站连接都必须经过的那一个地方。三个客户端各取一条策略：docling 走 `LANServicePolicy`（它的默认地址 `http://127.0.0.1:5001` 本来就是一台局域网服务），LLM 与 embedding 走 `ExternalMediaPolicy`（公网主机）。接线在 `assemble.go` 与 `kbassembly.go` 两处，`internal/llm`、`internal/embed`、`internal/parse` 只收一个 `outbound.DialFunc` 并把 `transport.DialContext` 交给它，它们**不 import `outbound` 以外的策略**，也不自己判断地址。

**接上它防的是配置写错的那扇门，不是用户输入的那扇。** 三个客户端的 base URL 全部来自 `.env`，没有一个来自请求数据，所以这一版没有关掉任何一条"用户能让服务器去连哪里"的路——那条路在 V5 就不存在。它挡的是把 `PARSER_DOCLING_BASE_URL` 填成一台公网机器、把 `LLM_API_BASE` 填回环地址这类事。这条记录（V5 的"零调用方"）到此结清，但结清的是"有人调用"，不是"防住了用户输入"。

判据取在**解析之后**：名字先被解析成一组地址，**组里每一个**都要过策略，然后拨的是**这组地址**而不是那个名字——把名字交回给拨号器等于让它再解析一次，而第二次解析是第二个答案，策略放过的那个名字可能回来指着 `127.0.0.1`，那样这一整层就只是对一个字符串的检查。它拦不住的只有两件事，都写在 `dialer.go` 的文件头：**名字仍然会被解析**（拦的是连接，不是查询，别的机器上的解析器还是知道这个名字），以及**自己装了 `Transport` 的调用方**（那是一次代码评审的问题，运行期没有任何机制能回答它）。

#### 文件库的删除有两级，导入导出是两次请求

- **软删（进回收站、字节还在）与 purge（字节没了、不可恢复）**是两回事。`is_deleted` / `deleted_at` 从 00005 起就是死列而 CHECK 一直在守它们，V5 把它们接上（一次写两列，CHECK 才满足）。**purge 之前会问一句"还有人在用吗"**：`documents` 或 `reading_materials` 里还有指向它（或指向它的文档）的行，就 409 `file_in_use` 并说出是谁挡着。
- **重复上传返回同一个 id**：上传前按 `sha256` 在自己名下的行里查一次，**活行优先、其次最早的**；命中一条软删的行就把它**恢复**而不是新建一行（`library_files_path_unique` 盖的是全部行，seekdb 没有 partial index，所以软删的行仍然占着它的路径）。于是"恢复"与"去重"是同一个动作。**去重的范围是每个账号自己**：跨账号命中会把 B 拿不到的一个 id 递给 B。
- **回收站是 `?deleted=true`，不是单独一条路由**——少一个要记得加归属过滤的地方。下载是唯一不走信封的一条（`Content-Disposition` 走 `mime.FormatMediaType`，加 `X-Content-Type-Options: nosniff`），`library_path` 与绝对路径不出现在任何响应里。
- **导入导出继承的是 §12.4 的三条纪律，不是它的三条接口**：本仓库一个 seekdb、一个上传根，"把数据从一个 Workspace 搬到另一个"没有对象。做的是把**选定的会话、材料、题库与知识库**打成一个 ZIP 再读回来。包里**没有账号设置、Memory、凭据、`llm_calls`**——不是过滤器，是封闭集：`Kind` 里没有这些名字，写入器会拒绝**整个包**而不是跳过一条。**素材不带正文、知识库不带分块与向量**（正文在文件库里，向量是文件加某个 embedding 模型的函数），所以跨实例导入一个包时它们是目录，正文要另外想办法。
- **预览零写入**，冲突不是错误而是预览里的一个条目；`sha256` 对不上就整体不落；**有活跃 turn 就拒绝**（粒度是整个实例，不是这次迁移涉及的那几个会话）。**恢复靠"记下来写过什么"**（`written_json` 在**每一批之后**写），不靠回滚——一个 ZIP 不可能是一个事务。**没有"续着上次接着导"**。
- 七条路由**挂 token 不挂 surface**：一个包是账号自己的东西离开又回来，加一层 surface 就是让某个 preset 能拒绝一个账号取自己那份副本。V6 补上的第七条是**下载**（`GET /api/v1/workspace/exports/{jobID}/content`），它照库那条下载路由写：`application/zip` 流式返回，`Content-Disposition` 走 `mime.FormatMediaType`，加 `X-Content-Type-Options: nosniff`——**第二处不走 JSON 信封**。归属在打开任何东西之前就应用，所以**别人的 jobID 与不存在的 jobID 拿到同一个 404**，谁也不证实谁。这里有一个**用例钉不住的地方**，是这一版唯一一处：包是流式发出去的，而 `workspace.Open`（会把整个包解析进内存的那个读取器）刻意**不在这条路上**——一个被解析的 512 MiB 包发出去的字节仍然与磁盘一致，只有进程的内存会说出区别。

#### 两条只读的运行状态

`GET /api/v1/system/version` 报版本、构建时间、Go 版本、migration 版本（`go run` 下如实说 `(devel)`，不报空串）。`GET /api/v1/system/status` 报运行模式（`APP_ENV`）、依赖的逐项 readiness、活跃回合数、索引 worker 是否在跑、以及 `llm_calls` 里**未收尾**的行数。**物理路径不报**（`UPLOAD_ROOT` / `WORKSPACE_ARCHIVE_ROOT` 只存在 runtime），**内存不报**，**可写设置与更新检查不做**。

**已知缺口（V5）**：~~`internal/outbound` 零调用方~~（**V6 已接上三处**，见上）；`cmd/seed` 不指派 `owner_id`（种子数据是无主的）；~~导出的包没有下载路由~~（**V6 已补**，见上）；限流按账号不按设备，且多实例下会静默放大；~~`MASTERY_MAX_ACTIVE_PATHS` 那一条仍在等产品裁决~~（已按代码处置：整条链删掉，见上）；审计与用量的保留期只被一条按窗口删的语句读到，没有到点就删的作业（**V6 给了执行的地方**：`go run ./cmd/cleanup`，但它仍然要有人去挂 cron，见下节）；导入的恢复是"回到这次导入之前"，不是"接着导"；~~V5 这一版的四条迁移只在空库上向前跑过，把既有部署的库升上来的那条路没有用例~~（**V6 已补**：`upgrade_db_test.go` 先 `UpTo(17)` 再灌行再升上来，另加一条 down 再 up，见「验证命令」）；包的七条路由一个面都不挂（`/workspace/*`、`/workspace/jobs/*` 那一组），这是有意的——一个包是"账号自己的东西出去再回来"，没有哪个面能表达这条规矩；**而没有用例钉着"这七条无面"这件事**——`router_surfaces_test.go` 钉三组（治理挂 `administrative`、系统读挂 `system`、**V6 的登出与改密不上任何策略**），包这一组不在其中，所以将来有人给这七条里的一条补上一个面不会红。

### 收口（V6）

V6 加的不是功能，是**让已经有的东西有客人**：两条 migration、四条路由（见「认证」里的表）、一个子命令、三个配置键，**零新 capability、零新事件类型、零新依赖**。认证那半边是真正的新东西——它是本仓库第一次把"当下的授权状态"写进库。

#### 撤回是一个时刻，不是一个列表

`users.token_valid_after` 存一个时刻，**签发时间早于或等于它（`iat <= tva`，含同一秒）的 token 一律作废**。不是一张吊销表：一条 token 记录一张表要在每次请求上多读一次、要给清理作业多一个对象，而这套东西要回答的问题只有"这个账号现在还能不能用自己的旧凭据"——一个时刻就够了。**NULL 表示从未撤销过**，与"一个很早的时刻"不是一回事，所以这一列没有默认值。

**两个时刻用同一个时钟**：`tva` 由 Go 写入而不是 SQL 的 `UTC_TIMESTAMP()`，因为它要跟 token 自己的 `iat` 比，而 `iat` 来自应用的时钟——差一秒的库钟能让刚签的 token 看起来比撤销还新，规则就会安静地不生效。

**秒级精度有一个角，取的是 fail-closed**：`iat <= tva` 判死，代价是"在同一秒里撤销又登录"会被拒。反方向（`iat < tva`）会把"同一秒内完成撤销"这件真事读成没发生——**一个撤销请求返回 204 而 token 还活着**，那是比多登一次严重得多的错误。而这条角要一个人在同一秒里走完一次往返才碰得到。

#### 锁定数的是"一轮"，不是历史

窗口内连续失败满 `AUTH_LOCKOUT_THRESHOLD` 次就写 `locked_until` 并**清掉计数**，所以 `failed_attempts` 数的是当前这一轮；`failed_window_start` 是这一轮什么时候开始的（超过窗口进来的失败从 1 重新起算）。**故意没有"最后一次失败是什么时候"这一列**——这一版要问的只有"当前这轮什么时候开始的"。`locked_until` 为 NULL 就是没锁，**而一个已经过去的时刻意思一样**（判据是"`locked_until` 晚于现在"），所以过期的锁不需要谁去清，停机穿越了窗口期也一样直接放行。

#### 一条规则交给 schema，第二次

"一个掌握路径最多一个活跃交互"以前只活在 `Ask` 的事务内读里。**那一次读在这个引擎上拦不住第二个写者**：READ COMMITTED 只取记录锁不取间隙锁，"这条路径没有开着的交互"正好是一个空区间，读空区间什么都不锁。00019 用的是 00009 留下的那个形状——生成列 `open_scope = IF(result_json IS NULL, path_id, NULL)`（**开着的行占住路径，答过的行占 NULL**，而唯一索引容许任意多个 NULL）+ 一个唯一键；前面是一段 repair-first 的删除（地址在引擎上要先包一层派生表，直接从子查询读的那张表里删会被拒）。

**代码跟着改了，这不是可选项**：插入撞上这个唯一键时要映射回 `*OpenInteractionError`——原来这个错误只在事务读里产生，不改的话并发下的第二个调用者拿到的是一次 500，而它应该拿到的是"这条路径上已经有一个开着的"。

#### 正确率改读列

`practice_review_events.is_correct`（00011 建的）以前没人读，统计查询还在逐行解 `outcome_json`。V6 换了那一半查询。**行为等价**：没人判过的行（跳过）在两边都是 NULL，`NULL = TRUE` 与 `is_correct = TRUE` 都是 NULL，所以跳过两边都不计入。**不需要 migration**——那一列和它的索引早就在。服务这个窗口的索引是 `idx_practice_review_events_entry_time`（窗口的谓词是 `review_time`，它领这个键），00011 那条 `..._entry_outcome` 把 `is_correct` 放在第二位，服务的是"这道题的历次复习怎么样"，不是这个问法。

#### 保留期有了执行的地方

`audit.Service.Prune` 与 `usage.Service.Prune` 实现完整但零生产调用方，V6 把它们接给 `go run ./cmd/cleanup`。**做成子命令而不是进程内的定时器**，因为它要回答两个运维必须能问的问题："我是不是跑了两次"与"它失败了吗"——cron 能被告知只留一份，非零退出码是调度器能报出来的东西，而进程内的 ticker 把这两个答案都丢掉。它**只删行**，删的都是已经过了窗口的记录（审计不再回答的那些、已经报过数的用量），并把计数打出来——**"0"是信息**，它区分了一个在工作的窗口与一个没人跑过的窗口。

**已知缺口（V6）**：**没有刷新 token、找回密码、邮箱验证、单端登出、2FA / CAPTCHA**——后三者都要一条邮件通道，那是本仓库第一个真正的外部服务依赖。**登出是全端下线**（本仓库没有"设备"这个概念），要做单端登出得先有 `jti` 与一张按 token 的吊销表。**锁定可以被人用来锁别人**：失败计数按用户名计，所以任何一个知道用户名的人都能把那个人挡在门外，直到窗口过去或管理员解锁——这是机制的固有属性，缓解只有阈值 + 窗口 + 管理员那条解锁路由。**撤销的精度是一秒**，所以"在同一秒里撤销又登录"会被拒（取 fail-closed，见上）。**`cmd/cleanup` 的真跑做过一次，但删的是 0 行**（2026-10-15，本地库两张表都空着，退出 0）——它删真行且不可回滚，所以这次跑之前先数过行数。`cmd/` 下的子命令按本仓库的惯例没有用例——它们做的事全在 `Prune` 那两个方法里，而那两条的用例在 `internal/audit`（三条）与 `internal/usage`（两条），都在真库上；子命令自己剩下的只有读配置、开库、决定退出码。**"删非零行"这件事只有用例做过**，而那是这个命令真正的判据。**这一版仍然没有把一条请求真的发给过上游**（见「验证命令」）。

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
- `.product/` 与项目代码无关，不查阅、不修改；唯一例外是每个版本收尾时核对 `.product/设计说明书.md`（文档与代码矛盾时改文档、不改代码，并先报告差异）。
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

**这套测试不证明什么**（写在这里，免得把"全绿"读成语义上的全绿）：

- ~~**没有一条请求真的发给过上游模型。**~~（**2026-10-15 三条路都走过了**。**对话**：`go run ./cmd/turnprobe` 对着真起来的服务驱动了一轮完整 `chat`，上游 `deepseek/deepseek-chat` 真的答了，`llm_calls` 落下两行真实用量——`chat` 390/32 tokens、1316ms、首字 893ms；`turn.title` 105/4、1185ms、首字 674ms。**检索**：上传一份 markdown 建库挂文档，本地 `text` 引擎解析 98 个字符后，索引 worker 真的发了 embedding，`document_chunks.embedding`（`VECTOR(1024)`）落下一行非空向量，两次不同 query 的检索都命中同一个 chunk——索引与查询两侧都真的出过网。**解析（同日补齐）**：本机原本没有 docling，PDF 挂进知识库只会**真的拨一次** `http://127.0.0.1:5001` 然后**被拒**（503 `parser_unavailable`，17ms），验到的是"发得出去"、不是"读得回来"。现在在一台**真的 docling** 上跑通了。先直接问引擎：977 字节的 PDF 进去，回来 `status=success`、`errors=[]`、`md_content` 317 字符，就是那份 PDF 的正文；再走完整链路——上传 → 建库 → 挂文档 → 重建，文档以 `engine=docling`、`text_chars=317` 落地，`vector_state` 从 `pending` 走到 `ready`，而库里 `document_chunks.content` 存的就是 PDF 里那几句话。**仍没验的是**：扫描件（要 OCR）、Office 格式（`.docx` / `.pptx` 走同一引擎但没试过）、以及大文档；而且这台 docling 是临时装起来的，不在仓库里。**仍然没碰的**：工具调用与 SSE 之外的能力；而且每条路都只有一轮，"稳定"没验。用量、配额、key 冷却、限流里那些注入的假 provider / 假 429 / 假时钟仍在原位——它们验证的是时序，不是"上游真的这么答"。）
- ~~**迁移只在空库上向前跑过。**~~（**V6 已补**：`internal/data/migration/upgrade_db_test.go` 走的是部署真正处在的两种状态——**先 `UpTo(17)` 再灌行、然后升上去**（灌进去的行一条不少、判决没有被改写、新的四列读回"从未撤销、从未失败、从未锁定"，再跑一次 `Up` 应当无事可做），以及**在有行的库上 down 再 up**；`testdb.OpenAt` 是为此新增的入口——建库、清空、就停在那里。**仍然没验的是**：把某个具体旧版本的真实生产库搬上来的那种形状，以及一次跑到一半失败的迁移。）
- **`go run ./cmd/cleanup` 只跑过一次，而且那次没删到行。** 本地库的两张表当时都是空的，所以那次验的是整条接线（读配置 → 开库 → ping → 两次 prune → 退出码），**不是"删对了行"**——那是 `internal/audit` 与 `internal/usage` 里那五条真库用例的事。

`cmd/` 下的子命令（`migrate` / `seed` / `cleanup` / `turnprobe`）按本仓库的惯例**没有用例**：它们做的事都在被它们调用的那些包里，而那些包对真库有用例。

清除到期的审计与用量（会真的删行，先在库上确认再跑）：

```bash
cd backend
go run ./cmd/cleanup            # 删掉过了 AUDIT_RETENTION_DAYS / USAGE_RETENTION_DAYS 的行，打印条数
go run ./cmd/cleanup -h         # 用法
```

前端：

```bash
cd frontend
npm run type-check
npm run build
```
