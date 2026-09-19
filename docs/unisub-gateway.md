# UniSub Gateway 模块

`internal/unisub/gateway.go` 负责外部 AI HTTP 请求转发。它是 UniSub 应用模块，通过 Service 注册 `/v1/` 并使用 AuthAPIKey，不负责浏览器登录、OAuth 授权交互或账号管理。

## 路由与账号选择

客户端可发送 `Authorization: Bearer <key>`（auth token，如 Claude `ANTHROPIC_AUTH_TOKEN`、Codex、Grok Build）和／或 `X-Api-Key`（api key，如 Claude `ANTHROPIC_API_KEY`）。框架同时读取两者作为候选，按 Bearer → X-Api-Key 顺序匹配已签发 Key；任一匹配即可认证。Key 绑定本地用户和持久化 Account，框架将校验结果（含匹配到的密钥明文）放入 Principal；网关通过 Account ID 获取 AIProvider 和运行时 Account，调用记录使用 Principal 中的密钥做归属。

路径保持 /v1/responses、/v1/messages、/v1/chat/completions 等形式，不增加本项目专用厂商前缀。同一路径可因 Key 绑定账号不同而到达不同上游，不能仅凭 URL 判断厂商。

当前注册整个 /v1/ 前缀，并不逐一验证模型、图像、音频、视频或文件端点的可用性。媒体类型、异步任务与 multipart 的实际支持由所选账号、认证方式和上游决定；通用转发不代表所有官方能力均已验证。WebSocket upgrade 尚未实现。

## 上游地址

api_endpoint 可覆盖基址；否则当前代码使用：

| 类型与认证 | 默认基址 |
| --- | --- |
| Claude | `https://api.anthropic.com/v1` |
| Grok | `https://api.x.ai/v1` |
| Codex API Key | `https://api.openai.com/v1` |
| Codex OAuth | `https://chatgpt.com/backend-api/codex` |
| Dummy | `http://dummy.local/v1` |

以上是仓库内置值，不是对平台当前服务范围的外部认证。拼接时去掉本地路径开头的 /v1，将其余部分追加到基址；空基址路径补 /v1，保留转义路径和请求 Query。只接受 HTTP/HTTPS 且有 Host 的目标地址。

## 请求与响应

1. 读取 Principal 和账号配置，确认运行时实例存在。
2. 创建带总超时的 Context，默认 5 分钟，沿用客户端取消信号。
3. 克隆请求，设置上游 URL、Host 并清空 RequestURI；Body 上限 64 MiB。
4. 移除 Connection 指定的头、hop-by-hop 头和 Cookie；上游认证由 AIProvider 设置。
5. 将响应 Writer 放入 Context，通过 Account.Handle 取得执行槽位并调用 AIProvider。
6. AIProvider 流式输出状态、响应头和字节；Recorder 将调用记录交给应用层持久化。

网关不把已经开始发送的响应替换成另一个 JSON 错误。完整响应继续发送给客户端，记录中的响应体最多保留前 1 MiB。下游 Key 和上游 token 不应出现在展示用请求头中。

## 运行时 Account 与并发

持久化实体是 `database.PersistedAccount`；真实运行时类型是 `aiprovider.Account`，定义于 `internal/aiprovider/account.go`，不是单独的 AIProviderAccount 或 AccountLimiter 类型。

```go
func (a *Account) Handle(
    r *http.Request,
    recorder APICallRecorder,
    queueLimit int,
) error
```

AIProviderManager 持有每账号唯一的 Account，所有该账号调用共用并发计数与 FIFO 队列；不同账号独立。Gateway 不直接维护 active 或 waiters。

| 配置 | 当前行为 |
| --- | --- |
| max_concurrent_connections | 负值拒绝，0 归一为 1 |
| GatewayQueueLimit | 非正值使用每账号 100 个等待请求；活跃请求不计入队列 |
| queue_timeout_seconds | 负值拒绝，0 使用 180 秒 |
| GatewayRequestTimeout | 非正值使用 5 分钟，包含排队和执行时间 |

没有等待者且存在空闲槽位时立即执行，否则按 FIFO 等待。队列满立即返回错误。Handle 使用 defer 释放槽位；客户端取消或排队超时会移除等待项。如果取消与授予并发发生，已授予的槽位也会被释放，不进入上游调用。

配置通过 AIProviderManager 更新：增大并发可唤醒等待者；降低上限不取消已经执行的请求，后续等待活跃数下降。禁用或删除账号会唤醒等待者并拒绝新请求，不强制取消已执行请求。

## 错误响应

| 场景 | 当前状态 |
| --- | --- |
| 缺少、无效或过期 Key | 401 |
| Key 对应用户停用／不存在，或绑定账号不存在 | 403 |
| 运行时账号不存在、关闭或停用 | 503 |
| 等待队列已满 | 429 |
| 排队或请求超时 | 504 |
| 上游目标无效或没有响应 | 502 |
| 客户端取消 | 不再主动写业务错误 |

队列满当前没有设置 Retry-After，不能把该响应头写成既有契约。上游已开始输出的状态与内容按流式转发结果处理。

## 调用记录与代理

应用层把 AIProviderCallTrace 转为数据库记录，包含账号、请求 ID、来源 IP、URL、时间、状态、模型、token 用量和截取的请求／响应。记录中的 `http_error_code` 使用标准 HTTP 状态：有上游／本地 HTTP 响应时写入该状态码（含 2xx）；网络或传输失败且没有 HTTP 响应时为 0，不把写给客户端的 502 回填进记录。展示头对 Authorization、X-Api-Key、Cookie、Set-Cookie、Proxy-Authorization 脱敏；持久化 APIKey 字段用于归属关联，管理 API 返回前清空。

排队中止且未调用 AIProvider 的请求不会产生一次已发送上游调用的记录。当前记录没有完整的队列深度、等待耗时和限流计数指标，不将这些字段写成现有监控能力。

AIProvider 使用注入的 ProxyResolver 选择代理与报告结果。独立 `internal/proxy` 及应用维度调度是目标设计，见 [Proxy](proxy.md)，不是本次代码变更。

## 验证边界

相关测试位于 `internal/unisub/gateway_test.go`、`dummy_functional_test.go` 与 `internal/aiprovider/account_test.go`，关注流式响应、认证、路径拼接、并发、取消和记录。真实平台账号可用性及各媒体端点能力需要对应账号验证，不能由本地模拟测试推定。
