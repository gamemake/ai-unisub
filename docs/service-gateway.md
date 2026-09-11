# `gateway` Service Module

`gateway` 是面向外部调用方的通用 AI API 网关 Service Module，不局限于 LLM 对话。它负责 API Key 认证、调用主体解析、Provider Account 选择，以及 Codex/Claude/Grok 的文本、图像、音频、视频和多模态 AI API 请求转发与调用记录。它不负责浏览器登录、OAuth callback 或 Credential 原始存储。

## 路由

`gateway` 对外暴露的 URL 应保持官方 API 的路径形状，不增加 `/api/codex`、`/api/claude`、`/api/grok` 这样的本项目专用 Provider 前缀。Provider 由 API Key 绑定的 Account 决定：同一个官方路径可以根据不同 API Key 转发到不同 Provider。

需要支持的官方兼容路径按能力和 Provider 实际支持情况注册，例如：

| 能力 | 官方兼容 URL 形状 | 说明 |
| --- | --- | --- |
| 文本和多模态理解 | `/v1/responses`、`/v1/chat/completions`、`/v1/messages` | 根据 Provider 使用 OpenAI/Codex、Grok 或 Claude 的官方协议 |
| 图像理解和生成/编辑 | `/v1/responses`、`/v1/chat/completions`、`/v1/messages`、`/v1/images/*` | 图像可以作为输入，也可以作为生成或编辑结果 |
| 音频 | `/v1/audio/*`、`/v1/realtime/*` 或官方协议对应路径 | 覆盖转写、语音生成和实时音频能力 |
| 视频 | `/v1/videos`、`/v1/videos/{video_id}`、`/v1/videos/{video_id}/content` | 覆盖视频生成任务、状态查询和媒体内容下载 |
| 文件和媒体资源 | `/v1/files/*` | 支持图像、视频、音频或其他官方 API 要求的文件资源 |
| 模型与能力发现 | `/v1/models` | 返回当前 Account 对应 Provider 可用的模型或能力 |

具体 endpoint 由各 Provider 的官方 API 兼容协议决定，例如 OpenAI/Codex 风格的 Responses、Chat Completions、Images、Audio、Videos，Claude 的 Messages，以及 Grok 的 Responses、Chat Completions 或媒体能力。Gateway 的本地请求路径、方法、查询、请求体、multipart 格式、流式响应和异步任务响应应尽可能与对应官方 API 保持一致；本项目只替换认证处理和上游目标，不改变客户端使用的官方路径形状。

由于不同 Provider 可能共享 `/v1/chat/completions` 或 `/v1/models` 等路径，不能仅靠 URL 判断 Provider。Gateway 必须先通过 API Key 找到 Account，再根据 Account 的 Provider 类型选择 Codex、Claude 或 Grok 的上游地址和认证方式。客户端不能通过额外提交 Provider 名称来绕过 Account 授权。

## 请求流程

解析 `Authorization: Bearer sk-...` → 校验 API Key → 根据 Account 确认 Codex/Claude/Grok Provider → 按官方 URL 形状和请求类型匹配文本、图像、音频或视频 API → 通过 ProviderManager 执行 → 记录状态/耗时/媒体类型/错误 → 返回官方兼容响应。Provider 调用必须沿用请求 Context；OAuth Provider 通过 `OAuthManager.GetValidAccessToken` 获取 token，不能返回 Credential 原文。

视频和部分图像生成接口通常是异步任务，Gateway 必须保留官方的创建任务、查询状态、取消/删除和下载内容语义，不能把异步媒体任务强行转换成一次性的文本响应。流式文本、音频和图像响应也应保持官方的 SSE、二进制或 multipart 行为。

## Provider Account 运行时实体

Gateway 中应区分两种 Account：

1. `database.PersistedAccount`：数据库中的持久化 Account，保存 ID、名称、Provider 类型和配置；
2. `ProviderAccount`：运行时 Account 实体，持有已创建的 Provider、并发控制器、运行状态和请求执行逻辑。

`ProviderAccount` 才是 `MaxConcurrentConnections`、排队、取消、Provider 可用性和请求转发的责任主体。Gateway Handler 不应自己操作 `Provider.Handle`、`active` 计数或等待队列，而应通过 Account 的方法执行请求：

```go
type ProviderAccount struct {
    id       string
    config   provider.ProviderConfig
    provider provider.Provider
    limiter  *AccountLimiter
}

func (a *ProviderAccount) Handle(r *http.Request, rec provider.APICallRecorder) error
func (a *ProviderAccount) UpdateConfig(config provider.ProviderConfig) error
func (a *ProviderAccount) Close() error
```

推荐的请求边界：

```text
gateway Handler
    -> API Key / Account 权限校验
    -> ProviderManager.GetAccount(accountID)
    -> ProviderAccount.Handle(request, recorder)
        -> AccountLimiter.Acquire(request.Context())
        -> provider.Provider.Handle(request, recorder)
        -> permit.Release()
```

这样可以保证所有进入同一个 Account 的请求都经过同一套并发和生命周期规则；Gateway 只负责 HTTP、认证、路由和响应，ProviderAccount 负责 Account 的运行时行为。

### Account 的状态和生命周期

ProviderManager 负责创建、获取、更新和删除运行时 Account：

- 创建 Account 时，根据持久化配置创建 Provider 和 `AccountLimiter`；
- 获取 Account 时返回同一个运行时实体，不能每个请求重新创建 limiter；
- 更新配置时由 `ProviderAccount.UpdateConfig` 原子更新 Provider 配置和 limiter 上限；
- 删除 Account 时先标记为 closing，拒绝新请求，处理等待队列，再释放 Provider 资源；
- Account 的 ID 在运行时实体和数据库实体之间保持一致；
- Provider 类型、Credential 引用等持久化信息由配置提供，运行时状态不写回普通 Account 配置。

如果需要表示状态，建议至少区分 `ready`、`disabled`、`closing` 和 `failed`。状态检查和转换应由 ProviderAccount 自己完成，Gateway 不应直接修改内部字段。

## 并发限制与排队

每个 ProviderAccount 独立使用自己的并发控制器，`ProviderConfig.MaxConcurrentConnections` 表示该 Account 同时允许执行的最大上游请求数。不能把不同 Account 或不同 Provider 类型共用一个并发计数，否则一个 Account 的拥堵会错误影响其他 Account。

请求进入 `gateway` 并完成 API Key、Account 和 Provider 校验后，必须通过该 ProviderAccount 获取执行槽位：

```text
请求
  -> API Key / Account / Provider 校验
  -> 尝试获取并发槽位
       ├── 有空闲槽位：立即执行 Provider.Handle
       └── 没有空闲槽位：进入等待队列
```

排队规则：

- 活跃执行数不得超过 `MaxConcurrentConnections`；
- 等待队列最多容纳 100 个请求，100 个是每个 Provider Account 的独立上限；
- 已经占用执行槽位的请求不计入等待队列长度；
- 队列满时不再等待，立即返回稳定的限流错误，例如 HTTP `429`，错误信息为 `provider request queue is full`，并可带 `Retry-After`；
- 队列应使用 FIFO，避免后进入的请求长期插队；
- Provider 请求完成、失败或客户端断开后，都必须释放执行槽位并唤醒下一个等待请求；
- `MaxConcurrentConnections` 的无效值必须在 Provider 配置校验阶段处理。建议要求它为正数，不使用“0 表示无限制”，避免无界并发。

### 客户端断开连接

等待队列中的请求必须监听 `r.Context().Done()`。如果客户端在排队期间断开连接：

1. 从等待队列中移除该请求；
2. 不再为它分配执行槽位；
3. 不调用 `Provider.Handle`；
4. 不记录为一次已发送的上游调用；
5. 唤醒或通知队列中的下一个请求。

如果请求已经获得槽位并开始执行，必须把原始 `Request.Context()` 传给 Provider，使上游 HTTP 请求也能因客户端断开而取消。执行槽位只能在 `Provider.Handle` 返回后释放，释放逻辑应使用 `defer`，避免错误路径泄漏槽位。

### 配置更新和可观测性

Provider Account 更新 `MaxConcurrentConnections` 时，并发控制器必须以并发安全方式更新。降低上限不能中断已经运行的请求，只影响后续槽位分配；提高上限可以唤醒等待队列中的请求，但仍不能超过新上限。

至少记录以下指标或调用记录字段：当前活跃数、当前排队数、最大队列长度、排队等待时间、队列满拒绝数、客户端取消数、Provider 执行耗时和上游结果。日志和指标不得包含 API Key 或 OAuth Credential。

## 并发控制器的实现设计

并发控制应放在 `gateway` 调用 Provider 的边界上，而不是放在具体的 Codex、Claude 或 Grok Provider 内部。这样三类 Provider 复用同一套排队、取消和统计规则，且限制对象明确为 Provider Account。

### 状态模型

每个 Provider Account 持有一个独立的 `AccountLimiter`：

```go
type AccountLimiter struct {
    mu       sync.Mutex
    limit    int              // MaxConcurrentConnections，必须 > 0
    active   int              // 当前正在执行 Provider.Handle 的数量
    queue    []*waiter        // FIFO，最多 100 项
    maxQueue int              // 固定为 100
}

type waiter struct {
    ctx   context.Context
    ready chan struct{}
    state waiterState
}
```

`active` 只统计已经进入 `Provider.Handle` 的请求；等待队列中的请求不占用并发槽位。每个 Account 只能有一个 limiter，不能把同一 Provider 类型的多个 Account 共用一个 limiter。

### 获取和释放槽位

Gateway Handler 在完成 API Key、Account 和 Provider 校验后调用 `Acquire`：

```go
permit, err := limiter.Acquire(r.Context())
if err != nil {
    writeQueueError(w, err)
    return
}
defer permit.Release()

provider.Handle(r, recorder)
```

`Acquire` 必须在同一把 mutex 下完成以下判断：

1. `active < limit` 且队列为空：立即增加 `active`，返回 permit；
2. 没有空闲槽位且 `len(queue) < 100`：把 waiter 放到队尾并等待；
3. `len(queue) >= 100`：不入队，立即返回 `ErrProviderQueueFull`。

只有 `permit.Release()` 能减少 `active`。Release 时从队首开始寻找第一个仍处于等待状态的 waiter，将其标记为 `granted`、增加 `active`，再关闭 `ready`；不能在锁外决定下一个 waiter，避免多个请求同时获得同一个槽位。

### 队列取消和竞态

等待请求必须同时等待 `ready` 和 `r.Context().Done()`：

```go
select {
case <-waiter.ready:
    // 已经获得 permit，进入 Provider.Handle
case <-r.Context().Done():
    // 尝试从 FIFO 队列删除 waiter
}
```

取消和授予可能同时发生，必须在 mutex 内通过 waiter 状态解决：

```text
queued   -> granted   -> running -> released
queued   -> canceled
granted  -> canceled   （Acquire 观察到客户端已断开）
```

规则如下：

- `queued -> canceled`：从队列移除，不增加 `active`；
- `queued -> granted` 后客户端才断开：必须消费并释放这个 permit，不能让槽位泄漏；
- 已经进入 `Provider.Handle` 后客户端断开：不再回到队列，依靠 Request Context 取消上游请求；
- 被取消的 waiter 不能再次被授予槽位；
- 任何状态转换和队列删除都必须在同一把 mutex 下完成；
- 客户端取消不返回普通业务错误，因为连接可能已经不存在；服务端只记录取消指标。

### FIFO 和唤醒策略

队列必须按进入顺序服务。一个请求被取消后，从队列中删除它，后面的请求向前移动；不能使用无界 channel 直接代替队列，因为无界 channel 不方便在客户端取消时删除指定等待项。

建议由以下事件触发唤醒：

- 一个正在执行的请求释放 permit；
- `MaxConcurrentConnections` 增大；
- Provider Account 从不可用状态恢复。

`Release` 每次最多授予一个空闲槽位对应的 waiter；如果配置一次增加多个并发槽位，可以在同一个锁保护区内连续授予多个 waiter，但每次授予都必须重新检查 limiter 状态。

### 队列满和 Provider 不可用

队列满返回稳定的 JSON 错误：

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 1
Content-Type: application/json; charset=utf-8
```

```json
{
  "error": "provider request queue is full"
}
```

如果 Account 被删除、禁用或 Provider 初始化失败，新的请求不应入队，应直接返回 `503 Service Unavailable` 和 `provider_unavailable`。已经排队但尚未执行的请求也应被唤醒并以同样错误结束；已经执行的请求按原有 Context 处理，不因为配置更新强行夺走正在使用的 permit。

### 动态更新 `MaxConcurrentConnections`

配置更新必须通过 limiter 的方法完成，而不是直接修改 `ProviderConfig` 字段：

```go
func (l *AccountLimiter) SetLimit(limit int) error
```

建议规则：

- `limit <= 0` 在配置校验阶段拒绝；
- 增大 limit：立即授予最多新增槽位数量的等待请求；
- 减小 limit：不取消正在执行的请求，也不抢占已有 permit；新的执行请求必须等到 `active` 降到新 limit 以下；
- 队列上限 100 不随 `MaxConcurrentConnections` 改变；
- ProviderAccount 删除时先标记 limiter 为关闭状态，再拒绝新请求并清理等待队列。

### 监控字段

每个请求至少记录：

- `provider_account_id`；
- `provider_type`；
- `max_concurrent_connections`；
- `queue_wait_duration`；
- `active_at_start`；
- `queue_length_at_arrival`；
- 是否发生 `provider_queue_full`；
- 是否在等待期间取消；
- 是否在执行期间取消；
- Provider 执行耗时和最终状态。

这些字段用于判断是 Provider 响应慢、并发上限过低、队列过满，还是客户端主动断开，不能把所有情况都记录成普通的 Provider 错误。

建议使用：

```go
type Principal struct {
    User      *database.PersistedUser
    Account   *database.PersistedAccount
    APIKeyID  string
    Method    AuthMethod
}
```

API Key 认证必须在生成 `Principal` 前确认 API Key、Account 和 User 都存在且归属有效，并把本次校验得到的 `User`、`Account` 对象放入 Principal。后续使用 `principal.Account.ID` 获取 ProviderAccount；ProviderAccount 的运行时对象仍由 `ProviderManager` 管理，不替代 Principal 中的持久化 Account 快照。

缺少或无效 Key 返回 `401`，越权返回 `403`，Provider 不存在返回 `404`，上游错误不暴露内部 URL 和 token。日志不得记录 Authorization Header，请求和响应体需要设置大小限制。

测试覆盖 API Key 生命周期、越权、Provider 错误、OAuth token 刷新、文本/多模态请求、图像生成与输入、音频流、视频异步任务、媒体下载、并发上限、FIFO 排队、队列满拒绝、等待期间客户端断开、执行期间取消、动态配置更新、上游超时/取消、调用记录和敏感信息泄漏。
