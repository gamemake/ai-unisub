# `api` Service Module

`api` 是面向 Web UI 和管理功能的 JSON API Service Module，负责当前用户、密码、用户管理、Provider Account、API Key、调用记录以及 OAuth JSON API。

OAuth 上游 callback 由 `oauthflow` Module 负责；项目内部 Provider 的 Codex、Claude、Grok 以及其他 AI 请求由 `gateway` Module 负责。

## 1. 通用约定

### Base URL

```text
/api
```

### 认证

除特别标明外，所有接口都需要 Web Session Cookie：

```http
Cookie: session=<session-token>
```

Service 的认证中间件先验证 Session，并将 `Principal` 写入 `request.Context`。Handler 再使用 `Principal` 进行角色判断、资源归属校验和数据过滤，不重新解析 Cookie，也不接受请求体中的 `user_id` 作为身份来源。

### 通用响应

列表接口：

```json
{
  "items": [],
  "total": 0
}
```

错误接口使用包含稳定英文消息的 JSON：

```json
{
  "error": "error message"
}
```

常用状态码：

| 状态码 | 含义 |
| --- | --- |
| `200` | 请求成功 |
| `201` | 资源创建成功 |
| `202` | 异步操作仍在等待，例如 OAuth Device Flow |
| `400` | 参数或业务数据无效 |
| `401` | 未认证或 Session 无效 |
| `403` | 已认证但无权操作 |
| `404` | 资源或一次性结果不存在 |
| `500` | 服务端或数据库错误 |

## 2. 当前用户和密码

| Method | Path | 参数 | 权限 | 作用 |
| --- | --- | --- | --- | --- |
| `GET` | `/api/me` | 无 | Session | 返回当前用户的 `id`、`name`、`role` 和 `server_version` |
| `POST` | `/api/password` | JSON：`old_password`、`new_password` | Session | 修改当前用户密码；新密码至少 8 位 |

`POST /api/password` 请求示例：

```json
{
  "old_password": "old-password",
  "new_password": "new-password"
}
```

## 3. 用户管理

| Method | Path | Path 参数 | Query 参数 | Body | 权限 | 作用 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET` | `/api/users` | 无 | 无 | 无 | Session + Admin | 返回用户列表 |
| `POST` | `/api/users` | 无 | 无 | `name`、`password`、`role` | Session + Admin | 创建用户 |
| `PUT` | `/api/users/{id}` | `id`：用户 ID | 无 | `role`、`enabled` | Session + Admin | 修改目标用户角色或启用状态 |
| `POST` | `/api/users/{id}/password` | `id`：用户 ID | 无 | `password` | Session + Admin | 管理员重置目标用户密码；密码至少 8 位 |
| `DELETE` | `/api/users/{id}` | `id`：用户 ID | 无 | 无 | Session + Admin | 删除目标用户 |

规则：

- `role` 只能是 `admin` 或 `user`；其他值按默认规则处理或返回 `400`；
- `enabled` 为布尔值；管理员不能停用自己，也不能把自己降为普通用户；
- 管理员不能重置自己的密码；
- 不能通过接口修改或删除当前登录用户自身的受保护操作；
- 普通用户不能访问用户管理接口；
- 用户 ID 来自 URL 只表示目标资源，不能替代当前请求的身份。

## 4. Provider Account 管理

| Method | Path | Path 参数 | Query 参数 | Body | 权限 | 作用 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET` | `/api/providers` | 无 | 无 | 无 | Session | 返回 Provider Account 列表；敏感 Credential 必须脱敏 |
| `POST` | `/api/providers` | 无 | 无 | `name`、`provider`、`config` | Session + Admin | 创建 Provider Account |
| `PUT` | `/api/providers/{id}` | `id`：Account ID | 无 | `name`、`provider`、`config` | Session + Admin | 编辑 Provider Account；Provider 类型不可变更 |
| `DELETE` | `/api/providers/{id}` | `id`：Account ID | 无 | 无 | Session + Admin | 删除 Provider Account，并清理无引用 Credential |

`POST /api/providers` 请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 否 | Account 显示名称 |
| `provider` | string | 是 | Provider 类型，例如 `codex`、`claude`、`grok` |
| `config` | object/string | 是 | Provider 配置；如果包含 OAuth Credential，保存后只保留 `credential_id` |

Provider Account 规则：

- 普通用户可以读取 Provider 列表，以便创建 API Key 或选择可用 Account；
- 创建和删除必须由管理员执行；
- API 响应不能包含 `access_token`、`refresh_token` 或其他 Credential 原文；
- 保存 OAuth Credential 和 Account 配置失败时，应执行补偿清理；
- 删除 Account 前要检查 Credential 是否仍被其他 Account 引用。

## 5. API Key 管理

| Method | Path | Path 参数 | Query 参数 | Body | 权限 | 作用 |
| --- | --- | --- | --- | --- | --- | --- |
| `GET` | `/api/keys` | 无 | 无 | 无 | Session | 返回当前用户拥有的 API Key，包含明文 `key` |
| `POST` | `/api/keys` | 无 | 无 | `name`、`account_id`、`valid_seconds` | Session | 创建 API Key，并返回明文 Key；`name` 必填；`valid_seconds=0` 表示永久有效 |
| `GET` | `/api/keys/{id}` | `id`：API Key ID | 无 | 无 | Session | 返回当前用户拥有的一把 API Key，包含明文 |
| `DELETE` | `/api/keys/{id}` | `id`：API Key ID | 无 | 无 | Session | 删除当前用户拥有的 API Key |

`POST /api/keys` 请求示例：

```json
{
  "name": "claude-code",
  "account_id": "account-id"
}
```

安全规则：

- API Key 必须绑定到当前用户有权使用的 Account；
- `name` 必填，最多 64 个字符，用于控制台展示，不能用 ID 代替；
- 明文 Key 在列表、按 ID 读取和创建成功的响应中返回；
- 删除和按 ID 读取必须校验 Key 所属用户。

## 6. 调用记录

| Method | Path | Path 参数 | Query 参数 | 权限 | 作用 |
| --- | --- | --- | --- | --- | --- |
| `GET` | `/api/calls` | 无 | `q`：可选搜索条件；`page`：从 1 开始；`page_size`：10、20、50 或 100 | Session | 查询调用记录 |
| `GET` | `/api/calls/{day}/{id}` | 无 | `day`：UTC 日期，格式为 `YYYYMMDD`；`id`：调用记录 ID | Session | 查询调用记录详情 |

`page_size` 必须在 10 到 100 之间，否则返回 `400`。

调用记录的查询范围由 `Principal` 决定：

- admin 可以查询全部允许范围内的记录；
- user 只能查询自己的记录；
- 客户端提交的用户筛选参数不能扩大当前主体的查询范围。

## 7. OAuth JSON API

OAuth JSON API 的业务契约属于 `api` Module；OAuth 上游 callback 属于 `oauthflow` Module。当前实现由 `OAuthFlowModule` 注册 `/api/oauth/` 路由，但两者的职责边界仍按本节和 [`service-oauthflow.md`](service-oauthflow.md) 理解。这里的 OAuth 上游服务不是项目内部的 `provider` Module 或 `ProviderManager`。

| Method | Path | Path 参数 | Query 参数 | Body | 权限 | 作用 |
| --- | --- | --- | --- | --- | --- | --- |
| `POST` | `/api/oauth/{service}/start` | `service`：`codex`、`claude`、`grok`、`dummy` | 无 | 通常为空对象 `{}` | Session | 创建 OAuth Session，返回授权地址或 Device Flow 信息 |
| `POST` | `/api/oauth/{service}/poll/{session}` | `service`、`session`：OAuth Session ID | 无 | 通常为空对象 `{}` | Session + 用户归属 | 轮询 Device Flow；等待时返回 `202` |
| `GET` | `/api/oauth/results/{id}` | `id`：一次性结果 ID | 无 | 无 | Session + 用户归属 | 读取并消费 OAuth 结果 |

`start` 成功响应至少包含以下字段：

```json
{
  "session_id": "oauth-session-id",
  "authorization_url": "https://example.com/authorize",
  "user_code": "ABCD-EFGH",
  "verification_uri": "https://example.com/verify",
  "expires_at": "2026-09-10T12:00:00Z"
}
```

PKCE 流程通常只返回 `session_id`、`authorization_url` 和 `expires_at`；Device Flow 还返回 `user_code` 和 `verification_uri`。

Device Flow 等待中的响应约定为：

```json
{
  "status": "pending",
  "error": "authorization_pending",
  "interval_seconds": 5,
  "expires_at": "2026-09-10T12:00:00Z"
}
```

完成轮询的响应为：

```json
{
  "status": "complete",
  "result_id": "one-time-result-id"
}
```

OAuth API 规则：

- `start` 创建的 Session 必须绑定当前用户；
- `poll` 和 `results` 必须校验当前用户与 Session/结果的归属；
- `results` 成功读取后立即删除，不能重复读取；
- OAuth Credential 仅可通过一次性 `/api/oauth/results/{id}` 返回；该接口返回完整的 `OAuthCredential`，并必须校验当前用户与 result 的 subjectID；
- Device Flow 轮询等待时返回 `202`，响应包含 `status`、`error`、`interval_seconds` 和 `expires_at`；
- callback 的 state、过期时间和 PKCE 校验由 `OAuthManager` 负责；
- callback URL、callback 的固定 HTML 页面、result ID 的交接方式和 OAuth 上游服务的具体协议由 [`service-oauthflow.md`](service-oauthflow.md) 维护；callback 成功时通过 `X-OAuth-Result-ID` 响应头交接 result ID，不把它写入 HTML、URL 或日志；
- `/api/oauth/results/{id}` 成功响应设置 `Cache-Control: no-store`，并返回完整的 `OAuthCredential`；除该一次性接口外，普通 API、Provider 列表、调用记录、callback 页面和日志都不得返回 Credential 敏感字段。

### OAuth API 中仍需确定或补齐的内容

- 前端如何接收 callback 返回的 `X-OAuth-Result-ID`，以及读取结果后创建 Provider、持久化 Credential 的完整流程仍待补齐；该交接不能依赖 callback HTML、URL 查询参数或日志。
- callback replay、上游 OAuth error、token exchange 失败、result 保存失败、Device Flow 的 pending/slow down/过期和并发 poll 仍需端到端测试；这些情况不能泄漏 Credential 或 result 是否属于其他用户。

## 8. 认证与业务授权

路由注册时声明最低认证要求：

```text
AuthSession middleware
    -> 确认 Session 属于哪个用户
    -> request.Context = Principal

api Handler
    -> 读取 Principal
    -> 判断 admin/user 是否可执行该操作
    -> 按 UserID、Role 和资源归属过滤查询
    -> 裁剪响应内容
```

`AuthSession` 只表示“必须登录”，不表示所有登录用户拥有相同权限。只有整条接口完全禁止普通用户时，才使用 `AuthSessionAdmin`。对于 admin 和 user 都能访问、但返回数据或可执行操作不同的接口，应使用 `AuthSession`，然后由 Handler 根据 `Principal` 完成业务授权。

如果角色、用户状态或资源归属可能在 Session 创建后发生变化，Handler 可以根据 `Principal.User.ID` 重新读取数据库进行授权检查。这是保证授权数据新鲜，不是重复认证。

## 9. 依赖与测试

本 Module 使用：

- `database.Database`：用户、Account、API Key、调用记录和 Credential；
- `ProviderManager`：创建、更新和删除 Provider 实例；
- `OAuthManager`：OAuth Session 和协议操作；
- `OAuthResultStore`：一次性 OAuth 结果；
- `AuthService`：Session、Principal 和管理员权限。

测试至少覆盖：

- admin/user 对同一路由的不同查询范围和响应内容；
- 普通用户无法访问管理员专属操作；
- URL、Query 或 Body 中的用户 ID 不能绕过 Principal；
- Provider 配置校验和 Credential 脱敏；
- API Key 只返回一次明文；
- OAuth Session 和结果的跨用户访问、过期和重复读取；
- 数据库失败时的状态码和 Credential 补偿清理。
