# ai-unisub 与 sub2api 处理逻辑核对

对照范围：`D:\Projects\ai-unisub`（当前项目）与 `D:\Projects\sub2api`（参考实现）。

只核对 **ai-unisub 已实现** 的能力；池调度、计费、协议转换、WebSocket、支付等未实现能力一律不展开。

核对日期：2026-09-07。

---

## 总体结论

两边产品定位不同：

| | **ai-unisub** | **sub2api** |
|---|---|---|
| 模型 | Key → **单一账号** 原生透传 | Key → 用户/分组 → **账号池** + 兼容层 |
| 请求体 | **不改写** | 大量 mimic / 映射 / bridge |
| 出站头 | 黑名单透传 + 注入身份 | 白名单 + 强制官方身份 |

**OAuth 契约（client_id、authorize/token/redirect、usage URL、上游 Responses URL）大体对齐。**  
真正有风险的是：**出站客户端身份头过时/不正确**，以及几处基础设施细节。

---

## 已对齐（MATCH）

1. **Claude OAuth 契约**：client_id、authorize/token/redirect、scopes、PKCE S256、JSON refresh/exchange 形态一致。
2. **Codex OAuth 契约**：`auth.openai.com`、client_id、`localhost:1455` redirect、scopes、`codex_cli_simplified_flow` 等一致；上游 `chatgpt.com/backend-api/codex/responses` 一致。
3. **Grok**：issuer / client_id 一致；CLI proxy Responses / billing URL 一致。
4. **Usage 主动查询**：Claude `/api/oauth/usage` + `oauth-2025-04-20`；Codex `/wham/usage`；Grok billing credits。
5. **SSE**：按行 flush、`X-Accel-Buffering: no`、请求 context 取消向上游传播。
6. **Responses 子路径安全校验**：段字符集 / 最多 8 段 / 每段 ≤128，规则基本一致（ai-unisub 还额外拒绝 `%` 编码）。
7. **上游 401 → 强制 refresh 再试一次**：单账号场景下语义正确。
8. **禁止跟随重定向**、每账号独立代理能力：方向一致。

---

## 高优先级差异（建议优先修）

### 1. Claude OAuth 出站身份 / beta — 易被判第三方

ai-unisub（`internal/server/proxy.go` `injectProviderHeaders`）只注入：

- `Authorization: Bearer …`
- `anthropic-version: 2023-06-01`（客户端未带时）
- `User-Agent: claude-cli/1.0 ai-unisub` ← **会覆盖客户端 UA**
- `x-app: cli`
- **不设置 `anthropic-beta`**
- 上游 URL 无 `?beta=true`

sub2api 对 OAuth mimic 会强制：

- UA：`claude-cli/2.1.220 (external, cli)`
- 完整 `anthropic-beta`（含 `claude-code-20250219`、`oauth-2025-04-20` 等）
- `X-Stainless-*`、`Anthropic-Dangerous-Direct-Browser-Access`
- URL：`/v1/messages?beta=true`

sub2api 注释写明：缺官方 beta 会被归到 *extra usage / third-party*。

**影响**：真实 Claude Code 若自带 beta，可部分掩盖；但 UA 仍被覆盖成 `1.0 ai-unisub`。通用 SDK 走 OAuth 账号时风险很高。

### 2. Codex 身份头 — 可能直接 404

ai-unisub：

```text
User-Agent: codex_cli_rs/ai-unisub
originator: codex_cli_rs
version: ai-unisub/0.1.0
```

sub2api（`openai_codex_identity.go`）：

- 默认 `codex-tui/<semver> (OS; arch) …`
- **`version` 最低门槛 `0.144.0`**；低于该值上游会 **404**（issue #3901）
- `ai-unisub/0.1.0` 不是合规 Codex semver

**这是当前最像“必现故障”的差异。**

### 3. Grok CLI 身份过时

| | ai-unisub | sub2api |
|---|---|---|
| 版本 pin | `1.0.6` | `0.2.114`（下限 `0.2.93`） |
| UA | `grok-shell/{ver} ai-unisub` | `xai-grok-workspace/{ver}` |
| 额外头 | 有 `x-authenticateresponse` | **没有** 该头也能工作 |

版本号数值上 `1.0.6` 可能仍被接受，但身份串与 sub2api 已知可用路径不一致，属于回归风险。

### 4. 并发槽容量改了不生效

`internal/server/server.go` `acquire`：

```go
LoadOrStore(accountID, make(chan struct{}, limit))
```

首次创建后 **不会随管理端修改 `concurrency_limit` 重建**。调高无效，调低也不缩容，需重启进程。  
（sub2api 用 Redis 槽，无此问题。）

### 5. 无代理账号仍可能走环境代理

`newHTTPTransport()` 使用 `http.ProxyFromEnvironment`；账号未配 `proxy_url` 时，`HTTP_PROXY`/`HTTPS_PROXY` 仍会生效，破坏“每账号独立出口”假设。  
sub2api 直连路径会显式关掉环境代理。

### 6. SOCKS5 本地 DNS

ai-unisub：`SOCKS5(..., xproxy.Direct)` → 本机解析目标再连代理。  
sub2api：倾向 `socks5h`，避免 DNS 泄漏。

### 7. 下游鉴权只认 Bearer

ai-unisub 只从 `Authorization: Bearer` 取 Key。  
sub2api 还认 `x-api-key` / `x-goog-api-key`。  
Anthropic SDK 默认 `x-api-key` 时会在 ai-unisub 上 **401**。

### 8. Claude 被动额度头解析家族不对

ai-unisub `quota_headers.go` 解析 `x-ratelimit-*`。  
Anthropic OAuth 实际多是 `anthropic-ratelimit-unified-*`。  
主动 `/api/oauth/usage` 仍可用；转发路径上的被动额度更新对 Claude 往往空转。

### 9. `Proxy-Authorization` 未剥离

日志会脱敏，但转发黑名单没收；可能把客户端代理凭据带到上游。

### 10. `Accept-Encoding` + `DisableCompression`

客户端带压缩协商时，上游可能返回 gzip，本地用量解析/日志体会读到压缩字节，**Token 统计可能为 0**。sub2api 会解压或避免透传该头。

---

## 中低优先级 / 可接受简化

| 项 | 说明 |
|---|---|
| Header 策略 | 黑名单透传 vs 白名单；有意简化，但噪音头更多 |
| Claude/Codex body mimic | system rewrite、model normalize、TLS fingerprint：计划外，合理 |
| Token refresh skew | Claude/Codex 10min vs sub2api 3min（更保守）；Grok 1min vs sub2api ~1h（偏激进） |
| Grok OAuth UX | device-code vs 浏览器 PKCE：产品选择，client_id 仍对齐 |
| Codex PKCE verifier | base64url(32) vs hex(64)：都合法 |
| Models | ai-unisub 真转发上游；sub2api 多返回本地列表：设计差异 |
| RPM | 按 API Key 进程内窗口 vs 用户/组 Redis：单机场景可接受 |
| Claude usage UA | `claude-cli/1.0` vs `claude-code/2.x`：usage 接口本身已对齐 |

---

## 按链路对照摘要

```text
下游请求
  → API Key 鉴权          [PARTIAL: 缺 x-api-key]
  → 平台/路由隔离         [MATCH 意图]
  → OAuth 预刷新          [MATCH; skew 不同]
  → RPM / 并发            [PARTIAL; 并发容量 sticky bug]
  → 子路径校验            [MATCH]
  → 剥认证头 + 注入身份   [DIFF: 身份过时/错误 ← 重点]
  → 每账号代理 Client     [PARTIAL: env 代理 / socks5 DNS]
  → 上游转发 + SSE        [MATCH]
  → 401 再刷一次          [MATCH]
  → 被动额度头            [Claude 家族不对]
  → 主动 usage 刷新       [MATCH URL/契约]
```

---

## Claude 专项对照

### OAuth 契约

| 项 | ai-unisub | sub2api | 结论 |
|---|---|---|---|
| Client ID | `9d1c250a-e61b-44d9-88ed-5944d1962f5e` | 相同 | MATCH |
| Authorize / Token / Redirect | 官方 Claude Code 公共客户端 URL | 相同 | MATCH |
| Browser scopes | `org:create_api_key user:profile user:inference …` | 相同 | MATCH |
| PKCE | 32 字节 → base64url；S256 | 相同 | MATCH |
| Pending flow TTL | 10 min | 30 min | DIFF（可接受） |
| Pre-expiry skew | 10 min | 3 min | DIFF（更保守） |

### 出站转发

| 项 | ai-unisub | sub2api | 结论 |
|---|---|---|---|
| Messages URL | `…/v1/messages`（无 `?beta=true`） | `…/v1/messages?beta=true` | DIFF |
| count_tokens | 无 `?beta=true` | 带 `?beta=true` | DIFF |
| OAuth auth | `Authorization: Bearer` | 相同 | MATCH |
| `anthropic-version` | `2023-06-01` | 相同 | MATCH |
| `anthropic-beta` | 不强制；仅透传客户端 | OAuth mimic 强制完整集合 | **likely bug** |
| User-Agent | `claude-cli/1.0 ai-unisub`（覆盖客户端） | `claude-cli/2.1.220 (external, cli)` | **likely bug** |
| Stainless / Dangerous-Direct-Browser | 不注入（可透传） | mimic 时强制 | DIFF |
| Body rewrite | 无 | system / metadata / model normalize | 有意简化 |

### Usage / 额度

| 项 | ai-unisub | sub2api | 结论 |
|---|---|---|---|
| Active usage URL | `https://api.anthropic.com/api/oauth/usage` | 相同 | MATCH |
| usage `anthropic-beta` | `oauth-2025-04-20` | 相同 | MATCH |
| 被动响应头 | `x-ratelimit-*` | `anthropic-ratelimit-unified-*` | **Claude 被动额度缺口** |

---

## Codex / Grok 专项对照

### Responses URL 与子路径

| 项 | 结论 |
|---|---|
| Codex ChatGPT backend Responses URL | MATCH |
| Grok CLI proxy Responses 默认 URL | MATCH |
| 子路径 allowlist（8×128、安全字符） | MATCH；ai-unisub 额外拒绝 `%` |

### Codex 出站身份

| 项 | ai-unisub | sub2api | 结论 |
|---|---|---|---|
| originator | `codex_cli_rs` | 默认 `codex-tui` | DIFF |
| User-Agent | `codex_cli_rs/ai-unisub` | `codex-tui/<semver> (OS; arch) …` | **likely bug** |
| version | `ai-unisub/0.1.0` | ≥ `0.144.0` 的 semver | **likely bug / 404** |
| chatgpt-account-id | 从凭据注入 | 相同意图 | MATCH |

### Grok 出站身份

| 项 | ai-unisub | sub2api | 结论 |
|---|---|---|---|
| Client version | `1.0.6` | `0.2.114`（下限 `0.2.93`） | DIFF / stale |
| User-Agent | `grok-shell/{ver} ai-unisub` | `xai-grok-workspace/{ver}` | DIFF |
| `X-XAI-Token-Auth` | `xai-grok-cli`（CLI host） | 相同 | MATCH |
| `x-authenticateresponse` | 有 | 无 | 不明；sub2api 不依赖 |

### OAuth / refresh

| 项 | 结论 |
|---|---|
| Codex authorize/token/client_id/redirect/scopes | MATCH |
| Codex refresh 是否带 `scope` | PARTIAL（sub2api 带 RefreshScopes） |
| Grok device-code vs 浏览器 PKCE | 产品差异；client_id/issuer MATCH |
| Grok refresh skew | 1 min vs ~1h（ai-unisub 更贴近过期） |

### Usage

| 项 | 结论 |
|---|---|
| Codex `/wham/usage` | MATCH（probe 身份头仍有 Codex 同类问题） |
| Grok billing credits URL | MATCH 意图；ai-unisub 额外拉 `/user` 属加固 |

---

## 公共基础设施对照

| ID | 关注点 | 结论 |
|---|---|---|
| A | 下游 API Key 鉴权 | PARTIAL（缺 `x-api-key` / `x-goog-api-key`） |
| B | 剥离客户端认证头 | PARTIAL（`Proxy-Authorization` 泄漏；黑名单 vs 白名单） |
| C | 每账号代理 / 隔离 | PARTIAL（环境代理 + SOCKS 本地 DNS） |
| D | 并发 / 队列超时 | PARTIAL（channel 容量 sticky bug） |
| E | RPM | PARTIAL（按 Key 进程内窗口；单机可接受） |
| F | SSE 转发 | MATCH |
| G | Body 大小 / JSON | MATCH |
| H | Responses 子路径安全 | MATCH |
| I | Models 端点 | DIFF（真转发 vs 本地列表） |
| J | 401 → refresh 重试 | MATCH（单账号类比） |
| K | Redirect / H2 / compression | PARTIAL（压缩与本地统计交互） |
| L | 请求日志 / usage 解析 | PARTIAL（解析可用；压缩体会挖空统计） |

---

## 行动项

状态约定：`todo` / `doing` / `done` / `wontfix`。优先级：`P0` 影响上游可用性或明显错误，`P1` 身份/额度正确性，`P2` 隔离与运维质量，`P3` 增强或可选。

### P0 — 上游可用性

| ID | 行动项 | 状态 | 涉及文件 | 验收标准 |
|---|---|---|---|---|
| A1 | **Codex 出站身份对齐**：`originator` / `User-Agent` / `version` 改为官方风格；`version` 使用合规 semver 且 ≥ `0.144.0`（对齐 sub2api `codex-tui` 路径） | done | `proxy.go`，`codex_usage.go`，`codex_oauth.go` | Codex OAuth Responses 不再因低版本/`ai-unisub/0.1.0` 被上游 404；UA 与 `originator`/`version` 一致 |
| A2 | **Claude：停止用错误 UA 覆盖真 CLI**：真实 Claude Code 请求透传客户端 `User-Agent`（以及已有的 `anthropic-beta` / Stainless 头），不要强制写成 `claude-cli/1.0 ai-unisub` | done | `proxy.go`，`claude_oauth.go` | 真 Claude Code 出站 UA 与入站一致（或仅做必要清洗），不再被 `1.0 ai-unisub` 覆盖 |
| A3 | **下游鉴权支持 `x-api-key`（及可选 `x-goog-api-key`）**：与 Bearer 等价解析 `unisub_*` | done | `proxy.go`，相关测试 | Anthropic SDK 默认 `x-api-key` 可鉴权；无效 Key 仍 401 |

### P1 — OAuth 身份与额度正确性

| ID | 行动项 | 状态 | 涉及文件 | 验收标准 |
|---|---|---|---|---|
| A4 | **Claude OAuth 非 CLI（mimic）路径**：客户端不像 Claude Code 时，补齐官方特征——`anthropic-beta`（至少含 `claude-code-20250219`、`oauth-2025-04-20` 等已知集合）、UA 使用当前 pin（如 `claude-cli/<pin> (external, cli)`）、messages/count_tokens URL 加 `?beta=true` | done | `claude_identity.go`，`proxy.go` | 非 CLI 客户端用 Claude OAuth 时不被轻易判第三方；真 CLI 仍走 A2 透传 |
| A5 | **说明并落地 Claude UA 策略**：文档/代码注释写明——`2.x (external, cli)` 是 mimic 版本钉，不是协议硬性要求；须与 beta（及若后续做 billing fingerprint）保持一致 | done | `claude_identity.go`，本文档 | 后续改版本时有单一 pin 来源，避免 UA/beta/cc_version 漂移 |
| A6 | **Grok CLI 身份对齐**：版本 pin 与 UA 对齐 sub2api 当前已知可用值（如 `xai-grok-workspace/0.2.114`）；复核是否仍需要 `x-authenticateresponse` | done | `proxy.go`，`config.go`，`grok_usage.go`，`grok_oauth.go` | CLI proxy 出站身份与已知可用路径一致；billing/Responses 均可用；已去掉不必要的 `x-authenticateresponse` |
| A7 | **Claude 被动额度头**：解析 `anthropic-ratelimit-unified-*`（保留现有 `x-ratelimit-*` 给 Codex/Grok） | done | `quota_headers.go`，相关测试 | Claude OAuth 转发响应能更新账号 `quota_json`；主动 `/api/oauth/usage` 行为不变 |

### P2 — 隔离、限流与统计正确性

| ID | 行动项 | 状态 | 涉及文件 | 验收标准 |
|---|---|---|---|---|
| A8 | **并发 limiter 随账号配置更新**：`concurrency_limit` 变更时重建/替换 channel；删除账号时清理 | todo | `server.go`，admin 更新路径 | 管理端调高/调低并发后无需重启即生效；进行中请求不 panic |
| A9 | **直连账号禁用环境代理**：无 `proxy_url` 时 `Transport.Proxy = nil`，禁止 `ProxyFromEnvironment` | todo | `account_proxy.go`，`server.go` | 设置 `HTTP_PROXY` 时，无代理账号仍直连上游 |
| A10 | **SOCKS5 使用远端 DNS（socks5h 语义）**：避免本机解析目标主机 | todo | `account_proxy.go` | 代理抓包/解析行为符合远端 DNS；配置校验文档同步 |
| A11 | **剥离 `Proxy-Authorization`**：加入转发黑名单（日志脱敏已有则保持） | todo | `proxy.go` | 客户端带 `Proxy-Authorization` 时上游请求不含该头 |
| A12 | **处理 `Accept-Encoding` / 压缩体**：不透传或出站关闭压缩协商，或对响应解压后再做本地 usage 解析与日志截断 | todo | `proxy.go`，`usage_parse.go` / `httplog.go` | 客户端带 `Accept-Encoding` 时本地 Token 统计仍正确 |

### P3 — 可选增强（非阻塞）

| ID | 行动项 | 状态 | 说明 |
|---|---|---|---|
| A13 | Codex refresh 请求补 `scope`（对齐 sub2api `RefreshScopes`） | todo | 低风险对齐；当前省略多半可用 |
| A14 | Grok refresh skew 评估（1min vs 更早刷新） | todo | 长请求/时钟偏差场景再调 |
| A15 | Claude usage 探测 UA 与 CLI pin 对齐 | todo | usage 契约已通；属观感/一致性 |
| A16 | 出站头改为白名单（对齐 sub2api） | todo | 产品若坚持薄透传可 `wontfix`；噪音头有泄漏/指纹风险时再做 |
| A17 | Claude OAuth mimic 的 body billing fingerprint / system rewrite | todo | 超出当前“原生透传”边界；仅当非 CLI OAuth 仍被判第三方时再评估 |

### 建议实施顺序

1. ~~**A1**（Codex 404 风险）→ **A2**（停止破坏真 Claude Code）→ **A3**（SDK 鉴权）~~ **已完成**
2. ~~**A4 / A5**（Claude 非 CLI OAuth）→ **A6**（Grok 身份）→ **A7**（Claude 被动额度）~~ **已完成**
3. **A8 → A9 → A10 → A11 → A12**（并发与出口隔离、统计）
4. **A13–A17** 按需

### Claude UA 决策备忘（供 A2/A4/A5）

- Anthropic **没有**要求所有请求固定 `claude-cli/2.1.220 (external, cli)`。
- Claude Code OAuth 凭据面向 Claude Code 流量；上游用 UA + `anthropic-beta`（+ billing 指纹）判断是否第三方。
- **真 Claude Code**：透传客户端身份（A2）。
- **非 Claude Code 用 OAuth**：才注入当前 CLI 版本钉做 mimic（A4）；版本钉须与 beta 等特征一起维护（A5）。

### P1 落地摘要（2026-09-07）

| 项 | 实现要点 |
|---|---|
| A4 | `claude_identity.go`：UA 匹配 `claude-cli/X.Y.Z` 则透传；否则 OAuth mimic 注入完整 beta + Stainless 头；messages/count_tokens 自动补 `beta=true` |
| A5 | pin 集中在 `claudeCLIVersionPin` / `defaultClaudeCLIUserAgent`；文件头注释说明与 beta 同步维护 |
| A6 | 默认 `UNISUB_GROK_CLIENT_VERSION=0.2.114`；UA=`xai-grok-workspace/<ver>`；去掉 `x-authenticateresponse`；CLI host 仍带 `X-XAI-Token-Auth` |
| A7 | `quota_headers.go` 解析 `anthropic-ratelimit-unified-{5h,7d,7d_oi}-*` 写入 `windows[]`；保留 `x-ratelimit-*` |

---

## 主要对照文件

### ai-unisub

- `internal/server/proxy.go` — 转发、头注入、SSE、子路径
- `internal/server/server.go` — 路由、并发、RPM
- `internal/server/account_proxy.go` — 每账号 HTTP Client / 代理
- `internal/server/oauth.go` / `pkce_oauth.go` / `claude_oauth.go` / `codex_oauth.go` / `grok_oauth.go`
- `internal/server/claude_usage.go` / `codex_usage.go` / `grok_usage.go` / `quota_headers.go`
- `internal/config/config.go` — 默认 URL / client_id / 版本 pin

### sub2api

- `backend/internal/service/gateway_forward.go` / `gateway_service.go`
- `backend/internal/service/openai_gateway_forward.go` / `openai_codex_identity.go`
- `backend/internal/pkg/claude/constants.go`
- `backend/internal/pkg/xai/cli_identity.go` / `billing.go`
- `backend/internal/pkg/oauth/oauth.go` / `pkg/openai/` / `pkg/xai/`
- `backend/internal/service/upstream_path_guard.go`
- `backend/internal/repository/claude_usage_service.go` / `openai_quota_service.go`
