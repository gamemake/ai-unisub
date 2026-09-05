# API 文档

本文描述 `ai-unisub` **当前已实现**的 HTTP 接口。实现以 [`internal/server/server.go`](../internal/server/server.go) 及同目录处理器为准；上游原生业务 payload（Messages / Responses）不做转换。

默认监听地址由 `UNISUB_LISTEN` / `UNISUB_PORT` 决定（常见为 `http://127.0.0.1:8080`）。管理控制台在 `/home`。

相关文档：

- 产品与运维总览：[`README.md`](../README.md)
- 数据库结构：[`docs/database.md`](database.md)

---

## 目录

1. [约定](#1-约定)
2. [公开与页面入口](#2-公开与页面入口)
3. [管理 API 鉴权](#3-管理-api-鉴权)
4. [认证与当前用户](#4-认证与当前用户)
5. [用户管理](#5-用户管理)
6. [账号管理](#6-账号管理)
7. [API Key 管理](#7-api-key-管理)
8. [用量与额度](#8-用量与额度)
9. [Grok OAuth](#9-grok-oauth)
10. [Claude / Codex OAuth](#10-claude--codex-oauth)
11. [请求日志](#11-请求日志)
12. [下游转发 API](#12-下游转发-api)
13. [错误码一览](#13-错误码一览)
14. [尚未实现](#14-尚未实现)

---

## 1. 约定

### 1.1 内容类型

除非另行说明：

- 请求体：`Content-Type: application/json`
- 成功/失败响应：`application/json`
- SSE 上游响应原样透传为 `text/event-stream`

### 1.2 统一错误体

网关自身返回的错误形如：

```json
{
  "error": {
    "type": "invalid_request",
    "message": "human readable reason"
  }
}
```

上游 Provider 的错误体一般原样转发，不包进上述结构。

### 1.3 两类鉴权

| 场景 | 头 | 说明 |
| --- | --- | --- |
| 管理 API（`/api/*`，除登录） | `Authorization: Bearer <admin-jwt>` | 登录后签发的短期 HMAC-SHA256 JWT |
| 下游转发（`/v1/*`、`/{provider}/v1/*`） | `Authorization: Bearer unisub_...` | 账号绑定的下游 API Key |

### 1.4 角色

| 角色 | 能力 |
| --- | --- |
| `admin` | 全部管理接口，含用户管理；可查看全部请求日志 |
| `user` | 可登录、改自己密码、管理账号/API Key/用量/Grok OAuth/请求日志（仅自己创建的账号相关日志） |

用户管理接口（`/api/users*`）额外要求 `admin` 角色，否则返回 `403 forbidden`。

### 1.5 ID 与时间

- 路径参数 `:id` 为正整数。
- 时间字段多为 RFC3339 / RFC3339Nano（UTC）。
- 请求日志按系统本地时区（`time.Local`）按天分表，`:day` 格式为 `YYYYMMDD`。

---

## 2. 公开与页面入口

这些入口**不需要**鉴权。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/` | `307` 重定向到 `/home` |
| `GET` | `/healthz` | 健康检查 |
| `GET` | `/home` | 管理页 HTML |
| `GET` | `/assets/admin.css` | 管理页样式 |
| `GET` | `/assets/admin.js` | 管理页脚本 |

### `GET /healthz`

**响应 `200`**

```json
{ "status": "ok" }
```

未知路由返回：

```json
{
  "error": {
    "type": "not_found",
    "message": "route not found"
  }
}
```

---

## 3. 管理 API 鉴权

除 `POST /api/login` 外，`/api/*` 均需有效 Bearer JWT，且对应用户存在且 `enabled=true`。

失败时：

```http
401 Unauthorized
```

```json
{
  "error": {
    "type": "unauthorized",
    "message": "valid admin bearer token required"
  }
}
```

Token 默认 TTL 为 8 小时（`UNISUB_ADMIN_TOKEN_TTL`）。管理页把 JWT 放在 `sessionStorage`，关闭标签页后清除；管理 API 不使用 Cookie。

---

## 4. 认证与当前用户

### `POST /api/login`

无需鉴权。同一来源 IP 每分钟最多 5 次，超出返回 `429 rate_limited`。

**请求**

```json
{
  "username": "admin",
  "password": "..."
}
```

**响应 `200`**

```json
{
  "token": "<jwt>",
  "token_type": "Bearer",
  "expires_in": 28800,
  "user": {
    "id": 1,
    "username": "admin",
    "role": "admin",
    "enabled": true,
    "created_at": "...",
    "updated_at": "..."
  }
}
```

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `invalid_request` | 400 | 缺用户名或密码 |
| `invalid_credentials` | 401 | 用户名或密码错误 |
| `rate_limited` | 429 | 登录过频 |
| `internal_error` | 500 | 签发 Token 失败 |

### `PUT /api/password`

修改**当前登录用户**密码。新密码至少 12 字符。

**请求**

```json
{
  "current_password": "...",
  "new_password": "at-least-12-chars"
}
```

**响应** `204 No Content`

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `invalid_request` | 400 | 新密码过短或体无效 |
| `invalid_credentials` | 401 | 当前密码错误 |

### `GET /api/me`

**响应 `200`**

```json
{
  "user": {
    "id": 1,
    "username": "admin",
    "role": "admin",
    "enabled": true,
    "created_at": "...",
    "updated_at": "..."
  }
}
```

---

## 5. 用户管理

以下接口均需 `admin` 角色。

### `GET /api/users`

**响应 `200`**

```json
{
  "data": [
    {
      "id": 1,
      "username": "admin",
      "role": "admin",
      "enabled": true,
      "created_at": "...",
      "updated_at": "..."
    }
  ]
}
```

### `POST /api/users`

**请求**

```json
{
  "username": "alice",
  "password": "at-least-12-chars",
  "role": "user"
}
```

| 字段 | 约束 |
| --- | --- |
| `username` | 必填；2–32 字符；仅字母、数字、`.`、`_`、`-` |
| `password` | 必填；至少 12 字符 |
| `role` | 可选，默认 `user`；仅允许 `admin` / `user` |

**响应 `201`**

```json
{ "user": { "...": "..." } }
```

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `invalid_request` | 400 | 校验失败 |
| `conflict` | 409 | 用户名已占用 |
| `forbidden` | 403 | 非 admin |

### `PUT /api/users/:id`

**请求**

```json
{
  "role": "user",
  "enabled": true
}
```

**响应 `200`**：`{ "user": ... }`

不能把最后一个启用的 admin 降级/禁用（`409 last_admin`）。

### `DELETE /api/users/:id`

**响应** `204 No Content`

不能删除当前登录用户（`400`）；不能删除最后一个启用 admin（`409 last_admin`）。

### `POST /api/users/:id/password`

管理员直接为指定用户设定新密码，**不需要**旧密码。普通成员修改自己的密码仍使用 [`PUT /api/password`](#put-apipassword)。

**请求**

```json
{
  "password": "at-least-12-chars"
}
```

| 字段 | 约束 |
| --- | --- |
| `password` | 必填；至少 12 字符 |

**响应** `204 No Content`

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `invalid_request` | 400 | 密码缺失或过短 |
| `forbidden` | 403 | 非 admin |
| `not_found` | 404 | 用户不存在 |

重置后旧密码立即失效；已签发的管理 JWT 在过期前仍可用（与自助改密行为一致）。

---

## 6. 账号管理

账号表示一个上游订阅身份（Claude / Codex / Grok）。凭据与代理 URL 以明文存库；列表接口**不**回显 `credentials` 与代理 URL，详情会回显凭据 JSON，仅返回 `proxy_configured` 布尔值。

`provider` 取值：`claude` | `codex` | `grok`  
`auth_type` 取值：`oauth` | `api_key`（创建时缺省 `oauth`）

### `GET /api/accounts`

**响应 `200`**

```json
{
  "data": [
    {
      "id": 1,
      "name": "codex-plus-1",
      "provider": "codex",
      "auth_type": "oauth",
      "metadata": {},
      "status": "...",
      "enabled": true,
      "concurrency_limit": 1,
      "concurrency_queue_timeout_seconds": 5,
      "proxy_configured": false,
      "token_expires_at": "2026-08-20T12:00:00Z",
      "api_key_count": 2,
      "created_by_user_id": 1,
      "created_at": "...",
      "updated_at": "..."
    }
  ]
}
```

### `POST /api/accounts`

手动导入上游 Token / API Key 并创建账号。OAuth 推荐走 [Grok OAuth](#9-grok-oauth) 或 [Claude / Codex OAuth](#10-claude--codex-oauth)。

**请求**

```json
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
  "metadata": {},
  "concurrency_limit": 1,
  "concurrency_queue_timeout_seconds": 5,
  "proxy_url": "socks5://user:password@127.0.0.1:1080",
  "token_expires_at": "2026-08-20T12:00:00Z"
}
```

| 字段 | 说明 |
| --- | --- |
| `name` | 必填 |
| `provider` | 必填，且合法 |
| `auth_type` | 缺省 `oauth`；须为 `oauth` 或 `api_key` |
| `credentials` | 必须能解析出 `access_token` 或 `api_key` |
| `credentials.chatgpt_account_id` | Codex + OAuth 时必填 |
| `concurrency_limit` | `<=0` 时落库为 `1`；更新接口限制 1–100 |
| `concurrency_queue_timeout_seconds` | 0–300；`0` 表示继承全局缺省（环境变量或 180 秒） |
| `proxy_url` | 可选；`http://` 或 `socks5://`，须含主机与端口；空串表示无代理 |
| `metadata` | 可选；若提供须为合法 JSON |

**响应 `201`**

```json
{ "account": { "...": "..." } }
```

创建后不会自动签发下游 API Key；请调用 [`POST /api/api-keys`](#post-apiapi-keys)。

### `GET /api/accounts/:id`

**响应 `200`**

```json
{
  "account": {
    "id": 1,
    "credentials": {
      "access_token": "...",
      "refresh_token": "..."
    },
    "proxy_configured": true
  },
  "local_usage": {
    "requests_24h": 10,
    "input_tokens_24h": 1000,
    "output_tokens_24h": 200,
    "cache_read_tokens_24h": 0,
    "cache_creation_tokens_24h": 0,
    "total_tokens_24h": 1200
  }
}
```

### `PUT /api/accounts/:id`

**请求**

```json
{
  "name": "codex-plus-1",
  "enabled": true,
  "concurrency_limit": 2,
  "concurrency_queue_timeout_seconds": 30,
  "proxy_url": "http://127.0.0.1:8080",
  "credentials": {
    "access_token": "...",
    "chatgpt_account_id": "..."
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `name` | 必填 |
| `enabled` | 是否启用 |
| `concurrency_limit` | 1–100 |
| `concurrency_queue_timeout_seconds` | 0–300 |
| `proxy_url` | 可选；省略则不改代理；传字符串会规范化后更新 |
| `credentials` | 可选；提供时覆盖凭据 |

**响应 `200`**：`{ "account": ... }`（含凭据回显）

修改代理会关闭该账号的 HTTP 客户端连接池，下次请求重建。

### `DELETE /api/accounts/:id`

**响应** `204 No Content`

级联删除该账号下的 API Key；关闭账号客户端并清理 Grok 刷新锁。

### `POST /api/accounts/:id/enable`

### `POST /api/accounts/:id/disable`

无请求体。**响应** `204 No Content`

### `PUT /api/accounts/:id/proxy`

**请求**

```json
{ "proxy_url": "http://127.0.0.1:8080" }
```

传空字符串清除代理。**响应** `204 No Content`

### `PUT /api/accounts/:id/concurrency-queue`

**请求**

```json
{ "concurrency_queue_timeout_seconds": 5 }
```

范围 0–300。**响应** `204 No Content`

---

## 7. API Key 管理

下游 Key 形如 `unisub_...`，与账号多对一：一把 Key 只绑定一个账号。鉴权比对 Key 的 SHA-256 哈希；明文保存在库中，供详情与导入使用。**列表接口不回显明文**。

### `GET /api/api-keys`

**响应 `200`**

```json
{
  "data": [
    {
      "id": 1,
      "account_id": 1,
      "account_name": "codex-plus-1",
      "provider": "codex",
      "name": "bot",
      "key_prefix": "unisub_xxxx",
      "enabled": true,
      "rpm_limit": 30,
      "expires_at": null,
      "created_at": "..."
    }
  ]
}
```

### `GET /api/api-keys/:id`

**响应 `200`**

```json
{
  "key": { "...": "..." },
  "api_key": "unisub_..."
}
```

`api_key` 为明文。

### `POST /api/api-keys`

**请求**

```json
{
  "account_id": 1,
  "name": "bot",
  "rpm_limit": 30,
  "expires_at": null
}
```

| 字段 | 说明 |
| --- | --- |
| `account_id` | 必填，`>0` |
| `name` | 可选展示名 |
| `rpm_limit` | 可选；若设置必须 `>0`，表示该 Key 每分钟请求上限 |
| `expires_at` | 可选过期时间 |

**响应 `201`**

```json
{
  "key": { "...": "..." },
  "api_key": "unisub_...",
  "warning": "This API key is shown only once."
}
```

（详情接口也可再次读取明文；创建响应中的 warning 仍建议客户端按「立即保存」处理。）

### `PUT /api/api-keys/:id`

**请求**

```json
{
  "name": "bot",
  "enabled": true,
  "rpm_limit": 60,
  "expires_at": null
}
```

`name` 必填。**响应 `200`**

```json
{ "api_key": { "...": "..." } }
```

注意：此处字段名是元数据对象，不是明文字符串。

### `POST /api/api-keys/:id/reset`

轮换明文 Key，旧 Key 立即失效。

**响应 `200`**

```json
{
  "api_key": "unisub_...",
  "warning": "The old key is invalid. This new key is shown only once."
}
```

### `DELETE /api/api-keys/:id`

**响应** `204 No Content`

---

## 8. 用量与额度

本地用量来自近 24 小时请求日志汇总。上游额度目前主要支持 **Grok OAuth** 主动刷新；Claude/Codex 权威上游额度适配器尚未实现。

### `GET /api/accounts/:id/usage`

**响应 `200`（示例）**

```json
{
  "account_id": 1,
  "provider": "grok",
  "status": "available",
  "subscription_tier": "...",
  "windows": [],
  "request_quota": null,
  "token_quota": null,
  "credit_balance": null,
  "local_usage": {
    "requests_24h": 3,
    "input_tokens_24h": 100,
    "output_tokens_24h": 50,
    "cache_read_tokens_24h": 0,
    "cache_creation_tokens_24h": 0,
    "total_tokens_24h": 150
  },
  "source": "upstream",
  "checked_at": "...",
  "stale": false,
  "error": null
}
```

无缓存额度时：`status` 为 `unknown`，`source` 为 `unavailable`，`stale` 为 `true`。若上次检查超过 15 分钟，`stale` 为 `true`。

### `POST /api/accounts/:id/usage/refresh`

仅 Grok OAuth 账号可用。同一账号每分钟最多 6 次。

**响应 `200`**：结构同 `GET .../usage`。

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `usage_refresh_unsupported` | 422 | 非 Grok OAuth |
| `rate_limited` | 429 | 刷新过频 |
| `usage_refresh_failed` | 502 | 上游查询失败 |

### `GET /api/usage/summary`

全部账号的本地 24h 用量摘要。

**响应 `200`**

```json
{
  "data": [
    {
      "account_id": 1,
      "provider": "claude",
      "name": "claude-1",
      "local_usage": { "...": "..." }
    }
  ]
}
```

---

## 9. Grok OAuth

设备码流程。Device code 只保存在**服务进程内存**中，重启后需重新发起。同一用户每分钟最多 10 次 `start`；同时最多 20 个进行中的 flow。

### `POST /api/providers/grok/oauth/device/start`

**请求**

```json
{
  "name": "grok-main",
  "metadata": {},
  "concurrency_limit": 1,
  "concurrency_queue_timeout_seconds": 0,
  "proxy_url": ""
}
```

`name` 必填；其余字段语义同创建账号。

**响应 `201`**

```json
{
  "flow_id": "...",
  "status": "pending",
  "user_code": "ABCD-EFGH",
  "verification_uri": "https://auth.x.ai/...",
  "verification_uri_complete": "https://auth.x.ai/...?user_code=...",
  "expires_at": "2026-08-27T12:00:00Z",
  "interval": 5
}
```

客户端应打开 `verification_uri_complete`，并按 `interval` 秒轮询 `poll`。

### `POST /api/providers/grok/oauth/device/poll`

**请求**

```json
{ "flow_id": "..." }
```

**进行中 `202`**

```json
{ "status": "pending", "retry_after": 5 }
```

或

```json
{ "status": "finalizing", "retry_after": 1 }
```

**完成 `201`**

```json
{
  "status": "complete",
  "account": { "...": "..." }
}
```

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `oauth_flow_not_found` | 404 | flow 不存在、非本人或不存在（含过期清理） |
| `oauth_access_denied` | 403 | 用户拒绝授权 |
| `oauth_expired` | 410 | 设备码过期 |
| `oauth_capacity` | 429 | 待处理 OAuth 过多 |
| `oauth_unavailable` / `oauth_rejected` / `oauth_invalid_response` | 502 | 与 xAI 交互失败 |

账号密码只在 xAI 官方页面输入，不会经过 UniSub。OAuth 与后续 Token 刷新会使用该账号配置的代理。Access Token 到期前约一分钟自动用 refresh token 续期；刷新失败时下游请求返回 `401 reauth_required`。

---

## 10. Claude / Codex OAuth

浏览器 PKCE 流程。`code_verifier` 和 `state` 只保存在**服务进程内存**中，重启后需重新发起。同一用户每分钟最多 10 次 `start`；Claude 与 Codex 合计同时最多 20 个进行中的 flow。授权码有效期约 10 分钟。

### `POST /api/providers/claude/oauth/start`

### `POST /api/providers/codex/oauth/start`

**请求**

```json
{
  "name": "claude-main",
  "metadata": {},
  "concurrency_limit": 1,
  "concurrency_queue_timeout_seconds": 0,
  "proxy_url": ""
}
```

`name` 必填；其余字段语义同创建账号。

**响应 `201`**

```json
{
  "flow_id": "...",
  "status": "pending",
  "authorization_url": "https://claude.ai/oauth/authorize?...",
  "expires_at": "2026-08-27T12:00:00Z"
}
```

客户端应打开 `authorization_url`，完成官方登录后调用 `exchange`。响应不会返回 `code_verifier`。

### `POST /api/providers/claude/oauth/exchange`

### `POST /api/providers/codex/oauth/exchange`

**请求**

```json
{
  "flow_id": "...",
  "code": "authorization-code#state"
}
```

`code` 接受：

- 纯授权码
- Claude 回调页显示的 `code#state`
- 完整回调 URL，例如 Codex 的 `http://localhost:1455/auth/callback?code=...&state=...`

**完成 `201`**

```json
{
  "status": "complete",
  "account": { "...": "..." }
}
```

| 错误 type | HTTP | 说明 |
| --- | --- | --- |
| `oauth_flow_not_found` | 404 | flow 不存在、非本人、平台不匹配或已过期 |
| `oauth_state_mismatch` | 400 | 粘贴的 state 与本次登录不一致 |
| `oauth_invalid_grant` | 400 | 授权码无效或已使用 |
| `oauth_capacity` | 429 | 待处理 OAuth 过多 |
| `oauth_unavailable` / `oauth_rejected` / `oauth_invalid_response` | 502 | 与官方 token 端点交互失败 |

Claude 使用 Claude Code 公共客户端和 `https://console.anthropic.com/oauth/code/callback`。Codex 使用 Codex CLI 公共客户端；官方 redirect 固定为 `http://localhost:1455/auth/callback`，远程部署时把浏览器地址栏完整 URL 粘贴回来。Codex 交换成功后会从 id_token 解析 `chatgpt_account_id`。账号密码只在官方页面输入。OAuth 与后续 Token 刷新会使用该账号配置的代理。Access Token 到期前约十分钟自动用 refresh token 续期；上游 401 时也会再刷新一次。刷新失败时下游请求返回 `401 reauth_required`。

---

## 11. 请求日志

转发调用按本地时区写入每日分表 `request_logs_YYYYMMDD`。敏感头（如 `Authorization`、`Cookie`、`x-api-key`）记录为 `[redacted]`；正文超过约 1 MiB 会截断。保留天数由 `UNISUB_REQUEST_LOG_RETENTION_DAYS` 控制（默认 30），整点清理过期整表。

非 `admin` 用户只能看到自己创建的账号相关日志。

### `GET /api/request-logs`

**Query**

| 参数 | 说明 |
| --- | --- |
| `account_id` | 账号 ID |
| `api_key_id` | API Key ID |
| `provider` | `claude` / `codex` / `grok` |
| `status` | HTTP 状态码 100–599 |
| `q` | 关键字搜索 |
| `limit` | 默认 50，最大 200 |
| `offset` | 默认 0 |

**响应 `200`**

```json
{
  "data": [
    {
      "id": 12,
      "day": "20260827",
      "account_id": 1,
      "account_name": "grok-main",
      "api_key_id": 3,
      "api_key_name": "bot",
      "api_key_prefix": "unisub_xxxx",
      "provider": "grok",
      "method": "POST",
      "path": "/v1/responses",
      "query": "",
      "client_ip": "127.0.0.1",
      "status_code": 200,
      "started_at": "...",
      "finished_at": "...",
      "duration_ms": 1200,
      "request_id": "...",
      "error_type": "",
      "model": "grok-...",
      "input_tokens": 100,
      "output_tokens": 50,
      "total_tokens": 150,
      "request_truncated": false,
      "response_truncated": false
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

列表项通常不含完整请求/响应正文；详情接口返回完整 HTTP 文本字段。

### `GET /api/request-logs/:day/:id`

示例：`GET /api/request-logs/20260827/12`

**响应 `200`**

```json
{
  "log": {
    "id": 12,
    "day": "20260827",
    "request_headers": "...",
    "request_body": "...",
    "response_headers": "...",
    "response_body": "...",
    "...": "..."
  }
}
```

无权查看时对非 admin 返回 `404 not_found`（避免泄露存在性）。

---

## 12. 下游转发 API

网关将请求**原生透传**到账号绑定的上游，不改写 `model`、`messages`、`input`、`tools`、`reasoning`、`stream` 等业务字段。

### 12.1 鉴权

```http
Authorization: Bearer unisub_xxxxxxxxx
```

Key 解析失败：`401 invalid_api_key`。

### 12.2 入口一览

根路径根据 Key 绑定的 Provider 决定上游；平台别名路径会强制校验 Provider 一致。

| 方法 | 路径 | Provider | 上游行为 |
| --- | --- | --- | --- |
| `GET` | `/v1/models` | Key 绑定方 | 转发该账号上游模型列表 |
| `POST` | `/v1/messages` | Claude | Anthropic Messages |
| `POST` | `/v1/messages/count_tokens` | Claude | Anthropic Count Tokens |
| `POST` | `/v1/responses` | Codex 或 Grok | Responses |
| `POST` | `/v1/responses/*subpath` | Codex 或 Grok | 安全子路径追加到上游 Responses URL |
| `GET` | `/claude/v1/models` | Claude | 模型列表 |
| `POST` | `/claude/v1/messages` | Claude | Messages |
| `POST` | `/claude/v1/messages/count_tokens` | Claude | Count Tokens |
| `GET` | `/codex/v1/models` | Codex | 模型列表 |
| `POST` | `/codex/v1/responses` | Codex | Responses |
| `POST` | `/codex/v1/responses/*subpath` | Codex | Responses 子路径 |
| `GET` | `/grok/v1/models` | Grok | 模型列表 |
| `POST` | `/grok/v1/responses` | Grok | Responses |
| `POST` | `/grok/v1/responses/*subpath` | Grok | Responses 子路径 |

显式别名与 Key 平台不一致、Claude Key 调 Responses、Codex/Grok Key 调 Messages：均返回 `403 provider_mismatch`。

默认上游（可用环境变量覆盖）：

| Provider | API | Models |
| --- | --- | --- |
| Claude | `https://api.anthropic.com/v1` + `/messages` 等 | `https://api.anthropic.com/v1/models` |
| Codex | `https://chatgpt.com/backend-api/codex/responses` | `https://chatgpt.com/backend-api/codex/models` |
| Grok | `https://cli-chat-proxy.grok.com/v1/responses` | `https://cli-chat-proxy.grok.com/v1/models` |

查询字符串会原样追加到上游 URL。

### 12.3 请求体要求

| 规则 | 说明 |
| --- | --- |
| 体积上限 | 默认 256 MiB（`UNISUB_MAX_BODY_BYTES`，最小 1024） |
| JSON | 非 `models` 路由体必须是合法 JSON |
| `model` | `POST .../messages` 与无子路径的 `POST .../responses` 必须包含非空字符串 `model` |

### 12.4 Responses 子路径安全规则

默认拒绝。通过条件：

- 后缀以 `/` 开头
- 最多 8 段，每段最长 128
- 每段仅允许 ASCII 字母、数字、`_`、`-`、`.`
- 禁止空段、`.`、`..`、百分号编码、斜杠注入

不满足时：`404 invalid_subpath`，**不会**降级到 `/v1/responses`。

合法示例：`POST /v1/responses/compact`、`POST /codex/v1/responses/compact`

### 12.5 并发、限流与取消

| 机制 | 行为 |
| --- | --- |
| 账号并发 | `concurrency_limit`（缺省 1）；满员时排队，超时返回 `429 concurrency_limited` |
| 排队超时 | 账号 `concurrency_queue_timeout_seconds`；为 0 时用全局缺省（默认 180 秒） |
| API Key RPM | Key 上配置的 `rpm_limit`；超出返回 `429 rate_limited`，并带 `Retry-After: 60` |
| 客户端取消 | 向上游传播；日志可能记为状态 `499` / `client_canceled` |
| SSE | 实时 flush；设置 `X-Accel-Buffering: no` |

每个账号使用独立 HTTP Client / 连接池；不同账号不复用连接。

### 12.6 敏感下游头清洗

转发前会剥离客户端传入的鉴权与部分 Provider 头，再由网关注入上游所需头，包括但不限于：

`Authorization`、`x-api-key`、`Cookie`、`chatgpt-account-id`、`Host`、`Content-Length`、`Connection`、`Transfer-Encoding`、`Upgrade`，以及若干 Grok 客户端头。

### 12.7 Token 过期

| Provider | 行为 |
| --- | --- |
| Grok OAuth | 到期前约一分钟自动 refresh；失败或需重登：`401 reauth_required` |
| Claude / Codex OAuth | 到期前约十分钟自动 refresh；上游 401 时再刷新一次；失败或需重登：`401 reauth_required` |

### 12.8 调用示例

**Claude Messages**

```http
POST /v1/messages
Authorization: Bearer unisub_xxxxxxxxx
Content-Type: application/json

{
  "model": "claude-sonnet-4-20250514",
  "max_tokens": 1024,
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}
```

或使用别名：`POST /claude/v1/messages`（Key 必须绑定 Claude）。

**Codex / Grok Responses**

```http
POST /v1/responses
Authorization: Bearer unisub_xxxxxxxxx
Content-Type: application/json

{
  "model": "...",
  "input": "Hello"
}
```

响应状态码与正文（含 SSE）基本等同上游。

### 12.9 下游错误 type（网关侧）

| type | HTTP | 说明 |
| --- | --- | --- |
| `invalid_api_key` | 401 | Key 无效 |
| `reauth_required` | 401 | OAuth 刷新失败，需重新网页登录 |
| `provider_mismatch` | 403 | 平台/路由不匹配 |
| `invalid_request` / `invalid_json` | 400 | 体或 model 校验失败 |
| `request_too_large` | 413 | 超过 body 上限 |
| `invalid_subpath` | 404 | Responses 子路径不安全 |
| `rate_limited` | 429 | Key RPM |
| `concurrency_limited` | 429 | 账号并发排队超时 |
| `credential_error` | 502 | 凭据不可用 |
| `proxy_error` | 502 | 账号代理客户端错误 |
| `upstream_unavailable` | 502 | 连不上游 |
| `unsupported_endpoint` | 501 | Provider 不支持该路由类型 |
| `internal_error` | 500 | 内部错误 |

---

## 13. 错误码一览

管理与下游共用同一错误信封。常见 `error.type`：

| type | 典型场景 |
| --- | --- |
| `unauthorized` | 管理 JWT 缺失/无效 |
| `forbidden` | 需要 admin 角色 |
| `invalid_request` | 参数校验失败 |
| `invalid_credentials` | 登录或改密失败 |
| `not_found` | 资源不存在 |
| `conflict` | 用户名冲突等 |
| `last_admin` | 不能移除最后一个启用 admin |
| `rate_limited` | 登录 / OAuth start / RPM / usage refresh |
| `oauth_*` | Grok 设备码 / Claude 与 Codex PKCE 流程 |
| `usage_refresh_unsupported` / `usage_refresh_failed` | 额度刷新 |
| `internal_error` | 服务器内部错误 |

仓库错误映射：`ErrNotFound` → `404`，`ErrConflict` → `409`，`ErrLastAdmin` → `409 last_admin`。

---

## 14. 尚未实现

以下能力在方案或 README 中提及，**当前代码未提供路由**：

- Claude / Codex 权威上游额度适配器（主动 refresh）
- Codex WebSocket（`GET /v1/responses` Upgrade）
- Grok `POST /v1/chat/completions` 透传（仅当上游原生支持时的可选能力）
- `POST /api/logout`、账号 `test` / 通用 `refresh` 等计划中的管理动作

以本文件与 `internal/server` 路由表为准；若与 [`SUBSCRIPTION_API_PLAN.md`](../SUBSCRIPTION_API_PLAN.md) 冲突，以已实现代码为准。
