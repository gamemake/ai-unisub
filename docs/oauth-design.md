# OAuth 模块设计与行动项

## 1. 目标

为 Grok、Codex、Claude 提供统一 OAuth 能力，并支持：

- Web Service：在添加 Provider 页面完成 OAuth，并显示完整 OAuth 结果。
- CLI：通过浏览器和 localhost 回调完成 OAuth；CLI 始终打印授权地址，也支持用户显式保存到本地凭据文件。

OAuth 是独立模块，不依赖 `service`、`cmd` 或具体 Provider 实现。Web 和 CLI 只负责交互，OAuth 模块负责授权流程、Token 交换、刷新、撤销和 Credential 生命周期管理；具体持久化由调用方注入的 `CredentialStore` 完成。CLI 运行期间可以在内存中保存一次性 state/PKCE；只有用户指定输出文件时，CLI 才将结果保存到本地。

## 2. 总体架构

```text
Web Service ─────┐
                 ├── OAuthManager ── GrokOAuthAdapter
CLI ─────────────┘                 ├── CodexOAuthAdapter
                                   └── ClaudeOAuthAdapter
                                           │
                                  OAuth Result
                                  returned to caller
```

依赖关系：

```text
service -> oauth
cmd/cli -> oauth
provider -> OAuthManager.GetValidAccessToken
oauth 不依赖 service、provider、cmd
cmd/cli 不依赖 service，也不调用 Web Handler
```

CLI 是独立的命令行应用。它自己创建 `OAuthManager`，自己启动临时的 localhost HTTP Server 接收 OAuth callback，不复用 `internal/service` 的 Server、Handler、Session 或路由。

建议目录：

```text
internal/oauth/
├── manager.go
├── types.go
├── adapter.go
├── errors.go
├── http.go
└── adapters/
    ├── grok.go
    ├── codex.go
    └── claude.go
```

## 3. 核心接口

### 3.1 OAuthAdapter

OAuthAdapter 是独立的上游 OAuth 协议适配器，不属于 `provider.Provider`，也不由 `ProviderManager` 注册或管理。每个上游 OAuth 服务实现一个 OAuthAdapter，隔离授权地址、Token 地址、请求参数、响应格式和账户信息解析差异。

这里的上游 OAuth 服务标识使用普通 `string`，通过常量提供已知服务名称：

```go
const (
    OAuthServiceGrok   = "grok"
    OAuthServiceCodex  = "codex"
    OAuthServiceClaude = "claude"
)
```

通用接口只定义所有服务都需要的能力；具体授权流程通过能力接口扩展。不要强迫 Device Flow 服务实现没有意义的 `BuildAuthorizationURL` 或 `Exchange` 方法。

```go
type OAuthAdapter interface {
    Service() string
    Refresh(context.Context, *OAuthCredential) (*OAuthCredential, error)
}

type PKCEAdapter interface {
    OAuthAdapter
    BuildAuthorizationURL(context.Context, AuthorizationInput) (AuthorizationResult, error)
    Exchange(context.Context, code, state, codeVerifier, redirectURI string) (*OAuthCredential, error)
}

type DeviceAdapter interface {
    OAuthAdapter
    StartDeviceAuthorization(context.Context, DeviceStartInput) (DeviceAuthorizationResult, error)
    PollDeviceToken(context.Context, deviceCode string) (*OAuthCredential, error)
}

type RevocableAdapter interface {
    Revoke(context.Context, *OAuthCredential) error
}
```

Claude 和 Codex 实现 `PKCEAdapter`，Grok 实现 `DeviceAdapter`。如果某个服务支持撤销，再额外实现 `RevocableAdapter`。三家可以共享 Session、错误处理、Token 结果转换和 HTTP 限制，但授权 URL、Token 请求格式、轮询、Header 和响应解析必须由各自适配器处理。

### 3.2 OAuthManager

```go
type OAuthManager struct {
    adapters map[string]OAuthAdapter
    mu       sync.RWMutex
    sessions map[string]OAuthSession
}

type OAuthSession struct {
    ID           string
    Service      string
    SubjectID    string
    RedirectURI  string
    State        string
    CodeVerifier string
    DeviceCode   string
    ExpiresAt    time.Time
}
```

`OAuthManager` 只负责注册 Adapter、创建和消费一次性 Session、校验用户归属、state、PKCE 和过期时间，然后调用对应的能力接口。Grok 的 `device_code`、`user_code`、轮询时间和过期时间属于 Session 状态，不应由 Grok Adapter 自己维护。

### SubjectID 和 SessionID

`SubjectID` 由调用方生成或提供，`SessionID` 由 `OAuthManager.Start` 生成，两者职责不同：

```text
SubjectID
    └── 表示“这次授权属于谁”

SessionID
    └── 表示“这一次 OAuth 流程是哪一次”
```

Web 场景中，`SubjectID` 使用当前登录用户的本地用户 ID：

```go
start, err := manager.Start(
    ctx,
    "claude",
    currentUser.ID,
    "https://example.com/oauth/claude/callback",
)
```

CLI 没有 Web 登录用户时，可以为空：

```go
start, err := manager.Start(
    ctx,
    "claude",
    "",
    "http://127.0.0.1:8765/oauth/callback",
)
```

`SessionID` 由 OAuthManager 使用密码学安全随机数生成，例如 128 bit 或更高随机性。它不应该由前端、CLI 参数或上游 OAuth 服务生成。

`OAuthManager.Start` 生成 `SessionID` 后，在内存中保存：

```text
SessionID
Service
SubjectID
RedirectURI
state
code_verifier
expires_at
```

并返回：

```go
type StartResult struct {
    SessionID        string
    AuthorizationURL string
    ExpiresAt        time.Time
}
```

调用方需要保存或传递 `SessionID`，但不需要理解其内容。Web 可以通过 HttpOnly、短期 Cookie 或自己的页面状态关联它；CLI 则在自己的进程变量或 callback Server 闭包中保存它。CLI 不通过 Web Service 传递或保存 `SessionID`。

OAuth callback 时，调用方将 `SessionID`、`code` 和 `state` 传给 `Complete`：

```go
result, err := manager.Complete(
    ctx,
    start.SessionID,
    callbackCode,
    callbackState,
)
```

Manager 使用 `SessionID` 找回内存 Session，然后校验：

1. Session 是否存在。
2. Session 是否过期。
3. callback 的 `state` 是否匹配。
4. callback 使用的 `code` 是否应该交给该 Session 对应的 OAuthAdapter。
5. Web 场景下，当前用户是否与 Session 的 `SubjectID` 一致。

完成后删除该 Session，防止 callback 重放。

OAuthManager 至少提供：

```go
Start(ctx context.Context, service, subjectID, redirectURI string) (*StartResult, error)
Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error)
Refresh(ctx context.Context, service string, credential *OAuthCredential) (*OAuthCredential, error)
Revoke(ctx context.Context, service string, credential *OAuthCredential) error
GetValidAccessToken(ctx context.Context, credentialID string) (string, error)
```

`Complete` 必须校验 Session、过期时间、`state` 和 PKCE `code_verifier`，完成 Token Exchange，返回 `OAuthCredential`，并删除一次性 Session。OAuthManager 不保存已完成的结果，也不维护 Credential 列表。已完成的 Credential 由调用方通过注入的 `CredentialStore` 持久化。

### 3.3 OAuthCredential

OAuth 结果只保留标准化字段，不保存上游原始响应：

```go
type OAuthCredential struct {
    AccessToken  string    `json:"access_token,omitempty"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    TokenType    string    `json:"token_type,omitempty"`
    ExpiresAt    time.Time `json:"expires_at,omitempty"`

    AccountID   string `json:"account_id,omitempty"`
    AccountName string `json:"account_name,omitempty"`
    Email       string `json:"email,omitempty"`
}
```

示例：

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "account_id": "account_xxx",
  "email": "user@example.com"
}
```

## 4. Web 集成

### 4.1 页面流程

```text
添加 Provider
    ↓
选择 Grok / Codex / Claude
    ↓
点击“完成 OAuth”
    ↓
跳转上游授权
    ↓
OAuth callback
    ↓
返回 Provider 添加页面
    ↓
显示完整 OAuth JSON
    ↓
用户填写名称、并发数；模型由每次请求指定
    ↓
保存 Provider
```

OAuth 完成后，页面展示：

```text
OAuth 完整结果
┌────────────────────────────────────────────┐
│ {                                          │
│   "access_token": "...",                  │
│   "refresh_token": "...",                 │
│   "token_type": "Bearer",                 │
│   "account_id": "account_xxx",            │
│   "email": "user@example.com"              │
│ }                                          │
└────────────────────────────────────────────┘

Provider 名称：[我的 Claude 账号]
并发数：[4]

请求模型：由调用方在每次请求中指定

[保存 Provider]
```

可以使用 JSON 编辑器或 `textarea`：

```javascript
document.querySelector("#oauth-result").value =
    JSON.stringify(result.result, null, 2);
```

### 4.2 Web API

启动 OAuth：

```http
POST /api/oauth/{provider}/start
```

响应：

```json
{
  "session_id": "oauth-session-xxx",
  "authorization_url": "https://provider.example/authorize?...",
  "expires_at": "2026-09-10T12:00:00Z"
}
```

回调：

```http
GET /oauth/{provider}/callback?code=xxx&state=yyy
```

读取一次性完整结果：

```http
GET /api/oauth/results/{result_id}
```

返回：

```json
{
  "service": "claude",
  "result": {
    "access_token": "...",
    "refresh_token": "...",
    "token_type": "Bearer",
    "account_id": "account_xxx",
    "email": "user@example.com"
  }
}
```

提交 Provider：

```http
POST /api/providers
```

请求：

```json
{
  "name": "我的 Claude 账号",
  "service": "claude",
  "config": {
    "oauth": {
      "access_token": "...",
      "refresh_token": "...",
      "token_type": "Bearer",
      "account_id": "account_xxx",
      "email": "user@example.com"
    },
    "max_concurrent_connections": 4
  }
}
```

### 4.3 临时 OAuth Credential

OAuth callback 不直接创建 Provider，而是保存短期临时结果：

```go
type PendingOAuthCredential struct {
    ID        string
    SubjectID string
    Service   string
    Result    OAuthCredential
    ExpiresAt time.Time
}
```

callback 完成后重定向：

```text
/providers/new?oauth_result=result_xxx
```

临时结果必须：

- 5 到 10 分钟内过期；
- 只能被发起授权的用户读取；
- 读取或提交后删除；
- URL 中只放结果 ID，不放 Token；
- 不允许跨 Provider 使用。

## 5. CLI 集成

### 5.1 命令设计

```bash
app oauth login grok
app oauth login codex
app oauth login claude
app oauth login claude --output ./oauth/claude.json
app oauth status --file ./oauth/claude.json
app oauth refresh --file ./oauth/claude.json
app oauth revoke --file ./oauth/claude.json
app oauth logout --file ./oauth/claude.json
```

CLI 不写服务器数据库，但可以在用户明确指定的本地文件中保存和管理 OAuth 结果。`--file` 指定凭据文件；`--output` 是 `login` 的别名语义，用于保存本次登录结果。没有指定文件时，`login` 只向 stdout 输出结果。

默认约定是一个文件保存一个 OAuth 结果，因此 Provider 从文件内容的 `provider` 字段中读取，不需要每个命令重复传入 `--provider`。命令执行时仍必须校验文件中的 Provider、Token 字段和结果格式。由于没有集合文件，CLI 不提供 `list`；由于文件可以直接读取，CLI 也不提供重复的 `show`。

`oauth login` 启动时始终打印授权地址，并尝试自动打开系统浏览器。授权地址和交互提示写入 stderr；OAuth 成功后，完整 JSON 结果写入 stdout，方便管道传给其他程序：

```json
{
  "provider": "claude",
  "access_token": "...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "account_id": "account_xxx",
  "email": "user@example.com"
}
```

用户可以选择写入文件：

```bash
app oauth login claude --output ./oauth/claude.json
```

也可以使用 shell 重定向，但推荐使用 `--output`，因为 CLI 可以在写入时执行格式校验、权限设置和原子替换：

```bash
app oauth login claude > oauth-result.json
```

如果 CLI 需要将结果提交给服务器创建 Provider，可以增加显式的 stdin 模式：

```bash
app provider add --provider claude --oauth-result-stdin
app provider list
app provider config --id <provider-id>
```

该命令从 stdin 读取 OAuth JSON 并提交给服务器。它不会自动把结果写入其他本地文件。

### 5.2 CLI 流程

```text
app oauth login claude
        ↓
CLI 监听 127.0.0.1:随机端口
        ↓
调用 OAuthManager.Start()
        ↓
打开系统浏览器
        ↓
打印授权地址
        ↓
用户完成 OAuth
        ↓
本地 callback server 接收 code/state
        ↓
调用 OAuthManager.Complete()
        ↓
向标准输出返回完整 OAuth 结果，或按 `--output` 写入本地凭据文件
        ↓
进程退出，内存中的 Session 被释放；本地文件由用户显式保留
```

CLI 应监听 `127.0.0.1`，不要默认监听 `0.0.0.0`。CLI 始终打印授权地址，并尝试自动打开浏览器；如果自动打开失败，用户可以手动复制已打印的地址。对于不支持 localhost redirect 的服务，需要额外实现 Device Flow 或对应的手动授权方式。

成功时默认输出完整 JSON；日志和错误信息写入 stderr，不污染 stdout 的 JSON 结果：

```text
OAuth authorization completed.
Provider: claude
Account: user@example.com
Account ID: account_xxx
Expires: 2026-09-10 14:00:00 UTC
Credential ID: cred_xxx
```

由于 CLI 的目标就是返回 OAuth 结果，Token 会出现在成功的标准输出中，或者被写入用户指定的凭据文件；错误日志仍然不得包含 Token。CLI 不写服务器数据库，也不自动写系统 Keychain 或默认配置文件。

### 5.3 本地凭据文件

CLI 的本地文件是显式的文件型存储，不属于服务器数据库。一个文件只保存一个 OAuth 结果，文件结构如下：

```json
{
  "provider": "claude",
  "account_id": "account_xxx",
  "account_name": "user@example.com",
  "access_token": "...",
  "refresh_token": "...",
  "token_type": "Bearer",
  "expires_at": "2026-09-10T14:00:00Z"
}
```

建议使用显式文件参数，不依赖隐含的默认位置：

```bash
app oauth login claude --output ./oauth/claude.json
```

文件操作要求：

- 文件不存在时由 `login --output` 创建。
- 每个文件只允许保存一个 OAuth 结果。
- 读取文件时校验文件中的 `provider` 字段和结果格式。
- 写入前校验 JSON 和 Provider 类型。
- 使用临时文件加原子 rename，避免中途写坏凭据文件。
- Unix 文件权限设置为 `0600`。
- Windows 使用当前用户私有 ACL。
- 读取或更新时不把 Token 写入普通日志。
- `--file` 未指定时只使用内存，不创建默认文件。

### 5.4 CLI OAuth 操作

| 命令 | 作用 |
|---|---|
| `login` | 完成 OAuth；可通过 `--output` 保存结果 |
| `status` | 检查单个文件中的 Token 是否有效、即将过期或已过期 |
| `refresh` | 使用单个文件中的 Refresh Token 更新该文件 |
| `revoke` | 调用该文件记录的 Provider 撤销授权，并从文件中删除或标记结果 |
| `logout` | 撤销并删除单个文件中的 OAuth 结果 |

`refresh`、`revoke` 和 `logout` 都只修改用户指定的本地文件，不写服务器数据库。需要查看完整结果时，直接读取 `--file` 指定的 JSON 文件即可。

## 6. Provider 配置

`ProviderConfig` 只表示所有 Provider 共用的配置，不能放入 OAuth、模型或某个具体上游服务专有的字段。每个具体 Provider 应该定义自己的配置类型，并在内部嵌入公共配置：

```go
type ProviderConfig struct {
    ID                       string   `json:"id"`
    Name                     string   `json:"name"`
    CredentialID             string   `json:"credential_id,omitempty"`
    Labels                   []string `json:"labels"`
    Proxy                    string   `json:"proxy"`
    Enabled                  bool     `json:"enabled"`
    MaxConcurrentConnections int      `json:"max_concurrent_connections"`
}

```

公共 `ProviderConfig` 保存 OAuth Credential 的引用，不需要把 Token 字段放入 Provider 配置。当前接入的 Provider 为 Grok、Codex 和 Claude：

```go
type GrokProviderConfig struct {
    ProviderConfig
}
```

```go
type ClaudeProviderConfig struct {
    ProviderConfig
}

type CodexProviderConfig struct {
    ProviderConfig
}
```

本阶段三个 Provider 只需要实现 OAuth 页面功能，包括在 Provider 添加页面发起对应的 OAuth、接收并展示 OAuth 结果，以及提交 OAuth Credential 和页面配置。Provider 的模型配置、上游请求转发和其他非 OAuth 能力不属于本阶段范围；这些能力由后续 Provider 接入工作单独实现。

`ProviderFactory` 仍然接收 `json.RawMessage`，由具体 Factory 解码到自己的配置类型。`ProviderManager` 不需要理解 OAuth、模型或其他专有字段。Provider 不与 Model 绑定；模型由每次请求的调用方指定，并由具体 Provider 按上游协议转发。

当前 `PersistedAccount.Config` 是 `json.RawMessage`，可以保存具体 Provider 的完整 JSON，但 Provider 列表 API 不应原样返回包含 Token 的 Config。

Provider 创建或更新时，Service 必须校验 `CredentialID` 属于当前用户，且 Credential 的 Service 与 Provider 类型一致。Provider 请求转发时只把 `CredentialID` 传给 OAuthManager，由 OAuthManager 取得当前有效的 Access Token。

删除 Provider 时，Service 先读取其 `CredentialID`，再删除 Provider 记录；如果该 Credential 没有被其他 Provider 引用，则一并删除对应的 OAuth Credential。这样可以避免删除 Provider 后留下无主 Credential，也避免误删被多个 Provider 共享的 Credential。

长期更推荐：

```text
GrokProviderConfig / ClaudeProviderConfig / CodexProviderConfig
    └── ProviderConfig       // 公共字段和 CredentialID

ProviderConfig
    └── CredentialID         // OAuth Credential 的引用

OAuth Credential
    └── 明文保存 access_token / refresh_token
```

这样每个 Provider 可以独立定义自己的配置，同时公共 Provider 接口保持稳定。完整 OAuth 结果只在授权完成页面或专门的 Credential 查询接口中返回；普通 Provider 列表接口只返回 `CredentialID` 和脱敏后的账户信息，不返回 Token。

## 7. 存储职责

Credential 的持久化和 Token 的生命周期管理分开：

- `CredentialStore` 只负责按 ID 读取、保存和删除 `OAuthCredential`；
- `OAuthManager` 或独立的 `CredentialManager` 负责过期判断、选择 `OAuthAdapter`、刷新 Token 和刷新并发控制；
- Provider 只保存 `credentialID`，不负责读取、刷新或持久化 Token；
- CLI 使用文件型 `CredentialStore`；Web Service 直接使用 `internal/database/database.go` 中的 `database.Database` 实现 `CredentialStore` 所需的方法，不单独实现 `DatabaseCredentialStore`。

推荐接口如下：

```go
type CredentialStore interface {
    LoadCredential(string) (json.RawMessage, error)
    SaveCredential(string, json.RawMessage) error
    DeleteCredential(string) error
}
```

`CredentialStore` 只把 OAuth Credential 当作不透明 JSON 处理，不依赖 `OAuthCredential` 类型。OAuthManager 负责在读取后将 JSON 解码为 `OAuthCredential`，刷新完成后再编码为 JSON 保存。方法名明确包含 `Credential`，即使值类型是 `json.RawMessage`，接口语义仍然清晰。由于 CredentialStore 是同步的本地存储接口，不承担请求取消和超时控制；需要超时的上游 HTTP 请求仍由 OAuthManager 和 OAuthAdapter 使用带超时的上下文负责。由于 Go 接口采用结构化匹配，`database.Database` 不需要引用 `oauth.CredentialStore`；只要声明相同的方法集，Database 实例即可直接作为 `oauth.CredentialStore` 使用：

```go
var store oauth.CredentialStore = db
```

不建议把 `GetValidAccessToken` 直接放入 `CredentialStore`。否则文件存储和数据库存储都必须理解 Token 过期策略、OAuthAdapter、上游刷新协议和并发刷新，导致存储层与 OAuth 业务逻辑耦合。`GetValidAccessToken` 应保留在 OAuthManager/CredentialManager 中，由它调用 Store 的 `LoadCredential` 和 `SaveCredential`。

短期授权 Session 直接保存在 OAuthManager 的内存字段中；它与已完成 Credential 的持久化不是同一类数据。Web Service 如果需要在 callback 和 Provider 页面之间传递结果，可以自行增加临时表或缓存；该临时存储属于 Web Service，不属于 OAuthManager 的 Session 存储。

CLI 的本地 OAuth 文件由 CLI 自己读写，或由 CLI 提供的 `FileCredentialStore` 读写。一个文件可以保存一个 OAuth 结果；`credentialID` 可以使用文件路径，也可以使用文件中生成的稳定 ID。写文件时必须使用临时文件加原子 rename，避免中途写坏凭据文件；Unix 使用 `0600`，Windows 使用当前用户私有 ACL。Token 和完整 OAuth Result 按当前要求以明文 JSON 保存，但不得写入普通日志。Session 始终使用内存实现，不使用 SQLite。

Web Service 应将 Credential 独立保存到数据库，Provider 只保存 `credential_id`：

```text
providers
    id
    owner_id
    service
    credential_id
    config

oauth_credentials
    id
    owner_id
    service
    access_token
    refresh_token
    token_type
    expires_at
    account_id
    account_name
    email
    raw
    version
    created_at
    updated_at
```

`credential_id` 不应单独作为权限依据。Web 请求必须同时校验当前用户是否拥有该 Credential，以及 Credential 的 Service 是否与 Provider 类型一致。多实例部署时，`internal/database/database.go` 中的数据库实现还必须使用行级锁、乐观锁或其他分布式并发控制，避免同一 Credential 被重复刷新。

OAuth Session 只保存在 OAuthManager 的内存字段中。不要为 OAuth Session 增加 SQLite 表；进程退出或重启后，未完成的 OAuth 流程失效。已完成的 OAuthCredential 由 CLI 文件存储或 `internal/database/database.go` 中的 `database.Database` 保存，OAuthManager 不维护 Credential 列表，而是通过注入的 Store 访问它们。项目不单独实现 `DatabaseCredentialStore`。

## 8. Provider 请求转发

OAuth 的通用授权流程、Token 读取、过期判断、刷新和并发控制都由 `OAuthManager` 或 `CredentialManager` 负责。Provider 不再依赖单独的 `token_resolver.go` 或自行实现 Token Resolver，也不直接调用具体的 OAuthAdapter。

Provider 只需要向 OAuth 模块请求当前可用的 Access Token：

```go
accessToken, err := oauthManager.GetValidAccessToken(ctx, credentialID)
if err != nil {
    return err
}
request.Header.Set("Authorization", "Bearer "+accessToken)
```

`GetValidAccessToken` 的内部流程为：

1. 根据 `CredentialID` 读取 Credential。
2. 在 Web 场景校验当前用户是否拥有 Credential，并校验 Service 与 Provider 类型一致。
3. 判断 Access Token 是否已经过期或进入刷新提前量。
4. 如果仍然有效，直接返回 Access Token。
5. 如果即将过期，通过对应的 `OAuthAdapter.Refresh` 刷新。
6. 保存新的 Access Token、Refresh Token 和过期时间。
7. 返回新的 Access Token。

同一个 Credential 的刷新必须使用并发控制，并在等待锁后再次检查 Token，避免多个请求重复刷新。单进程 CLI 可以使用进程内锁并配合文件锁；Web 多实例部署则应使用数据库行锁、乐观锁或分布式锁。Provider 不参与这些细节，只负责请求转发和设置认证 Header。

这样，Provider 只负责请求转发和设置认证 Header；Grok 的 Device Flow、Claude 的 Refresh JSON、Token 续期和并发刷新细节都不会泄漏到 Provider 或 Service 层。

## 9. 安全要求

- `state` 必须随机生成、服务端保存并一次性使用。
- PKCE `code_verifier` 必须随机生成并绑定 Session。
- Session 和临时 Result 必须有过期时间。
- Refresh Token 和 Access Token 按当前要求明文保存；不得写入日志或错误信息。
- 日志中禁止输出 Authorization Header、Access Token 和 Refresh Token。
- 普通 Provider 列表接口不返回完整 OAuth Config。
- OAuth Result 只能被创建它的用户读取。
- 创建 Provider 时重新校验 Provider 类型、用户归属和字段合法性。
- CLI callback 只监听 `127.0.0.1`。
- OAuth callback 成功或失败后都清理一次性 Session。
- Token 刷新需要并发控制。

## 10. 当前实现对比和迁移结论

### 10.1 `ai-unisub` 当前状态

当前项目已经完成独立的 `internal/oauth` 核心模块、Grok Device Flow、Codex/Claude
PKCE Adapter、CLI OAuth 命令，以及阶段 6 所需的 Web OAuth 启动、回调、临时结果和
Provider 绑定链路。Provider 层仍只保留公共配置和 Credential 引用；模型配置与上游
请求转发不属于阶段 6/7 的范围。

### 10.2 `ai-unisub` 参考实现

可运行的参考版本位于 `D:/Projects/ai-unisub`。它没有使用本文件前面规划的 `internal/oauth/*Adapter` 目录，而是采用“具体 Provider 持有协议实现，Server 持有 Web Flow”的结构：

```text
internal/provider/grok/oauth.go       Grok Token、Refresh、Device Flow 辅助逻辑
internal/provider/claude/oauth.go     Claude PKCE、Token Exchange、Refresh
internal/provider/grok/provider.go    Grok Provider 接入 OAuth
internal/provider/claude/provider.go  Claude Provider 接入 OAuth
internal/server/grok_oauth.go         Grok Device Flow、轮询和账号绑定
internal/server/pkce_oauth.go         Claude/Codex PKCE Flow 和账号绑定
internal/server/oauth.go              通用 OAuth 请求、刷新和订阅绑定
```

该版本已通过：

```text
go test ./...
```

其中包含 Grok Device OAuth、Claude PKCE、Token Refresh、state 校验、上游 401 重试和 Provider Header 测试。

### 10.3 Grok 和 Claude 不能共用同一种流程

Grok 当前使用 xAI Device Flow：

- Device Code endpoint：`{issuer}/oauth2/device/code`
- Token endpoint：`{issuer}/oauth2/token`
- Token grant：`urn:ietf:params:oauth:grant-type:device_code`
- Token 请求使用 `application/x-www-form-urlencoded`
- 需要保留 Grok CLI 相关请求头，例如 `x-grok-client-version` 和 `x-grok-client-surface`
- 必须处理 `authorization_pending`、`slow_down`、过期和轮询间隔

Claude 当前使用 PKCE Authorization Code Flow：

- Authorize URL：`https://claude.com/cai/oauth/authorize`
- Token URL：`https://platform.claude.com/v1/oauth/token`
- Redirect URI：`https://platform.claude.com/oauth/code/callback`
- Token 请求使用 JSON
- Authorization URL 必须包含 `code=true`、`code_challenge`、`code_challenge_method=S256` 和 `state`
- Token Exchange 必须发送 `code_verifier`、`redirect_uri`、`client_id` 和 `state`
- Refresh 时如果服务端没有返回新的 Refresh Token，必须保留旧值

因此，Grok、Claude 和 Codex 只能共享 Session、错误处理、Token 结果转换等通用能力；授权 URL、Token 请求格式、轮询、Header 和响应解析必须由各自的适配器实现。

### 10.4 本项目的实现决策

`ai-unisub` 后续应保留本文件前面定义的独立 OAuth 边界，但协议代码需要覆盖参考实现已经验证过的行为。推荐分层如下：

```text
internal/oauth/
├── manager.go       通用 Session、state、PKCE、过期和一次性消费
├── types.go         OAuthSession、OAuthCredential、Token、Flow 和错误类型
├── adapter.go       OAuthAdapter、PKCEAdapter、DeviceAdapter 接口
├── errors.go        通用 OAuth 错误
├── http.go          HTTP 请求、响应体限制和错误读取
└── adapters/
    ├── grok.go      Device Flow、轮询、Refresh
    ├── claude.go    PKCE、Token Exchange、Refresh
    └── codex.go     Codex 专用 PKCE 参数和 Token 解析

internal/service/   Web API、用户归属、临时 Result 和 Provider 页面
internal/provider/  Provider 配置和请求转发，不负责授权交互或 Token 刷新
cmd/                CLI callback server 和本地凭据文件
```

如果后续选择直接迁移 `ai-unisub` 的 Server 实现，而不是采用独立 `internal/oauth`，必须同步迁移完整的 Config、Model、Provider、Server OAuth、刷新锁和测试；只复制单个 Grok 或 Claude Adapter 不足以形成可运行链路。

## 11. 行动项

实施顺序明确为：

```text
核心 OAuth 模块和能力接口
    ↓
Grok DeviceAdapter / Codex PKCEAdapter / Claude PKCEAdapter
    ↓
    持久化策略和安全边界
    ↓
无状态 CLI
    ↓
Web Service
    ↓
Provider 请求转发集成
```

先完成 CLI 的原因是：CLI 不依赖 Web 页面，可以先验证三家独立 Adapter、Grok Device Flow、Claude/Codex PKCE、Token Exchange、完整结果格式和错误处理；Web 阶段只复用已经验证过的 OAuthManager 和 Adapter，Provider 不参与授权交互。

### 阶段一：核心模块

- [x] 创建 `internal/oauth`。
- [x] 定义 Provider、OAuthCredential 和 OAuthSession。
- [x] 定义 OAuthAdapter 和 OAuthManager。
- [x] 在 OAuthManager 内实现 Session 的创建、读取、删除和过期清理。
- [x] 实现 state、PKCE、超时和一次性 Session 测试。

### 阶段二：OAuthAdapter

- [x] 实现 Grok `DeviceAdapter`，包括 Device Code、轮询和 Refresh。
- [x] 实现 Codex `PKCEAdapter`。
- [x] 实现 Claude `PKCEAdapter`。
- [x] 编写授权 URL 和 Token 响应解析测试。
- [ ] 确认三家服务的 refresh、revoke 和账户信息接口。

### 阶段三：明文持久化和安全边界

- [x] 确认 OAuthManager 只保存内存 Session，不增加 OAuth Session 数据库表。
- [x] 在 CLI 文件存储或 Web Service 存储层按明文 JSON 保存 OAuth 结果。

### 阶段四：无状态 CLI

- [x] 增加 CLI OAuth 命令组。
- [x] 实现 `oauth login <provider>`。
- [x] 实现 localhost 临时 callback server。
- [x] 始终打印授权地址，并尝试自动打开浏览器。
- [x] 自动打开浏览器失败时，不影响用户手动复制授权地址继续操作。
- [x] 默认向 stdout 输出完整 JSON OAuth 结果。
- [x] 将日志和错误写入 stderr，保证 stdout 可以直接作为 JSON 管道。
- [x] 确保 CLI 不写服务器数据库、Keychain 或默认配置。
- [x] 实现显式 `--output` 本地凭据文件。
- [x] 实现 `status`、`refresh`、`revoke`、`logout`。
- [x] 实现凭据文件权限、原子写入和格式校验。
- [ ] 增加从 stdin 读取 OAuth 结果并提交服务器的可选命令。

### 阶段五：Web

- [ ] 增加 `POST /api/oauth/{provider}/start`。
- [ ] 增加 `GET /oauth/{provider}/callback`。
- [ ] 增加临时 OAuth Result API。
- [ ] 修改 Provider 添加页面，增加 OAuth 按钮。
- [ ] OAuth 成功后返回 Provider 页面。
- [ ] 显示完整 OAuth JSON。
- [ ] 支持用户确认或编辑完整 OAuth 结果。
- [ ] 提交 Provider 时保存 OAuth 结果和其他配置。
- [ ] 增加用户归属和 Provider 类型校验。

### 阶段六：接入 Provider

- [x] 定义 Grok、Codex、Claude 各自的 Config 类型。
- [x] 三个 Provider 支持 OAuth 页面所需的 OAuth Credential 引用字段。
- [x] 公共 `ProviderConfig` 保持只包含公共字段。
- [x] 在 Provider 添加页面接入 OAuth 启动、callback 结果展示和提交保存。
- [x] 本阶段不实现三个 Provider 的模型配置、上游请求转发或其他非 OAuth 功能。

阶段六的实现约定如下：`GrokConfig`、`CodexConfig` 和 `ClaudeConfig` 都复用公共
`ProviderConfig`，并通过 `OAuthConfig.CredentialID` 引用 Credential。创建 Provider
时，服务端接收页面提交的一次性 OAuth 结果，将 Credential 保存到 `CredentialStore`，
然后只把 `credential_id` 写入 Provider 配置；普通 Provider 列表会移除 `oauth`、
`access_token` 和 `refresh_token` 字段。Provider 页面通过同一套 OAuthManager 完成
启动、回调和临时结果确认，不在 Provider 层实现 OAuth 协议。

### 阶段七：验收

- [x] CLI 先完成 Grok、Codex、Claude OAuth，并输出完整结果。
- [x] Web 再复用同一套 OAuthManager 完成 Grok、Codex、Claude OAuth 并创建 Provider。
- [x] 验证不同用户无法读取彼此的 OAuth Result。
- [x] 验证页面刷新、重复点击和 callback 重放。
- [x] 验证完整 OAuth 结果不会出现在普通列表 API、日志和错误页面。
- [x] 验证 OAuth 模块不依赖 Service，Provider 不负责 OAuth 流程，也不依赖 OAuthAdapter。

阶段七验收结果：CLI 和 Web 都通过 `go test ./...` 的构建与单元测试检查。Web
临时结果绑定当前登录用户并在读取后立即删除；过期、重复读取、错误用户读取和
callback 重放均返回失败。OAuth Token 仅在 CLI 明确要求 stdout 或 Provider 页面
的临时结果接口中出现，不进入普通 Provider 列表；日志使用固定错误信息，不打印
Token。OAuthManager 只依赖 OAuth 自身的 CredentialStore，Web 负责交互和用户归属，
Provider 只保存 Credential 引用。

## 12. 第一版验收标准

第一版验收分两步进行：先完成 CLI 验收，再完成 Web 验收。第二步必须复用第一步已经验证过的 `OAuthManager` 和 `OAuthAdapter`，不能为 Web 另行实现一套 OAuth 流程。

### 第一步：CLI

CLI 验收通过的标准：

1. `app oauth login grok`、`app oauth login codex` 和 `app oauth login claude` 均可以通过浏览器或对应的 Device Flow 完成 OAuth。
2. CLI 使用 `127.0.0.1` 临时 callback server 接收 localhost 回调；始终打印授权地址，并尝试自动打开浏览器，自动打开失败时不影响用户手动访问。
3. OAuth 成功后，默认只向 stdout 输出完整 OAuth JSON；日志、提示和错误全部写入 stderr，且不包含 Access Token 或 Refresh Token。
4. `state`、PKCE、Session 过期时间、一次性 callback 消费和 Token Exchange 校验有效；Grok 的 Device Flow 轮询状态和过期行为正确。
5. CLI 不写服务器数据库、Web Session、Keychain 或默认配置文件；未指定 `--output` 时只在进程内保存结果。
6. 指定 `--output` 时可以创建或原子更新本地凭据文件，并执行 Provider 类型、JSON 格式和文件权限校验。
7. `status`、`refresh`、`revoke`、`logout` 可以针对 `--file` 指定的单个凭据文件工作，并且不会把 Token 写入普通日志。
8. OAuth 模块不依赖 `service`、`cmd` 或具体 Provider 实现；CLI 不调用 Web Handler 或 Web Service。

### 第二步：Web

Web 验收通过的标准：

1. 用户可以从 Provider 添加页面启动 Grok、Codex 或 Claude OAuth，并通过 `POST /api/oauth/{provider}/start` 获取授权地址。
2. OAuth callback 可以完成 Session、`state`、PKCE、过期时间和当前用户归属校验；成功或失败后都清理一次性 Session。
3. OAuth 成功后返回 Provider 添加页面，并通过临时 OAuth Result 显示完整 OAuth JSON；URL 中只包含结果 ID，不包含 Token。
4. 用户可以填写或确认 Provider 名称和并发数，并提交 `POST /api/providers` 保存 OAuth Credential 和其他 Provider 配置；Provider 不保存 Model。
5. 临时 OAuth Result 具备短期过期、用户归属、Provider 类型校验，读取或提交后删除，且不同用户无法读取彼此的结果。
6. Provider 只保存 `credential_id` 和自身配置；Provider 请求转发通过同一个 `OAuthManager` 获取有效 Access Token，并支持过期刷新和刷新并发控制。
7. 普通 Provider 列表 API、日志和错误页面不返回或泄露 Access Token、Refresh Token 和完整 OAuth Config。
8. Web 与 CLI 使用同一个 `OAuthManager` 和 `OAuthAdapter`；OAuth 模块不依赖 `service`，Provider 模块不负责 OAuth 授权流程。
