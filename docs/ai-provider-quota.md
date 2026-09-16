# AIProvider 额度查询设计

本项目的 `Quota` 泛指账号额度信息，包括订阅用量、限额、重置时间和 API 余额；它不是统一可计算的剩余额度，不包含本地调用历史的 Token `Usage`。

## 1. 总体设计

**语义约束：订阅 provider 只返回当前套餐使用量（含额度窗口、剩余比例和重置时间）；API provider 只返回账户余额；group 不查询、不返回 quota，也不聚合成员 quota。**

**AIProvider 统一提供两个入口：`FetchQuota` 主动获取并更新缓存，`GetCachedQuota` 仅读取缓存；模型供应商分别实现订阅查询与 API 查询。**

```text
AIProvider.FetchQuota（请求上游，成功后更新缓存并返回数据）
    ├─ 订阅类型 → SupplierAdapter.QuerySubscriptionUsage
    └─ API 类型 → SupplierAdapter.QueryAPIBalance

AIProvider.GetCachedQuota（仅读缓存，不请求上游）
    └─ 返回单个 provider 的用量快照及缓存状态
```

Group 列表项省略 `quota` 字段，界面不显示 quota 或刷新按钮；调用 Group 的 `refresh-quota` 接口返回 501，不触发任何成员查询。内部统一接口中，Group 的 `FetchQuota` 返回不支持，`GetCachedQuota` 返回 nil。

`Quota` 使用单层结构，不包含 `members`、成员 ID 或成员错误。订阅通过 `subscription` 数组返回标准化的时间维度、已用百分比和重置时间：

```json
{
  "subscription": [
    {"time_dimension": "5h", "usage": 25, "reset_at": "2030-01-01T12:00:00Z"},
    {"time_dimension": "weekly", "usage": 40, "reset_at": "2030-01-07T00:00:00Z"}
  ],
  "cache_status": "fresh",
  "updated_at": "2030-01-01T08:00:00Z"
}
```

其他类型继续返回 `items: [{name, value, source?}]`，保留原始字段和值，不改变 API 余额结构。`subscription` 与 `items` 不同时返回。`cache_status` 必填，取值为 `missing`、`fresh`、`stale`；未命中仅返回 `{"cache_status":"missing"}`。`updated_at` 为用量观测时间。查询失败通过接口错误返回，不混入 quota 数据；失败保留旧缓存。

订阅的 `usage` 统一为百分比，真实零值保留，超过 100 的值不截断；缺失用量不生成零值窗口。`reset_at` 为 UTC 时间，未获知时使用 Go 零时间，界面显示“未知”。Claude 主要维度为 `5h`、`weekly`，专属窗口保留后缀；Codex 根据实际窗口时长命名，不假定 primary 总是 5h；Grok billing 返回 `weekly`、`monthly`，response 限流单独使用 `requests`、`tokens`，界面标明限流窗口，不冒充周／月套餐。

## 2. 供应商能力总览与资料范围

### 2.1 查询范围

**本项目不支持 Anthropic／Claude、OpenAI／Codex、Grok 的 API quota／余额查询，仅整理这三家的订阅 quota 获取方法。** 订阅窗口、重置 credits、模型响应中的 Token usage 和速率限制均不能替代 API 账户余额；不使用历史费用或管理账务接口补充这些不支持的查询。

DeepSeek、智谱、Kimi 仅按 API 类型整理，不增加订阅查询。其中 DeepSeek 和 Kimi 列出余额获取方法；智谱普通 API 余额接口尚未确认。

| 模型提供商 | 订阅 quota：已整理的直接查询接口 | 从 response 中刷新 quota 信息 | API quota／余额接口整理情况 |
| --- | --- | --- | --- |
| Anthropic／Claude | `GET https://api.anthropic.com/api/oauth/usage` | 从正常模型 response 的 `anthropic-ratelimit-unified-*` Headers 采集并刷新订阅额度快照 | 不支持（本项目范围） |
| OpenAI／Codex | `GET https://chatgpt.com/backend-api/wham/usage`；另尝试读取 `/backend-api/wham/rate-limit-reset-credits` 补充重置额度 | 从 Responses 模型请求返回的 `x-codex-*` Headers 提取窗口用量、重置时间和窗口时长，刷新订阅额度快照 | 不支持（本项目范围）；重置额度不是 API 现金余额 |
| Grok | `GET https://cli-chat-proxy.grok.com/v1/billing?format=credits`（周）；`GET https://cli-chat-proxy.grok.com/v1/billing`（月） | 从正常转发或 Responses 探测的 response 中采集 `x-ratelimit-*-requests`／`x-ratelimit-*-tokens` Headers，刷新 Header 快照；不等同于 billing 周／月套餐额度 | 不支持（本项目范围）；不调用 `management-api.x.ai` 团队余额接口 |
| DeepSeek | 不适用（本项目仅按 API 类型整理） | 不适用 | `GET https://api.deepseek.com/user/balance`，查询账户余额 |
| 智谱／GLM | 不适用（本项目仅按 API 类型整理） | 不适用 | 普通按量 API 余额接口尚未确认，不补写请求地址或响应格式 |
| Kimi／Moonshot | 不适用（本项目仅按 API 类型整理） | 不适用 | `GET https://api.moonshot.cn/v1/users/me/balance`，查询账户余额 |

### 2.2 资料来源与验证边界

Claude、OpenAI／Codex、Grok 的订阅内容摘自 [sub2api 订阅用量获取机制调研报告](sub2api-subscription-usage-research.md)。DeepSeek、Kimi 的 API 响应结构、示例、类型说明及官方资料链接已直接列在本文对应章节，请求认证参考 [查询代码](../internal/aiprovider/quota_query.go)；智谱的 API 余额接口仍标为尚未确认。“本项目不支持”是查询范围约束，“尚未确认”是资料状态，均不用于断言供应商对外不存在相关能力；不补写资料中不存在的接口。

订阅来源报告基于 2026-09-16 对 sub2api 提交 `82f7dd14f717bef480879f73cba288791b9b9663` 的静态阅读，未请求真实账号端点。第三章的接口、Header、客户端版本、单位和分派行为均表示该版本代码的实现，不代表已验证的上游当前契约，也不表示 UniSub 已接入。请求头列表是源码发送的集合，不是已验证的最小必需集合。第四章复用现有 API 资料，不表示本次进行了真实账号验证。

## 3. 订阅 quota 获取方法

本章仅涉及订阅账号。Claude、OpenAI、Grok 的 API quota 查询均不支持，不将以下订阅接口用于 API 余额查询。

**UniSub 接入方式：** `FetchQuota` 已接入 Claude OAuth usage、Codex wham usage 和 Grok 周／月 billing；正常模型 response 的已知额度 Headers 自动更新同一实例缓存。以下 sub2api 的探测、快照持久化及节流说明属于来源项目，不全部照搬：UniSub 不为刷新 quota 额外发送模型请求，也不额外查询 Codex reset credits 端点。查询复用现有 OAuth 凭据和代理组，401 最多恢复令牌后重试一次；不调用三家的 API 余额接口。

Grok 两个 billing 响应在内部按来源区分，再转换为标准订阅窗口：周用量读取 `creditUsagePercent`，月用量计算 `used / monthlyLimit * 100`，重置时间分别读取 `currentPeriod.end`、`billingPeriodEnd`。月限额为零时不构造无意义的百分比，也不伪装成 0%。两个查询全部成功后一次性更新缓存，任一失败返回错误并保留整个旧快照及原更新时间；订阅对外不再暴露原始 `config` 或 `source`。

正常 response 在 HTTP 2xx 或 429 时采集 Headers；仅订阅类型生效，未知、重复或格式错误的值不入缓存，不将 API 限流 Header 当作余额。Claude utilization 比例乘以 100；Codex 已用百分数原值保留，相对重置秒数以响应观测时间计算；Grok 在同一响应提供有效 limit 和 remaining 时计算 `(limit - remaining) / limit * 100`。仅重置时间到达时不生成零用量。主动与被动结果按上游窗口标识合并，部分更新不刷新其他窗口或未更新字段的时效。Codex 的 primary／secondary 比例不是单个窗口的已用量，不单独生成窗口。测试使用模拟响应，不表示已进行真实账号在线验证。

### 3.1 Anthropic／Claude

#### 3.1.1 主动查询

```http
GET https://api.anthropic.com/api/oauth/usage
Authorization: Bearer <subscription_access_token>
anthropic-beta: oauth-2025-04-20
Accept: application/json, text/plain, */*
Content-Type: application/json
User-Agent: claude-code/2.1.7
```

- 使用订阅 OAuth Access Token。`claude-code/2.1.7` 是源码默认 User-Agent，有账号指纹缓存时优先使用缓存值。
- `FetchUsageWithOptions` 接收 Access Token、代理 URL、账号 ID、TLS Profile 和 Fingerprint；有 TLS Profile 时走 `HTTPUpstream.DoWithTLS`，否则使用普通 HTTP 客户端，超时 30 秒。
- 非 HTTP 200 返回错误，成功 Body 解码为 `ClaudeUsageResponse`。
- 上层 `fetchOAuthUsageRaw` 直接读取账号 `access_token`，该函数本身不执行 TokenProvider 刷新，不能表述为查询时必然刷新令牌。
- 源码说明主动查询需要 profile scope，但 `CanGetUsage()` 仅判断 OAuth 类型，没有在该方法中校验 scope。Setup Token 分支使用 session window／被动信息估算，不调用此接口。

#### 3.1.2 返回信息与被动采集

Dummy AIProvider 使用与 Claude 相同的标准化 `subscription` 数组，返回 `5h` 和 `weekly` 两个窗口，每项包含 `time_dimension`、`usage`、`reset_at`。数值与未来重置时间随机生成，仅用于演示；刷新结果写入缓存，不请求上游。

主动响应中投影的窗口如下：

| 字段 | 源码用途 |
| --- | --- |
| `five_hour` | 五小时窗口 |
| `seven_day` | 七天窗口 |
| `seven_day_sonnet` | Sonnet 专属七天窗口 |
| `seven_day_overage_included` | 源码称为 Fable 的专属窗口，缺失时可从被动采样补充 |

窗口中的 `utilization` 为百分数，`resets_at` 为字符串。源码只投影选定窗口；值结构不能证明严格区分缺失、null 和零值。

正常模型响应还采集以下 Headers：

```text
anthropic-ratelimit-unified-5h-utilization
anthropic-ratelimit-unified-7d-utilization
anthropic-ratelimit-unified-7d-reset
anthropic-ratelimit-unified-7d_oi-utilization
anthropic-ratelimit-unified-7d_oi-reset
```

Header utilization 按比例值解释，`0.25` 对应 25%，而 Body 的 `25.0` 已是百分数。被动采样对较大的重置时间戳作毫秒到秒兼容处理，这是 sub2api 的容错逻辑，不是上游契约证明。

sub2api 主动结果缓存 3 分钟、错误负缓存 1 分钟，并使用 `singleflight` 合并同账号并发请求。转换后的 `UsageInfo` 会加入本地窗口统计，不能将其视为上游原始 quota 响应。

来源：研究报告第三章「Claude」。

### 3.2 OpenAI／Codex

#### 3.2.1 专门额度入口：直接查询

`OpenAIQuotaService.QueryUsage` 请求：

```http
GET https://chatgpt.com/backend-api/wham/usage
Authorization: Bearer <subscription_access_token>
chatgpt-account-id: <chatgpt_account_id>
openai-beta: codex-1
originator: Codex Desktop
oai-language: zh-CN
accept: application/json
sec-fetch-site: none
sec-fetch-mode: no-cors
sec-fetch-dest: empty
priority: u=4, i
```

- 普通账号经 `OpenAITokenProvider` 获取有效 Token；账号 ID 优先使用 `chatgpt_account_id`，兼容回退到 `organization_id`。
- 特殊账号可附加 FedRAMP 或走 Agent Identity 认证分支。Spark 影子账号先解析到母账号，复用其认证与代理。
- 客户端来自 `PrivacyClientFactory`，复用指纹模拟机制，超时 20 秒。
- 查询还尝试读取 `/backend-api/wham/rate-limit-reset-credits`，补充重置额度明细；这些数据不是 API 现金余额。此处不涉及消耗重置额度的写操作。

响应投影包括套餐信息、`rate_limit` 和 `additional_rate_limits`。主要窗口字段如下：

| 字段 | 源码类型／语义 |
| --- | --- |
| `allowed`、`limit_reached` | boolean |
| `primary_window`、`secondary_window` | 可空窗口指针 |
| `used_percent` | float64，已用百分数 |
| `limit_window_seconds` | int64，窗口时长，秒 |
| `reset_after_seconds` | int64，距重置的相对秒数 |
| `reset_at` | int64，绝对 Unix 秒 |
| `additional_rate_limits` | 额外限额数组，其中 `rate_limit` 也是指针 |

HTTP 字段为 snake_case，不能与 App Server 的 camelCase 输出混淆。`OpenAIQuotaUsage` 是字段投影，`fetched_at` 为本地添加，不代表完整上游原始 Body；也不能将 primary 固定解释为某个特定时长的窗口。

#### 3.2.2 普通 usage 入口：快照与 Responses 探测

普通 `getOpenAIUsage` 路径先读取账号 `Extra` 中的 `codex_*` 快照。窗口缺失、账号限流、满足过期条件或强制刷新时尝试更新：普通账号走 Responses 探测；Spark 影子账号走直接额度查询，从额外限额提取专属窗口，不使用普通账号全局 Header 覆盖。

```http
POST https://chatgpt.com/backend-api/codex/responses
Authorization: Bearer <subscription_access_token>
Content-Type: application/json
Accept: text/event-stream
OpenAI-Beta: responses=experimental
```

请求还附加 Codex 身份头与账号范围头；探测模型为该仓库常量 `codex-auto-review`，总超时 15 秒。来源报告未提供完整探测 Body，此处不补写。

读取的额度 Headers：

```text
x-codex-primary-used-percent
x-codex-primary-reset-after-seconds
x-codex-primary-window-minutes
x-codex-secondary-used-percent
x-codex-secondary-reset-after-seconds
x-codex-secondary-window-minutes
x-codex-primary-over-secondary-limit-percent
```

该路径使用相对重置时间 `reset-after-seconds`；窗口时长 Header 使用分钟，直接查询 Body 使用秒。Responses 探测是实际模型请求，不能视为与纯额度 GET 等价的无消耗操作。

探测尝试节流为 10 分钟，强制刷新可跳过；按时间过期的判断受 WebSocket V2 配置影响，另有窗口缺失／限流触发条件，不能概括成所有账号每 10 分钟必刷新。探测失败时，这条 sub2api 路径可返回旧快照；该行为属于来源项目，不作为 UniSub `FetchQuota` 的失败处理约定。

来源：研究报告第四章「OpenAI／Codex」。

### 3.3 Grok

#### 3.3.1 CLI billing 直接查询

默认官方请求地址：

```http
GET https://cli-chat-proxy.grok.com/v1/billing?format=credits
GET https://cli-chat-proxy.grok.com/v1/billing
```

前者按周 credits 视图处理，后者按月账务视图处理。`ProbeBilling` 并发查询两个接口，合并成功结果。官方 public／regional API 主机的 billing 请求会转到 CLI 网关；自定义转发地址通过 URL 校验后，使用其 billing 路径，因此并非所有账号都无条件使用上述地址。

两个请求使用的认证与默认 Headers：

```http
Authorization: Bearer <grok_subscription_oauth_access_token>
x-xai-token-auth: xai-grok-cli
x-grok-client-version: 0.2.114
User-Agent: grok-pager/0.2.114 grok-shell/0.2.114 (macos; aarch64)
Accept: application/json
Content-Type: application/json
```

- `0.2.114` 是被调研代码固定的 CLI 标识，不是确认过的最新官方版本；账号 Header 覆盖在默认 Headers 后应用。
- `prepareProbe` 经 `GrokTokenProvider.GetAccessToken` 获取 Token，`loadGrokOAuthAccount` 要求平台为 Grok 且类型为 OAuth。
- 此路径不需要独立 Management API Key 或 `team_id`，不能与 Grok API 团队余额查询混为一谈。
- 请求使用账号代理与 `HTTPUpstream`，上游超时 20 秒；billing 最多尝试两次，重试间隔 100ms，共享探测通过 `singleflight` 合并。

#### 3.3.2 两个响应的数据与单位

两种响应都含顶层 `config`，但表示不同来源，不能直接放入同一无来源名称空间而相互覆盖。

| 来源 | 原始字段 | sub2api 的解释 |
| --- | --- | --- |
| 周 credits | `currentPeriod.type/start/end` | 当前窗口类型及时间字符串 |
| 周 credits | `creditUsagePercent` | 周额度已用百分数 |
| 周 credits | `productUsage[].product/quotaPercent` | 产品标签及产品维度已用百分数 |
| 周 credits | `prepaidBalance` | 美元，不除以 100 |
| 周 credits | `onDemandCap`、`onDemandUsed` | 美元，不除以 100 |
| 月账务 | `monthlyLimit`、`used` | 美元分，展示美元时除以 100 |
| 月账务 | `billingPeriodStart`、`billingPeriodEnd` | 月账务窗口时间字符串 |

上述单位依据代码注释、运算与测试断言，不是独立官方契约验证。`product: "Api"` 只是产品标签，不能据此认定使用 API Key 或团队管理 API。

金额字段以 `json.RawMessage` 接收，`parseCentValue` 兼容 `{"val": ...}`、数字和字符串，对象中的 `val` 也可为数字或字符串；兼容解析不证明上游必然返回所有变体，也不能因共用该函数就将所有金额解释为“分”。

`BuildBillingSummary` 会将 camelCase 原字段转换为 snake_case，并计算百分比、美元显示金额及套餐推断；这些是本地加工，不是可直接当作上游原文保存的响应。

#### 3.3.3 Responses 探测与被动 Headers

| sub2api 路径 | 获取行为 |
| --- | --- |
| `AccountUsageService.getGrokUsage` | 必要时调用 `ProbeBilling`，不主动生成文本，再组装已有 billing／Header 快照及本地统计 |
| `GrokQuotaService.QueryQuota` | 先调用 `ProbeBilling`；billing 未提供代码认可的权威信号时，回退 `ProbeUsage` |
| `ProbeUsage` | 向 Responses 发送实际模型请求，读取额度 Headers |

权威信号判断不仅包含周／月百分比，也包含正的月限额或非空套餐名，不能表述为“没有周百分比就必然探测”。来源报告没有列出 Grok Responses 探测的完整请求 URL，此处不推导或补写。

报告记录的探测 Body 为：

```json
{
  "model": "grok-4.5",
  "input": "hi",
  "stream": true
}
```

其中模型名是该版本默认值。探测主要读取：

```text
x-ratelimit-limit-requests
x-ratelimit-remaining-requests
x-ratelimit-reset-requests
x-ratelimit-limit-tokens
x-ratelimit-remaining-tokens
x-ratelimit-reset-tokens
```

源码还兼容其他 Header 别名、重试时间、套餐与 entitlement 信息；正常转发也能提供被动快照。requests／tokens 限流维度不能无条件等同于 billing 的周／月套餐百分比，也不是 API 现金余额。

#### 3.3.4 sub2api 的快照与失败处理

- 周／月合并结果保存在 `Extra.grok_billing_snapshot`，Header 观测保存在 `Extra.grok_usage_snapshot`。
- 正常 billing 快照按 10 分钟判断过期；缺失、部分失败或失败窗口会触发刷新需求，非强制尝试另有 1 分钟节流。
- 单个窗口失败时合并旧值，记录 `Partial`／`FailedWindows` 及各窗口状态，不将失败窗口变成新零值。
- 模型探测没有可用 Headers 时可记录 `no_headers`／`quota_unknown`，区分未观测与真实零额度。
- 本地 24 小时／7 天／月统计来自调用日志，与上游账务快照分开保存；这些 sub2api 行为仅作来源项目的实现说明，不作为 UniSub 的缓存设计约定。

来源：研究报告第五章「Grok 订阅」及第六章「对 UniSub 的适用结论」。

## 4. API quota／余额获取方法

本章仅整理 DeepSeek、智谱、Kimi 的 API 账户余额，不包含订阅查询。Claude、OpenAI 和 Grok 不在 API quota 支持范围内。

### 4.1 DeepSeek

本供应商在本文中仅整理 API 账户余额，不增加订阅查询。

#### 4.1.1 获取方法

现有查询代码使用 API Key，以 Bearer 方式认证：

```http
GET https://api.deepseek.com/user/balance
Authorization: Bearer <api_key>
Accept: application/json
Content-Type: application/json
```

#### 4.1.2 返回字段

| 字段 | 类型／含义 |
| --- | --- |
| `is_available` | boolean，账户可用状态；`false` 不应直接视为查询失败 |
| `balance_infos` | 余额数组 |
| `balance_infos[].currency` | 币种，`CNY` 或 `USD` |
| `balance_infos[].total_balance` | string，总余额 |
| `balance_infos[].granted_balance` | string，赠送余额 |
| `balance_infos[].topped_up_balance` | string，充值余额 |

金额保留原始字符串与币种，不跨币种求和，不将数值转换后重新序列化作为原文。

响应结构示例（示例金额仅为说明，不是真实账户抓包）：

```json
{
  "is_available": true,
  "balance_infos": [
    {
      "currency": "CNY",
      "total_balance": "12.30",
      "granted_balance": "2.30",
      "topped_up_balance": "10.00"
    }
  ]
}
```

缺失、`null`、零值和空数组必须区分。当前本地校验要求 `is_available` 为 boolean、`balance_infos` 为非空数组，每项含有效币种和三个十进制字符串金额；这描述的是本地接纳条件，不能据此把空数组或缺失字段改造成零余额。

来源：[DeepSeek 官方余额响应定义](https://api-docs.deepseek.com/zh-cn/api/get-user-balance/)、[quota_query.go](../internal/aiprovider/quota_query.go) 和 [quota_schema.go](../internal/aiprovider/quota_schema.go)。本节复用此前整理的字段资料，未进行真实账户查询。

### 4.2 智谱／GLM

本供应商在本文中仅按 API 类型整理，不增加订阅或 Coding Plan 查询。

现有资料尚未确认普通按量 API 的余额查询接口，因此此处不列出未经确认的 URL、HTTP 方法、认证参数或响应字段。Coding Plan 用量与普通按量 API 账户余额属于不同查询范围，不能用前者的接口或响应格式代替后者；也不以 Token 用量、速率限制或已发生费用替代账户余额。

接口整理状态为“尚未确认”，不是已证实供应商没有余额查询能力。

当前 [查询代码](../internal/aiprovider/quota_query.go) 的 API 余额分支仅列出 DeepSeek 和 Kimi，未为智谱定义余额端点；[响应校验代码](../internal/aiprovider/quota_schema.go) 也未定义智谱余额 Schema。这只能说明本项目尚无已确认的查询适配，不能作为供应商不存在接口的证据。本文不补充 Coding Plan 查询方法。

### 4.3 Kimi／Moonshot

本供应商在本文中仅整理 API 账户余额，不增加订阅查询。

#### 4.3.1 获取方法

现有查询代码使用 API Key，以 Bearer 方式认证：

```http
GET https://api.moonshot.cn/v1/users/me/balance
Authorization: Bearer <api_key>
Accept: application/json
Content-Type: application/json
```

#### 4.3.2 返回字段

| 字段 | 类型／含义 |
| --- | --- |
| `code` | integer，字段说明中 `0` 表示成功 |
| `status` | boolean |
| `scode` | string |
| `data.available_balance` | number，可用余额 |
| `data.voucher_balance` | number，代金券余额 |
| `data.cash_balance` | number，现金余额 |

三个余额是 JSON 数字，与 DeepSeek 的字符串金额不同；按原始 JSON 保留精度与写法，不自行添加币种字段。此前整理的官方示例写 `code: 123`，但字段说明明确以 `0` 表示成功，因此不能把示例占位值当作成功码。

响应结构示例（成功码按字段说明填写，示例金额不是真实账户抓包）：

```json
{
  "code": 0,
  "data": {
    "available_balance": 12.30,
    "voucher_balance": 2.30,
    "cash_balance": 10.00
  },
  "scode": "0x0",
  "status": true
}
```

当前本地校验要求 `code: 0`、`status: true`、非空字符串 `scode` 和三个数字余额。缺失、`null` 与零余额不等价；格式错误或查询失败不能转换成零余额，也不能覆盖最后一次有效缓存。

来源：[Kimi 官方余额响应定义](https://platform.kimi.com/docs/api/balance)、[quota_query.go](../internal/aiprovider/quota_query.go) 和 [quota_schema.go](../internal/aiprovider/quota_schema.go)。本节复用此前整理的字段资料，未进行真实账户查询。
