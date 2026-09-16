# sub2api 订阅用量获取机制调研报告

## 一、调研范围与证据边界

| 项目 | 内容 |
| --- | --- |
| 调研日期 | 2026-09-16 |
| 调研对象 | 本地 `D:\Projects\sub2api` 的 Claude、OpenAI／Codex、Grok 订阅 usage 实现 |
| 源码基线 | `82f7dd14f717bef480879f73cba288791b9b9663`；核对时已跟踪文件无工作区差异 |
| 方法 | 静态阅读请求构造、调用分派、响应类型、转换逻辑、缓存与测试夹具 |
| 未执行 | 未请求真实账号端点、未读取账号秘密、未运行 sub2api 测试、未修改 sub2api |
| 文档性质 | 独立源码调研，不是上游官方接口契约，也不是 UniSub 已实现能力说明 |

本文的“确认”表示确认该版本源码的行为，不表示上游当前仍接受这些接口、Header 或固定客户端版本。JSON 示例为类型结构说明或仓库测试夹具的简化，不是本次真实账户抓包。

源码链接指向本机文件，行号对应上述基线。阅读副本时，可按文末文件路径和函数名定位。UniSub 当前实现另见 [用量／余额设计](ai-provider-quota.md) 和 [原始响应格式](ai-provider-quota-raw.md)。

## 二、结论概览

| 提供商 | 主动获取路径 | 补充来源 | 主要认证 | 原始数据特征 |
| --- | --- | --- | --- | --- |
| Claude | `GET api.anthropic.com/api/oauth/usage` | 正常响应中的 `anthropic-ratelimit-unified-*` Header | 订阅 OAuth Access Token | Body 百分数与字符串时间；Header 比例值与时间戳 |
| OpenAI／Codex | 专门额度入口调用 `GET chatgpt.com/backend-api/wham/usage` | 普通 usage 路径可发送 Responses 探测并采集 `x-codex-*` Header | OAuth Token + ChatGPT Account ID；另有 Agent Identity 分支 | HTTP Body 为 snake_case；窗口时长为秒，Header 窗口时长为分钟 |
| Grok | CLI 网关 `/v1/billing?format=credits` 与 `/v1/billing` | 专门 quota 查询可回退到 Responses 探测；正常请求也采集额度 Header | Grok 订阅 OAuth Token + CLI 身份 Header | 顶层 `config`；周百分比和月金额分别读取，再加工合并 |

**核心发现：Grok 订阅用量不需要调用 `management-api.x.ai` 团队余额接口，也不需要独立 Management API Key／team_id。** sub2api 使用的是 Grok CLI 网关的订阅 OAuth billing 路径。这与 UniSub 已明确不支持的 Grok API 用量／余额查询是不同业务范围。

三家均需区分：

- 上游套餐额度：百分比、窗口、重置时间等。
- 单次推理消耗：模型响应 Body／SSE 中的 Token usage。
- 本地统计：由 sub2api 调用日志聚合的请求数、Token 和成本。

后两者不能冒充第一种数据。sub2api 的 `UsageInfo` 会同时承载额度和本地 `WindowStats`，这不是上游原始响应结构。

## 三、Claude

### 3.1 主动查询与认证

请求形式：

```http
GET https://api.anthropic.com/api/oauth/usage
Authorization: Bearer <subscription_access_token>
anthropic-beta: oauth-2025-04-20
Accept: application/json, text/plain, */*
Content-Type: application/json
User-Agent: claude-code/2.1.7
```

`claude-code/2.1.7` 是源码默认值；有账号指纹缓存时优先使用其 User-Agent。上述列表是该实现实际发送的 Header，不代表已验证的最小必需集合。

`FetchUsageWithOptions` 接收 Access Token、代理 URL、账号 ID、TLS Profile 和 Fingerprint。有 TLS Profile 时使用 `HTTPUpstream.DoWithTLS`；否则使用普通 HTTP 客户端，超时 30 秒。非 HTTP 200 返回错误，成功 Body 解码为 `ClaudeUsageResponse`。

来源：[claude_usage_service.go:45](D:/Projects/sub2api/backend/internal/repository/claude_usage_service.go:45)。

上层 `fetchOAuthUsageRaw` 直接读取账号的 `access_token` 并构造这些选项；该函数本身没有执行 TokenProvider 刷新，不应将它描述为查询时必然刷新令牌。来源：[account_usage_service.go:1560](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:1560)。

### 3.2 响应结构与单位

源码读取以下顶层窗口：

| 字段 | 源码用途 |
| --- | --- |
| `five_hour` | 五小时窗口 |
| `seven_day` | 七天窗口 |
| `seven_day_sonnet` | Sonnet 专属七天窗口 |
| `seven_day_overage_included` | 源码称为 Fable 的专属窗口；缺失时可从被动采样补充 |

单个窗口的结构示例：

```json
{
  "utilization": 25.0,
  "resets_at": "2026-09-16T12:00:00Z"
}
```

`utilization` 解码为 `float64`，语义为百分数；`resets_at` 解码为字符串。源码只投影选定窗口，不保留所有未知字段。这里的窗口使用值结构而不是指针，不能据此证明其严格区分缺失、null 和零值。

来源：[ClaudeUsageResponse 定义](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:249)。

### 3.3 缓存、被动采样与账号类型

主动查询成功缓存 3 分钟，错误负缓存 1 分钟；使用 `singleflight` 合并同账号并发请求。未命中时增加 0～800ms 随机延迟，分散多账号同时请求。局部注释仍写“缓存 10 分钟”，实际以常量和执行分支的 **3 分钟**为准。

主动结果转换为 `UsageInfo`，添加本地窗口统计，并同步到账号 `Extra`。本地统计另有 1 分钟缓存，不能当作上游额度。

正常请求响应中还采集：

```text
anthropic-ratelimit-unified-5h-utilization
anthropic-ratelimit-unified-7d-utilization
anthropic-ratelimit-unified-7d-reset
anthropic-ratelimit-unified-7d_oi-utilization
anthropic-ratelimit-unified-7d_oi-reset
```

Header utilization 按比例值处理，`0.25` 对应 25%；Body 的 `25.0` 已是百分数，两者不能直接套用同一个单位规则。该被动采样函数还对较大的重置时间戳作毫秒到秒兼容处理，这是 sub2api 的容错逻辑，不是上游契约证明。

来源：[查询与缓存分支](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:348)、[缓存常量](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:107)、[被动采样](D:/Projects/sub2api/backend/internal/service/ratelimit_service.go:1824)。

源码说明主动查询需要 profile scope，但 `CanGetUsage()` 实际仅判断账号是否为 OAuth，没有在该方法中校验 scope。Setup Token 分支使用 session window／被动信息估算，不调用该 usage 接口；不能把它当作已取得完整官方套餐用量。来源：[account.go:327](D:/Projects/sub2api/backend/internal/service/account.go:327)。

## 四、OpenAI／Codex

### 4.1 路径 A：专门额度接口直接查询

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

特殊账号可附加 FedRAMP 或使用 Agent Identity 认证分支。普通账号经 `OpenAITokenProvider` 获取有效 Token；账号 ID 优先 `chatgpt_account_id`，兼容回退到 `organization_id`。Spark 影子账号先解析到母账号，复用其认证与代理。

客户端来自 `PrivacyClientFactory`，复用指纹模拟机制，超时 20 秒。查询还会尝试读取 `/backend-api/wham/rate-limit-reset-credits`，补充重置额度明细；该额外数据不是 API 现金余额。本文不涉及消耗重置额度的写操作。

来源：[QueryUsage](D:/Projects/sub2api/backend/internal/service/openai_quota_service.go:145)、[账号与令牌解析](D:/Projects/sub2api/backend/internal/service/openai_quota_service.go:357)、[请求头](D:/Projects/sub2api/backend/internal/service/openai_quota_service.go:516)。

### 4.2 直接 HTTP 响应投影

以下是按源码类型构造的示意，不是实测响应：

```json
{
  "plan_type": "plus",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 25,
      "limit_window_seconds": 18000,
      "reset_after_seconds": 3600,
      "reset_at": 1789560000
    },
    "secondary_window": null
  },
  "additional_rate_limits": [
    {
      "limit_name": "example-feature",
      "metered_feature": "example-feature",
      "rate_limit": null
    }
  ]
}
```

| 字段 | 源码类型／语义 |
| --- | --- |
| `allowed`、`limit_reached` | boolean |
| `primary_window`、`secondary_window` | 可空窗口指针 |
| `used_percent` | float64，已用百分数 |
| `limit_window_seconds` | int64，窗口时长，秒 |
| `reset_after_seconds` | int64，距重置的相对秒数 |
| `reset_at` | int64，绝对 Unix 秒 |
| `additional_rate_limits` | 额外限额数组，其中 `rate_limit` 也是指针 |

`OpenAIQuotaUsage` 还投影用户／账号／套餐信息及重置额度；`fetched_at` 由本地查询完成后添加。不能把该 DTO 当作完整上游原始 Body。其 snake_case 字段也不应与 App Server 的 camelCase 输出混淆。

来源：[原始类型投影](D:/Projects/sub2api/backend/internal/service/openai_quota_service.go:40)。

### 4.3 路径 B：普通 usage 读取与 Responses 探测

普通账号的 `getOpenAIUsage` 并不是每次调用 `/wham/usage`：

1. 先从账号 `Extra` 读取 `codex_*` 快照。
2. 窗口缺失、账号限流、满足过期条件或强制刷新时，尝试刷新。
3. 普通账号走 `probeOpenAICodexSnapshot`，向 Responses 端点发送模型请求，读取返回 Header。
4. Spark 影子账号走路径 A，从额外限额中提取专属窗口，不用普通账号的全局 Header 覆盖。

Responses 探测：

```http
POST https://chatgpt.com/backend-api/codex/responses
Authorization: Bearer <subscription_access_token>
Content-Type: application/json
Accept: text/event-stream
OpenAI-Beta: responses=experimental
```

还附加 Codex 身份头与账号范围头；探测模型为仓库常量 `codex-auto-review`，总超时 15 秒。探测读取以下 Header：

```text
x-codex-primary-used-percent
x-codex-primary-reset-after-seconds
x-codex-primary-window-minutes
x-codex-secondary-used-percent
x-codex-secondary-reset-after-seconds
x-codex-secondary-window-minutes
x-codex-primary-over-secondary-limit-percent
```

**重要差异：此实现采集 `reset-after-seconds`，不能只考虑绝对时间形式的 `reset-at`。** 窗口时长 Header 使用分钟，Body 使用秒。Responses 探测属于模型请求，不是纯额度 GET，不应默认将两者视为等价、无消耗操作。

探测尝试节流为 10 分钟，强制刷新可以跳过。普通账号的“按时间过期”判断受 WebSocket V2 配置影响；窗口缺失／限流等另有触发条件，不能简单概括成所有账号每 10 分钟必刷新。探测失败时该路径可继续返回旧快照。

来源：[分派与过期条件](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:711)、[Responses 探测](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:828)、[Header 解析](D:/Projects/sub2api/backend/internal/service/openai_gateway_usage.go:897)。

## 五、Grok 订阅

### 5.1 两个 CLI billing 接口

默认请求地址：

```http
GET https://cli-chat-proxy.grok.com/v1/billing?format=credits
GET https://cli-chat-proxy.grok.com/v1/billing
```

前者按周 credits 视图处理，后者按月账务视图处理。`ProbeBilling` 并发请求两个接口，随后合并成功结果；不是调用团队管理 API。

若账号使用官方 public／regional API 主机，billing 构造会转到 CLI 网关；若配置自定义转发地址，则在 URL 校验通过后使用该自定义地址的 billing 路径。因此以上是默认官方地址，不是所有账号无条件使用的地址。

来源：[billing 路径](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:27)、[并行查询](D:/Projects/sub2api/backend/internal/service/grok_quota_service.go:269)、[主机选择](D:/Projects/sub2api/backend/internal/service/grok_upstream_url.go:120)。

### 5.2 认证与请求头

```http
Authorization: Bearer <grok_subscription_oauth_access_token>
x-xai-token-auth: xai-grok-cli
x-grok-client-version: 0.2.114
User-Agent: grok-pager/0.2.114 grok-shell/0.2.114 (macos; aarch64)
Accept: application/json
Content-Type: application/json
```

`0.2.114` 是被调研版本固定的 CLI 标识，不是本报告确认的最新官方版本。账号级 Header 覆盖会在默认 Header 后应用。

`prepareProbe` 使用 `GrokTokenProvider.GetAccessToken`；`loadGrokOAuthAccount` 要求平台为 Grok 且类型为 OAuth。独立 Management API Key 和 team_id 不参与此路径。

请求使用账号代理与 `HTTPUpstream`，上游超时为 20 秒，billing 最多尝试两次，重试间隔 100ms；共享探测通过 singleflight 合并。

来源：[CLI 身份头](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:139)、[请求执行](D:/Projects/sub2api/backend/internal/service/grok_quota_service.go:376)、[认证与账号限制](D:/Projects/sub2api/backend/internal/service/grok_quota_service.go:496)。

### 5.3 原始响应：周额度

以下为仓库测试夹具的简化：

```json
{
  "config": {
    "currentPeriod": {
      "type": "WEEKLY",
      "start": "2026-07-09T03:25:00Z",
      "end": "2026-07-16T03:25:00Z"
    },
    "creditUsagePercent": 2.0,
    "productUsage": [
      { "product": "Api", "quotaPercent": 2.0 }
    ],
    "prepaidBalance": { "val": 12 },
    "onDemandCap": { "val": 100 },
    "onDemandUsed": { "val": 5 },
    "isUnifiedBillingUser": true
  }
}
```

`product: "Api"` 是响应中的产品标签，不能据此把认证方式误判成 API Key 或团队管理 API。

### 5.4 原始响应：月账务窗口

以下同样来自测试结构：

```json
{
  "config": {
    "monthlyLimit": { "val": 15000 },
    "used": { "val": 78 },
    "billingPeriodStart": "2026-07-01T00:00:00Z",
    "billingPeriodEnd": "2026-08-01T00:00:00Z"
  }
}
```

源码还定义了 `topUpMethod` 等可选字段。金额采用 `json.RawMessage` 接收，再由 `parseCentValue` 兼容对象 `{"val": ...}`、直接数字和直接字符串；对象中的 `val` 也可为字符串或数字。该容错解析能力不证明上游必然返回所有这些变体。

来源：[原始 BillingConfig](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:47)、[周／月测试夹具](D:/Projects/sub2api/backend/internal/pkg/xai/billing_test.go:48)、[金额兼容解析](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:399)。

### 5.5 单位与展示转换

| 原始字段 | sub2api 中的解释／转换 |
| --- | --- |
| `creditUsagePercent` | 周额度已用百分数 |
| `productUsage[].quotaPercent` | 产品维度已用百分数 |
| `monthlyLimit`、`used` | 美元分；显示美元时除以 100 |
| `prepaidBalance` | 美元，不除以 100 |
| `onDemandCap`、`onDemandUsed` | 美元，不除以 100 |
| `currentPeriod.start/end` | 当前窗口时间字符串 |
| `billingPeriodStart/End` | 月账务窗口时间字符串 |

单位依据是这份代码的注释、运算与测试断言，不是独立官方契约验证。不能因为这些金额共用 `parseCentValue` 函数就全部解释成“分”。

`BuildBillingSummary` 将原始 camelCase 字段转换成 snake_case 展示字段，还计算月使用百分比、美元显示金额及套餐推断。月包含额度使用值可先取 `min(used, monthlyLimit)`，因此某些展示百分比可能被限制在套餐额度内。这些均属于本地转换，不能当作上游原样数据。

来源：[BuildBillingSummary](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:167)。

### 5.6 普通 usage 与专门 quota 的不同语义

| 路径 | 行为 |
| --- | --- |
| `AccountUsageService.getGrokUsage` | 必要时调用 `ProbeBilling`，不主动生成文本，然后组装已有 billing／Header 快照及本地统计 |
| `GrokQuotaService.QueryQuota` | 先调用 `ProbeBilling`；当 billing 未提供代码认可的权威信号时，回退 `ProbeUsage` |
| `ProbeUsage` | 向 Responses 发送实际模型请求，读取额度 Header |

`grokBillingHasAuthoritativeQuota` 的判断不仅包含周／月百分比，还包含正的月限额或非空套餐名；所以不能简单说“没有周百分比就必然探测”。

探测 Body 使用该版本默认模型，形如：

```json
{
  "model": "grok-4.5",
  "input": "hi",
  "stream": true
}
```

主要读取：

```text
x-ratelimit-limit-requests
x-ratelimit-remaining-requests
x-ratelimit-reset-requests
x-ratelimit-limit-tokens
x-ratelimit-remaining-tokens
x-ratelimit-reset-tokens
```

还兼容其他 Header 别名、重试时间、套餐与 entitlement 信息。正常转发也能提供被动快照。这些限流维度不能无条件等同于 billing 的周／月套餐百分比。

来源：[QueryQuota](D:/Projects/sub2api/backend/internal/service/grok_quota_service.go:93)、[普通 usage 路径](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:1097)、[探测 Body](D:/Projects/sub2api/backend/internal/service/grok_quota_service.go:557)、[Header 定义](D:/Projects/sub2api/backend/internal/pkg/xai/quota.go:67)。

### 5.7 持久化与失败处理

- 周／月合并结果保存在账号 `Extra.grok_billing_snapshot`。
- Header 观测结果保存在 `Extra.grok_usage_snapshot`。
- 正常 billing 快照按 10 分钟判断过期；缺失、部分失败或失败窗口会触发刷新需求，非强制尝试另有 1 分钟节流。
- 单个窗口失败时合并旧值，并标记 `Partial`／`FailedWindows` 及各窗口状态；不将失败窗口伪装成新的零用量。
- Responses 探测无可用 Header 时可记录 `no_headers`／`quota_unknown`，区分未观测和真实零额度。
- 本地 24 小时／7 天／月统计来自调用日志，与上游账务快照分别保存。

来源：[合并逻辑](D:/Projects/sub2api/backend/internal/pkg/xai/billing.go:278)、[快照组装](D:/Projects/sub2api/backend/internal/service/grok_quota_fetcher.go:23)、[过期与节流](D:/Projects/sub2api/backend/internal/service/account_usage_service.go:1224)。

## 六、对 UniSub 的适用结论

本节是依据源码作出的实现建议，不表示已完成接入，也不改变当前业务范围。

| 主题 | 可借鉴结论 | 不宜直接照搬的部分 |
| --- | --- | --- |
| Claude | OAuth usage 地址、Body 百分数、额外模型窗口、被动 Header 与主动查询并存 | 值结构对缺失／null 的区分不足；固定旧客户端版本；估算结果不能当真实 usage |
| Codex | snake_case HTTP 字段、可空窗口、额外限额；补充关注 `reset-after-seconds` Header | 不自动把模型探测当纯只读查询；不硬编码 primary 等于某个固定窗口；DTO 不代表完整原文 |
| Grok 订阅 | OAuth + CLI billing 两个接口是一条独立可参考路径 | 不复活 Grok API 管理余额查询；不新增该路径不需要的 Management API Key／team_id |
| 原始数据 | 分别保存 Body 与 Header 来源；周／月请求分别保留 | 不先转 float64 再序列化充当原文，不把本地 `BillingSummary` 当上游格式 |
| 缓存 | 单账号请求合并、失败节流、部分更新、旧数据保留 | 不让局部成功刷新所有旧窗口的时间，也不混淆本地读取时间与上游观测时间 |

Grok 两个响应都含顶层 `config`。若未来接入，应保留两个响应的来源边界，不能将两个 `config` 直接写入同一个无来源名称空间而相互覆盖。为原始数据增加来源封装时，也必须明确这些封装字段由 UniSub 添加，不是供应商原字段。

对于验证，源码与模拟测试能确认“代码如何发送、如何解释”，不能确认真实账号的套餐覆盖、字段可选性、接口当前可用性及最小必需 Header。报告中的客户端版本、探测模型、金额单位均应按这一边界使用。

## 七、核心源码索引

路径均相对于 `D:\Projects\sub2api`。

| 文件 | 关键内容 |
| --- | --- |
| `backend/internal/repository/claude_usage_service.go` | Claude usage 请求、Header、代理／TLS、HTTP 解码 |
| `backend/internal/service/account_usage_service.go` | 三家 usage 分派、缓存、主动／被动融合、本地统计 |
| `backend/internal/service/ratelimit_service.go` | Claude 响应 Header 被动采样 |
| `backend/internal/service/openai_quota_service.go` | Codex 直接 HTTP 查询、认证、响应类型与重置额度读取 |
| `backend/internal/service/openai_gateway_usage.go` | Codex Header 解析及快照处理 |
| `backend/internal/handler/admin/openai_oauth_handler.go` | 专门额度 GET 与持久化刷新入口 |
| `backend/internal/service/grok_quota_service.go` | Grok billing 并行查询、探测回退、OAuth 类型限制 |
| `backend/internal/service/grok_quota_fetcher.go` | Grok 快照读取与展示组装 |
| `backend/internal/service/grok_upstream_url.go` | Grok billing／Responses 地址解析与校验 |
| `backend/internal/pkg/xai/billing.go` | Grok 原始结构、CLI Header、金额与周／月合并逻辑 |
| `backend/internal/pkg/xai/billing_test.go` | 周／月响应夹具及单位断言，不是实测证明 |
| `backend/internal/pkg/xai/quota.go` | Grok requests／tokens Header 解析 |
| `backend/internal/pkg/xai/oauth.go` | CLI 网关默认地址 |
| `backend/internal/server/routes/admin.go` | accounts usage、OpenAI quota、Grok quota 路由注册 |
