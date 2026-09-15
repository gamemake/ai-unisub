# AIProvider 模块

`internal/aiprovider` 定义 AI 上游调用契约、配置、工厂、实例管理和运行时账号。它不依赖 database 或 Web 页面；应用层负责持久化转换和用户权限。

## 类型与命名

| 类型 | 职责 |
| --- | --- |
| AIProvider | Config、UpdateConfig、Handle、FetchUsage、ResetUsage |
| AIProviderConfig | 单个实例的公共配置 |
| AIProviderFactory | 根据 ID 与 JSON 创建具体实例 |
| AIProviderManager | 注册工厂、创建／恢复／更新／删除实例 |
| Account | 每账号唯一的运行时并发计数与 FIFO 队列 |
| AIProviderCallTrace、APICallRecorder | 调用结果及回调，不携带数据库实体 |

具体实现为 Codex、Claude、Grok 和 Dummy。前端页面是 `src/pages/ai-providers.tsx`，管理接口与缓存键分别为 /api/ai-providers、ai-providers。

兼容协议包括 /api/providers、旧 #providers/#accounts 页面入口、JSON 字段 provider/provider_type 和已有 SQLite 列名；这些不是 Go 类型名。OAuth CLI 的 provider 标识也保持其协议含义。

## 配置

| 字段 | 当前语义 |
| --- | --- |
| id/name/labels | 实例标识和元数据；创建时 ID 由调用方指定 |
| enabled | 未指定时为 true |
| auth_type | oauth（默认）或 api_key |
| credential_id | OAuth 凭据引用，兼容嵌套 oauth.credential_id |
| api_key | 上游密钥，api_key 认证必须非空；oauth 模式不接受该值 |
| api_endpoint | 可选 HTTP/HTTPS 上游基址 |
| proxy_group_id | 使用代理组 |
| proxy | 保留的直接代理 URL 配置 |
| max_concurrent_connections | 负值拒绝，0 归一为 1 |
| queue_timeout_seconds | 负值拒绝，0 在运行时使用 180 秒 |

旧 api_keys 配置不再接受。模型由客户端请求提供，不将所有模型能力写成账号固定属性。

## 认证与调用

OAuth 模式调用 OAuthManager.GetValidAccessToken(ctx, service, credentialID)，不自行执行授权交换或持久化刷新。API Key 模式使用配置密钥：Claude 使用 X-Api-Key，其他实现使用 Bearer；OAuth 使用 Token 及平台所需 Header。

Handle 保留请求转发契约；Web 网关通过 WithResponseWriter 注入响应接收者，AIProvider 输出状态、响应头及流式字节。Recorder 收集调用结果，由应用层转换为数据库记录。网关路径、Body 限制和记录边界见 [Gateway](unisub-gateway.md)。

## 运行时账号

AIProviderManager 为每个实例持有同一个 Account，不按请求创建独立队列。Account.Handle 接收 request、recorder 和 queueLimit，统一执行并发限制、FIFO、超时、取消与释放。

配置更新经 Manager 更新实例并唤醒等待者；删除或停用账号拒绝后续请求并唤醒排队请求。降低并发上限不取消已执行请求。详细行为见 [Gateway](unisub-gateway.md)。

## 代理契约

当前 ProxyResolver 提供 ResolveProxy(Context, groupID)、ProxyRetryLimit(groupID)、ReportProxy(groupID, URL, available)。Service 注入现有 ProxyManager；AIProvider 不直接持有数据库。

当前实现将网络错误或上游 5xx 视为可重试失败，并报告代理不可用；按代理组提供的重试上限再次选择代理。它尚未实现按应用分类的健康状态。

目标架构中独立 `internal/proxy` 提供代理管理与调度，调用方通过窄接口接入，不让代理包依赖具体 AIProvider。网络与应用错误隔离、优先级选择和全局统计详见 [Proxy](proxy.md)，不代表当前行为。

## 管理响应与验证

账号保存、凭据引用和响应字段以 [API](unisub-api.md) 为准：config 过滤敏感字段，但管理员响应可附带完整 Credential；不对整个响应作一概脱敏声明。

本地测试使用模拟上游验证认证、流式转发、用量和并发；真实平台可用性、模型与媒体接口取决于对应账号和上游，不能由模拟测试推定。
