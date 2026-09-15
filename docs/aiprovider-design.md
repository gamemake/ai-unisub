# AI Provider 模块

命名约定：Go 包为 `aiprovider`，接口、配置、工厂和管理器分别为 `AIProvider`、`AIProviderConfig`、`AIProviderFactory`、`AIProviderManager`。前端页面为 `src/pages/ai-providers.tsx`，管理接口及缓存键为 `/api/ai-providers`、`ai-providers`。

兼容边界：旧 `/api/providers` 路由仍指向同一处理器，旧 `#providers`、`#accounts` 链接仍可打开页面。已有 JSON 字段 `provider`、`provider_type` 和 SQLite 列名保持不变；它们是序列化协议，不是 Go 标识符，因此无需迁移或重建已有数据库。OAuth CLI 的服务标识、CC Switch 的 `resource=provider` 以及 React Context Provider 不属于本次领域重命名。

`internal/aiprovider` 提供 Codex、Claude、Grok 和 Dummy 实现。每个实例有稳定 ID，JSON 配置包含认证方式、凭据引用／上游 API Key、Base URL、代理组、启用状态及并发限制。

`auth_type=oauth` 从 OAuthManager 获取有效 token；`auth_type=api_key` 使用配置里的密钥（Claude 使用 `X-Api-Key`，其他平台使用 Bearer）。网关按绑定账号决定目标地址。

`AIProviderManager` 注册工厂、创建和恢复实例，持有每账号唯一的运行时 `Account`。Account 负责 FIFO、并发配额、排队取消与停用通知；`UpdateConfig` 修改配置并唤醒可用执行槽。删除账号唤醒排队请求并拒绝后续请求。

AIProvider 保留 `Handle(request, recorder)` 契约；HTTP 网关通过 `WithResponseWriter` 提供响应流接收者，AIProvider 直接发送状态、响应头和分块数据。Recorder 收到调用结果，由应用层持久化。不会将数据库类型引入 AIProvider 包。

默认地址可被 `api_endpoint` 覆盖；调用路径必须与所选平台／订阅实际支持的能力匹配。自动化测试使用本地上游验证三种平台的认证和流式转发，不把模拟测试视为真实账号认证成功。
