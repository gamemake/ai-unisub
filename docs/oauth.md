# OAuth 包设计

`internal/oauth` 负责 OAuth 协议适配、短期授权会话、Token 交换、刷新与可选撤销。它统一 OpenAI、Anthropic、xAI 和本地 Dummy 服务的差异，并把所有出站请求接入代理组调度与 `proxy_logs` 记录。

OAuth 包不注册 HTTP 路由、不识别登录用户，也不持久化业务账号或 OAuth Credential。Web 层负责身份校验和结果交接，AI Provider 层负责最终账号存储；命令行工具的凭据文件能力位于 `cmd/oauth`，不属于 OAuth 包。

## 包边界与依赖

Manager 的构造函数为：

```go
func NewManager(db database.Database, proxy proxy.ProxyManager) OAuthManager
```

直接依赖如下：

| 依赖 | 用途 |
| --- | --- |
| `database.Database` | 自定义 Revoke HTTP Client 的逐次调用日志；不用于存储 OAuth Credential 或 Session |
| `proxy.ProxyManager` | 为 Start、Complete、Poll、Refresh 选择代理组并记录调用日志 |
| `internal/logger` | 通过包级 `ModuleLogger` 输出结构化事件 |

`ProxyManager` 是 Start 和 Refresh 的必要依赖。`database.Database` 在使用外部传入的 Revoke Client 时必须存在。生产环境由 `internal/service` 创建 Database、ProxyManager 和 OAuthManager，并把同一组实例注入各业务模块。

OAuthManager 本身没有 `Open`、`Close` 等生命周期方法；Adapter 注册表和未完成 Session 都只存在于当前进程。

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `types.go` | Credential、Session、Adapter 能力接口和 OAuthManager 接口 |
| `manager.go` | 默认 Adapter 注册、授权流程、Session 状态与凭据操作 |
| `transport.go` | ProxyManager Transport 和自定义 Client 的数据库日志包装 |
| `adapter_common.go` | PKCE、OAuth HTTP 响应和 Token 响应的通用处理 |
| `adapter_openai.go` | OpenAI/Codex PKCE Adapter |
| `adapter_anthropic.go` | Anthropic/Claude PKCE Adapter |
| `adapter_xai.go` | xAI/Grok Device Flow Adapter |
| `adapter_dummy.go` | 本地 Device Flow 模拟实现 |
| `errors.go` | OAuth 包内公开状态错误与内部实现错误 |
| `logger.go` | 声明 `ModuleLogger = logger.ModuleLogger("oauth")` |

各 Adapter 与公共辅助代码都属于同一个 `oauth` 包，没有 `internal/oauth/adapters` 子包。

## 数据模型

### OAuthCredential

标准化凭据包含：

```go
type OAuthCredential struct {
    AccessToken  string
    RefreshToken string
    TokenType    string
    ExpiresAt    time.Time
    AccountID    string
    AccountName  string
    Email        string
}
```

Adapter 不保留完整上游响应。`expires_in` 会转换为 UTC 绝对时间；上游未返回 `token_type` 时使用 `Bearer`。Manager 要求 Exchange、Poll 和 Refresh 的结果必须包含非空 Access Token。

### OAuthSession

Session 是一次尚未完成的授权流程，保存：

- `ID`、`Service`、调用方提供的 `SubjectID`
- PKCE 使用的 `RedirectURI`、`State`、`CodeVerifier`
- Device Flow 使用的 `DeviceCode`
- 本次流程绑定的 `HTTPClient`
- `ExpiresAt`

Session 不会序列化到数据库。`SubjectID` 是 OAuth 包不解释的不透明业务主体标识；Web 集成使用当前用户 ID，CLI 使用空字符串。

### StartResult

Start 向调用方返回 `session_id`、`authorization_url` 和 `expires_at`。Device Flow 还会返回 `user_code` 与 `verification_uri`，但不会暴露 Device Code；PKCE Code Verifier 也不会离开 Session。

## Adapter 能力

```go
type OAuthAdapter interface {
    Service() string
    Refresh(context.Context, *OAuthCredential, *http.Client) (*OAuthCredential, error)
}

type PKCEAdapter interface {
    OAuthAdapter
    BuildAuthorizationURL(context.Context, AuthorizationInput) (AuthorizationResult, error)
    Exchange(ctx context.Context, code, state, codeVerifier, redirectURI string, client *http.Client) (*OAuthCredential, error)
}

type DeviceAdapter interface {
    OAuthAdapter
    StartDeviceAuthorization(context.Context, DeviceStartInput) (DeviceAuthorizationResult, error)
    PollDeviceToken(ctx context.Context, deviceCode string, client *http.Client) (*OAuthCredential, error)
}

type RevocableAdapter interface {
    Revoke(context.Context, *OAuthCredential, *http.Client) error
}
```

`NewManager` 默认注册以下 Adapter：

| Service | CLI 名称 | 流程 | 实现要点 |
| --- | --- | --- | --- |
| `openai` | `codex` | PKCE | Token 请求使用表单编码；从 ID Token claims 提取邮箱和账号 ID |
| `anthropic` | `claude` | PKCE | Token 请求使用 JSON；刷新时保留旧账号信息和缺失的 Refresh Token |
| `xai` | `grok` | Device Flow | 映射 `authorization_pending`、`slow_down` 状态 |
| `dummy` | 无 | Device Flow | 第一次 Poll 返回 pending，第二次返回本地模拟凭据 |

OpenAI、Anthropic 和 xAI 构造函数都支持覆盖端点、Client ID、Scopes 与默认 HTTP Client。Manager 调用时传入的 Session/Proxy Client 优先于 Adapter 配置中的 Client。

`Register` 可增加新 Service，但不允许空 Adapter、空 Service 或覆盖同名默认 Adapter。当前内置 Adapter 均未实现 `RevocableAdapter`。

## OAuthManager 接口

```go
type OAuthManager interface {
    Register(adapter OAuthAdapter) error
    Start(ctx context.Context, service, subjectID, redirectURI string, proxyGroup int) (*StartResult, error)
    Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error)
    SessionForState(state string) (OAuthSession, error)
    SessionForSubjectState(service, subjectID, state string) (string, error)
    SessionForSubject(sessionID, service, subjectID string) (OAuthSession, error)
    DiscardSession(sessionID string) error
    Poll(ctx context.Context, sessionID string) (*OAuthCredential, error)
    Refresh(ctx context.Context, service string, credential *OAuthCredential, proxyGroup int) (*OAuthCredential, error)
    Revoke(ctx context.Context, service string, credential *OAuthCredential, client *http.Client) error
}
```

Start、Complete、Poll、Refresh 和 Revoke 只返回操作结果，不自动保存 Credential。包内不存在 CredentialStore、`GetValidAccessToken` 或自动刷新能力。

## PKCE 流程

1. `Start` 校验 Service 和 Proxy Group，创建 30 秒超时的代理 Client。
2. Manager 生成 48 字符 Session ID、State，以及由两个随机值拼接成的 Code Verifier。
3. Adapter 以 S256 challenge 构建 Authorization URL。
4. Session 保存 State、Verifier、Redirect URI 和 HTTP Client。
5. 回调方先按业务需要调用 `SessionForSubject` 或 `SessionForSubjectState` 校验归属，再调用 `Complete`。
6. `Complete` 校验 State，原子消费 Session，然后调用 Adapter Exchange。
7. Manager 校验返回的 Credential 并交还调用方。

State 不匹配时返回 `ErrStateMismatch`，Session 保留，可使用正确 State 重试。State 正确后 Session 会在访问上游之前被消费，所以 Exchange 失败时不能复用原 Session，调用方必须重新 Start。并发 Complete 最多只有一个调用能成功消费 Session。

PKCE 要求非空 Redirect URI。默认 Session TTL 为 10 分钟；如果 Adapter 返回了更早的有效期，则缩短到该时间，不会用 Adapter 结果延长 Session。

## Device Flow

1. `Start` 通过 Adapter 获取 Device Code、User Code、Verification URI、有效期和建议轮询间隔。
2. Device Code 仅保存在 Session；调用方只收到用户需要的验证信息。
3. 调用方使用 Session ID 调用 `Poll`。
4. `ErrAuthorizationPending` 和 `ErrSlowDown` 表示仍可继续轮询，Session 保留。
5. 成功且 Credential 有效后，Manager 消费 Session 并返回凭据。

Device Adapter 返回非零有效期时会直接替换默认 10 分钟 TTL。Manager 不负责定时轮询，也不强制 Adapter 给出的 `Interval`；具体轮询节奏由 Web 或 CLI 调用方控制。

当前 Poll 没有按 Session 设置串行锁。并发轮询可能同时请求上游；成功后只有一个调用能消费 Session，其他调用会得到 Session 不存在，但不能假设上游只发生了一次 Token 请求。

## Session 生命周期与查询

- Session map 由 Manager Mutex 保护，进程重启后全部失效。
- 过期 Session 在按 ID 或匹配 State 访问时惰性删除，没有后台清理任务。
- `SessionForState` 线性扫描当前 Session；它只清理 State 匹配但已过期的项。
- `SessionForSubject` 同时校验 Session ID、Service 和 Subject ID；归属不符统一返回 `ErrSessionNotFound`，避免泄露其他主体的 Session。
- `DiscardSession` 用于显式消费 Session，例如上游回调携带错误时。

Manager 只提供授权会话的进程内并发保护，不提供多进程协调、持久化恢复或凭据级刷新锁。

## Refresh 与 Revoke

`Refresh` 根据 Service 选择 Adapter，并为本次请求按 `proxyGroup` 创建 Client。刷新响应未提供下列字段时，Manager 使用旧凭据补齐：

- Refresh Token
- Token Type
- Account ID
- Account Name
- Email

Refresh 不判断 Access Token 是否即将过期、不加刷新互斥锁，也不保存结果。是否刷新、何时刷新和如何原子更新业务账号由调用方负责。

Revoke 是 Adapter 的可选能力：

- 传入 Client 为 nil 时，Manager 使用 Proxy Group 0 的直连路径，仍通过 ProxyManager 记录日志。
- 传入自定义 Client 时，Manager 复制 Client，并在原 Transport 外包装 `recordingTransport`，把 URL、HTTP 状态和传输错误写入 Database。
- 自定义 Client 路径不会改写调用方的原 Client，但要求构造 Manager 时提供非 nil Database。
- Adapter 未实现 `RevocableAdapter` 时返回“不支持撤销”错误。当前四个内置 Adapter 都属于这种情况。

## HTTP 响应处理与安全边界

Adapter 公共请求处理遵循以下规则：

- 只接受 2xx；响应体最多读取 1 MiB。
- 非 2xx 响应优先解析 OAuth `error` 和 `error_description`。
- 无标准 OAuth Error 时，错误保留压缩空白后的响应体前 200 字节，供诊断 WAF 或代理页面。
- Token 响应必须包含 Access Token；缺失 Token Type 时使用 `Bearer`。
- `expires_in` 仅在大于零时转换为 UTC `ExpiresAt`。
- OpenAI ID Token 只做 JWT Payload 的 Base64 解码来提取展示信息，不验证签名，不能把这些 claims 当作本地身份认证依据。

Credential、Authorization Code、Verifier 和 Device Code 不应写入普通日志。需要特别注意：非 OAuth 错误响应的 `BodySnippet` 当前会进入返回错误及失败日志，代码并未对该片段做完整脱敏；上游错误页不得包含秘密信息。

## 代理与调用日志

Start 和 Refresh 接收 Proxy Group ID，而不是代理 URL：

- 负数 Group ID 直接拒绝。
- `0` 表示不使用代理组，但请求仍经过 ProxyManager 的直连路径并写入 `proxy_logs`。
- 大于零时，Transport 在每次请求时调用 `ProxyManager.Do`；Session 只固定 Group ID，不固定某个代理 URL，因此后续 Complete/Poll 会读取代理组的当前配置并由 ProxyManager 调度。
- 日志的 `app` 标识为 `oauth:<service>`。
- Transport 把 5xx 归类为应用失败，供代理调度记录；其他 HTTP 状态交给 Adapter 的 OAuth 响应处理。
- Session 保存创建时的 HTTP Client，确保 Start 与后续 Complete/Poll 使用同一个代理组配置入口。

具体代理组选择、重试、健康状态和日志字段见 [Proxy 包设计](proxy.md)。

## Web 集成边界

`internal/unisub/oauthflow.go` 才是 HTTP 边界，OAuth 包本身不注册路由。当前 Web 流程提供：

| 路由 | 用途 |
| --- | --- |
| `POST /api/oauth/{service}/start` | 读取可选的 `proxy_group_id` 并启动授权 |
| `POST /api/oauth/{service}/complete/{sessionID}` | 提交 PKCE Code 与 State |
| `POST /api/oauth/{service}/poll/{sessionID}` | 轮询 Device Flow |
| `GET /api/oauth/{service}/status/{sessionID}` | 查询当前用户的授权状态 |
| `GET /api/oauth/results/{resultID}` | 一次性读取授权结果 |
| `GET /auth/callback`、`/callback`、`/oauth/code/callback` | 接收无需登录态的上游回调 |

`/api/oauth/` 下的接口要求登录态和管理员身份，并把 Session 绑定到当前用户 ID。无登录态的回调依靠不可预测 State 找到 Session；上游返回错误时丢弃对应 Session。

Complete 或 Poll 得到的 Credential 先进入短期、一次性读取的 `OAuthResultStore`，再由应用流程取走并保存为业务账号。OAuth 包和 OAuthFlow 都不会直接把 Credential 写入 Database 的账号表。

## CLI

`cmd/oauth` 是 OAuthManager 的独立调用方：

```text
oauth login <codex|claude|grok> [--output <file>]
oauth status --file <file>
oauth refresh --file <file> [--provider <provider>]
oauth revoke --file <file> [--provider <provider>]
oauth logout --file <file> [--provider <provider>]
```

CLI 每次运行都会创建 SQLite 内存 Database 和仅使用 Group 0 的 ProxyManager。Login 总超时为 15 分钟，其他凭据操作为 2 分钟。

- Codex 回调路径为 `/auth/callback`，Claude 为 `/callback`；二者监听 `127.0.0.1` 随机端口。
- Grok 使用 Device Flow，当前固定每 5 秒 Poll，没有采用 Adapter 返回的 Interval。
- `status` 只按本地 `ExpiresAt` 输出 `valid`、`expiring` 或 `expired`，不会访问上游。
- Login 输出或保存的 JSON 带 CLI Provider 名称。
- Refresh 覆盖文件时只序列化标准 OAuthCredential，当前会丢失 Provider 元数据；后续操作可显式传 `--provider`。
- 当前内置 Adapter 均不支持 Revoke，因此 `revoke` 会失败并保留文件；`logout` 会忽略“不支持撤销”错误并删除本地文件。

凭据文件实现在 `cmd/oauth/credential_file.go`。写入时校验 JSON，创建同目录临时文件，设置目录 `0700`、文件 `0600`，执行 Sync 后 Rename；当前没有专用 Windows ACL 处理，也没有跨进程文件锁。

## 错误与日志

供调用方进行状态分支的公开错误为：

- `ErrSessionNotFound`
- `ErrSessionExpired`
- `ErrStateMismatch`
- `ErrUnsupportedFlow`
- `ErrAuthorizationPending`
- `ErrSlowDown`

其余参数、配置和上游响应错误只在 OAuth 包内声明，并通过包装后的 `error` 返回。每个错误都定义在本包 `errors.go`；OAuth 的包级日志入口定义在 `logger.go`，其他源码不自行创建模块 Logger。

主要结构化日志事件包括 Adapter 注册、授权开始/完成、State 不匹配、轮询等待、刷新/撤销成功，以及各阶段的上游失败。日志事件使用 Service 和错误文本定位问题，不记录完整 Credential。

## 当前限制与验证范围

当前实现有以下明确限制：

- Session 只在单进程内存中，重启后无法恢复，也没有主动清理循环。
- Credential 的持久化、所有权和加密不属于 OAuthManager。
- 没有自动 Token 有效性检查、自动刷新、刷新去重或分布式锁。
- 内置 Adapter 没有 Revoke 实现。
- JWT claims 仅用于补充账号元数据，未做密码学验证。
- Dummy 与 Mock Server 测试不能证明真实平台的 Client ID、Scopes 或端点长期兼容。

现有自动化验证集中在：

| 测试文件 | 覆盖范围 |
| --- | --- |
| `internal/oauth/manager_test.go` | PKCE State 与单次消费、OAuth 出站代理日志、Dummy Device Flow、刷新元数据保留 |
| `internal/oauth/adapters_test.go` | OpenAI PKCE 与 claims、Anthropic JSON Exchange、xAI pending 与 Token 响应 |

CLI 当前没有独立测试文件。涉及真实上游兼容性时，还需要使用受控测试账号做集成验证。
