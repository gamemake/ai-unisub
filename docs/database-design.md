# Database 模块

`internal/database` 定义持久化接口，提供 SQLite 与内存实现。服务端使用 `modernc.org/sqlite`，无需 CGO。`DatabaseURL` 接受 `sqlite://./data/ai-unisub.db`、绝对路径或 `sqlite::memory:`。

主要实体：

- `PersistedUser`：用户名、角色、启用状态和密码散列。
- `PersistedAccount`：AIProvider 类型、名称和 JSON 配置。
- `PersistedAPIKey`：所属用户、绑定账号、密钥和有效期。
- OAuth Credential：按不透明 ID 保存，由 OAuthManager 管理刷新。
- ProxyGroup/Proxy：代理地址、重试策略和可用状态。
- CallTrace：调用主体、时间、请求／响应、用量；列表查询只返回摘要。

数据库实体不依赖 AIProvider 的调用结构，网关在应用层执行调用结果的结构转换。API 层负责权限校验及响应字段筛选，不直接序列化密码散列。默认数据库文件和本地数据目录不提交到 Git。
