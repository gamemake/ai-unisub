# Service 框架

框架与底层包、应用模块之间的关系见 [系统结构](architecture.md)。本文维护 Service 的具体接口与实现契约。

`internal/service` 提供模块注册、HTTP 路由、身份认证及共享服务。`internal/unisub` 负责应用组装与具体 Handler，`cmd/unisub` 负责进程配置和监听。框架不导入应用层。

## 模块与上下文

当前接口定义于 `internal/service/service.go` 和 `context.go`：

```go
type Module interface {
    Name() string
    Init(ModuleContext) error
    Close() error
}

type RouteOptions struct {
    Auth AuthMode
    Name string
}
```

`ModuleContext` 提供 `Config`、`Handle`、`HandleFunc`、`Database`、`OAuth`、`AIProviders`、`Proxy`、`Auth`、`OAuthResults`。模块在 `Init` 中注册路由，不自行解析环境变量或重新创建共享 Manager。配置通过值返回，具体依赖由 Service 持有。

上下文不暴露 SQLite 连接或通用 HTTP Client。OAuth Adapter、AIProvider 和代理探测分别管理外部请求能力。

## 应用接入

UniSub 使用四个应用模块：[Static](unisub-static.md)、[API](unisub-api.md)、[OAuthFlow](unisub-oauthflow.md)、[Gateway](unisub-gateway.md)。它们实现框架的 Module 接口，但代码与业务契约属于 `internal/unisub`，不是框架内置业务。

模块间不直接调用彼此的 Handler，也不共享具体模块实例；公共状态通过上下文访问。完整组装和路由索引见 [UniSub](unisub.md)。

## 路由与请求处理

- 路径必须以 `/` 开头，Handler 不能为空；同一路径重复注册报错。
- 以 `/` 结尾的注册路径按前缀匹配，其余为精确匹配；多个匹配项取最长路径。
- 精确认证入口和更长业务前缀优先于 `/api/`，`/api/` 与 `/v1/` 优先于静态模块的 `/`。UniSub 中 `/api/login`、`/api/logout` 由 API 模块注册为 AuthNone，普通 `/api/` 仍为 AuthSession；`/api/oauth/` 保持 OAuthFlow 归属。
- HTTP 方法由各 Handler 判断，路由层不区分方法。未匹配路径和部分不支持的方法使用 `http.NotFound`，不能假定所有错误均为 JSON 或所有方法错误均为 `405`。
- 请求包装层提供 `X-Request-ID`、Context 中的请求 ID、访问日志及 panic 恢复。已有请求 ID 直接沿用，否则生成新值。
- panic 时若尚未写响应，返回通用 `500`；已经开始发送的响应不再覆盖状态。响应包装器保留 Flush 等接口，但这不表示网关支持 WebSocket。

## 认证与授权

本节对应 `internal/service/auth.go`，描述框架当前提供的认证能力。应用层的入口选择见 [UniSub](unisub.md)，登录、登出与业务授权的 HTTP 契约由 [API](unisub-api.md) 等应用模块文档维护。

### AuthService 与 Principal

```go
type AuthMethod int

type Principal struct {
    User     *database.PersistedUser
    Account  *database.PersistedAccount
    APIKeyID string
    Method   AuthMethod
}

type AuthService interface {
    Principal(context.Context) (Principal, bool)
    SessionPrincipal(*http.Request) (Principal, bool)
    EnsureAdmin(string, string) error
    CreateSession(*database.PersistedUser) (string, error)
    DeleteSession(string)
    SetSessionCookie(http.ResponseWriter, string)
    ClearSessionCookie(http.ResponseWriter, string)
}
```

Service 持有内部 authService，模块通过 `ModuleContext.Auth()` 使用它。内部实例维护受锁保护的 Session 映射，不提供独立的 HTTP 路由。

- `Principal(ctx)` 和包级 `PrincipalFromContext(ctx)` 读取中间件已写入的主体，不重新执行认证。
- `SessionPrincipal(request)` 显式校验 Cookie 和 Session 并返回主体，不把结果写入原请求 Context。
- Session 主体只设置 User 和 Method；API Key 主体另有绑定的 Account 与 APIKeyID。
- Account 是持久化账号快照，不是运行时执行队列；运行时对象由 AIProviderManager 管理。
- 当前 Method 使用 AuthMode 对应值转换为 AuthMethod；管理员 Session 的认证方式仍为 AuthSession，角色由 User 表示。

### 路由认证模式

| AuthMode | 行为 |
| --- | --- |
| `AuthNone` | 不执行认证；适用于公开静态资源、登录与幂等登出等入口 |
| `AuthSession` | 校验浏览器 Session 与当前启用用户 |
| `AuthSessionAdmin` | Session 校验后要求管理员角色 |
| `AuthAPIKey` | 校验 Bearer 或 X-Api-Key、有效期、启用用户和绑定账号 |

认证中间件按路由模式选择分支，不把 Cookie 和 API Key 相互替代。AuthNone 直接调用 Handler，不自动识别会话或写入 Principal。其他模式认证成功后，通过私有 Context key 写入主体，再执行 Handler。

AuthSessionAdmin 在会话认证后额外检查 admin 角色，非管理员返回 403。资源归属和更细粒度的业务权限仍由 Handler 检查，不能以客户端传入的用户 ID 替代 Principal。

### 初始管理员

`EnsureAdmin(name, password)` 要求名称和密码非空，然后读取用户列表：

- 不存在同名用户时，生成 16 字节随机 ID 的十六进制表示，创建启用的 admin 用户，并保存密码散列及创建／更新时间。
- 同名用户没有密码散列时，仅补上密码散列并更新时间。
- 同名用户已有密码散列时不更改任何内容；不会强制修改其角色或启用状态。

因此该方法是初始账号保障入口，不是重置密码、提权或重新启用已有用户的接口。

### Session 创建与校验

`CreateSession(user)` 检查参数非空、用户 ID 非空且数据库中存在该 ID，再生成 32 字节随机 token，并以十六进制字符串作为映射 key。映射值只有 UserID 和 ExpiresAt；默认 TTL 为 24 小时，Config.SessionTTL 为正值时使用配置。

该方法不验证密码，也不检查用户启用状态；调用方在登录验证成功后使用它。创建 Session 不自动设置 Cookie，两项操作分开调用。

Session 校验顺序：

1. 读取名为 session 的 Cookie，并在内存映射中查找 token。
2. 不存在则失败；过期则删除并失败，不延长有效期。
3. 经 Database 重新读取用户，要求相同 ID 的用户仍存在且启用。
4. 用户不存在或已停用时删除该 Session；成功时返回包含最新用户信息的 Principal。

当前用户读取失败也表现为会话认证失败，不单独返回数据库错误。Session 不持久化，重启后失效；过期项在访问时删除。DeleteSession 幂等删除指定 token。更改密码本身不自动删除已有 Session。

### Cookie 操作

| 属性 | 设置会话 | 清除会话 |
| --- | --- | --- |
| Name / Path | session / `/` | 相同 |
| Value | Session token | 空字符串 |
| HttpOnly | true | true |
| SameSite | Lax | Lax |
| MaxAge | TTL 的秒数 | -1 |

SetSessionCookie 只写响应 Cookie。ClearSessionCookie 在 token 非空时先删除对应 Session，然后写失效 Cookie；没有 token 时也写清除 Cookie。当前两者均未显式设置 Secure 或 Domain。

### API Key 校验

1. 读取客户端密钥：`Authorization` 恰好为 `Bearer <key>`（Bearer 大小写不敏感）时使用该 token；否则使用 `X-Api-Key`。两者都存在时以 Bearer 为准。缺少有效 token 返回 401。Claude Code 使用 `x-api-key`，Codex 与 Grok Build 使用 Bearer。
2. 读取全部 API Key，以 ConstantTimeCompare 比较明文 Key；缺少或不匹配返回 401。
3. ValidSeconds 为正时，按 CreatedAt 加有效秒数判断过期；已过期返回 401，非正值不在此处限制有效期。
4. Key 所属用户必须存在且启用，否则返回 403。
5. Key 绑定的持久化账号必须存在，否则返回 403。
6. 返回包含 User、Account、APIKeyID 和 AuthAPIKey Method 的 Principal。

读取 Key、用户或账号发生存储错误时返回 500。此处不检查账号配置的 enabled、运行时实例或并发可用性；这些由后续执行层处理。

### 密码处理

`HashPassword(password)` 使用 16 字节随机 salt，先计算 SHA-256(salt + password)，再对上一次摘要重复计算，总计 120000 次 SHA-256，输出 `sha256$<salt-hex>$<digest-hex>`。随机源失败时当前实现 panic。

`VerifyPassword(encoded, password)` 检查三段格式、算法标识与十六进制编码，重新计算摘要后使用 ConstantTimeCompare 比较；格式或匹配失败返回 false。该格式是当前代码的自定义迭代摘要，不是 PBKDF2、bcrypt 或 Argon2。密码长度等业务规则由调用方校验。

### 失败响应与当前边界

- AuthAPIKey 失败或路径以 `/api/` 开头的认证失败，输出 JSON 错误，沿用 401、403 或 500 状态。
- 当前非 API 路径的会话认证失败会 303 重定向到 `/login`；这是现有中间件行为，不是应用目标页面入口。
- 内部 writeError 转发给 common.WriteError，使用 unauthorized、forbidden 或 internal server error 等公共消息。
- UniSub 中的 `/` 为 AuthNone 静态页面，因此不进入会话失败重定向分支；前端通过 API 判断会话。该应用契约由 Static、API 和前端实现。

框架认证验证应覆盖主体读取、Session 生命周期、Cookie、用户状态变化、Key 有效期与绑定、管理员角色限制、密码格式和失败响应；登录请求参数及页面交互属于应用层验证范围。

## 共享依赖

| 依赖 | 当前职责 |
| --- | --- |
| `database.Database` | 用户、账号、Key、调用记录、凭据和代理组持久化；同时满足 `oauth.CredentialStore` |
| `oauth.OAuthManager` | Adapter 注册、短期 Session、协议执行与凭据刷新 |
| `aiprovider.AIProviderManager` | 工厂、AIProvider 实例及每账号唯一的运行时 Account |
| `proxy.Manager` | 独立代理管理实现，位于 `internal/proxy` |
| `AuthService` | 主体查询、初始管理员、Session 创建与清除 |
| `OAuthResultStore` | 有效期 10 分钟的一次性 Web OAuth 结果 |

OAuth Result 的 `Put` 生成随机 ID，`Take` 原子校验归属、有效期并删除；`FindSession` 只向原主体返回匹配 service/session 的结果 ID。结果及未完成 Session 均不入库。详见 [OAuthFlow](unisub-oauthflow.md)。

### 独立 Proxy 包边界

代理领域由 `internal/proxy` 独立拥有，Service 只负责构造、注入和生命周期管理，`ModuleContext.Proxy()` 暴露代理包能力。代理领域通过小型 Store 接口访问持久化，不反向依赖 Service、Web 模块或具体 AIProvider。

`Proxy()` 返回 `*proxy.Manager`。Service 使用 `proxy.DefaultPolicy()`，可通过 `Config.ProxyPolicy` 注入完整策略。关闭时先关闭模块，再关闭代理 Manager，统计持久化成功后才关闭数据库。

## 配置

`service.Config` 是显式传入的配置结构，不负责读取环境变量：

| 字段 | 说明 |
| --- | --- |
| `DatabaseURL` | `service.New` 必须提供数据库地址 |
| `AdminUsername`、`AdminPassword` | 由应用组装层调用 `EnsureAdmin` 使用 |
| `GatewayQueueLimit` | 网关传给 Account 的队列上限；非正值使用 100 |
| `GatewayRequestTimeout` | 网关请求总超时；非正值使用 5 分钟 |
| `SessionTTL` | 浏览器 Session 有效期；非正值使用 24 小时 |
| `OAuthCallbackBaseURL` | Web OAuth 回调公开基址 |
| `ListenAddr` | 结构中保留的字段；当前入口直接读取 `LISTEN_ADDR` 启动 HTTP 监听 |

入口只为已支持的环境变量赋值；不能将所有 Config 字段都推定为环境变量。DEV／PRD 与 WebDir 属于 `unisub.Config`，部署变量见 [README](../README.md)。

## 生命周期

1. `service.New` 创建并打开数据库，构建共享 Manager、认证与结果存储，注册 OAuth Adapter 和 AIProvider 工厂。
2. 应用层确保初始管理员、恢复账号，并选择和添加模块。
3. `AddModule` 调用 `Init`；初始化或路由注册失败时回滚本次新增路由并调用该模块的 `Close`，不保留失败模块。
4. `Handler()` 交给 HTTP Server 处理请求。
5. `Close` 幂等，逆序关闭已注册模块，然后关闭数据库，返回遇到的第一个错误。HTTP Server 本身不由该方法关闭。

框架边界验证包括路由冲突与优先级、模块失败回滚、认证、请求 ID、流式写入接口和关闭行为。测试文件位于 `internal/service/`，应用集成测试位于 `internal/unisub/`。
