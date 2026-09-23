# Gateway 错误路径审计与 502 调用分析

> 状态：临时分析文档
>
> 起因：排查 request_id `9f0ee15e392fa7fef9548aa609826212` 的 502，延伸为 unisub 出错路径是否都返回有效错误信息的审计。
>
> 数据来源：`data/ai-unisub.db`，采样窗口 2026-09-17 ~ 2026-09-22。

## 一、个案：request `9f0ee15e392fa7fef9548aa609826212`

### 1.1 记录概要

| 字段 | 值 |
| --- | --- |
| id / request_id | 719 / `9f0ee15e392fa7fef9548aa609826212` |
| 账号 | id=3 `codex-c`（supplier=openai、auth_type=api_key） |
| 入站 | `/v1/responses` |
| 出站 | `http://tech-codexmgr07.blackjack-inc.com:48760/v1/responses` |
| 状态 | 502，耗时 44.7s |
| 请求体 | 196502 字节，model `gpt-5.6-luna` |
| `http_error_info` | 空 |

响应体（base64 解码后）：

```json
{"error":{"code":"unknown_error",
  "message":"upstream error: error sending request for url (https://chatgpt.com/backend-api/codex/responses)",
  "type":"server_error"}}
```

响应头：`Server: tiny-http (Rust)`、`X-Codexmanager-Error-Code: unknown_error`、`X-Codexmanager-Trace-Id: trc_1790073482269_1aa8`。

### 1.2 结论

502 由 codex manager 自身返回，它作为 Rust/reqwest 客户端向 `chatgpt.com/backend-api/codex/responses` 发请求时传输层失败。unisub 原样透传，`http_error_info` 为空说明 unisub 侧无本地错误。

**"本地填 openai api key"的配置是正确的。** `oauth_aiprovider.go:173` 在 supplier 非 anthropic 时走 `Authorization: Bearer <token>`，正是 codex manager 期望的格式——它返回 502 而非 401 即为佐证。

### 1.3 账号 3 整体表现

近 5 天 500 条采样：**200 × 466、502 × 22、404 × 12**。配置本身工作正常。

### 1.4 时钟偏差（排除的假象）

每条 502 的"响应 Date → finished_at"恒定约 19 秒，初看像 unisub 在拖时间。但 `X-Codexmanager-Trace-Id` 内嵌的 unix 毫秒时间戳显示，codex manager 主机时钟稳定比本机慢 **18.0–18.8 秒**，且全天、200 与 502 一致。

该 19 秒是时钟偏差，不是延迟；unisub 收到响应后即刻返回。

### 1.5 502 的真实规律

按 codex manager 自身时钟校正后的上游耗时（秒）：

```
29.5 30.0 30.1 30.1 30.1 30.4 30.4 30.8 30.8 31.1 32.4
33.3 34.1 34.5 35.5 36.7 37.6 41.5 41.9 43.0 43.7 56.8
```

密集堆在 **30 秒**，典型固定超时特征。作为对照，成功请求的首字节时间通常仅 0.5–3 秒。

请求体大小与失败的关系是"暴露度"而非硬上限：

| 请求体大小 | 200 | 502 | 502 率 |
| --- | ---: | ---: | ---: |
| <50KB | 55 | 0 | 0.0% |
| 50-100KB | 59 | 0 | 0.0% |
| 100-150KB | 68 | 0 | 0.0% |
| 150-200KB | 94 | 3 | 3.1% |
| 200-300KB | 131 | 7 | 5.1% |
| 300-500KB | 208 | 7 | 3.3% |
| >500KB | 163 | 5 | 3.0% |

最大成功请求 1057847 字节，最小失败请求 152015 字节。150KB 以下 0/182 失败，以上约 3–5%。即：非体积卡死，而是大 body 上传耗时长、更易撞上 ~30s 超时窗口，本质是 codex manager 到 chatgpt.com 的网络不稳。

进一步定位需用 `trc_1790073482269_1aa8` 查 codex manager 侧日志。

### 1.6 顺带发现

**账号 3 的 `max_concurrent_connections: 1`。** 一个请求卡满 30 秒会阻塞后续全部请求，这解释了部分 502 端到端耗时达 42–57 秒。若 codex manager 能承受，建议调高。

**12 条 404 属另一问题**（与本次无关）：

```
model_not_found: gpt-5-codex     (6 条, 08:51)
model_not_found: deepseek-flash  (6 条, 06:26)
```

模型映射把 codex manager 不认识的模型名发了过去。每组均为 1 次真实请求 + 5 次 0.0s 的客户端重试。

**此类 502 unisub 不会自动重试。** `gateway.go:183` 的重试仅对 group 账号生效，且 `oauth_aiprovider.go:206` 对非 GET 方法只在 dial 错误时才判定可重试。对 POST 而言该保守策略是正确的，但意味着上游抖动会直接暴露给客户端。

## 二、错误路径审计

### 2.1 总体结论

**客户端方向有兜底**：每条路径最终都会收到一个 HTTP 响应。所有 provider 的 `Handle`（codex/claude/grok/api）汇聚到 `oauthAIProvider.handle`，group 的 `Handle` 也显式返回 503 trace（`group.go:62-64`），路径单一；`gateway.go:241-243` 的 `!recorded && !output.written` → 502 是最终 catch-all。

**问题在错误信息质量与调用记录完整性。**

### 2.2 网络层错误的真实原因被三重丢弃（最严重）

`oauth_aiprovider.go:225-226` 已取得真实错误 `trace.HTTPErrorInfo = err.Error()`（如 `dial tcp: i/o timeout`、`context deadline exceeded`），随后：

| 去向 | 结果 |
| --- | --- |
| `gateway.go:143` | 客户端收到固定的 `"upstream request failed"` |
| `gateway.go:172-173` | 数据库存的**也是**该固定字符串 |
| 日志 | 无——`gateway.go:175-177` 仅在 DB 写失败时打 |

真实错误字符串彻底消失，无处还原。这正是 1.1 中 `http_error_info` 无法提供线索的原因。`admin_call_record.go:53-54` 为同一模式。

### 2.3 流式响应中断后，客户端与日志均显示"成功"（最隐蔽）

gateway 总是注入 responseWriter（`gateway.go:85`），故 `handle` 永远走 `oauth_aiprovider.go:241` 的流式分支。该分支在读 body **之前**即 `w.WriteHeader(response.StatusCode)`，头一发出 `output.written` 即为 true。

于是 `io.Copy`（`oauth_aiprovider.go:250`）中途失败时：

- `trace.HTTPErrorInfo` 被设置，但 recorder 中 `!output.written` 不成立 → **客户端收不到任何错误**，只拿到被截断的 SSE 流
- `trace.ResponseStatus` 仍为 200 → `persistedCallHTTPCode` 返回 200 → **调用日志记为 200 成功**

HTTP 层面无法修改已发送的状态码（协议限制），但 SSE 可补发 `event: error`。日志记成 200 则是纯 bug——断流与正常完成在日志中无法区分。

### 2.4 一整类失败不产生调用记录

`Account.Handle` 在下列情况直接 return error，recorder 从不被调用：

- `account.go:121` acquire 失败 → `ErrQueueFull` / `ErrQueueTimeout` / `ErrUnavailable` / ctx 错误
- `account.go:125` → `ErrClientDenied`

客户端能收到正确的 429/504/503/403（`gateway.go:222-239` 分类正确），但数据库无记录。鉴于账号 3 为 `max_concurrent_connections: 1`，排队超时很可能发生，却在调用日志中完全不可见。

同理 `gateway.go:59-66`（Select 失败）、`72-83`（endpoint 无效）、`91-98`（body 读取失败）均在 recorder 建立之前，亦不留记录。

### 2.5 部分读取被当作完整响应

`oauth_aiprovider.go:256` 的 `io.ReadAll` 若读到一半失败，ResponseBody 残缺、HTTPErrorInfo 已设置，但 `gateway.go:142` 的条件 `len(trace.ResponseBody) == 0` 不成立 → 将残缺 body 配上原状态码发给客户端。

该路径在 gateway 下不会触发（总有 responseWriter），**仅影响 admin 的 provider 调用**。

### 2.6 错误分类错位

`gateway.go:215-218` 重试循环内 `upstreamURL` 失败 → break → 落入 222 分支输出 503 `provider unavailable`；而同样的失败在 `gateway.go:81` 为 502 `invalid upstream endpoint`。有错误信息，但归类不一致。

### 2.7 窄丢失窗口

`gateway.go:191`，若 `canSwitch` 为真、`retryTrace != nil` 且 `requestContext.Err() != nil` 同时成立 → break 时 err 为 nil → 落入 241 输出 502 `upstream returned no response`，retryTrace 携带的真实错误被丢弃且不落库。

需 ctx 恰在此刻超时才会触发，实际概率很低，但逻辑上确为缺口。

## 三、建议修复顺序

| 优先级 | 项 | 位置 | 处置 |
| --- | --- | --- | --- |
| 高 | 真实错误原文丢失 | `gateway.go:172-173` | 落库存 `trace.HTTPErrorInfo` 原文；客户端可仍返回泛化文案，避免泄露内部细节 |
| 高 | 断流记为 200 | `oauth_aiprovider.go:241-255` | 中断后将 `ResponseStatus` 改记为 0 或 599，使日志可区分；并考虑补发 SSE `event: error` |
| 中 | 队列/拒绝类失败无记录 | `account.go:118-128` | 让这几类失败也产生 trace 并落库 |
| 低 | 错误分类错位 | `gateway.go:215-218` | 与 `gateway.go:81` 对齐为 502 |
| 低 | 窄丢失窗口 | `gateway.go:191` | break 前若 retryTrace 非空则先 recorder |
| 低 | 部分读取当完整响应 | `oauth_aiprovider.go:256` | 读取出错时不复用原状态码 |

## 四、待办（超出本次范围）

- 账号 3 的 `max_concurrent_connections` 是否上调
- 模型映射产生的 `gpt-5-codex` / `deepseek-flash` 404
