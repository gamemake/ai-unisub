# Unisub AI Provider

`internal/aiprovider` 定义 AI 上游调用契约、配置、工厂、实例管理和运行时账号。它不依赖 database 或 Web 页面；应用层负责持久化转换和用户权限。

以下“功能设计”定义产品约束，参数和扩展方案标为建议；“当前实现与边界”明确已落地的能力，避免将可选扩展或真实平台兼容性写成已经验证的事实。

## 外部背景与模块边界

- **ProxyGroup** 包含多个网络代理，已实现按模型供应商维度自动选择代理的算法。AIProvider 复用该能力，不重复实现代理调度。
- **OAuth** 模块为 Anthropic、OpenAI、Grok 订阅账户提供认证服务，负责凭据及访问令牌管理；AIProvider 通过其公开接口获取有效令牌。
- **模型供应商** 描述厂商和默认服务 URL；**AIProvider** 描述可调用的订阅账户、API 服务或它们的组。AIProvider 组选择上游服务，ProxyGroup 选择网络代理，两者不是同一种组。

## 功能设计（目标）

### 客户端识别与访问限制

客户端类型配置包括 `Anthropic`、`OpenAI`、`Grok`、`Any`。前三项对应各厂商客户端，`Any` 表示不限制客户端类型，包括无法识别的客户端。

从 Request Headers 的 `User-Agent` 使用正则表达式识别客户端类型；正则表达式固化在代码中，不提供数据库或管理页面覆盖。建议按明确的产品标识匹配、忽略大小写，并固定匹配顺序；缺失、未匹配或存在歧义的 User-Agent 按未知客户端处理，仅允许访问 `Any`。具体官方客户端标识应通过实际请求样本确定，不将厂商名称的宽泛子串直接视为官方客户端。

客户端限制使用单值 `client_type`，界面为下拉单选，不支持数组或复选；省略时为 `Any`。旧 `client_types` 字段直接忽略，不迁移；再次保存时移除旧字段。User-Agent 分类只用于客户端兼容和访问策略，不能替代用户认证，也不能证明请求确实来自官方程序。

### 模型供应商

内置模型供应商为 **Anthropic、OpenAI、Grok、Deepseek、智谱、Kimi**，记录不允许删除。运行时视图为 `{id, name, claude_url, openai_url, models, supported_clients}`。API 类型 AIProvider 使用所选供应商的默认服务地址；已有接口配置中的 `api_endpoint` 仍可指定独立上游地址。

**不可配置（代码）**：`claude_url`、`openai_url`。由二者推导 `supported_clients`：有 Claude URL → Anthropic（Claude Code）；有 OpenAI URL → OpenAI（Codex）与 Grok。与供应商 id 无关。PUT 不得修改 URL。

**可配置**：`name`、`models`（及可选 usage header overrides）。代码内有缺省；相对缺省的差异写入 `PersistedConfig`（`type=supplier`, `name=<supplier id>`）。diff / merge 在 `aiprovider` 完成，database 只存不透明 JSON。全部字段恢复缺省后删除对应配置行。不提供整表批量 PUT。

| 供应商 | Claude URL | OpenAI URL | 支持客户端 |
| --- | --- | --- | --- |
| Anthropic | https://api.anthropic.com/v1 | 未提供 | Anthropic |
| OpenAI | 未提供 | https://api.openai.com/v1 | OpenAI, Grok |
| Grok | 未提供 | https://api.x.ai/v1 | OpenAI, Grok |
| Deepseek | https://api.deepseek.com/anthropic/v1 | https://api.deepseek.com/v1 | Anthropic, OpenAI, Grok |
| 智谱 | https://open.bigmodel.cn/api/anthropic/v1 | https://open.bigmodel.cn/api/paas/v4 | Anthropic, OpenAI, Grok |
| Kimi | https://api.moonshot.cn/anthropic/v1 | https://api.moonshot.cn/v1 | Anthropic, OpenAI, Grok |

模型供应商页面（`/#ai-catalog`）展示供应商、支持客户端、默认 URL 与模型摘要；详情可编辑显示名与模型列表，各可配置字段支持「恢复默认」，URL 与客户端只读。

### 模型映射（尚未实现）

**模型映射功能尚未实现。** 当前不提供映射配置、内置映射规则、数据库覆盖或映射编辑界面。请求正文中的 `model` 和 Grok 的 `X-Grok-Model-Override` 均原样转发，切换组成员时也不改写模型名。客户端需要直接指定目标上游支持的模型名称；供应商选择不进行模型名转换或跨协议转换。旧数据库中的映射字段不再读取或生效。

### AIProvider 类型

AIProvider 是上游模型服务的抽象，分为以下三种：

| 类型 | 配置与约束 |
| --- | --- |
| 订阅 | 支持 Anthropic、OpenAI、Grok 三种订阅账户；模型供应商由订阅账户决定，不允许独立指定为其他厂商；认证由 OAuth 模块提供 |
| API | 通过 `api_key` 和 URL 提供服务；可以指定模型供应商；`api_key` 必填；URL 未填写时使用所选模型供应商的默认 URL |
| 组 | 包含多个订阅或 API 类型 AIProvider；不持有独立的上游凭据，通过成员提供服务；禁止嵌套组，包括直接或间接自引用 |

订阅可以启用“仅原厂客户端”：开启后仅允许与订阅供应商对应的客户端类型，不能通过 `Any` 或组配置绕过；关闭时按单值 `client_type` 处理。

API 若未指定模型供应商，必须显式填写 URL；若 URL 与可用的供应商默认 URL 均不存在，应拒绝保存。目标设计中的 URL 对应当前配置字段 `api_endpoint`，不引入含义重复的第二个字段。认证和请求处理仍须由所选适配器明确支持。

### 组成员、权重与客户端兼容

每个组成员关联配置整数权重 **1～5，缺省为 3**。权重属于组与成员的关系，同一个 AIProvider 在不同组中可以使用不同权重；越界值和非整数拒绝保存。

组和成员均使用单值客户端配置，每个成员的有效客户端类型必须与组完全相同；`Any` 组只能包含 `Any` 成员。订阅开启“仅原厂客户端”后以原厂类型为有效类型。创建组、修改组限制、增删成员以及修改成员限制时，均须校验受影响的关联关系。

例如，`OpenAI` 组只能包含有效类型为 `OpenAI` 的成员，不能包含 `Anthropic` 或 `Any` 成员；不同有效客户端类型的成员不能加入同一个组。请求必须同时通过组和被选成员的客户端校验。无匹配成员时返回明确的无可用成员错误，不能绕过成员限制。

建议按以下顺序选择成员：

1. 校验用户访问权限和组的客户端限制。
2. 过滤停用、客户端不匹配、协议不兼容、处于回避期以及本次请求已尝试的成员。
3. 从可用候选中取最高权重档；高权重不可用时才降到下一档。权重表示优先级，不是按比例分配流量。
4. 在最高权重档内优先复用有效的 SessionID 绑定；没有可复用绑定时，对同权重成员均匀随机挑选。
5. 保留客户端提供的模型名，进入所选成员共享的 Account 并发队列，再通过其 ProxyGroup 选择网络代理。

### SessionID 粘性选择建议

目标是让相同 SessionID 的请求尽可能使用同一个 AIProvider，而不是固定使用同一个网络代理。

- **来源**：仅根据 User-Agent 识别的客户端读取下表的原生会话头，不读取 `X-Unisub-Session-ID`。头名大小写不敏感；按列出的优先级选取第一个存在的头，存在但为空、多值或非法时返回 400，不静默退回低优先级头。客户端或原生会话头未匹配时 SessionID 留空，按普通权重规则选择，不从 IP、请求正文、请求 ID 或设备标识猜测 SessionID。

  | 客户端 | 客户端类型 | 原生 SessionID 请求头（由高到低） |
  | --- | --- | --- |
  | Claude Code / Claude CLI | Anthropic | `X-Claude-Code-Session-Id` |
  | Codex CLI / Codex TUI | OpenAI | `Session-Id` → `Session_id` → `X-Session-Id` |
  | Grok CLI / Grok Shell / xai-grok-workspace | Grok | `X-Grok-Session-Id` → `X-Grok-Conv-Id` |

  Claude Code 的会话头见 [官方更新日志](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md)；不发送该头的旧客户端需要注入统一覆盖头。Codex 兼容旧版下划线 `session_id` 和短横线版本，部署在反向代理后时需确保旧版下划线头不会被丢弃；`X-Session-Id` 是兼容读取项，不代表网关已支持 Realtime WebSocket。Grok 的 session、conversation、request 字段见 [官方采样客户端](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/client.rs)：优先使用 session ID，只有缺失时才以 conversation ID 建立粘性；`X-Grok-Req-Id`、`X-Grok-Agent-Id` 和缓存 lineage 均不作为 SessionID。

  原生头只在对应客户端类型下解析，未知客户端的 SessionID 留空；不根据某个厂商头的存在反推客户端类型。原生会话头保留，避免破坏客户端与原厂的会话语义。
- **作用域**：绑定键建议为 `(已认证用户或租户 ID, 组 ID, 客户端类型, SessionID)`，避免不同用户和组之间互相影响。限制 SessionID 长度（建议不超过 128 字节），拒绝多值和控制字符；非法值返回请求参数错误。
- **生命周期**：维护 `绑定键 → AIProvider ID` 的有界缓存，建议空闲 TTL 为 30 分钟，并设置容量上限和 LRU 淘汰。单实例可使用内存，多实例应共享绑定状态或保证会话路由到同一实例。
- **优先级**：粘性不越过客户端限制、健康状态和权重。只有原绑定仍处于当前最高可用权重档时才复用；高权重成员恢复后，新请求可以离开低权重绑定。
- **失效与并发**：成员删除、停用、移出组或不再符合限制时立即失效。首次并发请求使用原子创建绑定，防止同一会话各自随机选择；故障切换成功后原子更新绑定，失败时清除失效绑定。
- **排队**：绑定成员繁忙时可在请求剩余时限内进入其 FIFO 队列；队列超时且满足安全重试条件时再尝试其他成员，不无限等待粘性成员。

### 错误回避与恢复建议

AIProvider 组管理成员级健康状态，ProxyGroup 继续管理网络代理健康状态；两层错误归因、尝试次数和恢复状态分别维护。成员健康状态建议按 AIProvider ID 跨组共享，避免同一故障账户在另一个组立即被重复选中；有明确模型级错误时，仅回避对应 `(AIProvider ID, 模型)`。

| 情况 | 回避策略 | 恢复策略 |
| --- | --- | --- |
| 网络连接错误、上游 5xx | 先使用现有 ProxyGroup 重试策略；仍失败时记录一次成员级失败。建议连续 3 次此类失败后打开断路器 | 初始冷却 30 秒，再失败时指数退避，最长 5 分钟，并加入随机抖动 |
| 429 限流 | 临时排除受限成员；有明确模型级范围时仅排除该模型；不判为代理故障 | 优先遵守有效 `Retry-After`，同时支持秒数和 HTTP 日期；缺失时建议从 30 秒指数退避，最长 5 分钟 |
| 401、明确的凭据失效 | OAuth 通过 OAuthManager 重新获取有效令牌，最多进行一次认证恢复后的重试；仍失败则暂停成员。API Key 失效直接暂停 | 凭据更新或重新授权后进行受控试用，避免持续认证重试 |
| 403 | 根据上游错误码区分账户禁用、模型权限和请求策略拒绝；不能仅凭状态码禁用整个成员 | 账户问题待权限或配置修复；模型权限问题仅回避对应模型；请求策略拒绝直接返回，不轮换账户规避 |
| 明确的额度耗尽 | 暂停相应账户或模型，不继续立即轮换同一成员 | 有重置时间时到期受控试用；无重置时间时等待额度更新或管理员恢复 |
| 请求参数错误、模型不存在等请求级 4xx | 不累计成员通用健康失败；明确模型不支持时可标记该成员对该模型不可用 | 修正请求或能力信息，不盲目重试相同错误 |
| 客户端取消、下游断开、本地队列超时 | 释放并发资源；不视为上游或代理健康故障 | 无需健康恢复；队列超时可作为本次请求排除繁忙成员的依据 |

建议采用 `健康（Closed）→ 回避（Open）→ 半开（Half-Open）` 状态机。临时故障冷却到期后，每个成员只放行一个符合权限和协议要求的真实请求试用；成功则清零连续失败计数并恢复，暂态失败则重新冷却并增加退避，认证等永久性错误则转为等待修复。半开请求取消时释放试用配额，不记健康失败。凭据失效、账户禁用等等待修复状态不由短期定时器自动解除。

重试必须有统一边界：建议单次请求最多尝试 **3 个不同 AIProvider（含首次）**，同时受总请求截止时间及现有 ProxyGroup 重试上限约束，不重新尝试本次已失败的成员。每层重试均消耗统一请求时间预算，避免组切换与代理轮换无限叠加。

只有请求可重放且确认未产生上游副作用，或适配器能够保证幂等时，才自动重试。对于已发送到上游但执行结果未知的生成请求，不能仅因尚未向客户端写出响应就视为安全；响应头或流式字节已写出后，禁止切换成员或拼接另一成员的响应。所有候选不可用时，区分访问限制、限流和暂时不可用返回错误，适用时附上 `Retry-After`，不绕过客户端限制或代理配置兜底直连。

建议记录候选排除原因、命中权重、粘性命中、成员切换、错误类别、冷却截止时间及半开结果；会话只记录作用域内的哈希标识，不记录原始 SessionID、令牌或 API Key。

## 当前实现与边界

- 已实现订阅、API、组三种类型；沿用 `provider` 字段指定适配器，新增 `api` 和 `group` 值，旧 `claude/codex/grok/dummy` 保持兼容。组只引用已有非组成员，修改任一关联配置都校验客户端允许集合，仍被组引用的成员不能删除。
- 供应商目录通过 `/api/ai-catalog` 管理，内置六个供应商不允许删除；可配置字段（name/models）按 supplier 写入 `PersistedConfig`，URL 与 `supported_clients` 由代码固定。模型映射尚未实现，所有模型名原样转发，不做跨协议转换。组按成员的请求协议过滤候选；直接绑定账号保持原有透明转发路径。API Key 的 `client_types` 仅按供应商协议能力（group 为成员交集），供 CC Switch；网关访问仍受 Provider `client_type` 策略约束。
- SessionID 已适配上表的原生请求头。绑定在单实例内存中按用户、组、客户端和会话隔离，键使用 SHA-256，空闲 TTL 30 分钟、容量 10000；配置更新会失效绑定，多实例共享缓存不在当前实现范围内。
- 组成员先按客户端、协议、启用和健康状态过滤，再取最高权重；同权重优先粘性，否则均匀随机。底层成员共享原有 Account 并发队列，调用记录归属实际执行的成员。
- 网络／5xx 连续失败 3 次后回避，30 秒指数退避至 5 分钟；429 优先遵守 `Retry-After`；认证恢复后仍为 401 或明确 `insufficient_quota` 时暂停，更新配置后解除暂停。OAuth 401 最多通过 OAuthManager 刷新并重试一次，API Key 不执行 OAuth 刷新。普通 403、参数及模型错误不禁用整个账号；模型级隔离、厂商细分额度重置时间属于后续适配扩展。
- 单请求最多尝试 3 个不同组成员，仅在尚未发送的连接失败、获取令牌失败或未执行的排队失败时切换。已到达上游的 POST 5xx、结果未知的断线以及已输出的流式响应不自动重放。配置版本号阻止旧凭据请求的迟到结果重新暂停已更新账号。
- 绑定缓存不保存原始 SessionID；调用记录仍遵循现有 API 的客户端会话字段语义，原生 SessionID 可供查询，不将 Dashboard 登录会话混入。调用记录与组粘性选择使用相同的原生头解析规则；未匹配时留空，不使用通用覆盖头或请求正文兜底。

## 类型与命名

| 类型 | 职责 |
| --- | --- |
| AIProvider | Config、UpdateConfig、Handle、FetchQuota、GetCachedQuota、ResetUsage；已接入部分真实查询与内存缓存，Dummy Fetch 返回随机演示数据 |
| AIProviderConfig | 单个实例的公共配置 |
| AIProviderFactory | 根据 ID 与 JSON 创建具体实例 |
| AIProviderManager | 注册工厂、创建／恢复／更新／删除实例 |
| Account | 每账号唯一的运行时并发计数与 FIFO 队列 |
| AIProviderCallTrace、APICallRecorder | 调用结果及回调，不携带数据库实体 |

具体实现包括 Codex、Claude、Grok、APIProvider、组和 Dummy。前端页面是 `src/pages/ai-providers.tsx`，供应商只读详情页面为 `src/pages/ai-catalog.tsx`；管理接口与缓存键分别为 /api/ai-providers、ai-providers。

兼容协议包括 /api/providers、旧 #providers/#accounts 页面入口、JSON 字段 provider/provider_type 和已有 SQLite 列名；这些不是 Go 类型名。OAuth CLI 的 provider 标识也保持其协议含义。

## 用量与余额查询接口

订阅 quota 统一返回时间维度、已用百分比和 UTC 重置时间；API 余额仍保留原始字段和数值文本。查询错误通过接口错误返回，不放入 quota 数据。详细结构与单位规则见 [Quota 设计](ai-provider-quota.md)。

**严格定义：订阅返回当前套餐使用量，其他非 group provider 返回账户余额；group 不查询、不返回 quota，也不聚合成员数据。不提供历史用量或费用报表，也不以它们替代余额。不再使用 `UsageQuery` 或时间／模型筛选入参。**

**AIProvider 实例提供两个接口签名：`FetchQuota(context.Context) (*Quota, error)` 与 `GetCachedQuota() *Quota`。** 定义位于 [internal/aiprovider/aiprovider.go](../internal/aiprovider/aiprovider.go)。目标行为分别为主动查询并更新缓存、仅读取缓存；已实现 Codex／Claude／Grok 订阅查询和 DeepSeek／Moonshot 余额查询；其他组合明确返回不支持。Dummy 返回随机演示数据并写入缓存。缓存为实例内存存储，TTL 5 分钟，过期保留旧数据。quota 快照属于 `AIProviderState`，由 `aiprovider` 在 FetchQuota、配置失效和被动 Header 更新后提交；应用通过 `SetStateStore` 用原有 `ListAccounts`／`SaveAccount` 写入 `accounts.state`，`aiprovider` 不导入 database。启动时 `RestoreState` 恢复额度，fresh／stale 仍按 `updated_at` 与 TTL 判断。旧库中独立的 `accounts.quota` 列在加载时并入 state。Group 的查询返回不支持，缓存读取返回 nil；管理列表省略其 quota 字段，界面不展示额度和刷新按钮。

用量展示快照与缓存读取结果采用单层结构：`Quota` 订阅返回 `subscription: [{time_dimension, usage, reset_at}]`，API 余额保留 `items: [{name, value, source?}]`、必填 `cache_status` 和可选 `updated_at`，不包含成员结果或成员错误。`name/value` 仅供展示，不用于调度计算。数据类型与默认请求头分别定义于 [quota.go](../internal/aiprovider/quota.go) 和 [catalog_defaults.go](../internal/aiprovider/catalog_defaults.go)。适配器、测试替身及前端类型已同步；Supplier 的 `subscription_usage_header_overrides` 和 `api_usage_header_overrides` 仅保存非认证请求头覆盖项并支持深拷贝，由现有 JSON 配置流程承载。已新增管理员查询路由和网页展示；五家供应商查询路径、凭据注入及内存缓存读写已实现，其他供应商范围见独立设计文档。网页入口见非组 AI Provider 的“用量 / 余额”，打开时仅读缓存，点击“刷新”主动查询。

按以下职责划分（供应商支持范围见独立设计文档）：

- **AIProvider** 的 `FetchQuota` 根据订阅／API 业务类型分派查询，成功后更新缓存并返回数据；`GetCachedQuota` 只返回缓存快照及未命中／新鲜／过期状态，不访问上游、不隐式刷新。主动刷新失败保留旧缓存并返回错误，不将旧数据伪装成成功结果。实例提供凭据引用、账号范围和代理配置，不按认证方式判断业务类型。
- **模型供应商 Supplier** 只保存订阅与 API 两类查询的非认证 Header 覆盖项；**SupplierAdapter** 分别实现 `QuerySubscriptionUsage` 和 `QueryAPIBalance`，负责厂商请求构造与响应转换。两类查询均为可选能力。
- **OAuth / Credential** 提供有效令牌及账号身份。查询 URL、HTTP 方法和默认 Headers 由代码固定，供应商仅保存 Header 覆盖项；Token、Cookie、查询密钥和账号／组织／团队 ID 不保存在供应商公共配置中，由实例上下文动态注入。
- **组类型** 只负责成员路由，不参与 quota 查询和展示；**Account** 保持并发与队列职责。本系统调用记录统计与订阅用量／非订阅余额查询分开，历史用量和费用不纳入这两个入口。

本文只记录查询入口位置、职责归属与当前实现边界；详细设计以独立的 [AIProvider 用量与余额查询设计](ai-provider-quota.md) 为准。该文档反向引用本文，并集中维护返回结构、配置规则、各供应商查询 URL／HTTP 方法／Headers、参数来源与接口核实状态，避免在两处重复维护接口清单。`ResetUsage` 不属于该只读查询设计的通用能力。

目标设计还包括从正常模型响应中被动采集用量：Body／SSE 的 Token 消耗记入本次调用，订阅供应商额度 Headers 更新订阅用量快照（非订阅仅采集明确余额），并与 `FetchQuota` 主动刷新配合。当前已解析 Token 用量并保存原始响应 Headers，已将已知 Claude／Codex／Grok 额度 Headers 转换为缓存展示项；三家订阅的字段、证据与处理规则见 [响应用量采集](ai-provider-quota.md#3-订阅-quota-获取方法)。

## 配置

| 字段 | 当前语义 |
| --- | --- |
| id/name/labels | 实例标识和元数据；创建时 ID 由调用方指定 |
| kind | subscription、api 或 group；旧配置由适配器及 auth_type 推导 |
| supplier | 供应商标识；订阅由账户适配器确定，API 可指定 |
| client_type | 单值枚举：Any、Anthropic、OpenAI、Grok；省略视为 Any；数组不合法 |
| official_only | 仅订阅有效，只允许供应商对应的原厂客户端类型 |
| members | 组成员 `{id, weight}` 列表，weight 缺省 3，取整数 1～5；禁止嵌套 |
| enabled | 未指定时为 true |
| auth_type | oauth（默认）或 api_key |
| credential_id | OAuth 凭据引用，兼容嵌套 oauth.credential_id |
| api_key | 上游密钥，api_key 认证必须非空；oauth 模式不接受该值 |
| api_endpoint | 可选 HTTP/HTTPS 上游基址 |
| proxy_group_id | 使用代理组 |
| proxy_application_error_statuses | 可选的代理应用错误 HTTP 状态码列表（400～599）；省略时仅 5xx 计为应用健康失败，其他 4xx 不清除或触发共享代理的应用熔断 |
| max_concurrent_connections | 负值拒绝，0 归一为 1 |
| queue_timeout_seconds | 负值拒绝，0 在运行时使用 180 秒 |

旧 `api_keys` 配置不再接受。直接 `proxy` 字段作为未知字段忽略，不校验、不迁移，也不用于代理连接；仅 `proxy_group_id` 生效。模型由客户端请求提供并原样发送，不将所有模型能力写成账号固定属性。组不保存上游密钥、供应商、URL 或代理组，实际调用使用成员配置。

## 认证与调用

OAuth 模式调用 OAuthManager.GetValidAccessToken(ctx, service, credentialID)，不自行执行授权交换或持久化刷新。API Key 模式使用配置密钥：Claude 使用 X-Api-Key，其他实现使用 Bearer；OAuth 使用 Token 及平台所需 Header。

Handle 保留请求转发契约；Web 网关通过 WithResponseWriter 注入响应接收者，AIProvider 输出状态、响应头及流式字节。Recorder 收集调用结果，由应用层转换为数据库记录。网关路径、Body 限制和记录边界见 [Gateway](unisub-gateway.md)。

## 运行时账号

AIProviderManager 为每个实例持有同一个 Account，不按请求创建独立队列。Account.Handle 接收 request、recorder 和 queueLimit，统一执行并发限制、FIFO、超时、取消与释放。

配置更新经 Manager 更新实例并唤醒等待者；删除或停用账号拒绝后续请求并唤醒排队请求。降低并发上限不取消已执行请求。详细行为见 [Gateway](unisub-gateway.md)。

## 代理契约

ProxyResolver 提供 `ResolveProxy(ctx, groupID, application, tried) (*proxy.Endpoint, error)`、`ProxyRetryLimit(groupID)`、`ReportProxy(endpoint, application, class) error`。Service 注入独立 `proxy.Manager`；AIProvider 不直接持有数据库。

调用使用模型供应商标识作为应用维度（无供应商时回退服务标识），并维护已尝试地址。网络错误报告为 network，上游 5xx 默认报告为 application；其他 4xx 默认报告为 application_ignored，不因账户凭据或请求错误禁用共享代理。可通过 `proxy_application_error_statuses` 覆盖应用错误状态码映射。Context 取消释放半开配额，不将代理标为不可用。代理重试受组上限及安全重放条件约束；生成 POST 仅在明确的连接建立前失败时重试，响应发出后不再重试。

OAuth 刷新和上游调用显式使用通过 `proxy_group_id` 选出的 Endpoint。AIProvider 忽略直接 `proxy` 字段；未指定代理组时不使用代理，指定的组不存在或无可用候选时失败，不绕过代理直连。优先级、共享状态及分层统计见 [Proxy](proxy.md)。

## 管理响应与验证

账号保存、凭据引用和响应字段以 [API](unisub-api.md) 为准：config 过滤敏感字段，但管理员响应可附带完整 Credential；不对整个响应作一概脱敏声明。

本地测试使用模拟上游验证认证、流式转发、用量和并发；真实平台可用性、模型与媒体接口取决于对应账号和上游，不能由模拟测试推定。
