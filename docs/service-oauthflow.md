# `oauthflow` Service Module

`oauthflow` 负责 Web Service 与外部 OAuth 授权服务之间的 callback 衔接。本文所说的 OAuth 上游是外部 authorization server 和 token endpoint，不是项目内部的 `provider` Module 或 `ProviderManager`。

OAuth 协议、PKCE、Device Flow、Token Exchange、刷新和撤销由 `internal/oauth` 提供；OAuth JSON API（`start`、`poll`、`result`）由 `api` Module 提供；`oauthflow` 只负责无浏览器 Session 的上游 callback。项目内部 Provider 的 AI 请求转发由 `gateway` 负责。

## 1. 模块边界

`oauthflow` 可以通过 `ModuleContext` 使用以下能力：

- `OAuth()`：创建和消费 OAuth Session，调用对应 Adapter 完成协议操作；
- `OAuthResults()`：保存 callback 产生的一次性 OAuth 结果；
- `Auth()`：仅在需要调用用户相关业务 API 时使用。callback 本身不依赖浏览器登录 Session；
- `Handle` / `HandleFunc`：注册 callback 路由。

`oauthflow` 不负责：

- 用户登录、用户管理、Provider 管理、API Key 或调用记录；
- OAuth Credential 的长期持久化；
- 在 callback 中创建或修改 Provider；
- 实现某个具体上游的 OAuth 协议；
- 渲染业务页面。

callback 完成后只保存短期、一次性的结果。用户随后通过 `api` Module 读取结果，并在自己的业务流程中确认、保存 Credential 和创建 Provider。

## 2. 路由与认证

callback 路径必须兼容官方 CLI 使用的 redirect URI。路径不能作为 OAuth service 的唯一依据，也不能使用未经验证的 URL 参数决定用户归属。服务端可以将多个兼容路径注册到同一个 Handler：

| 路径 | 使用场景 | 路由认证 |
| --- | --- | --- |
| `/auth/callback` | Codex CLI 的默认 loopback callback，例如 `http://localhost:1455/auth/callback` | `AuthNone`；依赖 OAuth state 校验 |
| `/callback` | Claude Code、Grok CLI 的 loopback callback；端口由启动流程决定 | `AuthNone`；依赖 OAuth state 校验 |
| `/oauth/code/callback` | Claude 官方托管 OAuth callback 兼容路径 | `AuthNone`；依赖 OAuth state 校验 |

这些 callback 路径只允许 `GET`。其他 HTTP Method 应返回统一的 Method Not Allowed 响应，不能进入 OAuth 完成逻辑。

callback 不要求浏览器 Session，因为官方 CLI 的 callback 请求通常没有登录 Cookie。授权开始时，OAuth Session 必须已经保存 `service`、`subjectID`、redirect URI、state、PKCE verifier 或 device code，以及过期时间。callback 必须使用 Session 中保存的值恢复上下文，不能信任 callback 请求中的 service、subject 或 redirect URI。

`start`、`poll` 和 `result` 属于已认证的 `api` Module：

- `start`、`poll`、`result` 必须要求当前用户登录；
- `poll` 必须校验当前用户与 OAuth Session 的 `SubjectID` 一致；
- `result` 必须校验当前用户与临时结果的 `SubjectID` 一致；
- callback 只负责校验 OAuth Session，不把 callback 请求视为一个已登录的浏览器请求。

## 3. Session 与临时结果

`OAuthManager` 的 Session 和 Service 的 OAuth result 都是进程内存状态，当前固定 TTL 为 `10 * time.Minute`。它们不写入 SQLite，也不提供配置项覆盖 TTL；进程退出或重启后，未完成 Session 和未消费结果直接失效。过期数据在访问时惰性删除，不需要后台清理任务。

临时结果由 Service 创建并由 `oauthflow`、`api` 共享：

```go
type OAuthResult struct {
	SubjectID  string
	Service    string
	Credential oauth.OAuthCredential
}

func (s *OAuthResultStore) Put(result OAuthResult) (id string, err error)
func (s *OAuthResultStore) Take(id, subjectID string) (OAuthResult, error)
```

`Put` 要求 `SubjectID` 和 `Service` 非空，并生成不可预测的随机 result ID。`Take` 在同一把锁内完成用户校验、过期检查和删除：

- 成功读取后立即删除，result 只能读取一次；
- 用户不匹配时不能删除其他用户的 result；
- 不存在、过期和跨用户读取统一返回 not found，避免泄漏 result 是否存在；
- 并发读取同一个 ID 时最多只有一个请求成功。

结果 ID 不是 Credential ID。它只用于完成当前授权流程，不能作为长期引用，也不能写入 Provider 配置。

## 4. 标准流程

### 4.1 PKCE callback 流程

1. 已认证的 `api` Handler 调用 `OAuthManager.Start(ctx, service, subjectID, redirectURI)`。
2. `OAuthManager` 创建 Session，并为 PKCE 流程生成 state 和 code verifier；code verifier 只保存在服务端 Session 中。
3. `start` 将授权 URL 和 Session ID 返回给前端。前端打开授权 URL，但不得获得 code verifier。
4. OAuth 上游将浏览器或 CLI 导向兼容的 callback 路径，并携带 `code`、`state`，或携带上游 `error` 参数。
5. callback 根据 state 找到待处理 Session，并以 Session 中保存的 service、subjectID 和 redirect URI 为准调用 `OAuthManager.Complete`。
6. `Complete` 校验 Session、过期时间、state 和一次性消费语义，然后使用保存的 PKCE verifier 与保存的 redirect URI 完成 token exchange。
7. 成功获得 Credential 后，callback 将完整 Credential 写入 `OAuthResultStore`，得到一次性 result ID，并返回固定的成功页面。
8. 前端使用已认证的 `GET /api/oauth/results/{id}` 读取并消费结果；服务端随后再将 Credential 持久化到 CredentialStore，并只在 Provider 配置中保存 `credential_id`。

### 4.2 Device Flow 流程

1. `api` Handler 调用 `Start`，OAuth Adapter 返回 device code、user code、verification URI 和上游过期时间。
2. `OAuthManager` 将 device code 和过期时间保存到 Session，`start` 返回验证地址、user code、Session ID 和过期时间。
3. 用户在上游页面完成授权后，前端调用 `poll`。
4. `Poll` 在 pending 或 slow down 时保留 Session；只有成功取得 Credential 时才消费 Session。
5. 成功时将 Credential 放入 `OAuthResultStore`，客户端再通过 result API 一次性读取。

Device Flow 错误映射如下：

| 上游状态 | API 行为 |
| --- | --- |
| `authorization_pending` | 返回 `202`，保留 Session，客户端继续轮询 |
| `slow_down` | 返回 `202`，保留 Session，并增加下一次轮询间隔 |
| device code 或 Session 过期 | 终止本次流程，返回授权过期错误 |
| 成功取得 Credential | 保存一次性 result，消费 Session |
| 成功后的重复 poll | 返回 Session 不存在或已完成 |

`202` 响应至少应包含以下语义字段，具体响应包装以 [`service-api.md`](service-api.md) 为准：

```json
{
  "status": "pending",
  "error": "authorization_pending",
  "interval_seconds": 5,
  "expires_at": "2026-09-10T12:00:00Z"
}
```

## 5. callback Handler 契约

callback Handler 应按以下顺序处理请求：

1. 检查请求 Method 和 callback 参数；缺少 `state` 时立即返回参数错误，不查找或消费任何 Session。
2. 如果存在上游 `error`，不进行 token exchange；根据 state 找到 Session 后调用 `DiscardSession`，返回失败页面。若 state 缺失，则不消费 Session。
3. 根据 state 定位 Session，并检查 Session 是否存在、是否过期以及其 service 和 subjectID 是否有效。
4. 对成功授权请求要求 `code` 存在，调用 `Complete` 完成一次性校验和 token exchange。
5. 仅在获得非空 Access Token 后调用 `OAuthResultStore.Put`。保存失败时返回失败页面，不向响应写出 Credential。
6. 成功时返回固定的成功页面；失败时返回固定的失败页面。页面不得承载 OAuth 业务数据。

`Complete` 当前在 state 校验成功后先消费 Session，再访问 token endpoint。因此 token exchange 失败也会使原 Session 失效；客户端必须重新执行 `start`。这是有意采用的一次性 callback 语义，不为网络失败开放 Session 重试。

### Session 定位接口的实现要求

callback 的安全依据必须是 state 与 Session 的绑定关系。当前代码中的接口是：

```go
SessionForState(service, subjectID, state string) (string, error)
```

该接口需要 service 和 subjectID 作为查找条件，而 callback 路径本身不携带可信的用户上下文。因此，在实现 Service callback Handler 时，必须解决这一边界：

- 推荐为 `OAuthManager` 增加仅凭 state 查找 Session 的内部接口，并由返回的 Session 提供 service 和 subjectID；或
- 明确规定 callback 使用另一个可信的、与授权开始阶段绑定的上下文，再调用现有接口。

无论采用哪种实现，URL 中的 service、浏览器 Cookie 或用户提交字段都不能覆盖 Session 中保存的归属。`Complete` 仍必须作为最终的 state、过期和一次性消费校验点。

## 6. Callback 结果页与 API 交接

callback 只返回固定的最小 HTML 页面：

- 成功：`授权成功，可以关闭此窗口`；
- 失败：`授权失败，请重新开始授权`；
- 不显示 `code`、`state`、Access Token、Refresh Token、result ID 或其他 OAuth 数据；
- 不自动跳转到 `/home` 或其他页面；
- 不把 callback 页面当作 OAuth JSON API。

result ID 应通过前端已拥有的流程状态、约定的安全交接机制或后续 API 流程传递，不能放入 callback HTML 页面、日志或 URL 查询参数中。若产品流程要求浏览器 callback 直接把结果交给前端，需要另外定义安全的、短期的交接机制，并保持 result API 的用户归属和一次性消费约束。

OAuth JSON API 的职责见 [`service-api.md`](service-api.md)：成功读取 result 时返回完整的 `OAuthCredential`，包括 Access Token、Refresh Token 和其他标准化字段。完整 Credential 只允许出现在这个一次性 result API 中，不得出现在 Provider 列表、调用记录、普通管理 API、callback 页面或日志中。

## 7. 错误、日志与安全要求

建议的 HTTP 映射如下：

| 场景 | 状态码或表现 |
| --- | --- |
| 未登录访问 `start`、`poll`、`result` | `401` |
| Session 不存在、已过期或 state 无效 | callback 统一失败页面；API 按通用错误约定返回 `400` |
| OAuth 上游暂时不可用或 token exchange 失败 | `502` 或 `503`；callback 返回失败页面 |
| Device Flow pending / slow down | `202` |
| result 不存在、过期或跨用户访问 | `404` |
| 不支持的 Method | `405` |

实现和日志必须满足：

- state 必须使用密码学安全随机值，并与唯一 Session 绑定；
- PKCE verifier、authorization code、device code、Access Token 和 Refresh Token 不得写入日志；
- 上游错误返回用户前必须限制长度并清理内容，不得原样暴露内部请求细节；
- callback 请求不能通过 service、subjectID 或 redirect URI 参数改变 Session 归属；
- result API 应禁止缓存，成功消费后不得再次返回 Credential；
- 部署层可以额外限制 callback 的 Host、来源地址或监听端口，但这些限制不能替代 state、Session 归属和过期时间校验。

## 8. 实现状态与验收清单

当前仓库已经具备：

- `internal/oauth` 的 OAuthManager、Session、PKCE、Device Flow 和 Adapter；
- `OAuthManager.Complete`、`Poll`、`SessionForState` 和 `DiscardSession`；
- Service 所有权的进程内 `OAuthResultStore`，包含过期、用户归属和一次性消费；
- `ModuleContext.OAuth()` 与 `ModuleContext.OAuthResults()` 能力。

当前已由 Service 层实现：

- `OAuthFlowModule` 及上述兼容路径的注册；
- `api` Module 语义下的 `start`、`poll`、`result` Handler；
- callback 的 state-only Session 定位、固定 HTML 结果页和 result ID 响应头交接；
- poll 的当前用户、service 和 Session 归属校验。

当前仍需继续补齐或验证：

- 前端对 `X-OAuth-Result-ID` 的交接和完整 Provider 创建流程；
- Handler 的上游错误、callback replay 和 Device Flow 端到端测试；
- Credential 持久化、Provider 配置保存和失败补偿流程。

至少应覆盖以下测试：

- callback 缺少 state/code、错误 state、未知 Session、过期 Session 和重复 callback；
- 上游 OAuth error、token exchange 失败和 result 保存失败；
- Device Flow pending、slow down、过期、成功完成和重复 poll；
- 不同用户读取 result、过期读取、并发读取和重复读取；
- 成功 result API 返回完整 Credential，普通 API 和日志不泄漏敏感字段；
- callback 不依赖浏览器 Session，而 `start`、`poll`、`result` 依赖并校验当前用户；
- 重复路由、Method、未匹配路由和统一认证失败响应。
