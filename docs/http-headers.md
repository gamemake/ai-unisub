# Grok / Claude / Codex HTTP 头修改规则

本文整理 `ai-unisub` **当前实现**中，转发链路对 HTTP 头的剥离、注入与回传规则。实现以 [`internal/server/proxy.go`](../internal/server/proxy.go)、[`internal/server/claude_identity.go`](../internal/server/claude_identity.go) 为准；用量探测见各 Provider 的 `*_usage.go`。

业务 payload（Messages / Responses）**不做改写**；本文只覆盖请求/响应头与相关查询参数。

---

## 1. 处理流水线

转发上游请求时，顺序固定为：

1. **复制下游请求头**（黑名单剥离）→ `copyDownstreamHeaders`
2. **按 Provider 注入上游身份与鉴权头** → `injectProviderHeaders`
3. 收到上游响应后：**复制上游响应头回下游**（hop-by-hop 剥离）→ `copyUpstreamHeaders`
4. SSE 时额外设置 `X-Accel-Buffering: no`

```text
客户端 ──► UniSub
            │  1. strippedRequestHeaders 剥离敏感/冲突头
            │  2. injectProviderHeaders 注入鉴权与身份
            ▼
         上游 Provider
            │  3. copyUpstreamHeaders（去掉 hop-by-hop）
            ▼
         客户端
```

Claude Messages / Count Tokens 还会在上游 URL 上确保查询参数 `beta=true`（与头策略配套，见 §3.1）。

---

## 2. 下游鉴权头（进入网关时）

网关从客户端头解析 `unisub_*` API Key，优先级：

| 优先级 | 头 | 取值 |
| --- | --- | --- |
| 1 | `Authorization` | `Bearer <key>` |
| 2 | `x-api-key` | 明文 Key |
| 3 | `x-goog-api-key` | 明文 Key |

三者等价；解析失败返回 `401 invalid_api_key`。这些头随后会在出站前被剥离，不会原样带到上游。

---

## 3. 出站前剥离（黑名单）

`strippedRequestHeaders`：客户端传入且匹配下列名称的头**一律丢弃**，再由网关注入正确值。

| 类别 | 头名（大小写不敏感） | 原因 |
| --- | --- | --- |
| 鉴权 | `authorization`, `proxy-authorization`, `x-api-key`, `x-goog-api-key`, `cookie` | 避免下游 Key / 代理凭据泄漏到上游 |
| Codex 身份 | `chatgpt-account-id` | 必须用账号凭据中的值覆盖 |
| 连接/传输 | `host`, `content-length`, `connection`, `proxy-connection`, `keep-alive`, `transfer-encoding`, `upgrade` | hop-by-hop / 由 Go HTTP 客户端自行管理 |
| 压缩 | `accept-encoding` | 出站禁用压缩；透传会导致 gzip 体破坏本地 usage 解析 |
| Grok 客户端 | `x-xai-token-auth`, `x-authenticateresponse`, `x-grok-client-version`, `x-grok-client-identifier`, `x-grok-client-mode` | 由网关按上游主机与配置重写 |

**未列入黑名单的头会透传**（黑名单策略，非白名单）。常见透传例子：客户端自带的 `User-Agent`、`anthropic-beta`、`X-Stainless-*`、自定义追踪头等——是否最终保留取决于下一步 Provider 注入是否覆盖。

---

## 4. 公共注入

所有 Provider 出站请求都会设置：

| 头 | 值 |
| --- | --- |
| `Content-Type` | `application/json` |

---

## 5. Claude

源码：`injectProviderHeaders` + `applyClaudeOutboundHeaders`。

### 5.1 鉴权

| 账号 `auth_type` | 注入头 |
| --- | --- |
| `api_key` | `x-api-key: <upstream token>` |
| `oauth`（及其他） | `Authorization: Bearer <access_token>` |

### 5.2 通用补齐

| 头 | 规则 |
| --- | --- |
| `anthropic-version` | 客户端未带时设为 `2023-06-01` |
| `Accept` | 强制 `application/json` |
| `x-app` | 客户端未带 `x-app` / `X-App` 时设为 `cli` |

### 5.3 身份策略（UA + beta）

Anthropic 用 `User-Agent` + `anthropic-beta`（及部分 CLI 头）区分 Claude Code 与第三方。网关规则：

| 客户端 `User-Agent` | 行为 |
| --- | --- |
| 匹配 `^claude-cli/\d+\.\d+\.\d+`（大小写不敏感） | **视为真 Claude Code**：保留客户端 UA；保留客户端已有的 `anthropic-beta` / Stainless 等头；仅在缺少 `anthropic-beta` 时按 auth 补默认集合 |
| 其他（curl、SDK、空 UA 等） | **mimic 固定 CLI 身份**（见下） |

当前 pin：

- `User-Agent`: `claude-cli/2.1.220 (external, cli)`

#### 真 Claude Code（透传路径）

| `auth_type` | 缺少 `anthropic-beta` 时补齐 |
| --- | --- |
| `api_key` | `claude-code-20250219,interleaved-thinking-2025-05-14`；Count Tokens 再追加 `token-counting-2024-11-01` |
| `oauth` | 完整 OAuth mimic 集合（见下） |

#### 非 Claude Code（mimic 路径）

| `auth_type` | 行为 |
| --- | --- |
| `api_key` | 覆盖 UA 为 pin；仅在缺少 `anthropic-beta` 时补 API Key 默认集合 |
| `oauth` | **强制**覆盖 UA 与 `anthropic-beta` 为 mimic 集合，并写入 Stainless / 浏览器直连头 |

OAuth mimic `anthropic-beta` 集合（逗号连接）：

```text
claude-code-20250219
oauth-2025-04-20
interleaved-thinking-2025-05-14
prompt-caching-scope-2026-01-05
effort-2025-11-24
context-management-2025-06-27
extended-cache-ttl-2025-04-11
[+ token-counting-2024-11-01 仅 count_tokens]
```

OAuth mimic 额外强制头：

| 头 | 值 |
| --- | --- |
| `X-Stainless-Lang` | `js` |
| `X-Stainless-Package-Version` | `0.94.0` |
| `X-Stainless-OS` | `Linux` |
| `X-Stainless-Arch` | `arm64` |
| `X-Stainless-Runtime` | `node` |
| `X-Stainless-Runtime-Version` | `v24.3.0` |
| `X-Stainless-Retry-Count` | `0` |
| `X-Stainless-Timeout` | `600` |
| `Anthropic-Dangerous-Direct-Browser-Access` | `true` |

### 5.4 URL 查询参数

对 `messages` / `count_tokens`：若上游 URL 尚无 `beta`，追加 `beta=true`。客户端原有 query 仍会原样合并。

### 5.5 主动用量探测（非转发）

`GET` Claude usage URL 时固定头（不走黑名单透传）：

| 头 | 值 |
| --- | --- |
| `Authorization` | `Bearer <token>` |
| `Accept` | `application/json` |
| `anthropic-version` | `2023-06-01` |
| `anthropic-beta` | `oauth-2025-04-20` |
| `User-Agent` | pin CLI UA |
| `x-app` | `cli` |

---

## 6. Codex

源码：`injectProviderHeaders` + `applyCodexIdentityHeaders`。

### 6.1 鉴权与 Accept

| 头 | 值 | 是否覆盖客户端 |
| --- | --- | --- |
| `Authorization` | `Bearer <access_token>` | 是（下游鉴权头已剥离） |
| `Accept` | `text/event-stream` | 是 |
| `chatgpt-account-id` | 账号凭据中的 `chatgpt_account_id` | 是（黑名单剥离后注入） |

### 6.2 强制 CLI 身份

上游对过低 `version` 会 404（门槛约 `0.144.0`）。当前固定：

| 头 | 值 |
| --- | --- |
| `originator` | `codex-tui` |
| `version` | `0.146.0` |
| `User-Agent` | `codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color` |

以上三项**始终覆盖**客户端同名头（客户端 `User-Agent` 未在黑名单中，但注入阶段会 `Set` 覆盖）。

### 6.3 主动用量探测

| 头 | 值 |
| --- | --- |
| `Authorization` | `Bearer <token>` |
| `Accept` | `application/json` |
| `User-Agent` / `originator` / `version` | 与转发相同 |
| `ChatGPT-Account-Id` | 凭据中有 account id 时设置（注意大小写形式与转发头名不同，语义相同） |

---

## 7. Grok

源码：`injectProviderHeaders` + `applyGrokCLIIdentityHeaders`。

### 7.1 鉴权与 Accept

| 头 | 值 |
| --- | --- |
| `Authorization` | `Bearer <access_token>` |
| `Accept` | `application/json, text/event-stream` |

### 7.2 CLI 身份

版本来自配置 `UNISUB_GROK_CLIENT_VERSION`（默认 `0.2.114`）；空则回退 `0.2.114`。

| 头 | 值 | 说明 |
| --- | --- | --- |
| `User-Agent` | `xai-grok-workspace/<version>` | 始终覆盖 |
| `X-Grok-Client-Version` | `<version>` | 始终设置 |
| `x-grok-client-version` | `<version>` | 同内容双写（大小写变体） |
| `x-grok-client-identifier` | `grok-shell` | 始终设置 |
| `X-Grok-Client-Mode` | `interactive` | 始终设置 |
| `X-XAI-Token-Auth` | `xai-grok-cli` | **仅当**上游主机名为 `cli-chat-proxy.grok.com` 时设置 |

客户端传入的 Grok 身份相关头已在黑名单中剥离，避免与上述注入冲突。

### 7.3 主动用量 / 用户探测

Billing 与 `/user` 探测：

| 头 | 规则 |
| --- | --- |
| 身份头 | 与转发相同（`applyGrokCLIIdentityHeaders`） |
| `X-XAI-Token-Auth` | **始终**设为 `xai-grok-cli`（含测试 URL，不依赖主机名判断） |
| `x-userid` | Billing 请求在凭据含 user id 时额外设置 |

---

## 8. 三者对照摘要

| 项目 | Claude | Codex | Grok |
| --- | --- | --- | --- |
| 上游鉴权 | OAuth → `Authorization`；API Key → `x-api-key` | `Authorization` | `Authorization` |
| `Accept` | `application/json` | `text/event-stream` | `application/json, text/event-stream` |
| UA 策略 | 真 CLI 透传；否则覆盖为 pin | **始终**覆盖为 `codex-tui/...` | **始终**覆盖为 `xai-grok-workspace/...` |
| 关键身份头 | `anthropic-version` / `anthropic-beta` / `x-app`；mimic 时加 Stainless | `originator` + `version` + `chatgpt-account-id` | Grok Client-* 系列；条件性 `X-XAI-Token-Auth` |
| URL 附加 | Messages/CountTokens 确保 `?beta=true` | 无强制 | 无强制（主机名影响 Token-Auth） |
| 客户端同名身份头 | 真 CLI 可保留 beta/UA | 被覆盖 | 黑名单剥离后重写 |

---

## 9. 上游响应头回传

`copyUpstreamHeaders` 会把上游响应头复制给下游，但剥离 hop-by-hop：

- `connection`
- `keep-alive`
- `proxy-connection`
- `transfer-encoding`
- `upgrade`

SSE（`Content-Type` 含 `text/event-stream`）时，网关额外设置：

- `X-Accel-Buffering: no`

其余上游响应头（含 `request-id` / `x-request-id`、各类 rate-limit 头）原样回传。

### 9.1 被动额度解析（写入账号 `quota_json`，不改响应体）

| 来源头家族 | 用途 |
| --- | --- |
| `x-ratelimit-*-requests` / `x-ratelimit-*-tokens` | Codex / Grok 等通用限流窗口 |
| `anthropic-ratelimit-unified-{5h,7d,7d_oi}-*` | Claude OAuth 统一额度窗口 |

---

## 10. 调用日志头

写入请求日志时，请求头、上游请求头、响应头均按原文序列化为 JSON 落库，**不做脱敏**。正文超过约 1 MiB 仍会截断（与头无关）。

---

## 11. 实现索引

| 逻辑 | 文件 / 符号 |
| --- | --- |
| 剥离 + 注入入口 | `internal/server/proxy.go` → `copyDownstreamHeaders`, `injectProviderHeaders` |
| Claude 身份 | `internal/server/claude_identity.go` → `applyClaudeOutboundHeaders` |
| Codex 身份 | `proxy.go` → `applyCodexIdentityHeaders` |
| Grok 身份 | `proxy.go` → `applyGrokCLIIdentityHeaders` |
| Claude `?beta=true` | `proxy.go` 转发路径 + `ensureQueryParam` |
| 响应头回传 | `proxy.go` → `copyUpstreamHeaders` |
| 被动额度 | `internal/server/quota_headers.go` |
| 调用日志头序列化 | `internal/server/httplog.go` → `headersJSON` |
| 用量探测头 | `claude_usage.go` / `codex_usage.go` / `grok_usage.go` |

版本 pin（Claude CLI `2.1.220`、Codex `0.146.0`、Grok 默认 `0.2.114`）随上游风控可能调整；以源码常量为准。
