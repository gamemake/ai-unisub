# `internal/service` 重构方案

## 1. 目标

将 `internal/service` 从一个包含所有 Web 行为的 `Server`，重构为一个只负责组装和分发请求的 Service。Service 内部采用统一的 `Service Module` 概念，具体业务按边界拆分为四个相互独立的 Module：

1. `static` Module：静态文件、HTML 页面以及登录/登出等 Web UI 入口。
2. `api` Module：面向 Web UI 和管理功能的 JSON API，例如用户、Provider、API Key、调用记录。
3. `oauthflow` Module：OAuth 上游 callback 和授权协议衔接；不依赖项目内部 `provider` Module。
4. `gateway` Module：面向 API Key 或其他调用方的 AI 接口，以及 Provider 调用转发。

Service 本身只负责以下事情：

- 创建共享依赖；
- 接受模块注册路由；
- 根据 URL 将请求交给已注册的模块；
- 提供统一的认证、日志、错误和基础设施能力；
- 启动和关闭 HTTP Server。

Service Module 不应通过调用 `Service` 或其他 Module 的具体业务方法来协作。Module 之间通过数据库、OAuth Manager、Provider Manager 或明确的领域接口协作。

## 2. Service Module 概念

`Service Module` 是 Service 内部可独立初始化、独立注册路由、独立持有业务状态并可独立测试的功能单元。`static`、`api`、`oauthflow` 和 `gateway` 都是 Service Module；它们不是四种特殊的 Service，也不是由 Service 通过路径 `switch` 硬编码的处理分支。

每个 Module 具有以下职责：

- 声明自己的名称；
- 在初始化阶段从 Service 获取受控的 `ModuleContext`；
- 通过 `ModuleContext` 注册自己负责的 URL；
- 创建和持有自己的 Handler、业务对象以及模块私有状态；
- 在关闭阶段释放自己创建的资源；
- 不依赖其他 Module 的具体实现。

Service 具有以下职责：

- 创建共享基础设施；
- 按固定顺序初始化所有 Service Module；
- 接收并校验 Module 的路由注册；
- 为路由统一添加日志、恢复、认证和错误处理；
- 按相反顺序关闭 Module；
- 暴露最终的 `http.Handler`。

推荐的生命周期接口如下：

```go
type Module interface {
    Name() string
    Init(ModuleContext) error
    Close() error
}
```

`Close` 可以通过一个无操作默认实现兼容无状态 Module。Module 初始化失败时，Service 应停止初始化并关闭已经成功初始化的 Module，避免产生半初始化的 Service。

## 3. Service 的目标状态

重构完成后，`internal/service` 应成为一个清晰的组合根和 HTTP 请求分发层，而不是集中承载所有业务逻辑的 Handler。Service 的目标状态如下：

- Service 负责创建共享依赖、初始化 Service Module、注册路由、应用统一中间件并管理生命周期；
- `static`、`api`、`oauthflow`、`gateway` 都通过统一的 `Module` 接口接入 Service；
- 每个 Module 在初始化阶段自行声明和注册负责的路由，Service 只负责匹配、冲突检查和分发；
- Module 只能通过 `ModuleContext` 使用数据库、OAuth Manager、Provider Manager 和认证服务；
- Module 不直接依赖 `*Service`，也不直接调用其他 Module 的具体实现；
- Session、API Key 和管理员权限由统一认证服务提供，具体路由通过认证模式声明所需认证；
- OAuth 协议逻辑由 `internal/oauth` 负责，官方 CLI 兼容的 OAuth callback 和 OAuth JSON API 分别由对应的 Service Module 处理；
- 静态资源、管理 API、OAuth callback 和 AI API 使用各自独立的 Handler、认证策略、错误响应和测试；
- 新增 Module 或新增路由时，不需要修改 Service 的中心路径 `switch`；
- Service 在所有 Module 成功初始化并完成路由冲突检查后才对外提供 Handler。

现有公开 URL 和兼容行为在迁移过程中保持不变；URL 的具体归属、Handler 行为和测试要求由各自的 Module 文档维护。

## 4. 建议的目录结构

第一阶段可以仍然保留 `internal/service` 作为包，逐步把不同模块放到子目录中：

```text
internal/service/
├── service.go                 # Service、依赖创建、启动/关闭
├── router.go                  # 路由注册表和最终 Handler
├── context.go                 # Module 可使用的 Service 能力边界
├── auth.go                    # 跨 Module 的认证与调用主体解析
├── errors.go                  # 跨 Module 的错误响应和错误映射
├── static/                    # static Service Module
│   ├── module.go              # Module 入口和路由注册
│   ├── app.js
│   ├── oauth.js
│   └── ...
├── api/                       # api Service Module
│   ├── module.go              # Module 入口和路由注册
│   └── ...
├── oauthflow/                 # oauthflow Service Module
│   ├── module.go              # Module 入口和路由注册
│   └── ...
└── gateway/                   # gateway Service Module
    ├── module.go              # Module 入口和路由注册
    └── ...
```

`internal/oauth` 继续保持领域模块，不移动到 `internal/service`。它不应依赖 HTTP Service、页面或具体 Web Handler。

## 5. 核心抽象

### 5.1 Service Module 接口

每个 Service Module 在初始化时向 Service 注册自己负责的 URL：

```go
type Module interface {
    Name() string
    Init(ModuleContext) error
    Close() error
}
```

Service 创建完依赖后，由组装层通过 `AddModule` 添加需要启用的 Module。`AddModule` 按调用顺序初始化 Module，并立即完成路由注册：

```go
func (s *Service) AddModule(module Module) error {
    if err := module.Init(s.moduleContext()); err != nil {
        return fmt.Errorf("init service module %s: %w", module.Name(), err)
    }
    return nil
}
```

默认 Module 实例必须在 `cmd/server/main.go` 创建，而不是藏在 Service 内部。`Module` 接口本身不携带配置；Module 在 `Init` 阶段通过 `ModuleContext.Config()` 获取统一的、只读的 Service 配置：

```go
modules := []Module{
    staticmodule.New(),
    apimodule.New(),
    oauthflow.New(),
    gateway.New(),
}

srv, err := service.New(cfg)
if err != nil {
    return nil, err
}
for _, module := range modules {
    if err := srv.AddModule(module); err != nil {
        return nil, err
    }
}
```

Service 对外只提供接收单个 Module 的接口，构造函数不接收 Module 列表：

```go
func New(cfg Config) (*Service, error)
func (s *Service) AddModule(module Module) error
```

这样 Module 的创建和启用顺序完全由 `cmd/server/main.go` 或测试组装层控制。`AddModule` 应负责初始化失败回滚、路由冲突检查；已经成功添加的 Module 由 Service 统一关闭。所有 Module 添加完成后才能获取最终 Handler 或启动 Service。

模块不应该直接取得 `*Service`。它只能获得 `ModuleContext`，从而限制依赖方向。这里的 `staticmodule`、`apimodule`、`oauthflow` 和 `gateway` 都是 Service Module 的具体实现。

`context.go` 的职责就是定义这个边界：明确 Module 可以使用哪些 Service 能力，同时避免 Module 依赖 Service 的完整实现。它解决的是“Module 如何获得共享服务”，而不是承载任何具体业务逻辑。

### 5.2 ModuleContext 与路由注册

建议使用显式的 `Route` 方法而不是让模块直接修改 `http.ServeMux`：

```go
type RouteOptions struct {
    Auth AuthMode // AuthNone、AuthSession、AuthAPIKey、AuthSessionAdmin
    Name string   // 日志、指标和测试使用
}

type ModuleContext interface {
    Config() Config
    Handle(pattern string, options RouteOptions, handler http.Handler)
    HandleFunc(pattern string, options RouteOptions, handler http.HandlerFunc)
    Database() database.Database
    OAuth() *oauth.OAuthManager
    Providers() *provider.ProviderManager
    Auth() AuthService
    OAuthResults() *OAuthResultStore
}

```

Module 通过 `ModuleContext` 注册路由并获取 Service 提供的公共能力。总文档只规定接口和边界；具体路由列表、Handler 和业务流程由各 Module 文档维护。日志不作为通用 ModuleContext 能力暴露，统一由 Service 的请求日志中间件和各基础设施组件负责。

`ModuleContext.Config()` 返回本次 Service 实例使用的最终配置快照。Module 只能读取配置，不能修改 Service 配置，也不能自行重新解析命令行参数或环境变量。Module 应只读取自己负责的字段，例如 `gateway` 读取 `GatewayQueueLimit`；OAuth session 和 OAuth result 的保留时间由 OAuth 基础设施中的固定常量决定，不作为 Service 配置暴露。跨 Module 读取配置字段应视为设计例外。

Service 不管理定时任务。需要后台任务的 Module 自行创建和管理 `time.Ticker`、goroutine、取消 Context 及关闭等待；这些任务不属于 Service 的生命周期，也不能阻塞 Service 关闭。任务如果访问 Service 提供的数据库、OAuth 或 Provider Manager，仍必须遵守对应依赖的并发和关闭约束。

每条路由注册时必须声明最低认证要求。这个声明表示请求进入 Handler 之前必须具备什么身份，不表示调用者已经拥有所有业务权限：

- `AuthNone`：不需要身份，例如静态资源、登录页和 OAuth 上游 callback；
- `AuthSession`：必须有合法 Web Session，适用于页面、管理 API 和 OAuth JSON API；
- `AuthAPIKey`：必须有合法 API Key，适用于 `gateway` 的外部 AI API；
- `AuthSessionAdmin`：只有当整条路由确实完全禁止普通用户时才使用。

如果同一个 URL 允许 admin 和 user 访问但返回内容不同，路由应声明 `AuthSession`，而不是 `AuthSessionAdmin`。通过认证后，Handler 从 `request.Context` 读取 `Principal`，再完成资源归属、角色判断、数据过滤和响应裁剪。这样可以避免把“需要登录”和“只能管理员访问”混为一谈。

路由匹配规则必须由 Router 统一定义并测试，不能由每个 Module 自行使用 `strings.HasPrefix`。如果项目 Go 版本允许，优先使用标准 `http.ServeMux` 的方法和通配符语义；否则实现一个小型、可测试的前缀 Router。

### 5.3 路由匹配规则

采用最长前缀匹配，并在初始化时拒绝有歧义的注册：

- 精确路径优先于前缀路径；
- `/api/users/` 不应匹配 `/api/users`，除非显式注册两者或统一做尾斜杠规范化；
- 同一方法和同一路径不能重复注册；
- 两个模块注册同一优先级的重叠前缀时，初始化失败；
- Service 启动前完成全部注册，运行期间不允许动态修改路由表。

建议为路由增加 `Name`，便于日志、指标和测试定位，例如 `oauth.callback`、`ai.chat`。

## 6. Service 提供的共享服务

### 6.1 HTTP 能力与边界

`Handler()` 返回最终的 `http.Handler`；`ListenAndServe` 或 `http.Server` 的生命周期由 Service 或上层 `cmd/server` 负责。

ModuleContext 不直接暴露通用的 `*http.Client`。当前 OAuth adapter 和 Provider 已经各自拥有外部 HTTP 请求所需的 Client 配置，因此 Module 不需要获得任意外部网络访问能力。OAuth adapter、Provider 或其他明确的基础设施组件仍必须使用带超时的 HTTP Client，不能使用无超时的 `http.DefaultClient`。

如果未来确实出现由 Module 直接访问外部服务的需求，应先为该能力定义明确的领域接口或专用 Client，而不是把通用 `*http.Client` 直接加入 `ModuleContext`。

### 6.2 Database 服务

模块通过 `database.Database` 访问持久化数据：

```go
Database() database.Database
```

数据库接口本身已经覆盖 User、Account、API Key、Call Trace 和 OAuth Credential。模块不应绕过接口访问 SQLite 连接，也不应把数据库具体实现类型暴露给其他模块。

Credential 的持久化由 `database.Database` 直接提供，数据库实例也可以作为 `oauth.CredentialStore` 使用。保存 OAuth Credential、在 Provider 配置中只保存 `credential_id`、删除无引用 Credential 等规则属于 `api` Module 的 Provider/Account 业务逻辑，不需要额外提升为 Service 级别的 `CredentialService`。

### 6.3 OAuth Manager 服务

模块通过：

```go
OAuth() *oauth.OAuthManager
```

使用 `Start`、`Complete`、`Poll`、`GetValidAccessToken` 等能力。OAuth Manager 继续负责 OAuth 协议和 session 的短期状态；Web 模块只负责 HTTP 参数、当前用户绑定和响应格式。

OAuth Web 结果存储属于 Service 提供的共享基础设施，由相关 Module 通过 `ModuleContext` 使用；具体接口、生命周期和一致性要求放在对应 Module 文档中。OAuth 协议的最终校验由 `OAuthManager` 负责，Service 总文档不展开具体流程。

Service 提供的结果存储接口如下：

```go
type OAuthResult struct {
    SubjectID  string
    Service    string
    Credential oauth.OAuthCredential
}

func (s *OAuthResultStore) Put(result OAuthResult) (id string, err error)
func (s *OAuthResultStore) Take(id, subjectID string) (OAuthResult, error)
```

当前实现为公开的 `service.OAuthResultStore`，由 `service.NewOAuthResultStore()` 创建，结果保留时间由 Service 内部固定常量决定。它将结果保存在进程内存中，适用于当前单进程 Service；进程退出时未消费的结果会丢失。Module 直接使用该 Service 提供的具体类型。

`Put` 必须要求非空的 `SubjectID` 和 OAuth Service；`Take` 必须原子地校验用户归属和过期时间，并在成功读取后删除结果。归属校验失败不能删除仍有效的结果，过期结果可以在读取时清理。

### 6.4 认证服务（`auth.go`）

`auth.go` 不是因为存在两个 Module 才单独存在，而是因为认证属于多个请求入口共享的 Service 能力。它负责统一提供：

- Web Session 的读取和校验；
- API Key 的解析和校验；
- 当前用户或 API 调用主体的解析；
- 管理员权限判断；
- 认证模式以及认证失败处理。

认证服务还负责 Session 的生命周期辅助操作。登录 Module 在完成用户密码校验后通过 `CreateSession` 创建随机 Session，并使用 `SetSessionCookie` 写入 HttpOnly Cookie；登出时通过 `DeleteSession` 和 `ClearSessionCookie` 使其失效。Session 只保存短期凭证和用户 ID，认证请求必须重新加载仍存在的 User；Session 过期或 User 已删除时不得生成 `Principal`。Session 的默认保留时间由 Service 配置控制。

对应的 Service 能力边界为：

```go
type AuthService interface {
    Principal(context.Context) (Principal, bool)
    EnsureAdmin(username, password string) error
    CreateSession(user *database.PersistedUser) (token string, err error)
    DeleteSession(token string)
    SetSessionCookie(http.ResponseWriter, token string)
    ClearSessionCookie(http.ResponseWriter, token string)
}
```

当前不同 Module 使用不同认证方式：

- `static` 和 `api` 共享 Web Session；
- `gateway` 使用 API Key；
- `oauthflow` 的 OAuth 上游 callback 通常不使用 Web Session，而使用 OAuth Manager 的 state/session 校验；它不依赖项目内部 `provider` Module。

因此认证逻辑应由 Service 统一提供，而不是分别复制到各个 Module 中。Module 只声明路由需要的认证模式，具体认证实现由 Service 的认证服务和中间件完成。如果未来某个 Module 完全不共享这些认证机制，认证逻辑也可以保留在该 Module 内部；`auth.go` 不是强制的文件名，而是跨 Module 认证能力的实现位置。

认证成功后，认证中间件应把已经确认的调用身份写入 `request.Context`。Module 不需要再次解析 Cookie、Authorization Header 或重复执行认证流程，而是从 Context 读取 `Principal`：

```go
type Principal struct {
    User       *database.PersistedUser
    Account    *database.PersistedAccount // API Key 场景需要
    APIKeyID   string                     // API Key 场景需要
    Method     AuthMethod
}

func PrincipalFromContext(ctx context.Context) (Principal, bool)
```

认证中间件在生成 `Principal` 时必须完成身份链路的完整校验：

- Session 认证：确认 Session 对应的 User 仍然存在，并加载本次请求使用的 User 对象；
- API Key 认证：确认 API Key 存在且有效，加载它绑定的 Account，再确认 Account 所属 User 存在且仍可用；
- 校验失败时不能生成部分有效的 `Principal`，也不能让 Handler 自己补查缺失的 User 或 Account。

认证成功后，`Principal.User` 和 `Principal.Account` 保存本次请求已经校验过的对象指针。后续 Handler 应优先复用这些对象，而不是根据 ID 再查询一次数据库。User ID、Account ID 和 Role 统一以 `Principal.User.ID`、`Principal.Account.ID` 和 `Principal.User.Role` 为准，不在 Principal 中保存重复副本。

API Key 认证使用 `Authorization: Bearer <key>`。认证服务必须先确认 Key 有效，再加载 Key 绑定的 Account 和所属 User；任一环节失败都不能生成部分有效的 `Principal`。无效或缺失 Key 返回 `401`，Key 仍存在但绑定的 User 或 Account 不存在时返回 `403`，持久化层错误返回 `500`。API Key 原文只能用于校验，不能写入 `Principal` 或日志。

这些对象是请求范围内的已认证快照：

- Handler 可以读取，但不应直接修改并期待修改自动持久化；
- 需要修改 User 或 Account 时，必须通过明确的数据库更新流程，并更新后续使用的本地值；
- 如果某项权限或状态要求在长时间请求期间保持最新，业务逻辑可以显式重新校验，但这属于数据新鲜度/授权检查，不是重复认证；
- `Principal` 不应保存数据库连接、事务或其他请求结束后仍需管理的资源。

这里要区分两件事：

- 认证（authentication）：确认请求属于哪个用户或调用主体，由 Service 外层完成；
- 授权（authorization）：判断这个主体能否执行某个业务操作，以及响应中能看到哪些数据，由对应 Module 的业务 Handler 完成。

因此同一个 API 可以同时服务 admin 和 user。外层只负责把二者都识别为合法主体，并加载对应的 User/Account 对象；`api` Module 再根据 `Principal.User.Role`、`Principal.User.ID`、`Principal.Account.ID` 和资源归属决定查询范围、可执行操作和响应字段。用户身份应来自受保护的 Context，不应来自 URL、JSON body 或客户端自行提交的 `user_id`。

### 6.5 统一错误处理（`errors.go`）

`errors.go` 用于统一不同 Module 的 HTTP 错误响应和内部错误映射，负责区分页面请求与 JSON/API 请求，并避免把数据库错误、Provider 地址、OAuth Token 等敏感信息直接返回给客户端。

Service 至少提供统一的 JSON 错误输出能力，例如：

```go
func WriteError(w http.ResponseWriter, status int, message string)
func WriteCodedError(w http.ResponseWriter, status int, code, message string)
```

认证失败、路由未找到、路由处理 panic 和基础设施错误不得把敏感内部错误直接返回给客户端。请求处理链应统一完成 Request ID、恢复、访问日志和路由声明的认证，然后才进入 Module Handler；Request ID 同时写入响应 Header 和请求 Context。

它与 `auth.go` 的关系是：认证服务负责判断请求是否有权访问，错误处理服务负责将认证失败和业务失败转换为一致的 HTTP 响应。两者都属于 Service 的横切能力，不属于某个具体 Module。

这些文件名不是架构要求；如果实现规模较小，可以把 `context.go`、`auth.go` 和 `errors.go` 合并到少量 Service 基础设施文件中，但职责边界应保持不变。

## 7. 四个 Service Module 的职责

四个 Module 的详细设计独立维护在以下文档中：

- [`static` Module](service-static.md)：静态资源、HTML 页面、登录和登出。
- [`api` Module](service-api.md)：面向 Web UI 的管理 JSON API。
- [`oauthflow` Module](service-oauthflow.md)：OAuth 上游 callback 和协议衔接。
- [`gateway` Module](service-gateway.md)：API Key 认证、Provider 选择和 AI 请求转发。

总文档只定义它们和 Service 的边界。每个 Module 文档独立说明自己的路由、依赖、状态、认证方式、错误格式和测试要求。

| Module | 主要职责 | 默认认证 | 详细文档 |
| --- | --- | --- | --- |
| `static` | 静态文件、页面、登录/登出 | 视路由而定 | [`service-static.md`](service-static.md) |
| `api` | 用户、Provider、API Key、调用记录管理 | `AuthSession` | [`service-api.md`](service-api.md) |
| `oauthflow` | OAuth 上游 callback 和 OAuth 协议衔接 | callback 无认证 | [`service-oauthflow.md`](service-oauthflow.md) |
| `gateway` | API Key、Codex/Claude/Grok AI API、Provider 调用 | `AuthAPIKey` | [`service-gateway.md`](service-gateway.md) |

## 8. 认证和中间件边界

Service 可以在路由注册阶段根据 `RouteOptions.Auth` 自动包装 Handler：

```text
Route Handler
    -> Recover / RequestID / AccessLog
    -> AuthSession 或 AuthAPIKey
    -> Module Handler
```

建议的认证模式：

| 模式 | 用途 | 失败响应 |
| --- | --- | --- |
| `AuthNone` | 静态资源、登录页、OAuth callback | 不认证 |
| `AuthSession` | 页面、管理 API、OAuth start/result/poll | 页面 302；JSON 401 |
| `AuthAPIKey` | AI API | JSON 401/403 |
| `AuthSessionAdmin` | 用户、Provider、调用记录管理 | JSON 403 或页面 403 |

认证失败响应格式必须由路由模式决定，而不是由业务 Handler 临时判断。OAuth callback 的“无 session”是有意设计，不是认证遗漏。

### 8.1 身份在请求中的传递

推荐的请求链路是：

```text
HTTP Request
    -> 通用日志/恢复中间件
    -> AuthSession 或 AuthAPIKey
    -> 将 Principal 写入 request.Context
    -> Module Handler 从 Context 读取 Principal
    -> Handler 执行资源授权、数据过滤和响应裁剪
```

Handler 内部可以通过 `AuthService.Principal(r.Context())` 或同等的包级辅助函数取出身份。这里的“再拿一次用户身份”只是读取 Context 中已经完成认证的结果，不是再次访问 Cookie、解析 API Key 或重复查询认证信息。

如果业务需要最新的用户角色、账户状态或资源归属，Handler 可以使用 `Principal.User.ID` 再访问数据库做业务校验。这是数据新鲜度和业务授权检查，不应被误认为重复认证。

身份传递有三种常见方式：

| 方式 | 优点 | 风险/代价 | 建议 |
| --- | --- | --- | --- |
| Handler 参数传递 `Principal` | 类型明确 | 需要改造所有 Handler 和中间件签名；不适合标准 `http.Handler` | 领域服务内部可用，HTTP 边界不推荐 |
| `request.Context` | 符合 Go HTTP 中间件习惯；无需修改 Handler 签名 | 需要类型安全的读取函数，不能使用裸字符串 Key | 推荐 |
| 再从 Cookie/Header 读取 | 代码直观 | 重复认证、规则容易不一致，可能绕过外层策略 | 禁止 |

推荐约束：外层认证只生成可信的 `Principal`；Module 只读取它；业务授权由 Module 自己完成；任何客户端提交的用户 ID 只能作为待校验的数据，不能作为身份来源。

## 9. 初始化和生命周期

### 9.1 配置来源和优先级

配置来源只由进程入口处理，优先级固定为：

```text
命令行参数 > 环境变量 > 默认值
```

建议由 `cmd/server/main.go` 负责读取命令行参数和环境变量、应用默认值并生成最终配置。Service 和各个 Module 都不应调用 `flag`、`os.Getenv` 或读取 `os.Args`，也不需要知道某个值来自命令行、环境变量还是默认值。

本方案假设 `service.Module` 接口本身没有配置对象，也不暴露配置访问方法；配置通过 `ModuleContext.Config()` 提供给 Module。所有 Service 和对外服务 Module 所需的配置字段统一摊平到一个 `service.Config` 中，由字段名前缀区分归属。

例如：

```go
type Config struct {
    DatabaseURL   string
    AdminUsername string
    AdminPassword string

    GatewayQueueLimit       int
    GatewayRequestTimeout   time.Duration
    SessionTTL              time.Duration
}
```

这种设计的含义是：Module 接口保持简单，配置属于 Service 的组装输入；具体 Module 在初始化时只读取自己需要的字段，不需要再定义一套嵌套的 `ModuleConfig`。不使用 `map[string]any`，字段仍然保持静态类型和编译期检查。

建议的配置项：

| 配置项 | 命令行参数 | 环境变量 | 默认值 | 使用方 |
| --- | --- | --- | --- | --- |
| 数据库地址 | `--database-url` | `DATABASE_URL` | `sqlite://./data/ai-unisub.db` | Service |
| 初始管理员用户名 | `--admin-username` | `ADMIN_USERNAME` | `admin` | Service |
| 初始管理员密码 | `--admin-password` | `ADMIN_PASSWORD` | `admin12345` | Service |
| Gateway 请求队列上限 | `--gateway-queue-limit` | `GATEWAY_QUEUE_LIMIT` | `100` | Gateway Module |
| Gateway 单请求超时 | `--gateway-request-timeout` | `GATEWAY_REQUEST_TIMEOUT` | 按实现约定 | Gateway Module |
| Web Session 保留时间 | `--session-ttl` | `SESSION_TTL` | `24h` | Service |
| HTTP 监听地址 | `--listen-addr` | `LISTEN_ADDR` | `:8080` | `cmd/server` |

`listen-addr` 由 `cmd/server/main.go` 使用来创建监听器。它可以保存在同一个 `ServiceConfig` 中以便统一填充，但 Service 本身不读取该字段，也不负责创建监听器。

配置解析建议保持为纯函数，便于测试：

```go
type ServiceConfig struct {
    DatabaseURL    string
    AdminUsername  string
    AdminPassword  string

    GatewayQueueLimit     int
    GatewayRequestTimeout time.Duration
    SessionTTL            time.Duration
    ListenAddr             string
}

func LoadConfig(args []string, lookupEnv func(string) (string, bool)) (ServiceConfig, error)
```

`LoadConfig` 应放在 `cmd/server` 或独立的启动配置包中，由启动层负责实现“命令行覆盖环境变量、环境变量覆盖默认值”的规则，并在返回前完成格式校验。它直接填充并返回一个扁平的 `ServiceConfig`，不再通过 `ResolvedConfig` 包装。`internal/service` 包可以将接收的类型命名为 `Config`；两者字段保持一致即可。

### 9.2 Module 的组装

`cmd/server/main.go` 负责填充 `ServiceConfig`、创建 Module 实例，并通过 `AddModule` 按顺序加入 Service。Module 不需要通过构造函数接收配置，而是在 `Init(ModuleContext)` 中通过 `ctx.Config()` 获取配置。

```go
cfg, err := LoadConfig(os.Args[1:], os.LookupEnv)
if err != nil {
    log.Fatal(err)
}

modules := []service.Module{
    static.New(),
    api.New(),
    oauthflow.New(),
    gateway.New(),
}

srv, err := service.New(cfg)
if err != nil {
    log.Fatal(err)
}
for _, module := range modules {
    if err := srv.AddModule(module); err != nil {
        log.Fatal(err)
    }
}

handler := srv.Handler()
defer srv.Close()
```

每个配置字段都使用相同的优先级：命令行参数覆盖环境变量，环境变量覆盖默认值。例如 Gateway 的队列配置可以使用 `--gateway-queue-limit`、`GATEWAY_QUEUE_LIMIT` 和代码默认值；OAuthFlow 的配置使用自己的参数命名空间。不同 Module 的配置名称必须带 Module 前缀，避免命令行和环境变量冲突。Module 实例不自行读取配置来源，也不负责解析参数，只通过 `ModuleContext.Config()` 读取 Service 已经解析好的配置。

这种安排解决了两个容易混淆的问题：

- `service.Module` 是生命周期和路由注册协议，不因为新增配置项而不断扩展接口；测试替身只需要实现 `Name`、`Init` 和 `Close`。
- 对外服务 Module 仍然可以拥有必要的运行参数，例如 Gateway 的排队上限、OAuthFlow 的结果保留时间；这些参数从扁平的 `service.Config` 读取，不再额外定义一层嵌套的 `ModuleConfig` 或构造参数对象。

`LoadConfig` 可以放在 `cmd/server` 内部，也可以放到不依赖 Service/Module 实现的独立配置包中，但配置来源解析不能下沉到 `internal/service` 或具体 Module。

### 9.3 Service 初始化和生命周期

`NewFromEnv` 已拆除。Service 对外只接收已经整理好的 Config；Module 由调用方创建后通过 `AddModule` 注册：

```go
type Config struct {
    DatabaseURL   string
    AdminUsername string
    AdminPassword string

    GatewayQueueLimit     int
    GatewayRequestTimeout time.Duration
    SessionTTL            time.Duration
    ListenAddr             string
}

func New(cfg Config) (*Service, error)
func NewWithDependencies(cfg Config, db database.Database, providers *provider.ProviderManager) (*Service, error)
func (s *Service) AddModule(module Module) error
func (s *Service) Handler() http.Handler
func (s *Service) OAuthResults() *OAuthResultStore
func (s *Service) Close() error
```

这里的 `Config` 不包含命令行解析器或环境变量读取器。`ListenAddr` 虽然保存在同一个配置对象中，但只由 `cmd/server/main.go` 使用，Service 不读取它，也不负责创建监听器。默认构造路径由 `New` 创建数据库和 Provider Manager；需要由上层持有依赖时使用 `NewWithDependencies`。Module 列表始终由 `cmd/server/main.go` 或测试组装层创建并通过 `AddModule` 注册。

初始化顺序：

1. 校验并保存 `database.Database` 和 Provider Manager；
2. 创建并注册默认 OAuth adapters，构造 `OAuthManager`；
3. 注册默认 Provider factories；
4. 创建认证服务和一次性 OAuth ResultStore；
5. 创建路由注册表；
6. `cmd/server/main.go` 创建 Module，并逐个调用 `AddModule`，初始化 Module 及注册路由；
7. 校验路由冲突；
8. 构造最终 Handler；
9. 返回最终 Handler，由 `cmd/server` 使用解析后的 `ListenAddr` 启动监听。

关闭顺序相反：先停止接收新请求，再关闭 Module，最后释放数据库、OAuth Manager 和其他基础设施资源。Module 自己创建的后台任务必须在 Module 的 `Close` 中取消并等待退出，不能留下后台 goroutine。

建议不在 `New` 内部隐式监听 `127.0.0.1:1455`。监听端口属于进程入口配置，Service 应只提供 Handler；如果 Codex OAuth 需要本地 callback，应该由启动层显式创建并管理该 listener，或由配置明确开启。

## 10. 迁移步骤

采用可编译、可回滚的增量迁移：

### 阶段一：抽出路由注册层

- 新增 `Service` 或保留 `Server` 名称但引入 `Router`；
- 将当前 `handle` 中的路径判断转换为显式路由；
- 暂时让旧方法继续作为 Handler 实现；
- 添加路由冲突、尾斜杠和未匹配路径测试。

### 阶段二：抽出共享 Context 和认证

- 将 `db`、`oauth`、`providers` 和认证能力封装进 `ModuleContext`；外部 HTTP Client 只注入 OAuth adapter、Provider 等基础设施组件；
- 将 session 解析、API Key 解析和管理员判断移入 `AuthService`；
- Handler 不再读取 `Server` 的字段。

### 阶段三：迁移 OAuth callback

- 新建 `oauthflow` 模块，只迁移 OAuth 上游 callback；
- 搬迁 `oauth.go` 中的 OAuth 上游 callback；start、poll、result 的 JSON Handler 迁移到 `api` Module；
- 把 `pendingOAuth` 和 `oauthByState` 迁移为 Service 提供的 OAuth ResultStore，结果读取路由由 `api` Module 注册；
- 保留现有 URL 和现有的一次性读取、用户绑定语义；
- 优先补齐跨用户、过期、重复读取、state 错误测试。

### 阶段四：迁移普通 API

- 按资源将 `api.go` 拆成 users、providers、keys、calls；
- 每个资源在自己的 `Init` 中注册 `/api/...`；
- 将 Credential 保存、引用和清理规则收拢到 `api` Module 的 Provider/Account 业务逻辑，删除 API 与页面中的重复处理。

### 阶段五：迁移 static

- 将模板、嵌入 FS 和页面 Handler 搬到 static 模块；
- 保持 `/static/*` 和页面行为不变；
- `static/oauth.js` 只依赖公开 API，不依赖 Service 内部状态。

### 阶段六：新增 gateway 模块

- 先定义 API Key 认证、Principal、Provider 选择和错误格式；
- 用独立模块注册新前缀；
- 为上游超时、取消、调用记录和 Credential 刷新补充测试；
- 最后再移除旧的中心分发代码。

## 11. 测试要求

每个模块应使用自己的最小 `ModuleContext` 测试，不需要创建完整 Service。Service 层至少覆盖：

- 模块初始化顺序；
- 重复路由和重叠路由被拒绝；
- 精确路由、前缀路由和未匹配路由；
- AuthNone、AuthSession、AuthAPIKey 的统一失败响应；
- 模块注册后请求确实进入对应 Handler。

OAuth callback 至少保留并扩展当前测试：

- 不同用户不能读取结果；
- 结果只能读取一次；
- 结果过期后不可读取；
- state 不匹配不能完成；
- callback 不依赖 Web session，但 start/result 依赖 Web session；
- Provider 列表不泄漏 access token 和 refresh token。

AI API 需要覆盖：

- API Key 不存在、禁用或不属于目标 Account；
- Provider 不存在或初始化失败；
- 上游超时和取消；
- 调用记录成功、失败和耗时；
- OAuth token 刷新失败时不把 Credential 内容返回给客户端。

## 12. 关键设计决策总结

- Service 是组合根和路由器，不是业务 Handler 的集合。
- 模块通过 `ModuleContext` 获取 `database.Database`、`OAuthManager` 等受控服务，不获取整个 Service；通用外部 HTTP Client 不作为 ModuleContext 能力暴露。
- 路由由模块声明，Service 负责注册、冲突检查和统一中间件。
- `/api/oauth/*` 归入 `api` Module；OAuth 上游 callback 归入 `oauthflow` Module。这里的 OAuth 上游服务不是项目内部的 `provider` Module。
- OAuth callback 不要求浏览器 session；OAuth start、poll、result 要求 session 并校验用户归属。
- `pendingOAuth` 等状态属于 OAuth Web 模块，不应继续成为 Service 的公共字段。
- AI API 使用 API Key 认证，与浏览器 session 和管理 API 分离。
- Service 不应在构造函数中隐式监听端口；监听生命周期交给 `cmd/server` 或明确的启动配置。
- 先保留 URL 和行为，再逐模块迁移；每一步都通过现有测试和新增边界测试验证。
