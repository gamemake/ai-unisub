# Claude、Codex、Grok 订阅转 API 实现方案

## 1. 目标与边界

本项目实现一个严格的“原生协议订阅转发网关”：Claude 只接受 Anthropic 原生请求，Codex 和 Grok 只接受各自的 Responses 原生请求。每个订阅账号拥有独立的下游 API Key，API Key 与订阅账号一对一绑定。网关仅负责下游鉴权、绑定账号解析、OAuth 刷新、必要请求头、HTTP/SSE/WebSocket 转发和额度查询，不做任何跨协议转换。

### 1.1 不实现的能力

- Anthropic Messages 与 OpenAI Responses 之间的转换。
- Chat Completions 与 Responses 之间的转换。
- Claude Code 通过 Codex 或 Grok 账号调用。
- Codex 通过 Claude 账号调用。
- 自动修改 `tools`、`messages`、`reasoning` 等业务结构。
- 将非流式请求自动转换成流式请求，再缓存并组装响应。
- 图片、视频和声音生成、编辑、下载或实时语音接口。
- 多账号池调度、粘性会话和跨账号故障切换。
- 多用户、充值、支付、套餐和按量计费。

### 1.2 实现的能力

- Claude 原生 Messages 请求透传。
- Codex 原生 Responses 请求透传。
- Grok 原生 Responses 请求透传。
- OAuth 授权、刷新和凭证明文存储。
- 每个订阅账号配置一个独立下游 API Key。
- API Key 一对一解析订阅账号，不进行账号池调度。
- 单账号并发限制、可配置的并发等待队列时长、限流状态和健康状态维护。
- 每个账号可独立配置 HTTP 或 SOCKS5 代理，代理地址明文保存。
- 每个账号使用独立的 HTTP Client、Transport 和 keep-alive 连接池，账号之间不复用 HTTP 连接。
- 必要的上游认证头和官方客户端身份头。
- SSE 原样转发。
- Codex Responses WebSocket，完整对齐 Sub2API 的直连、连接池和 HTTP-SSE bridge 行为。
- `/v1/models`、`/v1/responses` 及安全的 Responses 子路径转发。
- 单管理员账号和管理页面。
- 在管理页面查询每个订阅账号的上游使用额度。
- SQLite 本地持久化。

## 2. 对外 API 设计

API Key 已与订阅账号一对一绑定，因此可以同时提供标准根路径和带平台命名空间的别名。根路径先通过 API Key 找到唯一账号，再校验该账号的平台是否支持当前端点；不会根据模型或请求内容切换平台。

| 方法和入口 | 允许账号 | 行为 |
|---|---|---|
| `GET /v1/models` | Claude、Codex、Grok | 转发或返回该账号上游的原生模型清单，不混合其他平台模型 |
| `POST /v1/messages` | Claude | Anthropic Messages 原生透传 |
| `POST /v1/messages/count_tokens` | Claude | Anthropic Count Tokens 原生透传 |
| `POST /v1/responses` | Codex、Grok | 对应账号的 Responses 原生透传 |
| `POST /v1/responses/*subpath` | Codex、Grok | 校验安全路径后，把子路径原样追加到对应上游 Responses URL |
| `GET /v1/responses` | Codex | WebSocket Upgrade，完整对齐 Sub2API Codex Responses WS 行为 |
| `POST /v1/chat/completions` | Grok，可选 | 仅在 xAI 上游原生支持时透传，不转换到 Responses |

同时保留以下显式别名，便于排障和固定平台配置：

```text
/claude/v1/messages
/claude/v1/messages/count_tokens
/claude/v1/models

/codex/v1/models
/codex/v1/responses
/codex/v1/responses/*subpath

/grok/v1/models
/grok/v1/responses
/grok/v1/responses/*subpath
/grok/v1/chat/completions
```

显式别名必须与 API Key 所绑定账号的平台一致，否则返回 HTTP 403。项目不提供任何图片、视频或声音路由。

`GET /v1/models` 的规则：

- 保留 `client_version` 等上游支持的查询参数。
- 使用 API Key 绑定账号的认证和平台适配器查询模型。
- 不合并不同账号或不同平台的模型。
- 不做模型别名、模型替换或跨平台映射。
- 上游没有可用的模型清单接口时返回明确的 404/501，不伪造其他平台格式。

`/v1/responses/*subpath` 的规则：先完成 API Key 鉴权，再校验路径片段，最后将安全后缀原样追加到绑定账号的 Responses 上游路径。路径不安全时必须拒绝，不能降级成 `/v1/responses`。

客户端统一使用项目自己的 API Key：

```http
Authorization: Bearer unisub_xxxxxxxxx
```

网关收到请求后，必须删除客户端传入的上游认证信息：

```text
Authorization
x-api-key
x-goog-api-key
Cookie
chatgpt-account-id
```

随后注入该 API Key 唯一绑定订阅账号的认证信息。

如果后续需要让原生 SDK 使用独立域名，也可以配置：

```text
claude.example.com/v1/messages
codex.example.com/v1/responses
grok.example.com/v1/responses
```

域名只负责限制允许的平台；最终仍以 API Key 所绑定账号为准，并且不进行协议转换。

## 3. 推荐技术架构

项目当前为空目录。考虑到目标是单节点、单管理员和账号级独立 API Key，建议采用以下技术栈：

- Go
- Gin
- SQLite，启用 WAL、`busy_timeout` 和外键约束
- `net/http` 自定义 Transport
- 成熟的 WebSocket 库与独立连接管理器
- 依靠数据库文件权限与部署环境隔离保护明文凭据
- Docker Compose

SQLite 数据库文件只允许一个服务实例直接写入。本方案不支持多个服务实例共享同一个 SQLite 文件；如果未来需要横向扩容，再迁移到 PostgreSQL，并重新设计分布式锁和连接状态存储。

逻辑组件如下：

```text
HTTP Server
  ├─ Downstream Auth
  ├─ Provider Router
  ├─ Request Validator
  ├─ API Key → Account Resolver
  ├─ Token Provider
  ├─ Upstream Client
  ├─ SSE Relay
  ├─ WebSocket Relay
  └─ Error/Rate-limit Observer

Control Plane
  ├─ Account Management
  ├─ Claude OAuth
  ├─ Codex OAuth
  ├─ Grok OAuth/SSO
  ├─ API Key Management
  ├─ Account Usage Query
  ├─ Single Admin Authentication
  └─ Account Health

Background Workers
  ├─ OAuth Token Refresher
  ├─ Account Health Checker
  ├─ Usage/Quota Refresher
  └─ Request Log Cleanup
```

建议目录结构：

```text
ai-unisub/
├─ cmd/
│  └─ server/
│     └─ main.go
├─ internal/
│  ├─ config/
│  ├─ database/
│  ├─ crypto/
│  ├─ model/
│  ├─ repository/
│  ├─ auth/
│  │  ├─ apikey/
│  │  └─ oauth/
│  ├─ provider/
│  │  ├─ claude/
│  │  │  ├─ oauth.go
│  │  │  ├─ token.go
│  │  │  ├─ transport.go
│  │  │  └─ handler.go
│  │  ├─ codex/
│  │  │  ├─ oauth.go
│  │  │  ├─ token.go
│  │  │  ├─ identity.go
│  │  │  ├─ transport.go
│  │  │  └─ handler.go
│  │  └─ grok/
│  │     ├─ oauth.go
│  │     ├─ sso.go
│  │     ├─ token.go
│  │     ├─ identity.go
│  │     ├─ transport.go
│  │     └─ handler.go
│  ├─ relay/
│  │  ├─ http.go
│  │  ├─ sse.go
│  │  └─ websocket.go
│  ├─ admin/
│  ├─ usage/
│  └─ worker/
├─ web/
├─ migrations/
├─ deploy/
├─ go.mod
└─ README.md
```

## 4. 统一数据模型

### 4.1 accounts

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE accounts (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT NOT NULL,
    provider              TEXT NOT NULL CHECK (provider IN ('claude', 'codex', 'grok')),
    auth_type             TEXT NOT NULL CHECK (auth_type IN ('oauth', 'api_key')),

    credentials_json      BLOB NOT NULL,

    metadata_json         TEXT NOT NULL DEFAULT '{}',
    proxy_url             TEXT,

    status                TEXT NOT NULL DEFAULT 'active',
    enabled               INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),

    concurrency_limit                  INTEGER NOT NULL DEFAULT 1,
    concurrency_queue_timeout_seconds  INTEGER NOT NULL DEFAULT 0,

    token_expires_at      TEXT,
    rate_limit_reset_at   TEXT,

    quota_json            TEXT,
    quota_checked_at      TEXT,
    quota_error           TEXT,

    last_used_at          TEXT,
    last_error            TEXT,

    created_at            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at            TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

`provider` 可取：

```text
claude
codex
grok
```

`auth_type` 可取：

```text
oauth
api_key
```

虽然目标是订阅转发，但保留 `api_key` 类型有利于调试和降级。初版管理界面可以不暴露该类型。

`proxy_url` 保存账号级代理 URL，支持：

```text
http://host:port
http://username:password@host:port
socks5://host:port
socks5://username:password@host:port
```

代理 URL 必须包含协议、主机和端口，不允许携带 path、query 或 fragment。它与账号凭据一样以明文写入数据库，管理接口只返回 `proxy_configured`，不得回显代理地址、用户名或密码。未配置账号代理时使用服务默认网络环境；配置代理后，该账号的所有 HTTP/SSE 上游请求都必须使用该代理。

`concurrency_queue_timeout_seconds` 表示账号达到 `concurrency_limit` 后，请求等待并发槽位的最长时间，允许范围为 `0` 至 `300` 秒，采用“内置缺省值 → 环境变量 → 账号”的覆盖层级：

- 内置缺省值：`180` 秒，即 3 分钟。
- 环境变量：`UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS`，覆盖内置缺省值；未设置或设置为 `0` 时继续使用内置缺省值。
- 账号值为 `0`：继承环境变量解析后的全局缺省值。
- 账号值大于 `0`：覆盖全局缺省值，仅作用于当前账号。
- 大于 `0`：等待期间只要有请求释放槽位，就继续处理当前请求。
- 等待超时：返回 `429 concurrency_limited`，错误信息明确表示并发等待队列超时。
- 客户端在等待期间断开：立即取消等待，不调用上游。

该字段新增时必须提供 SQLite 在线迁移：启动阶段通过 `PRAGMA table_info(accounts)` 检查列是否存在，旧数据库缺列时执行 `ALTER TABLE ... ADD COLUMN ... DEFAULT 0`，并记录新的 `schema_migrations` 版本。若数据库中存在旧的 `concurrency_queue_timeout_ms` 字段，则按 `(ms + 999) / 1000` 向上取整迁移到秒，迁移只执行一次。

### 4.2 api_keys

```sql
CREATE TABLE api_keys (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    key_hash        BLOB NOT NULL UNIQUE,
    key_prefix      TEXT NOT NULL,

    enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),

    rpm_limit       INTEGER,
    concurrency     INTEGER,
    expires_at      TEXT,

    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);
```

每个账号必须且只能关联一个下游 API Key；`account_id UNIQUE` 保证一个账号最多一个 Key，应用层必须在同一 SQLite 事务中创建账号和 Key，从而保证账号创建完成时恰好有一个 Key。API Key 只在创建或重置时显示一次，数据库只保存哈希，不保存明文。请求通过 `key_hash` 直接解析唯一账号，不再查询账号池，也不进行粘性绑定。

### 4.3 admin

系统只允许一个管理员账号。首次启动通过初始化向导或 CLI 创建，之后不开放管理员注册接口：

```sql
CREATE TABLE admin (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    username        TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    totp_secret     TEXT,
    created_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

应用层创建管理员时固定写入 `id=1`，已有记录时拒绝再次初始化。密码使用 Argon2id 或 bcrypt，管理员会话使用短期 JWT 或随机服务端 Session Cookie。

### 4.4 usage_logs

额度页面主要显示上游订阅额度，同时保留本地请求统计，便于确认消耗来源：

```sql
CREATE TABLE usage_logs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      INTEGER NOT NULL,
    provider        TEXT NOT NULL,
    endpoint        TEXT NOT NULL,
    model           TEXT,
    status_code     INTEGER NOT NULL,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    request_count   INTEGER NOT NULL DEFAULT 1,
    started_at      TEXT NOT NULL,
    finished_at     TEXT,
    request_id      TEXT,

    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX idx_usage_logs_account_started
ON usage_logs(account_id, started_at DESC);
```

本地统计不是计费系统。若上游响应没有 usage 字段，只记录请求次数、状态码和持续时间，不推算 Token。

### 4.5 按日请求日志分表

每次转发接口调用都必须记录，包括鉴权失败、Provider 不匹配、请求校验失败、RPM 限流、并发队列超时、上游连接失败和正常响应。日志按系统当前时区中请求开始时所在的自然日写入独立表：

```text
request_logs_YYYYMMDD
```

每张表结构一致：

```sql
CREATE TABLE request_logs_YYYYMMDD (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id    INTEGER,
    api_key_id    INTEGER,
    provider      TEXT,
    method        TEXT NOT NULL,
    path          TEXT NOT NULL,
    status_code   INTEGER NOT NULL,
    started_at    TEXT NOT NULL,
    finished_at   TEXT NOT NULL,
    duration_ms   INTEGER NOT NULL,
    request_id    TEXT,
    error_type    TEXT
);
```

未通过 API Key 鉴权的请求允许 `account_id`、`api_key_id` 和 `provider` 为空。不得保存 Authorization、Cookie、query、请求体或上游凭据。账号删除后，历史请求日志不级联删除，因此动态日志表不设置账号外键。

运行参数：

```text
UNISUB_REQUEST_LOG_RETENTION_DAYS=30
```

- 分表日期和整点调度直接使用操作系统或容器的当前时区，即 Go `time.Local`；不提供额外的应用时区环境变量。
- 保留天数允许 1 至 3650，默认 30。
- 每个整点按系统当前时区执行一次清理。
- 当前自然日和之前 `retention_days - 1` 个自然日保留；更早的 `request_logs_YYYYMMDD` 整表删除。
- 表名必须由服务端日期生成，并用固定正则校验，禁止把用户输入拼入 SQL 标识符。
- 清理只匹配严格符合 `request_logs_[0-9]{8}` 的表，名称相似的其他表不得删除。

### 4.6 OAuth 凭证结构

数据库在 `credentials_json` 中直接保存完整的明文 JSON BLOB。数据库文件与备份必须依靠文件权限和运行环境隔离进行保护。

Claude：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "expires_at": "2026-08-19T12:00:00Z",
  "scope": "...",
  "org_uuid": "...",
  "account_uuid": "..."
}
```

Codex：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "id_token": "...",
  "client_id": "...",
  "expires_at": "2026-08-19T12:00:00Z",
  "chatgpt_account_id": "...",
  "chatgpt_user_id": "...",
  "organization_id": "...",
  "plan_type": "plus"
}
```

Grok：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "id_token": "...",
  "client_id": "...",
  "expires_at": "2026-08-19T12:00:00Z",
  "scope": "...",
  "team_id": "...",
  "subscription_tier": "..."
}
```

## 5. Claude 实现方案

### 5.1 账号导入

支持两个方式。

首选方式是 OAuth PKCE：

```text
POST /admin/providers/claude/oauth/start
POST /admin/providers/claude/oauth/exchange
```

流程：

```text
生成 state + code_verifier
  → 返回 Claude OAuth URL
  → 管理员浏览器授权
  → 提交 authorization code
  → 交换 access_token/refresh_token
  → 明文保存
```

可选方式是 `sessionKey` 自动交换：

```text
POST /admin/providers/claude/session/import
```

服务端临时使用 `sessionKey`：

1. 请求 `https://claude.ai/api/organizations`。
2. 选择 organization。
3. 请求 Claude OAuth authorization code。
4. 交换 OAuth token。
5. 立即丢弃 `sessionKey`。

`sessionKey` 不得落库、不得写日志。

### 5.2 转发入口

```text
GET  /v1/models
GET  /claude/v1/models
POST /v1/messages
POST /v1/messages/count_tokens
POST /claude/v1/messages
POST /claude/v1/messages/count_tokens
```

上游：

```text
POST https://api.anthropic.com/v1/messages?beta=true
```

注入必要请求头：

```http
authorization: Bearer <access_token>
content-type: application/json
anthropic-version: 2023-06-01
accept: application/json
user-agent: <固定 Claude Code UA>
x-app: cli
```

处理原则：

- 请求 body 原样转发。
- `model` 不改写。
- `messages` 不改写。
- `tools` 不改写。
- `system` 不注入。
- `metadata` 不生成。
- 客户端 `anthropic-beta` 默认允许透传，但应配置 allowlist。
- 下游必须提交合法的 Anthropic Messages 请求。
- 如果上游要求更完整的 Claude Code 指纹，由调用方负责提交 Claude Code 原生 payload。

这意味着普通 Anthropic SDK 请求能否稳定使用 Claude 订阅，取决于上游风控。初版不要加入 system、metadata、tool 等指纹改写，否则就不再是纯转发。

### 5.3 Token 刷新

当满足以下条件时执行刷新：

```text
expires_at <= now + 10 minutes
```

刷新机制必须包括：

- 账号级进程内互斥锁。
- `singleflight`，同一账号同一时刻只发起一次刷新。
- 失败后保留旧 token，直到其真正过期。
- `invalid_grant` 时将账号标记为 `reauth_required`。
- refresh token 轮换时使用 SQLite 事务原子更新。

本方案以单实例 SQLite 部署为边界，因此不引入 Redis 分布式锁。

## 6. Codex 实现方案

### 6.1 账号导入

管理接口：

```text
POST /admin/providers/codex/oauth/start
POST /admin/providers/codex/oauth/exchange
```

使用 Codex CLI OAuth PKCE：

```text
Authorize: https://auth.openai.com/oauth/authorize
Token:     https://auth.openai.com/oauth/token
Redirect:  http://localhost:1455/auth/callback
```

授权 URL 需要包含：

```text
openid profile email offline_access
id_token_add_organizations=true
codex_cli_simplified_flow=true
```

交换 Token 后解析 `id_token`，提取：

- `chatgpt_account_id`
- `chatgpt_user_id`
- `organization_id`
- `plan_type`

其中 `chatgpt_account_id` 是后续请求的必要 header。

### 6.2 转发入口

```text
GET  /v1/models
GET  /codex/v1/models
POST /v1/responses
POST /v1/responses/compact
POST /v1/responses/*subpath
POST /codex/v1/responses
POST /codex/v1/responses/compact
POST /codex/v1/responses/*subpath
GET  /codex/v1/responses        # WebSocket Upgrade
```

上游：

```text
POST https://chatgpt.com/backend-api/codex/responses
POST https://chatgpt.com/backend-api/codex/responses/compact
```

必要请求头：

```http
Authorization: Bearer <access_token>
Host: chatgpt.com
chatgpt-account-id: <account id>
content-type: application/json
accept: text/event-stream
user-agent: <固定 Codex CLI UA>
originator: codex_cli_rs
version: <固定 Codex 版本>
```

普通 Responses 请求必须由调用方遵循 Codex 内部方言，网关不自动改写 body。

`POST /v1/responses/*subpath` 与 `/codex/v1/responses/*subpath` 必须像 Sub2API 一样采用默认拒绝的路径校验：

- 后缀必须以 `/` 开头。
- 最多 8 个路径片段。
- 每段最长 128 字节。
- 每段只允许 ASCII 字母、数字、`_`、`-`、`.`。
- 拒绝空片段、`.`、`..`、百分号二次编码、斜杠注入和控制字符。
- 校验失败返回 HTTP 404 或 400，不得静默退化到裸 `/responses`。
- 校验通过后把后缀原样追加到账号对应的上游 Responses URL。

首期至少验证并支持 `/responses/compact`；其他子路径不硬编码业务语义，只进行安全校验和原生转发。上游返回 404 时原样返回。

建议在入口进行严格校验，而不是改写：

```json
{
  "stream": true,
  "store": false,
  "model": "...",
  "input": []
}
```

如果请求包含已知不支持字段，直接返回本地 HTTP 400：

```text
temperature
top_p
frequency_penalty
presence_penalty
stream_options
user
metadata
```

这种“验证后拒绝”不属于协议转换，同时能避免模糊的上游错误。

不要自动执行以下动作：

- 不把 `system` 转成 `instructions`。
- 不把 `functions` 转成 `tools`。
- 不把字符串 `input` 转成数组。
- 不注入默认 instructions。
- 不修正 tool call ID。
- 不把非流式请求改成流式。

客户端需要直接提交 Codex 可以接受的 payload。

### 6.3 账号与会话字段

每个 API Key 已固定绑定一个订阅账号，因此不实现粘性会话，也不改写 `session_id`、`conversation_id`、`previous_response_id` 或 `x-codex-turn-state`。这些字段由调用方和同一个上游账号自行维护并原样透传。

如果请求使用了另一个账号的 API Key，上游是否接受相关状态由上游决定；网关不维护跨 Key 的 response/session 绑定表，也不尝试修复状态。

### 6.4 SSE

Codex 上游以 SSE 为主，必须逐帧转发：

- 禁止 gzip 缓冲。
- 收到一个完整 SSE frame 就 flush。
- 客户端断开时取消上游请求。
- SSE 已开始后不允许换号。
- 401 时允许刷新当前账号 Token 后重试一次；不切换到其他账号。
- 429、403 和 5xx 原样返回，并更新当前账号的限流或健康状态。

### 6.5 WebSocket Responses

Codex WebSocket 必须完整对齐 Sub2API 的 `/v1/responses` WebSocket 直通能力，但去掉其中的账号池选择和粘性逻辑。入口为：

```text
GET /v1/responses
GET /codex/v1/responses
Connection: Upgrade
Upgrade: websocket
Authorization: Bearer <account-bound-api-key>
```

实现要求：

- 完成客户端 WebSocket Upgrade 后，为绑定的 Codex 账号建立或复用上游 WebSocket。
- 上游鉴权、`chatgpt-account-id`、Codex 客户端版本和身份头与 HTTP Responses 保持一致。
- 二进制帧、文本帧、Ping、Pong、Close Code 和 Close Reason 正确双向转发。
- 保持消息边界，不把多条 JSON 消息拼接，也不拆分单条 WebSocket 消息。
- 处理客户端首消息超时、读写超时、最大消息大小和异常关闭。
- 客户端断开后取消上游读取；上游断开后向客户端发送对应 Close 帧。
- 转发上游 Responses 事件，不转换事件类型或 payload。
- 从 terminal 事件中只读解析 `response_id` 和 `usage`，用于本地使用记录；不得修改事件。
- 支持 HTTP/1.1 Upgrade；反向代理部署文档必须包含 Upgrade/Connection 头配置。
- 实现账号级空闲连接池、最小/最大空闲连接、连接健康检查、失败冷却、并发上限和慢消费者背压，行为对齐 Sub2API。
- 一个下游 WebSocket 在生命周期内始终使用 API Key 绑定的同一账号，不做账号 failover，不需要 sticky/session store。
- 与 Sub2API 一样支持 `ctx_pool`、`passthrough`、`http_bridge` 和 `off` 四种账号级模式；默认优先直连上游 Responses WebSocket。
- 当账号或上游不支持 WS、管理员强制 HTTP、消息超过直连阈值，或直连进入冷却时，可以使用 Sub2API 风格的 HTTP-SSE bridge：把 WebSocket `response.create` transport envelope 转为同账号 HTTP `/responses` 请求，再把原生 Responses SSE 事件逐条写回 WebSocket。
- HTTP bridge 只允许删除 WebSocket transport 字段，例如 `type`、`generate`，并设置同一协议要求的 `stream=true`；不得转换 Chat Completions/Anthropic 协议，不得切换账号或供应商。
- bridge 必须保留连续 turn、工具调用上下文、错误事件、usage 和取消语义；`previous_response_id` 存在时按 Sub2API 的能力判定选择直连或明确拒绝，不得静默丢失上下文。

WebSocket 需要覆盖 Sub2API 同等级别的测试场景：Upgrade 鉴权、首帧、连续多 turn、Ping/Pong、客户端取消、上游异常关闭、超大消息、慢客户端背压、usage 采集和连接池复用。

## 7. Grok 实现方案

### 7.1 账号导入

支持：

```text
POST /admin/providers/grok/oauth/start
POST /admin/providers/grok/oauth/exchange
POST /admin/providers/grok/sso/exchange
```

标准 OAuth：

```text
Issuer:    https://auth.x.ai
Authorize: https://auth.x.ai/oauth2/authorize
Token:     https://auth.x.ai/oauth2/token
```

scope：

```text
openid profile email offline_access grok-cli:access api:access
```

SSO 导入可以作为第二阶段：

```text
临时 sso Cookie
  → xAI device authorization
  → OAuth access/refresh token
  → 丢弃 sso Cookie
```

不建议实现密码登录。密码登录通常涉及 Turnstile 或 Captcha 服务，会增加安全、合规和维护成本。直接接受 OAuth 或管理员提供的临时 SSO 即可。

### 7.2 转发入口

```text
GET  /v1/models
GET  /grok/v1/models
POST /v1/responses
POST /v1/responses/*subpath
POST /grok/v1/responses
POST /grok/v1/responses/*subpath
```

OAuth 上游：

```text
POST https://cli-chat-proxy.grok.com/v1/responses
```

必要请求头：

```http
Authorization: Bearer <access_token>
Content-Type: application/json
Accept: application/json, text/event-stream
User-Agent: <固定 Grok CLI UA>
X-Grok-Client-Version: <固定版本>
x-grok-client-identifier: <Grok CLI identifier>
X-Grok-Client-Mode: interactive
```

处理原则：

- body 原样转发。
- 不修改模型。
- 不过滤 tools。
- 不删除不支持字段。
- 不生成 cache identity。
- 不修改 reasoning。
- 不做 encrypted content 修复。

可以做只读校验：

- 必须存在 `model`。
- `stream` 必须为布尔值。
- body 必须为合法 JSON。
- body 不得超过配置上限。

Grok 图片、视频和语音明确不在项目范围内，不注册相关路由，也不在后续阶段预留兼容转换能力。

## 8. API Key 与账号路由

项目不实现账号池调度。每个订阅账号创建时生成一个独立下游 API Key，后续可以重置，但不能让同一个 Key 绑定多个账号。

每次请求的处理顺序：

1. 对下游 API Key 做常量时间哈希验证。
2. 通过 `api_keys.account_id` 取得唯一订阅账号。
3. 检查 API Key 和账号是否启用、是否过期。
4. 检查入口与账号平台是否匹配。
5. 检查该账号的 RPM 限制。
6. 申请该账号的并发槽位；满额时最多等待 `concurrency_queue_timeout_seconds`。
7. 必要时刷新当前账号 OAuth Token。
8. 使用该账号独立的 HTTP Client 和可选代理转发到当前账号上游。
9. 完整读取普通响应或完成 SSE/WS 生命周期后释放并发槽位。
10. 记录使用量、限流头和额度状态。

不支持以下行为：

```text
X-Unisub-Session 粘性
模型驱动的账号选择
随机或加权账号选择
账号池 fallback
请求在账号 A 失败后切换账号 B
```

单账号错误处理规则：

| 情况 | 处理 |
|---|---|
| 并发已满且等待时间为 0 | 立即返回 `429 concurrency_limited` |
| 并发等待期间释放槽位 | 获取槽位并继续使用当前账号转发 |
| 并发等待队列超时 | 返回 `429 concurrency_limited`，不调用上游 |
| 并发等待期间客户端断开 | 取消等待，不调用上游 |
| 获取 Token 失败 | 返回 502 或 401，标记账号状态 |
| 建立上游连接失败 | 返回 502，不切换账号 |
| 401 | 刷新当前账号 Token 后最多重试一次 |
| 403 | 原样返回，账号可能标记为 `reauth_required` 或受限 |
| 429 | 原样返回，解析并记录 `Retry-After` 和限流头 |
| 500/502/503/529 | 原样返回并记录健康状态 |
| 400/404/422 | 原样返回，通常是请求或端点不受支持 |
| SSE/WS 已输出 | 关闭当前流或连接，不重试 |
| 客户端主动断开 | 立即取消当前账号的上游请求 |

账号不可用时由管理员修复或让客户端换用另一个账号对应的 API Key。

## 9. 传输层要求

HTTP 连接池必须按账号隔离，而不是只按 Provider 隔离。每个账号建立并缓存独立的 `http.Client` 和 `http.Transport`：

```go
type AccountTransport struct {
    AccountID      int64
    Provider       string
    Client         *http.Client
    MaxIdleConns   int
    IdleTimeout    time.Duration
    HeaderTimeout  time.Duration
}
```

连接复用规则：

- 同一账号可以复用自己的 HTTP/1.1 keep-alive 或 HTTP/2 连接。
- 不同账号不得共享 `http.Client`、`http.Transport`、空闲连接或 HTTP/2 session。
- 即使两个账号属于同一个 Provider，仍不得共享连接。
- 即使两个账号配置了相同代理，仍不得共享连接。
- 修改账号代理或删除账号时，从缓存移除该账号 Client，并关闭其空闲连接。
- 服务关闭时关闭所有账号 Transport 的空闲连接。

需要支持：

- 账号级独立上游连接池。
- 每账号可选 HTTP 或 SOCKS5 代理，包括可选的用户名密码认证。
- HTTP 代理必须支持 HTTPS CONNECT；SOCKS5 必须通过账号 Transport 的 `DialContext` 建立连接。
- ResponseHeaderTimeout。
- SSE 不设置整体响应超时。
- 客户端取消向上游传播。
- 禁止自动重定向到非白名单域名。
- 上游地址固定或经过严格 allowlist。
- 禁止管理员通过 `base_url` 访问内网，防止 SSRF。

允许的官方域名至少限定为：

```text
api.anthropic.com
platform.claude.com
claude.ai

auth.openai.com
chatgpt.com

auth.x.ai
accounts.x.ai
cli-chat-proxy.grok.com
api.x.ai
*.api.x.ai
```

### 9.1 账号连接、代理与并发队列验收

必须覆盖以下自动化测试：

- 同一账号的连续请求复用同一个 Client 和 Transport。
- 两个相同 Provider 的不同账号获得不同 Client 和 Transport，不共享 HTTP/1.1 或 HTTP/2 连接。
- 两个配置相同代理的不同账号仍然使用不同 Transport。
- HTTP 代理收到正确的目标地址、请求头和请求体；HTTPS 上游通过 CONNECT 转发。
- SOCKS5 无认证和用户名密码认证均能建立连接，客户端取消可以中断拨号或等待。
- 代理 URL 在数据库中只保存密文，账号列表和详情只暴露 `proxy_configured`。
- 修改或清除代理后，旧 Client 从缓存移除，后续请求使用新 Transport。
- 并发未满时不创建等待定时器，直接获得槽位。
- 并发已满时，请求在配置时间内等待；槽位释放后能够继续处理。
- 等待超时后返回 `429 concurrency_limited`，且没有请求到达上游。
- 等待期间客户端取消后立即退出，且没有请求到达上游。
- 账号 `concurrency_queue_timeout_seconds=0` 时继承全局缺省；全局未配置时实际等待 3 分钟。
- 旧 SQLite 数据库启动后自动新增队列等待字段，并保留已有账号数据。

## 10. 使用额度与管理页面

管理页面必须提供账号列表和账号详情页，管理员可以查看每个 Claude、Codex、Grok 订阅账号的当前套餐、上游额度、本地使用量和最近查询状态。

### 10.1 统一额度结构

不同上游返回的额度维度不同，后端只做展示层归一化，不把额度用于跨账号调度：

```json
{
  "account_id": 1,
  "provider": "codex",
  "subscription_tier": "plus",
  "status": "available",
  "windows": [
    {
      "name": "five_hour",
      "used_percent": 32.5,
      "remaining_percent": 67.5,
      "reset_at": "2026-08-19T16:00:00Z"
    }
  ],
  "request_quota": null,
  "token_quota": null,
  "local_usage": {
    "requests_24h": 20,
    "input_tokens_24h": 10000,
    "output_tokens_24h": 2500
  },
  "source": "upstream",
  "checked_at": "2026-08-19T12:00:00Z",
  "stale": false,
  "error": null
}
```

Provider 适配原则：

- Claude：优先调用订阅账号可用的官方 usage/limits 接口；无法查询时显示 `unsupported` 或 `unknown`，不能根据本地 Token 反推官方剩余额度。
- Codex：对齐 Sub2API 的 Codex 使用额度查询，展示订阅计划、滚动窗口使用率和重置时间。
- Grok：优先读取官方 billing/usage 信息，并结合正常响应中的 xAI 限流头展示请求和 Token 窗口；没有权威数据时标记 `unknown`。
- 上游额度查询失败不得影响正常转发，只更新 `quota_error` 和陈旧状态。
- 管理员手动刷新需要账号级限频，后台定时刷新建议 5 至 15 分钟一次。

### 10.2 管理页面

页面最少包含：

- 管理员登录、退出和修改密码。
- 订阅账号列表：平台、名称、状态、API Key 前缀、套餐、额度百分比、重置时间、最近查询时间。
- 账号详情：OAuth 状态、Token 到期时间、上游额度窗口、本地 24 小时/7 天请求和 Token 统计、最近错误。
- 添加账号、OAuth 授权、测试、刷新 Token、启用、禁用和删除。
- 创建账号时配置并发上限、并发队列等待毫秒数以及可选的 HTTP/SOCKS5 代理。
- 已有账号可以修改或清除代理，也可以动态修改并发队列等待时长；代理密文不得回显。
- 创建时显示一次账号 API Key；支持重置 API Key，旧 Key 立即失效。
- “刷新额度”操作及查询失败原因展示。
- WebSocket 当前连接数、HTTP/SSE 当前并发数和最近请求记录。

管理页面不包含用户管理、支付、充值、套餐销售和账单功能。

## 11. 凭证安全

### 11.1 明文存储

账号凭据和代理 URL 直接以明文保存到 SQLite，不设置主密钥，也不进行应用层加密。生产环境必须限制数据库文件、WAL 文件和备份的读取权限，并避免将真实凭据复制到不可信环境。

数据库不得保存：

- OAuth code。
- PKCE verifier，授权结束后立即删除。
- Claude sessionKey。
- Grok SSO。
- 管理员密码明文。
- 下游 API Key 明文。

### 11.2 日志脱敏

禁止记录：

```text
Authorization
Cookie
sessionKey
sso
refresh_token
access_token
id_token
chatgpt-account-id
完整请求 body
```

日志只记录：

```text
account_id
api_key_id
provider
endpoint
status_code
request_id
duration
transport
websocket_connection_id
```

## 12. 管理接口

建议最小控制面：

```text
POST   /admin/login
POST   /admin/logout
PUT    /admin/password

POST   /admin/accounts

GET    /admin/accounts
GET    /admin/accounts/:id
PATCH  /admin/accounts/:id
DELETE /admin/accounts/:id
POST   /admin/accounts/:id/api-key/reset
POST   /admin/accounts/:id/test
POST   /admin/accounts/:id/refresh
POST   /admin/accounts/:id/enable
POST   /admin/accounts/:id/disable
PUT    /admin/accounts/:id/proxy
PUT    /admin/accounts/:id/concurrency-queue
GET    /admin/accounts/:id/usage
POST   /admin/accounts/:id/usage/refresh
GET    /admin/usage/summary

POST   /admin/providers/claude/oauth/start
POST   /admin/providers/claude/oauth/exchange
POST   /admin/providers/claude/session/import

POST   /admin/providers/codex/oauth/start
POST   /admin/providers/codex/oauth/exchange

POST   /admin/providers/grok/oauth/start
POST   /admin/providers/grok/oauth/exchange
POST   /admin/providers/grok/sso/exchange
```

创建账号时可同时提交：

```json
{
  "name": "codex-plus-main",
  "provider": "codex",
  "auth_type": "oauth",
  "credentials": {
    "access_token": "...",
    "chatgpt_account_id": "..."
  },
  "concurrency_limit": 2,
  "concurrency_queue_timeout_seconds": 5,
  "proxy_url": "socks5://username:password@127.0.0.1:1080"
}
```

修改或清除账号代理：

```http
PUT /admin/accounts/:id/proxy
Content-Type: application/json

{"proxy_url":"http://127.0.0.1:8080"}
```

传入 `{"proxy_url":""}` 清除代理。修改并发队列等待时间：

```http
PUT /admin/accounts/:id/concurrency-queue
Content-Type: application/json

{"concurrency_queue_timeout_seconds":5}
```

管理接口必须与转发接口分离，并要求：

- 只允许数据库中 `id=1` 的管理员登录。
- 管理员 JWT 或 `HttpOnly + Secure + SameSite=Strict` Session Cookie。
- TOTP 二次认证可选但建议启用。
- CSRF 防护。
- 操作审计。
- 凭证永不回显。
- 登录和额度刷新接口限流。

## 13. 实现阶段

### 13.1 第一阶段：最小可用

实现：

- Go 服务骨架。
- SQLite 数据模型、迁移、WAL 和备份策略。
- 凭证和代理地址明文持久化。
- 单管理员账号初始化与登录。
- 每个订阅账号独立 API Key。
- 单账号 Claude 转发。
- 单账号 Codex 转发。
- 单账号 Grok 转发。
- SSE relay。
- `/v1/models`、`/v1/responses` 和 Responses 子路径。
- 手动导入 OAuth token。
- 基础管理页面和额度查询页面。
- 基础测试。

暂不实现：

- OAuth 浏览器流程。
- 自动刷新。
- Codex WebSocket。

### 13.2 第二阶段：完整账号生命周期

实现：

- 三个平台 PKCE OAuth。
- Claude sessionKey 换 OAuth。
- Grok SSO 换 OAuth。
- 后台 token refresh。
- 账号测试与状态维护。
- 账号级 `singleflight` 和 SQLite 原子更新。
- 三个平台额度适配器和定时刷新。

### 13.3 第三阶段：WebSocket 与稳定性

实现：

- Codex Responses WebSocket，完整对齐 Sub2API 的直连、连接池、协议决策和 HTTP-SSE bridge 行为。
- WebSocket 连接池、背压、Ping/Pong、关闭语义和指标。
- 单账号 HTTP/SSE/WS 并发限制，以及可取消、可超时的并发等待队列。
- 429/额度响应观测。
- HTTP/SOCKS5 账号代理配置、明文存储和运行时修改。
- HTTP Client、Transport、keep-alive 与 HTTP/2 session 的账号级隔离。
- 运行指标。
- SQLite 在线备份和 usage_logs 清理。

### 13.4 第四阶段：管理体验

可选实现：

- Grok Chat Completions 原生透传。
- 更多上游原生 Responses 子路径。
- 额度历史趋势和导出。
- 管理页面操作审计查看。
- TOTP 二次认证。

始终不实现：图片、视频、声音、多租户、支付、账单、粘性会话和账号池调度。

## 14. 最终协议契约

为了始终保持“不做兼容协议”，应在 README 和接口文档中明确：

```text
/v1/messages 和 /claude/* 只接受 Claude/Anthropic 原生 payload。
/v1/responses、/codex/* 和 /grok/* 只接受 API Key 所绑定平台的原生 payload。
/v1/models 只返回 API Key 所绑定账号的原生模型能力。
/v1/responses/*subpath 只做安全路径校验和原生路径追加。

网关不会自动转换 messages、input、tools、reasoning、stream
或任何模型请求结构。调用方必须提供对应上游能够直接接受的请求。
```

这样实现复杂度会明显低于完整的兼容网关。项目需要重点投入的是 OAuth 生命周期、账号级 API Key 隔离、官方客户端身份头、SSE、WebSocket、额度查询、SQLite 可靠性和凭证安全，而不是协议转换或账号池调度。
