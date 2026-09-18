# aiprovider 重构计划

## 核心原则

`AIProvider` 本身由 `config`、`state`、`quota` 三部分组成，并且 provider 的实例 ID 统一为 `int`；manager 负责 provider 生命周期，但不直接理解数据库模型。

代码中不直接引入 `database.PersistedAccount`。数据库层或上层模块只负责把数据库记录映射成 `aiprovider` 自己定义的输入结构。

## 1. 拆分 provider 数据模型

在 `aiprovider` 包中明确三个独立模型：

```go
type AIProviderConfig struct {
	// provider 类型、认证、endpoint、并发、路由、group members 等配置
}

type AIProviderState struct {
	// 需要由 provider 自己管理的可持久化状态
}

type AIProviderQuota struct {
	// quota 快照、更新时间、缓存状态等
}
```

`AIProviderConfig` 只保存 provider 配置；`AIProviderState` 只保存明确需要跨恢复保留的 provider 状态；`AIProviderQuota` 保存可序列化的配额快照。

## 2. 调整 `AIProvider` 接口

接口需要直接对应三部分数据：

```go
type AIProvider interface {
	Config() AIProviderConfig
	State() AIProviderState
	Quota() AIProviderQuota

	UpdateConfig(json.RawMessage) error
	RestoreState(json.RawMessage) error
	RestoreQuota(json.RawMessage) error

	Handle(*http.Request, APICallRecorder)
	FetchQuota(context.Context) (*Quota, error)
	ResetUsage(context.Context) error
}
```

具体命名结合现有 API 兼容性决定。`GetCachedQuota()` 可以保留为运行时查询 API，或者由 `Quota()` 统一承担。

## 3. 定义 `aiprovider` 自有的构造输入

不直接引入 `database.PersistedAccount`，在 `aiprovider` 包中定义自己的组合结构：

```go
type ProviderData struct {
	Config json.RawMessage
	State  json.RawMessage
	Quota  json.RawMessage
}
```

或使用已解码模型：

```go
type ProviderSnapshot struct {
	Config AIProviderConfig
	State  AIProviderState
	Quota  AIProviderQuota
}
```

工厂和 manager 使用该结构：

```go
type AIProviderFactory func(id int, data ProviderData) (AIProvider, error)
```

`AIProviderConfig.ID`、manager 的 provider map、`Account` 关联、group member ID 以及所有 manager/provider API 的 provider ID 均使用 `int`，不再保留字符串 ID 版本。

构造过程：

```text
ProviderData.Config → 解码、规范化、校验
ProviderData.State  → provider 初始化后恢复
ProviderData.Quota  → quota cache 初始化后恢复
```

## 4. 调整所有 provider 构造函数

统一适配以下构造路径：

- `AIProviderManager.Create`
- `newOAuthAIProvider`
- `NewDummyAIProvider`
- `newGroup`
- `NewClaudeAIProvider`
- `NewCodexAIProvider`
- `NewGrokAIProvider`
- `APIProviderFactory` 及其他 factory

特殊行为：

- group provider 不拥有 quota；
- group provider 不恢复成员 quota；
- state 使用零值，除非后续明确增加可持久化 state；
- Dummy provider 继续支持测试 quota。

## 5. 明确三部分的数据边界

### Config

包含：

- provider 类型和 supplier；
- client type；
- endpoint；
- 认证方式、credential 引用或 API key；
- enabled、并发数和队列配置；
- proxy/group 配置；
- group members；
- provider name、labels 等用户配置。

配置更新继续由各 provider 的 `UpdateConfig` 负责，并保留现有默认值、校验和兼容逻辑。

### State

只保存 provider 自己需要跨重启保留的状态。

当前不应直接保存：

- `active`；
- `waiters`；
- `closed`；
- `affinityBinding`；
- `revisions`；
- 临时 `memberHealth`；
- mutex、HTTP client、OAuth manager；
- 正在执行的请求或 quota 查询。

如果当前业务没有明确需要跨重启保存的 provider state，`AIProviderState` 可以先保持为空结构或只包含确实需要的字段。

### Quota

保存：

- subscription quota；
- 普通 quota items；
- `UpdatedAt`；
- cache status；
- reset 时间。

不保存：

- quota cache 的 mutex；
- generation；
- sequence；
- fullSequence；
- 请求上下文和并发控制信息。

## 6. 分离 quota 快照与内部 cache

当前 `quota.go` 的对外 quota 数据和 `quota_cache.go` 的内部并发 cache 需要分离：

```text
AIProviderQuota       可序列化的 quota 快照
quotaCache            负责并发、过期、请求序列和缓存更新
```

在 `quotaCache` 内部增加类似转换：

```go
func (c *quotaCache) snapshot() AIProviderQuota
func (c *quotaCache) restore(value AIProviderQuota)
```

需要接入的 quota 更新点：

- `quota_query.go` 的主动 quota 查询；
- `oauth_aiprovider.go` 的上游 header 被动更新；
- `dummy.go` 的 Dummy quota；
- 配置更新时的 quota invalidation。

## 7. 调整 manager 职责

`AIProviderManager` 负责：

- provider 注册和创建；
- provider 实例管理；
- provider ID 和 adapter 管理；
- group 关系校验；
- 账号并发控制；
- 路由选择；
- provider 删除和配置更新。

manager 不负责：

- 解析 `PersistedAccount`；
- 直接读写数据库；
- 拼装数据库账号对象；
- 直接操作 provider 的 quota cache 内部字段。

建议围绕 `ProviderData` 提供：

```go
func (m *AIProviderManager) Create(id int, providerType string, data ProviderData) (AIProvider, error)
func (m *AIProviderManager) Export(id int) (ProviderData, bool)
func (m *AIProviderManager) UpdateConfig(id int, raw json.RawMessage) error
```

`Export` 只导出 provider 的三部分内容，供上层持久化。

## 8. 三部分的更新语义

### Config 更新

1. 通过 `UpdateConfig` 更新；
2. 校验新的 provider config；
3. 使受影响的 quota 失效；
4. 不自动覆盖 state；
5. 不把 manager 的运行时状态写进 state。

### State 更新

- 只通过 provider 明确提供的 state API；
- 恢复失败时返回错误；
- state 不参与 provider 类型推断。

### Quota 更新

- `FetchQuota` 成功后更新 provider quota；
- 请求失败不覆盖已有 quota；
- 配置变化时清空或使 quota 失效；
- 恢复后根据 `UpdatedAt` 重新判断 fresh/stale/missing。

## 9. 修复整数 ID 重构遗留问题

同步统一：

- manager 中遗留的字符串 ID 与整数 ID 的混用；
- factory 参数类型；
- `aiProviders`、`accounts`、`adapters`、`health`、`revisions` 的 key 类型；
- group member ID 校验；
- `Create`、`Get`、`Remove`、`UpdateConfig`、`Select`、`Referenced` 的参数和 map 访问全部改为 `int`。

## 10. 测试计划

只修改 `internal/aiprovider`：

1. 每种 provider 都能通过 `ProviderData` 构造；
2. `Config()`、`State()`、`Quota()` 三部分互不串数据；
3. config 更新不会误覆盖 state；
4. quota 查询只更新 quota；
5. quota 恢复后正确判断 fresh/stale/missing；
6. group provider 不拥有 quota；
7. provider snapshot 导出后可以重新构造等价 provider；
8. 空 state、空 quota、旧数据兼容；
9. manager 整数 ID 生命周期；
10. 确认 `aiprovider` 不 import `internal/database`。

## 目标结构

```text
AIProvider
├── Config() → AIProviderConfig
├── State()  → AIProviderState
├── Quota()  → AIProviderQuota
├── UpdateConfig(...)
├── RestoreState(...)
├── RestoreQuota(...)
└── Handle / FetchQuota / ResetUsage
```

数据库层只负责存取 `config`、`state`、`quota` 三个 JSON 字段；三部分的含义、校验和恢复逻辑全部由 `aiprovider` 自己负责。
