# Unisub AI Provider

`internal/aiprovider` 定义 AI 上游调用契约、配置、工厂、实例管理和运行时账号。它不依赖 database 或 Web 页面；应用层负责持久化转换和用户权限。

以下“功能设计”定义产品约束，参数和扩展方案标为建议；“当前实现与边界”明确已落地的能力，避免将可选扩展或真实平台兼容性写成已经验证的事实。

## 外部背景与模块边界

- **ProxyGroup** 包含多个网络代理，已实现按模型供应商维度自动选择代理的算法。AIProvider 复用该能力，不重复实现代理调度。
- **OAuth** 模块为 Anthropic、OpenAI、Grok 订阅账户提供认证服务，负责凭据及访问令牌管理；AIProvider 通过其公开接口获取有效令牌。
- **模型供应商** 描述厂商和默认服务 URL；**AIProvider** 描述可调用的订阅账户、API 服务或它们的组。AIProvider 组选择上游服务，ProxyGroup 选择网络代理，两者不是同一种组。

## 功能设计（目标）

### 客户端识别与访问限制

客户端类型配置包括 `claude`、`codex`、`grok` 三种值。省略 `client_type` 表示不限制客户端类型，包括无法识别的客户端。

从 Request Headers 的 `User-Agent` 使用正则表达式识别客户端类型；正则表达式固化在代码中，不提供数据库或管理页面覆盖。建议按明确的产品标识匹配、忽略大小写，并固定匹配顺序；缺失、未匹配或存在歧义的 User-Agent 按未知客户端处理，仅允许访问未设置客户端限制的账号。具体官方客户端标识应通过实际请求样本确定，不将厂商名称的宽泛子串直接视为官方客户端。

客户端限制使用单值 `client_type`，界面为下拉单选，不支持数组或复选；省略时表示不限客户端。旧 `client_types` 字段直接忽略，不迁移；再次保存时移除旧字段。历史的 `Anthropic`、`OpenAI`、`Grok`、`Any` 值读取时分别归一化为 `claude`、`codex`、`grok`、未设置。User-Agent 分类只用于客户端兼容和访问策略，不能替代用户认证，也不能证明请求确实来自官方程序。

### 模型供应商

内置模型供应商为 **Anthropic、OpenAI、xAI、Deepseek、智谱、Kimi**，记录不允许删除。运行时视图为 `{id, name, claude_url, openai_url, models, model_mappings, supported_clients, subscription_plan_weights?}`。API 类型 AIProvider 使用所选供应商的默认服务地址；已有接口配置中的 `api_endpoint` 仍可指定独立上游地址。

**不可配置（代码）**：`claude_url`、`openai_url`。由二者推导 `supported_clients`：有 Claude URL → `claude`（Claude Code）；有 OpenAI URL → `codex`（Codex）与 `grok`。与供应商 id 无关。PUT 不得修改 URL。订阅套餐 **ID 目录与缺省 plan** 仍由代码按适配器固定（见账号 `subscription_plan`）；供应商配置不增删 plan ID，只配置各 ID 的订阅套餐用量权重。

**可配置**：`name`、`models`、`model_mappings`、可选 usage header overrides，以及 **`subscription_plan_weights`**（订阅套餐用量权重）。代码内有缺省；相对缺省的差异写入 `PersistedConfig`（`type=supplier`, `name=<supplier id>`）。diff / merge 在 `aiprovider` 完成，database 只存不透明 JSON。全部字段恢复缺省后删除对应配置行。不提供整表批量 PUT。

| 供应商 | Claude URL | OpenAI URL | 支持客户端 |
| --- | --- | --- | --- |
| Anthropic | https://api.anthropic.com/v1 | 未提供 | `claude` |
| OpenAI | 未提供 | https://api.openai.com/v1 | `codex`, `grok` |
| Grok | 未提供 | https://api.x.ai/v1 | `codex`, `grok` |
| Deepseek | https://api.deepseek.com/anthropic/v1 | https://api.deepseek.com/v1 | `claude`, `codex`, `grok` |
| 智谱 | https://open.bigmodel.cn/api/anthropic/v1 | https://open.bigmodel.cn/api/paas/v4 | `claude`, `codex`, `grok` |
| Kimi | https://api.moonshot.cn/anthropic/v1 | https://api.moonshot.cn/v1 | `claude`, `codex`, `grok` |

#### 订阅套餐用量权重（供应商）

本文将其称为**订阅套餐用量权重**（Subscription Plan Usage Weight），用于将来同组成员、**同一组成员优先级权重档内**按相对用量做负载均衡；**不等于**组成员优先级权重（Group Member Priority Weight）。后者对应 `members[].weight`，前者对应 `subscription_plan_weights`。

- **存放位置**：模型供应商运行时字段 `subscription_plan_weights`。
- **摊开存储**：一层 `map[plan_id]weight`（JSON 对象），`plan_id` 为键、正整数权重为值；**不**使用嵌套数组或 `{id, weight}` 列表，也**不**把权重写在账号 `AIProviderConfig` 上。
- **代码缺省**（builtin；overlay 仅存与缺省不同的项，与 models 等 overlay 规则一致）：

| 供应商 | plan_id | 缺省订阅套餐用量权重 |
| --- | --- | --- |
| openai | `codex_plus` | 1 |
| openai | `codex_pro_5x` | 5 |
| openai | `codex_pro_20x` | 20 |
| anthropic | `claude_pro` | 1 |
| anthropic | `claude_max_5x` | **5** |
| anthropic | `claude_max_20x` | **20** |
| grok | `super_grok` | 1 |
| grok | `super_grok_plus` | 3 |
| grok | `super_grok_heavy` | 10 |
| deepseek / zhipu / kimi | （无订阅 plan 目录） | 空 map；不参与订阅用量加权 |

- **校验**：键必须是该供应商允许的 plan ID（openai 仅 `codex_*`，anthropic 仅 `claude_*`，grok 仅 `super_grok*`）；值为整数且 **≥ 1**；未知键、非正数拒绝保存。恢复缺省时 map 与 builtin 完全一致则删除 overlay 中该字段。
- **查询函数（已实现）**：`UsageWeight(supplierID, plan)`（builtin）与 `AIProviderManager.UsageWeight` / `UsageWeightFromConfig`（合并 catalog）只读供应商表 + 账号 `subscription_plan`；不读 quota、不读上游。空 plan 用该适配器缺省 plan；API／组固定订阅套餐用量权重 **1**。Dummy 订阅读 **anthropic** 供应商用量权重。
- **调度**：当前 `Select` 同一组成员优先级权重档内仍均匀随机；接入订阅套餐用量权重为后续，不改变 `members[].weight` 档位语义。

模型供应商页面（`/#suppliers`）展示供应商、支持客户端、默认 URL 与模型摘要；详情可编辑模型列表、模型映射与**订阅套餐用量权重**（openai／anthropic／grok；支持「恢复默认」；模型列表可从同供应商账号刷新），URL 与客户端只读。

套餐目录、账号套餐字段与用量权重的详细约定见 [AIProvider 订阅套餐与用量权重](ai-provider-subscription.md)。

### 模型映射

每个供应商可配置有序的 `model_mappings: [{from, to}, …]`。网关在 **Select 出实际执行账号之后**、发往上游之前，按该账号所属供应商（`config.supplier`，订阅账号由适配器推导）的规则，按**第一条命中**改写：

- 请求 JSON 顶层 `model` 字段（不改写 `metadata.model` 等嵌套字段）
- Grok 的 `X-Grok-Model-Override` 请求头

`from` 支持至多一个 `*` 通配符（如 `claude-*`、`*-preview`）；`to` 为上游模型名，界面从该供应商当前「支持模型」列表中选择。无映射或未命中时原样转发。组故障切换到其他成员时，按**新成员**供应商重新映射。默认无内置映射。

### AIProvider 类型

AIProvider 是上游模型服务的抽象，分为以下三种：

| 类型 | 配置与约束 |
| --- | --- |
| 订阅 | 支持 Anthropic、OpenAI、Grok 三种订阅账户；模型供应商由订阅账户决定，不允许独立指定为其他厂商；认证由 OAuth 模块提供 |
| API | 通过 `api_key` 和 URL 提供服务；可以指定模型供应商；`api_key` 必填；URL 未填写时使用所选模型供应商的默认 URL |
| 组 | 包含多个订阅或 API 类型 AIProvider；不持有独立的上游凭据，通过成员提供服务；禁止嵌套组，包括直接或间接自引用 |

订阅可以启用“仅原厂客户端”：开启后仅允许与订阅供应商对应的客户端类型，不能通过未设置限制或组配置绕过；关闭时按单值 `client_type` 处理。

API 若未指定模型供应商，必须显式填写 URL；若 URL 与可用的供应商默认 URL 均不存在，应拒绝保存。目标设计中的 URL 对应当前配置字段 `api_endpoint`，不引入含义重复的第二个字段。认证和请求处理仍须由所选适配器明确支持。

### 组内调度

组成员兼容性、健康度筛选、额度等级调度、成员选择顺序、SessionID 粘性、成员健康回避与恢复、重试安全边界，以及当前实现与后续边界，统一见 [AIProvider 组内调度](ai-provider-group-routing.md)。健康度只决定账号是否可参与，额度等级决定可用账号之间的调度顺序。

## 当前实现与边界

- 已实现订阅、API、组三种类型；沿用 `provider` 字段指定适配器，新增 `api` 和 `group` 值，旧 `claude/codex/grok/dummy` 保持兼容。组只引用已有非组成员，修改任一关联配置都校验客户端允许集合，仍被组引用的成员不能删除。
- 供应商通过 `/api/suppliers` 管理，内置六个供应商不允许删除；可配置字段（name/models/model_mappings）按 supplier 写入 `PersistedConfig`，URL 与 `supported_clients` 由代码固定。模型列表可在供应商编辑页手工维护，也可选定同供应商账号后调用 `POST /api/accounts/{id}/fetch-models` 从上游刷新（写入仍走供应商 PUT）。OpenAI 订阅账号的刷新读取 Codex 公开 `models.json`（`models[].slug`），并使用该账号的 `proxy_group_id`；其它账号仍走上游 `GET {base}/models`。模型映射按供应商规则改写顶层 `model` 与 Grok 覆盖头；未命中则原样转发。组按成员的请求协议过滤候选；直接绑定账号保持原有透明转发路径。API Key 的 `client_types` 仅按供应商协议能力（group 为成员交集），供 CC Switch；网关访问仍受 Provider `client_type` 策略约束。
- 组内调度的当前实现与边界（包括 SessionID 粘性、成员健康状态、重试限制和响应安全）见 [AIProvider 组内调度](ai-provider-group-routing.md#当前实现与边界)。

## 类型与命名

| 类型 | 职责 |
| --- | --- |
| AIProvider | Config、UpdateConfig、Handle、FetchQuota、GetCachedQuota、FetchModels、ResetUsage；已接入部分真实查询与内存缓存，Dummy Fetch 返回随机演示数据；FetchModels 按实例凭据查询上游模型列表（OpenAI 订阅改为拉取 Codex 公开 `models.json`，并使用账号 `proxy_group_id`） |
| AIProviderConfig | 单个实例的公共配置 |
| AIProviderFactory | 根据 ID 与 JSON 创建具体实例 |
| AIProviderManager | 注册工厂、创建／恢复／更新／删除实例 |
| Account | 每账号唯一的运行时并发计数与 FIFO 队列 |
| AIProviderCallTrace、APICallRecorder | 调用结果及回调，不携带数据库实体 |

具体实现包括 Codex、Claude、Grok、APIProvider、组和 Dummy。管理端账号与供应商接口分别为 `/api/accounts`、`/api/suppliers`，前端查询键分别为 `accounts`、`suppliers`。

JSON 字段 `provider`／`provider_type`、已有 SQLite 列名和 OAuth CLI 的 provider 标识保持其原有协议含义；它们不是管理资源名。

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
| subscription_plan | 仅订阅：最高套餐档 ID（只相信配置，不从上游推断）。Codex：`codex_plus` / `codex_pro_5x` / `codex_pro_20x`（缺省 `codex_plus`）；Claude 与 Dummy：`claude_pro` / `claude_max_5x` / `claude_max_20x`（缺省 `claude_pro`）；Grok：`super_grok` / `super_grok_plus` / `super_grok_heavy`（缺省 `super_grok`）。加载时空值补缺省；非法 ID 拒绝。API／组不得携带。订阅套餐用量权重不在此字段，见供应商 `subscription_plan_weights` |
| client_type | 单值枚举：`claude`、`codex`、`grok`；省略表示不限客户端；数组不合法 |
| official_only | 仅订阅有效，只允许供应商对应的原厂客户端类型 |
| members | 组成员 `{id, weight}` 列表，`weight` 缺省 3，取整数 1～5；禁止嵌套。此为**组成员优先级权重**，与供应商上的订阅套餐用量权重无关 |
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
