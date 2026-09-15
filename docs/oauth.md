# OAuth 模块

`internal/oauth` 提供独立 OAuth 协议能力，负责授权会话、协议适配、凭据交换、刷新与撤销。它不感知调用方的应用模块、HTTP 路由、页面、登录体系或数据库具体实现；持久化通过调用方注入的 CredentialStore 完成。

## 结构与职责

| 位置 | 职责 |
| --- | --- |
| `internal/oauth/types.go` | Credential、Session、Adapter 能力和存储接口 |
| `internal/oauth/manager.go` | Adapter 注册、授权流程、Session 与凭据刷新 |
| `internal/oauth/adapters/` | Codex、Claude、Grok 的协议实现 |
| `internal/oauth/file_store.go` | CLI 的单文件凭据存储 |
| `internal/oauth/proxy.go` | 当前代理工具兼容入口；目标改为显式依赖 Proxy 对象，不保留原工具函数的转发包装 |
| `internal/oauth/dummy.go` | 本地模拟适配器 |
| `cmd/oauth/main.go` | 独立 CLI，直接调用 OAuthManager |

OAuthAdapter 只负责上游授权协议，由 OAuthManager 注册和调用；调用方如何使用取得的 Token 不属于 OAuth 模块职责。

## Adapter 能力

```go
type OAuthAdapter interface {
    Service() string
    Refresh(context.Context, *OAuthCredential) (*OAuthCredential, error)
}

type PKCEAdapter interface {
    OAuthAdapter
    BuildAuthorizationURL(context.Context, AuthorizationInput) (AuthorizationResult, error)
    Exchange(ctx context.Context, code, state, codeVerifier, redirectURI string) (*OAuthCredential, error)
}

type DeviceAdapter interface {
    OAuthAdapter
    StartDeviceAuthorization(context.Context, DeviceStartInput) (DeviceAuthorizationResult, error)
    PollDeviceToken(ctx context.Context, deviceCode string) (*OAuthCredential, error)
}

type RevocableAdapter interface {
    Revoke(context.Context, *OAuthCredential) error
}
```

| service | 当前适配方式 |
| --- | --- |
| codex | PKCE；Token 请求使用表单编码，解析账号信息 |
| claude | PKCE；Token 请求使用 JSON，保留账号和刷新信息 |
| grok | Device Flow；设备授权、轮询、pending/slow_down 错误映射 |
| dummy | 本地模拟授权，供测试使用 |

各适配器独立定义授权地址、请求参数、Header、响应转换与 HTTP Client。服务标识使用普通字符串和 OAuthService 常量。撤销是可选能力，不支持时 Manager 返回错误。

此处记录仓库实现，不代表三家平台当前账号、scope 或接口兼容性已经通过真实环境验证。

## OAuthManager 接口

```text
NewManager(store CredentialStore) *OAuthManager
NewOAuthManager(store CredentialStore) *OAuthManager
Register(adapter OAuthAdapter) error
Start(ctx context.Context, service, subjectID, redirectURI string) (*StartResult, error)
Complete(ctx context.Context, sessionID, code, state string) (*OAuthCredential, error)
Poll(ctx context.Context, sessionID string) (*OAuthCredential, error)
SessionForState(state string) (OAuthSession, error)
SessionForSubjectState(service, subjectID, state string) (string, error)
SessionForSubject(sessionID, service, subjectID string) (OAuthSession, error)
DiscardSession(sessionID string) error
Refresh(ctx context.Context, service string, credential *OAuthCredential) (*OAuthCredential, error)
GetValidAccessToken(ctx context.Context, service, credentialID string) (string, error)
Revoke(ctx context.Context, service string, credential *OAuthCredential) error
```

以上省略方法接收者和重复的 func 关键字，仅列出当前调用签名。Start、Complete、Poll 返回结果，不自动长期保存 Credential；GetValidAccessToken 才通过 Store 读取并保存刷新结果。

## Session 与身份

SessionID 表示一次授权流程，由 Manager 生成；SubjectID 是调用方提供的不透明主体标识。OAuth 不解释该标识对应的用户、角色或登录方式；不需要主体绑定的调用场景可以使用空 SubjectID。

Session 保存 ID、Service、SubjectID、RedirectURI、State、CodeVerifier、DeviceCode、Proxy 和 ExpiresAt。默认有效期 10 分钟，Device Flow 若返回上游过期时间则使用该时间。Session 仅保存在进程内存中，重启后失效，访问过期 Session 时清理。

StartResult 返回 session_id、authorization_url、expires_at；设备授权额外返回 user_code 和 verification_uri，不返回 verifier 或 device code。

PKCE 的 Complete 校验 Session 与 state，然后消费 Session 再交换 token；交换失败不能重用原 Session。需要主体约束的调用方先通过 SessionForSubject 校验 Session 归属；Complete 不负责识别调用者身份。

Device Flow 的 Poll 成功后消费 Session，pending/slow_down 保留 Session；当前没有按 Session 串行化并发轮询，因此不能承诺并发 poll 只进行一次交换。

## Credential 与存储

标准化 Credential 字段为 access_token、refresh_token、token_type、expires_at、account_id、account_name、email；不保留上游原始响应。

```go
type CredentialStore interface {
    LoadCredential(string) (json.RawMessage, error)
    SaveCredential(string, json.RawMessage) error
    DeleteCredential(string) error
}
```

存储实现把 Credential 视为不透明 JSON，OAuth 负责序列化。调用方注入满足 CredentialStore 的实现；OAuth 不要求具体数据库、表结构或应用存储包装层。FileCredentialStore 提供显式的单文件存储能力。

Credential ID 标识存储中的凭据，Session ID 标识一次授权流程，二者不能混用。当前凭据 JSON 和存储接口不包含主体归属或 service 绑定校验信息；调用方负责访问授权，不能从一个不透明 ID 推定所有权。

## 有效 Token 与刷新

调用方通过 `GetValidAccessToken(ctx, service, credentialID)` 获取有效 Token：

1. 从 Store 读取 Credential，拒绝空 Access Token。
2. 没有过期时间，或距离过期超过 60 秒，直接返回 Token。
3. 否则取得该 credentialID 对应的进程内锁。
4. 锁内重新读取并检查，避免等待期间重复刷新。
5. 调用对应 Adapter.Refresh，保存新 Credential 后返回 Token。

刷新响应缺少 refresh_token、token_type 或账号信息时，Manager 保留原值。同一 Manager 内的并发刷新有锁保护，但不存在数据库级或多进程刷新锁，不将其描述成分布式互斥。

普通 Refresh 返回新结果，不负责写 Store；调用方决定是否保存。包含凭据的协议结果应由调用方按其访问控制契约处理，不写入普通日志。

## 调用方边界

- 调用方提供 service、SubjectID、redirect URI、代理选项和请求 Context。
- Manager 返回授权地址或设备验证信息，由调用方决定如何展示或打开。
- 调用方将授权回调中的 code/state 交给 Complete，或通过 Poll 执行设备授权轮询；OAuth 不注册回调路由或启动应用服务器。
- Complete/Poll 返回标准化 Credential，不创建调用方的业务资源，也不维护应用专用的结果交接存储。
- 页面、Cookie、HTTP 状态码、角色权限及结果展示均由调用方定义，不属于 OAuth 包契约。

OAuth 的调用契约不随某个应用的模块划分、路由命名或前端交互方式变化。

## CLI

```sh
go run ./cmd/oauth login codex
go run ./cmd/oauth login claude --output ./oauth/claude.json
go run ./cmd/oauth login grok --output ./oauth/grok.json
go run ./cmd/oauth status --file ./oauth/grok.json
go run ./cmd/oauth refresh --file ./oauth/grok.json --provider grok
go run ./cmd/oauth revoke --file ./oauth/grok.json --provider grok
go run ./cmd/oauth logout --file ./oauth/grok.json --provider grok
```

| 命令 | 行为 |
| --- | --- |
| login | 输出授权地址并尝试打开浏览器；无 output 时输出 Credential JSON，有 output 时保存指定文件 |
| status | 仅按文件中的过期时间显示 valid、expiring（不足 1 小时）、expired，不访问上游验证 |
| refresh | 刷新并覆盖指定文件 |
| revoke | Adapter 支持撤销且成功时删除文件；不支持则返回错误 |
| logout | 尝试撤销；不支持撤销时仍删除本地文件，其他上游错误不忽略 |

Codex/Claude 的 CLI 回调仅监听 127.0.0.1 的随机端口，路径分别为 /auth/callback 和 /callback；Grok 不开回调监听，当前每 5 秒轮询。CLI 授权 Context 总超时 15 分钟，但 Session 仍受自身过期时间约束。

login 保存的文件带 provider 标识。当前 refresh 保存标准 Credential 时不保留 provider 字段，所以后续操作可显式传 --provider；不声称连续刷新会自动保留该元数据。

FileCredentialStore 校验 JSON，通过同目录临时文件、Sync 和 Rename 写入；请求目录权限 0700、文件权限 0600。当前没有专用 Windows ACL 设置逻辑，也没有跨进程文件锁。未指定输出文件不会创建默认凭据文件，不写服务器数据库或系统凭据库。

## 代理边界

目标依赖方向为 `oauth -> proxy`，Proxy 不导入 OAuth。OAuth 通过显式参数接收已校验的代理对象，不从 Context 读取代理地址；Common 不保留代理能力。本文前面的 Manager 与 Adapter 签名描述当前实现，下面是尚未落地的目标调用契约。

目标参数示意：

```go
type RequestOptions struct {
    Proxy *proxy.Endpoint // nil 表示不指定代理
}
```

- 调用方将输入地址交给 `proxy.NewEndpoint` 构造对象，构造失败在开始授权前返回；未配置地址时显式传 nil。
- Start 通过 RequestOptions 接收代理对象，Session 保存本次授权选定的不可变 Endpoint；Session 不持有原请求的 Context。
- Complete/Poll 使用 Session 中的代理对象和当前请求的 Context，调用方不能用新的代理参数覆盖授权会话绑定。
- Refresh、GetValidAccessToken 和 Revoke 没有授权 Session，使用显式 RequestOptions；内部触发刷新时继续传递相同选项。
- Manager 向 Adapter 显式传递代理选项或已配置的专用 HTTP Client，不再让 Adapter 从 Context 读取代理；客户端配置由 Proxy 包封装，不能修改共享 Client/Transport 导致并发串用代理。
- Context 只控制取消和超时，代理对象不参与 Credential 序列化或长期凭据保存。

OAuth 不负责代理组优先级或健康状态。代理对象、输入校验与传输配置统一由 [Proxy](proxy.md) 定义。当前代码仍使用 Common 的代理 Context 工具，本次只更新文档，不表示实现已改变。

## 验证范围

协议层测试位于 `internal/oauth/manager_test.go`、`file_store_test.go`、`adapters/adapters_test.go` 与 `dummy_functional_test.go`。目标验证覆盖 state、过期、主体匹配、单次回调、刷新互斥、文件读写、显式代理参数、Session 代理绑定和并发隔离，不依赖具体应用的 Handler、页面或业务账号。现有测试不代表目标契约已验证，模拟结果也不等同于真实平台授权成功。
