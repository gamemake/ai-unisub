# ai-unisub

Claude、Codex 与 Grok 订阅账号的原生协议转发网关。每个下游 `unisub_*` API Key 只绑定一个上游账号；网关不会转换 Anthropic Messages、OpenAI Responses 或任何业务 payload。

当前代码实现了 `SUBSCRIPTION_API_PLAN.md` 第一阶段的可运行后端基线：

- Go + Gin 服务、SQLite WAL/外键/`busy_timeout`。
- 账号凭据 JSON 和代理 URL 以明文保存；数据库仅保存下游 API Key 的哈希，不保存其明文。
- 单管理员初始化、bcrypt 密码哈希、短期 HMAC-SHA256 Bearer JWT。
- 账户创建、列表、详情、启停、删除、API Key 重置和本地 24 小时用量。
- Claude Messages/Count Tokens、Codex Responses、Grok Responses 和三者 Models 原生透传。
- SSE 实时 flush、客户端取消向上游传播、独立 Provider 连接池。
- 平台别名隔离、账号并发限制、API Key RPM 限制、请求体上限。
- 转发调用按系统当前时区写入每日请求日志分表，并在每个整点自动清理超过保留期的分表。
- 默认拒绝的 Responses 子路径校验和敏感下游请求头清洗。
- 一个轻量管理页：`/admin`。

尚未实现：浏览器 PKCE OAuth、自动 Token 刷新、权威上游额度适配器、Codex WebSocket、代理配置和完整审计 UI。这些分别属于方案第二、第三阶段。当前通过管理接口手动导入 OAuth Token；额度页会如实显示 `unknown`，并展示本地请求统计，不会推算官方剩余额度。

## 快速启动

需要 Go 1.25 或更高版本。

```powershell
Copy-Item .env.example .env
# 可选：未设置时缺省为 admin，生产环境请务必覆盖
$env:UNISUB_ADMIN_PASSWORD = 'replace-with-a-long-password'
$env:UNISUB_DB_PATH = '.\data\unisub.db'
go run ./cmd/server
```

启动时若 `admin` 表为空，服务使用 `UNISUB_ADMIN_USERNAME` 和 `UNISUB_ADMIN_PASSWORD` 创建唯一管理员。两个缺省值都是 `admin`，可通过环境变量覆盖。环境变量只用于首次初始化，后续启动不会覆盖数据库中已有的密码。生产环境不得使用缺省密码。

打开 `http://127.0.0.1:8080/admin`，登录后可手动导入账号凭据。生产部署应放在具备 TLS 的反向代理之后，且不要公开管理入口。

## 管理 API

登录：

```http
POST /admin/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

创建 Codex 账号并手动导入 Token：

```http
POST /admin/accounts
Authorization: Bearer <admin-jwt>
Content-Type: application/json

{
  "name": "codex-plus-1",
  "provider": "codex",
  "auth_type": "oauth",
  "credentials": {
    "access_token": "...",
    "refresh_token": "...",
    "id_token": "...",
    "chatgpt_account_id": "..."
  },
  "concurrency_limit": 1,
  "concurrency_queue_timeout_seconds": 5,
  "proxy_url": "socks5://user:password@127.0.0.1:1080",
  "rpm_limit": 30,
  "token_expires_at": "2026-08-20T12:00:00Z"
}
```

`proxy_url` 可选，支持 `http://` 和 `socks5://`，必须包含主机和端口；用户名和密码可放在 URL 中。代理地址和账号凭据均以明文保存，管理 API 只返回 `proxy_configured`，不会回显代理地址。

每个账号拥有独立的 HTTP Client、Transport 和 keep-alive 连接池。不同账号不会复用 HTTP 连接，即使它们属于同一个 Provider 或使用相同代理；同一账号内部仍会复用自己的空闲连接。

`concurrency_queue_timeout_seconds` 控制账号达到最大并发数后的等待时间，范围为 0 到 300 秒。等待期间有请求释放并发槽位时会继续处理；超时后返回 `429 concurrency_limited`。账号值为 0 时继承环境变量 `UNISUB_CONCURRENCY_QUEUE_TIMEOUT_SECONDS`；环境变量未设置或为 0 时使用内置缺省值 180 秒（3 分钟）。

请求日志按操作系统或容器的当前时区（Go `time.Local`）分天写入 `request_logs_YYYYMMDD` 表，不提供单独的应用时区配置。`UNISUB_REQUEST_LOG_RETENTION_DAYS` 指定保留天数，默认 30 天；服务按系统时区在每个整点检查并删除超出保留期的整张日志表。日志只保存账号/API Key ID、Provider、方法、路径、状态码、时间、耗时、上游 request ID 和错误类型，不保存鉴权头、query 或请求体。

已有账号可通过 `PUT /admin/accounts/:id/concurrency-queue` 修改，请求体为 `{"concurrency_queue_timeout_seconds":5}`。

已有账号可通过 `PUT /admin/accounts/:id/proxy` 修改代理，请求体为 `{"proxy_url":"http://127.0.0.1:8080"}`；传入空字符串可清除代理。

响应中的 `api_key` 只显示一次。重置入口是 `POST /admin/accounts/:id/api-key/reset`，旧 Key 会立即失效。

其他已实现入口：

```text
PUT    /admin/password
GET    /admin/accounts
GET    /admin/accounts/:id
DELETE /admin/accounts/:id
POST   /admin/accounts/:id/enable
POST   /admin/accounts/:id/disable
POST   /admin/accounts/:id/api-key/reset
GET    /admin/accounts/:id/usage
GET    /admin/usage/summary
```

凭据字段不会由管理 API 回显。不要把 Token、Cookie、完整请求体或 API Key 写入日志。

## 转发 API

下游统一使用账号绑定 Key：

```http
Authorization: Bearer unisub_xxxxxxxxx
```

入口：

```text
GET  /v1/models
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1/responses
POST /v1/responses/*safe-subpath

/claude/v1/...
/codex/v1/...
/grok/v1/...
```

显式平台别名与 Key 所绑定平台不一致时返回 `403`。Claude Key 不能调用 Responses；Codex/Grok Key 不能调用 Messages。请求的 `model`、`messages`、`input`、`tools`、`reasoning` 和 `stream` 不会被改写。

Responses 子路径最多 8 段，每段最多 128 字节，只允许 ASCII 字母、数字、`_`、`-`、`.`；空段、`.`、`..`、百分号编码和斜杠注入会被拒绝。

官方 OpenAI API 文档确认公开 Responses API 使用 Bearer 鉴权并通过 `POST /v1/responses` 创建响应：[Responses API reference](https://developers.openai.com/api/reference/java/resources/beta/subresources/responses)。本项目的 Codex 订阅转发目标和身份头来自项目方案中的 Codex CLI 契约，并非公开 OpenAI API 的稳定性承诺；相关内部上游若变化，需要更新 Provider 适配器。

## 安全与运行约束

- 账号凭据和代理 URL 以明文写入 SQLite。必须严格限制数据库文件、备份和宿主机的访问权限。
- 管理 API 不回显账号凭据和代理 URL，但这不代表数据库中的字段经过加密。
- 默认只允许方案列出的官方 HTTPS 上游，避免管理员配置任意 URL 造成 SSRF。`UNISUB_ALLOW_TEST_UPSTREAMS=true` 仅供自动化测试使用。
- SQLite 数据文件只允许一个服务实例直接写入，不要让多个容器共享同一个数据库文件。
- 管理 JWT 保存在管理页的 `sessionStorage`，页面关闭后清除；管理 API 不使用 Cookie，因此不依赖 Cookie CSRF 保护。
- 登录限制为每来源 IP 每分钟 5 次。生产反向代理应限制管理网段，并确保传入的客户端地址可信。

## SQLite 备份

WAL 模式下不要只复制主 `.db` 文件。使用 SQLite 在线备份命令，并把备份写到数据库卷之外：

```sh
sqlite3 /app/data/unisub.db ".timeout 5000" ".backup '/backup/unisub-$(date +%F-%H%M%S).db'"
```

定期验证备份：

```sh
sqlite3 /backup/unisub-YYYY-MM-DD-HHMMSS.db "PRAGMA integrity_check;"
```

## Docker Compose

```sh
cd deploy
export UNISUB_ADMIN_PASSWORD="replace-with-a-long-password" # 未设置时缺省为 admin
docker compose up --build -d
```

Compose 默认只绑定 `127.0.0.1:8080`。可分别使用 `UNISUB_HOST_PORT` 和 `UNISUB_PORT` 设置宿主机与容器端口：

```sh
export UNISUB_HOST_PORT=19090
export UNISUB_PORT=9090
docker compose up --build -d
```

直接运行镜像时也可通过环境变量覆盖运行参数：

```sh
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  -e UNISUB_PORT=9090 \
  -e UNISUB_ADMIN_PASSWORD="replace-with-a-long-password" \
  -v unisub-data:/app/data \
  ai-unisub
```

`UNISUB_LISTEN` 可用于指定完整的监听地址，并优先于 `UNISUB_PORT`。例如 `UNISUB_LISTEN=127.0.0.1:9090`。生产环境请通过 TLS 反向代理暴露所需入口。

## 验证

```sh
go test ./...
go vet ./...
go build ./cmd/server
```

测试覆盖凭据与代理明文持久化、管理员 Token 签名、API Key 生命周期、平台隔离、敏感头剥离、SSE 透传和 Responses 子路径安全校验。

## VS Code

安装工作区推荐的 Go 扩展后，可以直接使用：

- `F5` → `ai-unisub: Debug server`：在 `127.0.0.1:18081` 启动服务，使用独立的 `data/debug.db`。
- 在 Go 测试文件中按 `F5` → `ai-unisub: Debug current test`：调试当前测试包。
- `Ctrl+Shift+B`：执行默认构建任务，输出到 `bin/ai-unisub.exe`。
- 命令面板的 `Tasks: Run Task`：运行 `ai-unisub: test` 或 `ai-unisub: vet`。

调试配置中的管理员密码是公开的开发值，只能用于本地调试，不能用于部署生产环境。账号凭据以明文写入调试数据库，不要导入真实凭据。
