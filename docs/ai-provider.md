# AIProvider 包设计

`internal/aiprovider` 负责管理 AI 上游账号、供应商能力、网关转发、账号并发限制、OAuth Token 刷新、代理调用、额度查询与调用记录。当前核心对象是 `ProviderManager`、`Account`、`Supplier` 和 `Gateway`；包内已经没有旧版 `AIProvider`、`AIProviderFactory`、`APICallRecorder` 或独立 Provider 实例接口。

该包直接依赖 Database、ProxyManager 和 OAuthManager。它不注册 UniSub 管理路由，但提供完整的 Gateway `http.Handler`；`internal/unisub` 只负责把该 Handler 挂载到 `/v1/`，并为账号、供应商、额度和模型列表提供管理 API。

## 依赖与构造

```go
func NewProviderManager(
    db database.Database,
    proxyManager proxy.ProxyManager,
    oauthManager oauth.OAuthManager,
) (ProviderManager, error)
```

| 依赖 | 当前用途 |
| --- | --- |
| `database.Database` | 账号与 Supplier Overlay 持久化、Gateway API Key/用户校验、调用记录 |
| `proxy.ProxyManager` | 模型请求、额度查询和模型列表查询的代理组调度与 `proxy_logs` |
| `oauth.OAuthManager` | Subscription Account 的 Access Token 刷新 |
| `internal/logger` | 通过包级 `ModuleLogger` 记录 Gateway 与持久化异常 |

Database 和 ProxyManager 是构造时的必要依赖；OAuthManager 可以为 nil，但 Subscription Account 需要刷新时会失败。正常生命周期是：

1. Service 打开 Database 和 ProxyManager。
2. 创建 ProviderManager 并调用 `Open`。
3. `Open` 加载内置 Supplier、Supplier Overlay 和所有 Account，然后启动脏状态定时落库任务。
4. Service 关闭时先调用 ProviderManager `Close`，等待后台任务退出并执行最后一次落库，再关闭 ProxyManager 和 Database。

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `types.go` | Account 配置、运行状态和 Quota 数据结构 |
| `manager.go` | ProviderManager 接口、构造、生命周期、Gateway 认证入口 |
| `manager_account.go` | Account CRUD、额度与模型入口 |
| `manager_database.go` | Account/Supplier 的加载、保存和定时落库 |
| `manager_supplier.go` | Supplier Overlay 与模型刷新 |
| `account.go` | Account 校验、Token 获取、模型/额度和组成员解析 |
| `limiter.go` | 单账号并发限制和等待队列 |
| `scheduler.go` | Group Account 的成员选择 |
| `supplier.go` | Supplier 接口、公共请求、Overlay、模型映射和被动额度采集 |
| `supplier_*.go` | 各供应商的内置配置与协议差异 |
| `gateway.go`、`gateway_call.go` | API Key 认证、请求转发、响应流和调用记录 |
| `errors.go` | 包内 Sentinel Error、安全 HTTP 消息和错误响应辅助函数 |
| `logger.go` | 声明 `ModuleLogger = logger.ModuleLogger("aiprovider")` |

## ProviderManager

```go
type ProviderManager interface {
    Open() error
    Close() error
    Handler() http.Handler

    ListAccounts() []*Account
    NewAccount(value json.RawMessage) (*Account, error)
    DelAccount(id int) error
    SetAccountConfig(ctx context.Context, id int, value json.RawMessage) error
    FetchQuota(ctx context.Context, id int) (AccountQuota, error)
    ResetQuota(ctx context.Context, id int, resetType string) error
    GetModels(ctx context.Context, id int, clientType string) ([]string, error)

    ListSuppliers() []Supplier
    SetOverlayConfig(ctx context.Context, supplierID string, value json.RawMessage) error
    RefreshModels(ctx context.Context, supplierID string, accountID int) error
}
```

Manager 在内存中按数据库 ID 保存唯一的 `*Account`，列表按 ID 升序返回。新增、修改、主动额度查询和额度重置会立即保存账号；Gateway 被动更新、OAuth 刷新和并发计数变化只标记 `Dirty`，由每 30 秒一次的后台任务或 `Close` 落库。

`NewAccount` 以 ID 0 交给 Database 新增记录，最终 ID 由持久化层回填。更新配置会整体替换 `AccountConfig`；若传入 Subscription Credential 的非空 Refresh Token 与当前值相同，Account 会保留当前完整 Credential，避免用管理页面中的旧 Access Token 覆盖运行时刷新结果。

`DelAccount` 只删除指定账号并关闭其 Limiter，不自行检查 API Key 或 Group 引用。UniSub 管理 API 在调用删除前负责检查这些引用。

## Account 数据模型

```go
type Account struct {
    ID     int
    Config AccountConfig
    State  AccountState
    Quota  AccountQuota
    Dirty  bool
}
```

`AccountConfig` 按用途分为公共、Subscription、API 和 Group 字段：

| 字段 | 当前语义 |
| --- | --- |
| `kind` | `subscription`、`api` 或 `group`，创建时必须明确提供 |
| `name` | 必填，解析时去除首尾空白 |
| `labels` | 业务标签；aiprovider 当前不解释内容 |
| `supplier` | 非 Group 必须引用已加载 Supplier，统一转为小写 |
| `client_type` | `claude`、`codex`、`grok` 或空值 |
| `proxy_group_id` | 非负；具体请求交给 ProxyManager 处理 |
| `enabled` | 是否允许新的 Gateway 认证；省略时 Go 零值为 false |
| `max_concurrent_connections` | 非负；0 在运行时按 1 处理 |
| `queue_timeout_seconds` | 非负；0 在运行时使用 180 秒 |
| `subscription_plan` | Subscription 的套餐元数据；当前不校验目录，也不参与调度 |
| `official_only` | 仅允许出现在 Subscription；当前 Gateway 尚未执行该策略 |
| `credential` | Subscription 内联保存的 OAuth Credential，Access Token 必填 |
| `api_endpoint` | 可选 HTTP/HTTPS 基础 URL，不允许 UserInfo |
| `api_key` | API Account 必填，Subscription 禁止携带 |
| `members` | Group 成员 `{id, weight}` 列表 |

公共校验会拒绝空名称、负数代理组/并发/超时、未知 Client Type 和非法 Endpoint。

Subscription Account 必须有非空 Access Token，不能携带 API Key。API Account 必须有 API Key，不能携带 OAuth Credential、`official_only` 或 `subscription_plan`。两者都必须引用已知 Supplier。

Group Account 必须包含成员，不得持有 Supplier、Credential、API Key、Endpoint 或 `official_only`。每个成员 ID 和 Weight 都必须为正数，成员不能重复或引用自身；创建和更新时还要求成员已经存在且不是 Group，并禁止一个已被其他 Group 引用的账号再变为 Group。当前 Weight 没有 1～5 上限，也没有缺省值。

管理 API 禁止修改既有 Account 的 Kind，并对 JSON 使用未知字段拒绝策略；这两项不是 `ProviderManager.SetAccountConfig` 自身的完整保证。直接调用包接口时，调用方必须维持相同约束。

## 持久化

每个 Account 对应一条 `database.PersistedAccount`：

| 数据库字段 | 来源 |
| --- | --- |
| `id` | `Account.ID` |
| `ai_provider` | `string(Account.Config.Kind)` |
| `name` | `Account.Config.Name` |
| `config` | 完整 `AccountConfig` JSON |
| `state` | `AccountState` JSON |
| `quota` | `AccountQuota` JSON |

API Key 和 OAuth Access/Refresh Token 当前都内联存放在 `accounts.config`，不是 Credential ID 引用。管理 HTTP 响应会清空这些秘密字段，但 `ProviderManager.ListAccounts` 返回的是包内完整运行时对象。

`AccountState` 保存 `active_connections`、`queued_connections` 和 `refresh_at`。从数据库恢复时，两个连接计数强制重置为 0，`refresh_at` 保留。Quota 从独立的 `accounts.quota` 字段恢复，不存放在 State 中。

## Supplier

Supplier 同时定义上游地址、协议能力、模型目录、模型映射、套餐权重、额度查询和请求前后处理：

```go
type Supplier interface {
    GatewaySupplierListener

    GetID() string
    GetName() string
    SupportClients() []ClientType
    GetModels() []string
    GetBuiltinConfig() SupplierConfig
    GetConfig() SupplierConfig
    DiffConfig(SupplierConfig) (SupplierOverlayConfig, error)
    GetOverlayConfig() SupplierOverlayConfig
    SetOverlayConfig(SupplierOverlayConfig, bool) error
    RefreshModel(context.Context, *Account) ([]string, error)
    FetchQuota(context.Context, *Account) (AccountQuota, error)
    ResetQuota(context.Context, *Account, string) error
}
```

`Open` 固定注册七个 Supplier：

| ID | 名称 | Claude URL | OpenAI URL | `SupportClients` | 主动 Quota |
| --- | --- | --- | --- | --- | --- |
| `anthropic` | Anthropic | `https://api.anthropic.com/v1` | 无 | `claude` | Subscription Usage |
| `openai` | OpenAI | 无 | `https://api.openai.com/v1` | `codex`、`grok` | Subscription Usage |
| `xai` | xAI | 无 | `https://api.x.ai/v1` | `codex`、`grok` | Subscription Billing Items |
| `deepseek` | DeepSeek | `https://api.deepseek.com/anthropic/v1` | `https://api.deepseek.com/v1` | 三种 | API Balance Items |
| `kimi` | Kimi | `https://api.moonshot.cn/anthropic/v1` | `https://api.moonshot.cn/v1` | 三种 | API Balance Items |
| `zhipu` | zAI | `https://open.bigmodel.cn/api/anthropic/v1` | `https://open.bigmodel.cn/api/paas/v4` | 三种 | 不支持 |
| `dummy` | Dummy | `http://dummy.local/v1` | 无 | `claude` | 本地随机演示数据 |

`SupportClients` 只按内置 URL 推导：存在 Claude URL 即支持 Claude，存在 OpenAI URL 即支持 Codex 和 Grok。Account 的 `client_type` 会进一步缩小 `Account.SupportedClients` 的返回结果；Group 返回成员能力的交集。

这套能力目前用于管理展示和 API Key 导出，不是 Gateway 的访问控制。Gateway 不读取 User-Agent，也不调用 `SupportedClients`；`client_type` 和 `official_only` 尚不会拒绝实际请求。

### Supplier Overlay

内置 URL 和 Supplier 名称不可修改。可覆盖字段为：

- `Models []string`
- `Mappings []ModelMapping`
- `Weights []SubscriptionPlanWeight`

nil Slice 表示继承内置值，非 nil 的空 Slice 表示明确清空。模型必须非空、无首尾空白且不能重复；Mapping 的 Pattern/Target 必须非空且 Target 必须存在于有效模型列表；套餐 Weight 只要求名称非空、名称不重复且整数大于 0，当前允许内置目录之外的名称。

Overlay 以 `PersistedConfig{type: "supplier", name: <supplier id>}` 保存。`SetOverlayConfig` 会保存传入 Overlay；恢复成全内置值时通常由管理层先调用 `DiffConfig` 生成全 nil Overlay，但当前仍会保留一条空配置记录，不会自动删除数据库行。加载时，属于未知 Supplier 的配置行会被删除。

内置套餐权重包括 OpenAI 的 `codex_plus=1`、`codex_pro_5x=5`、`codex_pro_20x=20`，Anthropic/Dummy 的 `claude_pro=1`、`claude_max_5x=5`、`claude_max_20x=20`，以及 xAI 的 `super_grok=1`、`super_grok_plus=3`、`super_grok_heavy=10`。这些权重当前只是 Supplier 配置元数据，Group Scheduler 尚未使用。

## 上游地址与认证

每次请求先解析一个具体非 Group Account，再取得 Access Token 与 API Base URL：

1. `api_endpoint` 非空时始终优先使用。
2. OpenAI Subscription 的非 Claude 请求使用 `https://chatgpt.com/backend-api/codex`。
3. 路径以 `/messages` 或 `/messages/count_tokens` 结尾时选择 Supplier 的 Claude URL。
4. 其他请求选择 Supplier 的 OpenAI URL。
5. 所需协议 URL 不存在时返回错误，不自动切换另一种协议。

API Account 直接使用 `api_key`。Claude 请求写入 `X-Api-Key`，其他请求写入 `Authorization: Bearer`。Subscription Account 始终使用 Bearer Token；Anthropic Subscription 额外合并 OAuth Beta Header，OpenAI Subscription 添加 Codex Beta、User-Agent 和可选 ChatGPT Account ID。

Subscription Token 刷新由 Account 串行化。以下任一条件成立时调用：

- `State.RefreshAt` 为空，即账号创建或旧状态从未成功刷新；
- 距上次刷新已满 17 小时；
- Credential 的 `ExpiresAt` 距当前不足 5 分钟。

刷新调用为 `OAuthManager.Refresh(ctx, supplier, credential, proxy_group_id)`。成功后替换完整 Credential、更新 `RefreshAt` 并标记 Dirty。首次 Gateway 请求通常会触发刷新，因此真实 Subscription Account 除 Access Token 外还需要可用 Refresh Token。Dummy Supplier 覆盖了 Access 获取逻辑，不执行 OAuth 刷新。

主动 Quota 与模型列表查询直接读取 Account 当前保存的 Credential/API Key，不经过 `Account.GetAccess`，因此不会先执行 OAuth 刷新。

## Group 当前行为

Group 只保存成员引用，不拥有 Supplier、Credential 或 Endpoint；实际请求的凭据、上游地址和 Proxy Group 均取自具体成员，Group 自身的 `proxy_group_id` 不参与调用。当前 `groupScheduler.GetAccount` 始终返回 `members[0]` 指向的账号：

- 不读取 Weight。
- 不按模型、Client Type、额度或健康状态筛选。
- 没有随机、轮询、Session 粘性、故障回避或成员重试。
- 首成员不存在时直接不可用，不尝试后续成员。
- Gateway 会再次检查选中成员是否启用。

因此 Group Weight 和 Supplier 套餐权重目前都不影响流量。Group 的模型列表是另一套只读计算：它收集各成员模型与精确 Mapping 别名，逐成员应用 Mapping 后求交集；带 `*` 的 Pattern 不会单独生成一个可枚举别名。

## 并发限制与队列

Gateway 在选出具体成员后才使用该成员的 `AccountLimiter`。Group 自身的并发和超时字段不参与执行。

- 空闲且没有等待者时，`active < max(1, configured)` 即可立即进入。
- 无槽位时增加 `queued_connections` 并等待释放、配置变化、关闭、Context 取消或超时。
- 超时缺省 180 秒；超时和 Context Deadline 对客户端映射为 504，取消映射为 499。
- Release 减少 Active 并广播唤醒等待者。
- 配置变化会唤醒等待者重新判断容量；降低上限不会取消已执行请求。
- 删除账号会关闭 Limiter，拒绝等待者和后续 Acquire。

等待者被广播后重新竞争锁，当前不保证严格 FIFO。`enabled` 在 Gateway 认证和成员选择阶段检查；配置改为禁用不会强制取消已经执行或已经通过认证进入队列的请求。

## Gateway 请求流程

ProviderManager 自己实现 `GatewayAuthListener`，所以 `/v1/` 不经过 Service Session 认证。一次调用按以下顺序执行：

1. 从 `Authorization: Bearer` 读取客户端 API Key；没有有效 Bearer 格式时回退到 `X-Api-Key`。
2. 从 Database 列出 API Key，使用常量时间比较找到未过期记录，再确认所属用户启用。
3. 取得 Key 绑定的根 Account，检查根账号启用；若为 Group，则按当前 Scheduler 解析首成员并检查成员启用。
4. 根据具体成员的 Supplier 创建调用上下文。认证在此之前失败的请求不写 Call Trace。
5. 读取并关闭客户端 Body，限制为 64 MiB。
6. 通过具体成员 Limiter 获取执行槽位。
7. 获取上游凭据与 Base URL，移除 Hop-by-hop、Cookie 和客户端认证 Header，注入上游认证。
8. Supplier 应用模型映射和供应商 Header，再通过 ProxyManager 发出请求。
9. 原样转发上游状态和大部分 Header，删除 Hop-by-hop Header 与 `Set-Cookie`；SSE 响应逐块 Flush。
10. 完整读取成功后调用 Supplier `PostResponse` 被动更新 Quota，并在请求结束时保存 Call Trace。

上游 URL 以 Base URL 的 Path 为前缀，把入站 `/v1` 后缀拼接上去，并保留 Query。Gateway 不允许 Base URL 含 UserInfo。

请求前的模型映射只处理 JSON 顶层 `model` 字段，按 Overlay 顺序使用第一条匹配规则。Pattern 支持任意数量的 `*`；未命中保持原值。配置了 Mapping 时，非空且无法解析为 JSON 的请求会在转发前失败。当前实现不会改写 Grok 专用模型 Header。

响应会在发送给客户端的同时完整保留在内存中供 Call Trace 和 Usage 解析使用。代码没有为原始上游响应设置总大小上限；只对 Call Trace 的 gzip 解压结果设置 64 MiB 上限。大响应会产生相应的内存压力。

## Proxy 行为

所有真实 Supplier 请求都调用：

```go
ProxyManager.Do(proxyGroupID, supplierID, request, body, classifier)
```

Group ID 0 仍经过 ProxyManager 的直连路径并记录 `proxy_logs`。日志 `app` 使用 Supplier ID。

请求具备 `Idempotency-Key`，或方法为 GET、HEAD、OPTIONS、PUT、DELETE 时，aiprovider 提供响应分类回调，把 429 和 5xx 视为应用失败。其他请求不按 HTTP 状态触发代理轮换。ProxyManager 返回非 nil Response 时，Supplier 总是把最终响应交给 Gateway，即使同时存在分类错误；没有 Response 时才返回请求错误。

代理地址选择、网络错误重试、熔断和日志字段见 [Proxy 包设计](proxy.md)。

## 模型目录

非 Group 的 `GetModels` 返回 Supplier 的有效模型列表；ProviderManager 当前忽略传入的 `clientType` 参数。Group 返回成员映射后的模型交集。

`RefreshModels` 要求 Account 不是 Group 且其 Supplier 与请求 Supplier 一致。除 Dummy 外，各实现使用当前 Account 凭据访问：

- Anthropic：`{api_endpoint 或 ClaudeURL}/models`
- 其他真实 Supplier：`{api_endpoint 或 OpenAIURL}/models`

响应兼容 `data[].id` 和 `models[].id`，忽略空 ID 并去重。刷新结果写入 Supplier Overlay 的 Models，同时保留已有 Mappings 与 Weights；这会影响同一 Supplier 的所有 Account，而不是只影响发起刷新的账号。

模型查询使用 30 秒 Context Timeout，响应体上限 1 MiB，并为每次实际上游交换写一条 Call Trace。

## Quota

```go
type AccountQuota struct {
    Subscription []SubscriptionQuotaItem
    Items        []QuotaItem
    CacheStatus  QuotaCacheStatus // missing 或 fresh
    UpdatedAt    time.Time
}
```

当前没有 Quota TTL、`stale` 状态或单独的 GetCachedQuota 方法。管理列表直接读取 `Account.Quota`；空状态由 UniSub API 展示为 `missing`。

主动 `FetchQuota` 的当前范围：

- Anthropic Subscription：查询 OAuth Usage，标准化为 5h/weekly 等窗口。
- OpenAI Subscription：查询 Codex Usage，按实际窗口长度生成维度。
- xAI Subscription：查询 Billing，并把响应叶子字段摊平成 Items。
- DeepSeek/Kimi API：查询 Balance，并把响应叶子字段摊平成 Items。
- zAI：返回 `ErrQuotaUnsupported`。
- Dummy：生成随机 5h/weekly 演示窗口。
- Group：统一返回 `ErrQuotaUnsupported`。

真实 Supplier 配置了自定义 `api_endpoint` 时，主动 Quota 查询返回 `ErrQuotaNotConfigured`，不会向自定义地址猜测额度端点。成功结果立即更新 Account、标记 `fresh` 并保存数据库；失败保留旧 Quota。

Gateway 的被动采集在完整收到响应后发生：Anthropic/OpenAI Subscription 把已知 Rate Limit Header 合并到已有窗口；其他实现把名称含 `ratelimit` 或 `rate-limit` 的 Header 保存为 Items。被动结果只标记 Dirty，稍后由定时任务落库。

`ResetQuota` 先调用 Supplier Hook，成功后只把本地缓存改为 `missing` 并保存。除 zAI 返回不支持外，当前 Supplier Hook 都是 No-op；该方法不会重置上游平台额度，`resetType` 目前也未被使用。

主动 Quota/模型请求的响应体限制为 1 MiB，并写入 Database Call Trace。当前这些辅助请求直接复制认证 Header 到 Trace，没有使用 Gateway 的 Header 脱敏函数；数据库和管理输出必须按敏感数据边界处理。

## Call Trace 与安全边界

Gateway Trace 记录具体成员 Account ID、Supplier ID、User ID、客户端 API Key 明文、入站路径、完整出站 URL、请求/响应大小、排队与调用耗时、状态、Session ID、模型和 Token Usage。API Key 明文用于调用归属和所有权检查，UniSub 的调用记录列表与详情在返回浏览器前会清空该字段。

- 入站/出站 Header 会脱敏 Authorization、X-Api-Key、Cookie、Set-Cookie 和 Proxy-Authorization。
- Request Body 与 Response Body 会保存；gzip 响应会尽量解压后再存储。
- JSON 和 SSE 中的 Anthropic/OpenAI Usage 字段会归一到 Token 计数。
- Session ID 从一组 Claude/Codex/Grok 常见原生 Header 中取第一个非空值。
- 上游 Response 存在时，`http_error_code` 保存实际上游状态，包括 2xx。
- 客户端写入失败记录为 499；在收到上游响应之前失败时状态可能保持 0。
- 认证阶段失败不产生 Trace；读取 Body、排队、准备上游或传输阶段失败会产生 Trace。

Gateway 面向客户端只返回 `errors.go` 中定义的安全消息，不直接暴露内部错误或上游响应内容。完整内部错误用于结构化日志或受控的数据库诊断字段。

## 错误与日志约束

调用方可使用 `errors.Is` 判断的主要公开错误包括：

- 账号与 Gateway：`ErrAccountNotFound`、`ErrGatewayUnauthorized`、`ErrGatewayForbidden`、`ErrUnavailable`、`ErrQueueTimeout`
- Supplier：`ErrSupplierNotFound`、`ErrModelsUnsupported`、`ErrUpstream`、`ErrInvalidResponse`
- Quota：`ErrQuotaUnsupported`、`ErrQuotaNotConfigured`、`ErrAuthentication`、`ErrRateLimited`

所有包级 Sentinel Error、客户端安全消息和 JSON 错误响应辅助函数统一声明在 `errors.go`。模块 Logger 只在 `logger.go` 声明，其他文件统一使用该 `ModuleLogger`。

## Web 集成边界

- `internal/unisub/gateway.go` 只把 `ProviderManager.Handler()` 挂到 `/v1/`，不重复实现认证或转发。
- `internal/unisub/api.go` 负责管理员权限、账号响应脱敏、引用检查和账号 Kind 不可变约束。
- `internal/unisub/ai_catalog.go` 把 Supplier 内部 Slice 结构转换为管理 API 的 `model_mappings` 和 `subscription_plan_weights` 视图。
- `internal/unisub/ai_provider_models.go`、`ai_provider_quota.go` 提供模型和额度管理路由。

因此 ProviderManager 是当前 Gateway 的领域实现和持久化协调者，不是一个与 Database/Web 完全隔离的纯工厂层。

## 当前限制与验证范围

当前实现需要明确保留以下边界：

- Group 只选首成员；Weight、套餐权重、健康、粘性和重试均未接入。
- `client_type` 与 `official_only` 不参与 Gateway 访问控制。
- `subscription_plan` 与套餐 Weight 没有目录约束，也不参与运行时决策。
- Quota 没有 TTL/stale 语义；Reset 只清理本地快照。
- Subscription 首次调用会尝试刷新；主动 Quota/模型查询却不会刷新 Token。
- 从数据库加载 Group 时不执行跨账号嵌套与成员存在性复核；创建/更新时才执行。
- 直接调用 Manager 可以绕过 UniSub API 的 Kind 不可变和删除引用检查。
- Gateway 会在内存及数据库 Call Trace 中保留请求/响应 Body；响应捕获没有原始大小上限。
- 本地 Mock 与 Dummy 测试不能证明真实供应商端点、Header 或账号权限长期有效。

自动化测试当前覆盖：

| 测试文件 | 主要范围 |
| --- | --- |
| `account_access_test.go` | Group 成员解析、OAuth 刷新、Endpoint 和凭据更新 |
| `limiter_test.go` | Limiter 关闭与等待者释放 |
| `scheduler_test.go` | 固定首成员和 Group 嵌套校验 |
| `supplier_test.go` | 多通配符 Mapping、Overlay、组模型交集和客户端能力 |
| `supplier_anthropic_test.go`、`supplier_openai_test.go` | 主动/被动 Subscription Quota 转换 |
| `supplier_dummy_test.go` | Dummy 本地响应与演示 Quota |
| `gateway_usage_test.go` | JSON/SSE Usage、gzip、客户端取消和流写失败 |
| `gateway_integration_test.go` | Database、Group、模型映射、Proxy、Gateway、Quota Header 与 Trace 端到端集成 |
