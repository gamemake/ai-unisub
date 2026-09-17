# Database 模块

`internal/database` 定义统一的数据库访问与持久化契约。SQLite 负责持久化；`MemoryDatabase` 是数据库模块内部的数据结构，用来统一数据库的内存缓存逻辑，不是与 SQLite 并列的独立数据库后端，也不以测试替代实现作为其架构定位。服务器使用 `modernc.org/sqlite`，无需 CGO。

## 数据库地址

`NewDatabase` 只接受带 sqlite scheme 的地址，不接受裸文件路径。

| 地址／入口 | 含义 |
| --- | --- |
| `sqlite://./data/ai-unisub.db` | 相对当前工作目录的文件 |
| `sqlite:///var/lib/app/data.db` | Unix 绝对路径 |
| `sqlite:///C:/data/app.db` | Windows 绝对路径 |
| `sqlite::memory:` | SQLite 引擎将数据存放在进程内存中，不写数据库文件；与内部 MemoryDatabase 缓存结构不是同一概念 |

Database 提供 Open、Close 以及各实体读写，应用模块不绕过接口直接操作 SQLite。

## 内部内存缓存

`database_memory.go` 中的 `MemoryDatabase` 集中封装内存数据结构、并发访问、实体读写和数据副本逻辑。`SQLiteDatabase` 在内部持有该结构，复用同一套缓存管理能力，业务模块不单独创建或维护数据库缓存。

| 层次 | 职责 |
| --- | --- |
| Database 接口 | 向调用方提供统一读写契约，隐藏缓存与持久化细节 |
| SQLiteDatabase | 管理数据库连接、持久化读写、启动加载及缓存同步 |
| MemoryDatabase | 管理内部内存数据与并发访问，统一缓存中的实体操作 |

当前数据流：

- 创建 SQLiteDatabase 时初始化内部 MemoryDatabase；打开数据库后，将账号、用户、代理组和 API Key 加载到缓存。
- 这些实体的列表读取复用内存缓存；保存和删除先执行 SQLite 操作，成功后再更新对应缓存。
- Credential 的保存和删除同步内部缓存，但当前 LoadCredential 直接读取 SQLite，启动加载也不预载 Credential；不能将所有读取都描述为缓存命中。
- 调用记录和代理日志直接由 SQLite 保存和查询，不进入 MemoryDatabase 缓存；两者均按 UTC 日期分表。
- SQLite 数据是持久化依据，缓存是进程内状态，可从已保存数据重建，不作为独立部署或切换后端的选项。

`NewMemoryDatabase` 用于构造该内部结构，不是对外配置的数据库入口。代理日志相关操作在 MemoryDatabase 中不支持并返回错误。

## 数据契约

| 实体 | 内容与边界 |
| --- | --- |
| PersistedProxyGroup | ID、name、JSON config、JSON state 与时间；代理明细由 config 保存，不在 database 包中定义代理领域类型 |
| PersistedProxyLog | group ID、proxy URL、app type、HTTP 错误码、HTTP 错误消息与时间；SQLite 按 UTC 日期分表 |
| PersistedAccount | ID、name、provider、JSON config、JSON state、JSON quota 与时间 |
| PersistedUser | 名称、角色、启用状态、标签、密码哈希和时间；哈希不参与 JSON 输出 |
| PersistedAPIKey | 所属用户、绑定账号、明文 Key、有效秒数和时间 |
| Credential | 按不透明 ID 保存 JSON；结构由 OAuth 定义 |
| PersistedCallTrace | 调用账号、关联 Key、请求／响应、模型、用量与时间 |
| PersistedCallTraceSummary | 列表摘要，不带大体积 Body/Header |

账号与调用记录的数据库类型不依赖 AIProvider 调用结构，Gateway 在应用层完成转换。数据库保存的 Key 用于认证和调用归属关联；API 返回字段另行筛选，不直接公开整个存储实体。

## 模块配置存储

各模块的全局配置通过统一接口读写，不为 AI Provider 等模块增加专用数据库方法：

```go
LoadModuleConfig(module string) (json.RawMessage, error)
SaveModuleConfig(module string, config json.RawMessage) error
DeleteModuleConfig(module string) error
```

- 每个模块使用稳定且唯一的名称作为键，例如 AI Provider 使用 `aiprovider`，保存供应商名称与服务地址结构。模型映射功能尚未实现，不保存或加载映射配置。
- SQLite 使用 `module_configs(module PRIMARY KEY, config)` 表；保存以完整 JSON 文档原子覆盖，不合并字段。各模块独立存储。
- 模块名不能为空或带首尾空白，数据库只校验 JSON 语法。配置结构、默认值、版本迁移和业务校验由所属模块负责。
- 未配置或已删除时读取返回 `nil, nil`；删除不存在的键也成功。删除持久化配置不会自动修改运行时状态，运行时重载由模块负责。
- 内存实现读写均复制 JSON，防止调用方修改底层切片；SQLite 直接读写持久化数据。

## OAuth 存储

Database 直接满足 CredentialStore 的 LoadCredential、SaveCredential、DeleteCredential 接口。凭据按 JSON 保存，不新增专用 DatabaseCredentialStore。

OAuth Session、Web 一次性结果和浏览器 Session 不存入数据库，重启后失效；已保存 Credential 和账号配置可恢复。当前凭据存储没有独立 owner/service 契约，不能描述成存储层自动验证凭据用户归属。

## 调用查询

PersistedCallTrace 和 PersistedCallTraceSummary 包含 `SessionID`（JSON 为 `session_id`）。SQLite 日表直接创建该字段及索引。

`QueryCallTraces` 接收 `CallTraceFilter` 和必填的 `TimeRange`，支持账号 ID、用户名、HTTP 错误码及精确文本搜索。只有应用层授权后才启用 `SearchUsernames`；归属条件独立于文本 OR 条件，不能通过搜索跨用户读取记录。接口提供分页和匹配总数，列表返回摘要；`GetCallTrace` 使用日期与记录 ID 获取详情。调用记录额外持久化请求和响应字节数，但不在 JSON 响应中显示。

`CleanupCallTrace` 按 UTC 日期清理调用数据，不删除用户、账号或 Key。记录包含用于用户关联的 APIKey 信息，管理 API 返回前清空；最终权限由 API Handler 和查询范围共同决定。不存在的详情使用 `ErrCallTraceNotFound`。

## 代理日志

`QueryProxyLogs` 接收 `ProxyLogFilter`、分页参数，返回当前页记录、匹配总数和错误。`ProxyLogFilter` 中 `GroupID` 与 `TimeRange` 必填，`ProxyURL` 和 `AppType` 可选；可选字段非空时分别精确匹配对应字段。SQLite 使用 `proxy_logs_YYYYMMDD` 分表，按 UTC 日期写入，查询按时间倒序返回。每条记录包含 `app_type` 字符串。

```go
QueryProxyLogs(filter ProxyLogFilter, page, pageSize int) ([]PersistedProxyLog, int, error)
QueryCallTraces(filter CallTraceFilter, page, pageSize int) ([]PersistedCallTraceSummary, int, error)
```

`CleanupProxyLog(days)` 清理指定天数以前的完整代理日志分表；`days == 0` 清理今天以前的分表。MemoryDatabase 不缓存代理日志，记录、查询和清理方法均返回不支持错误。

## Proxy 包的存储边界

代理组的 `Config` 和 `State` 是 database 层的不透明 JSON 文档。
