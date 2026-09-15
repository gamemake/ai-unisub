# Database 模块

`internal/database` 定义统一的数据库访问与持久化契约。SQLite 负责持久化；`MemoryDatabase` 是数据库模块内部的数据结构，用来统一数据库的内存缓存逻辑，不是与 SQLite 并列的独立数据库后端，也不以测试替代实现作为其架构定位。服务器使用 `modernc.org/sqlite`，无需 CGO。

## 数据库地址

NewDatabase 只接受带 sqlite Scheme 的地址，不接受裸文件路径。

| 地址／入口 | 含义 |
| --- | --- |
| `sqlite://./data/ai-unisub.db` | 相对当前工作目录的文件 |
| `sqlite:///var/lib/app/data.db` | Unix 绝对路径 |
| `sqlite:///C:/data/app.db` | Windows 绝对路径 |
| `sqlite::memory:` | SQLite 引擎将数据存放在进程内存中，不写数据库文件；与内部 MemoryDatabase 缓存结构不是同一概念 |

Database 提供 Open、Close 以及各实体读写，应用模块不绕过接口直接操作 SQLite。

## 内部内存缓存

`database_memory.go` 中的 MemoryDatabase 集中封装内存数据结构、并发访问、实体读写和数据副本逻辑。`SQLiteDatabase` 在内部持有该结构，复用同一套缓存管理能力，业务模块不单独创建或维护数据库缓存。

| 层次 | 职责 |
| --- | --- |
| Database 接口 | 向调用方提供统一读写契约，隐藏缓存与持久化细节 |
| SQLiteDatabase | 管理数据库连接、持久化读写、启动加载及缓存同步 |
| MemoryDatabase | 管理内部内存数据与并发访问，统一缓存中的实体操作 |

当前数据流：

- 创建 SQLiteDatabase 时初始化内部 MemoryDatabase；打开数据库后，将账号、用户、代理组和 API Key 加载到缓存。
- 这些实体的列表读取复用内存缓存；保存和删除先执行 SQLite 操作，成功后再更新对应缓存。
- Credential 的保存和删除同步内部缓存，但当前 LoadCredential 直接读取 SQLite，启动加载也不预载 Credential；不能将所有读取都描述为缓存命中。
- 调用记录直接由 SQLite 保存和查询，不进入 MemoryDatabase 缓存；按 UTC 日期分表，列表只返回摘要。
- SQLite 数据是持久化依据，缓存是进程内状态，可从已保存数据重建，不作为独立部署或切换后端的选项。

`NewMemoryDatabase` 用于构造该内部结构，不是对外配置的数据库入口。测试可以直接验证内存结构的行为，但不改变其统一缓存逻辑的职责。

## 数据契约

| 实体 | 内容与边界 |
| --- | --- |
| PersistedUser | 名称、角色、启用状态、标签、密码散列和时间；散列不参与 JSON 输出 |
| PersistedAccount | ID、name、provider、JSON config 和时间 |
| PersistedAPIKey | 所属用户、绑定账号、明文 Key、有效秒数和时间 |
| Credential | 按不透明 ID 保存 JSON；结构由 OAuth 定义 |
| PersistedProxyGroup / PersistedProxy | 代理组内嵌代理地址、状态和错误桶 |
| PersistedCallTrace | 调用账号、关联 Key、请求／响应、模型、用量与时间 |
| PersistedCallTraceSummary | 列表摘要，不带大体积 Body/Header |

账号与调用记录的数据库类型不依赖 AIProvider 调用结构，Gateway 在应用层完成转换。数据库保存的 Key 用于认证和调用归属关联；API 返回字段另行筛选，不直接公开整个存储实体。

## OAuth 存储

Database 直接满足 CredentialStore 的 LoadCredential、SaveCredential、DeleteCredential 接口。凭据按 JSON 保存，不新增专用 DatabaseCredentialStore。

OAuth Session、Web 一次性结果和浏览器 Session 不存入数据库，重启后失效；已保存 Credential 和账号配置可恢复。当前凭据存储没有独立 owner/service 契约，不能描述成存储层自动验证凭据用户归属。

## 调用查询

QueryCallTraces 提供分页、筛选与时间范围，列表返回摘要；GetCallTrace 使用日期与记录 ID 获取详情。CleanupCallTrace 只清理调用数据，不删除用户、账号或 Key。

记录包含用于用户关联的 APIKey 信息，管理 API 返回前清空；最终权限由 API Handler 和查询范围共同决定。不存在的详情使用 ErrCallTraceNotFound。

## 独立 Proxy 包的目标存储边界

当前代理组、状态和错误桶均通过 Database 的组读写接口保存。目标 `internal/proxy` 定义自身模型与最小 Store 接口，数据库层提供适配与模型转换；代理包不依赖整套 Database 或具体驱动。

目标要求地址全局去重、应用维度状态、1 分钟内存桶和 10 分钟持久化聚合，尚不是现有数据库结构。领域语义集中在 [Proxy](proxy.md)，本次文档更新不改变表结构或数据。

## 数据与验证

默认 data 目录、数据库与凭据不提交到 Git。数据库测试覆盖实体契约、调用归属等；应用重启恢复行为在 `internal/unisub/restart_test.go` 验证。字段兼容与管理响应见 [AIProvider](aiprovider.md) 和 [UniSub API](unisub-api.md)。
