# Database 模块

`internal/database` 提供 SQLite 持久化实现。`MemoryDatabase` 不是数据库后端，也不实现 `Database` 接口；它只作为 `SQLiteDatabase` 的进程内缓存。

## 数据库地址

`NewDatabase` 只接受 SQLite URL：

| 地址 | 含义 |
| --- | --- |
| `sqlite://./data/ai-unisub.db` | 当前工作目录下的数据库文件 |
| `sqlite:///var/lib/app/data.db` | Unix 绝对路径 |
| `sqlite:///C:/data/app.db` | Windows 绝对路径 |
| `sqlite::memory:` | 进程内 SQLite 数据库，主要用于测试 |

## ID 规则

数据库实体的主键和外键均为 `int`，由 SQLite 的 `INTEGER PRIMARY KEY AUTOINCREMENT` 自动生成。创建实体时将 `ID` 保持为 `0`，`Save*` 成功后会把生成的 ID 回写到传入对象；更新时传入已有的正数 ID。

以下标识不是数据库实体自增 ID，继续使用 `string`：

- Credential ID
- RequestID
- SessionID
- API Key 本身
- OAuth SubjectID

实体 ID 包括账号、用户、API Key、代理组、通用配置对象和调用记录 ID，以及 `UserID`、`AccountID`、`GroupID` 等关联字段。

## MemoryDatabase 缓存

`NewMemoryDatabase` 只用于构造 SQLite 内部缓存，不应作为 `Database` 传给业务层。缓存内容包括：

- 账号、用户、API Key 和代理组；
- 通用配置对象（Config）；
- Module Config；
- Credential。

SQLite 启动时从持久化表加载实体缓存（含 Config）。Module Config 和 Credential 在成功从 SQLite 读取后写入缓存；写入或删除 SQLite 成功后同步更新缓存。调用记录和代理日志不进入缓存，始终直接使用 SQLite。

## Database 接口

`Database` 负责数据库生命周期和持久化操作：

```go
Open() error
Close() error
SaveAccount(*PersistedAccount) error
SaveUser(*PersistedUser) error
SaveAPIKey(*PersistedAPIKey) error
SaveProxyGroup(*PersistedProxyGroup) error
```

`Save*` 使用 `ID == 0` 创建记录，使用正数 ID 更新记录。删除和查询方法的实体 ID 参数也使用 `int`。

Module Config 使用模块名作为字符串键；Credential 使用 OAuth 提供的字符串 ID，并以不透明 JSON 保存。OAuth 的 `CredentialStore` 契约保持不变。

通用配置对象（`PersistedConfig`）字段为 `id`、`type`、`name`、`value`（不透明 JSON）。`(type, name)` 唯一。接口提供：

- `ListConfigs` / `ListConfigsByType`：列出全部或指定类型；
- `LoadConfig(type, name)`：按类型与名称加载；不存在时返回零值（`ID == 0`）和 `nil` error；
- `SaveConfig`：创建或更新；更新时只允许修改 `value`，`type` 与 `name` 不可变；
- `DeleteConfig(id)`：按 ID 删除。

通用配置与 Module Config 独立：后者仍是「模块名 → 单份 JSON」；前者用于同类型下多条命名配置对象。

模型供应商可配置覆盖使用 `type=supplier`、`name=<supplier id>`；`value` 仅为相对代码缺省的 overlay。diff 由 `aiprovider` 计算，database 不解释 `value` 内容。

## 调用记录与代理日志

调用记录和代理日志按 UTC 日期写入日表：

- `call_traces_YYYYMMDD`
- `proxy_logs_YYYYMMDD`

`QueryCallTraces` 和 `QueryProxyLogs` 支持分页与过滤，过滤条件中的 `TimeRange` 必须同时填写起止时间，且开始时间不得晚于结束时间；数据库不限制该范围是否落在最近 30 天。`GetCallTrace` 使用 UTC 日期和 `int` 类型记录 ID 查询完整内容。

账号用量与用户用量在数据库层按调用记录聚合，不把明细行拉入应用内存：

- `QueryAccountUsage(timeRange)`：按 `account_id` 聚合 `COUNT` 与各类 token `SUM`，返回 `[]AccountUsageRow` 与合计 `UsageTotals`；
- `QueryUserUsage(timeRange, accountID)`：按 API Key 归属的 `user_id` 聚合（`LEFT JOIN api_keys`，`apikey` 匹配 `key_value` 或 key id 字符串）；`accountID == 0` 表示全部账号，否则仅该账号；无法归属的记录归入 `user_id = 0`。

两方法均在日表 `call_traces_YYYYMMDD` 上做 `UNION ALL` 后 `GROUP BY`，只扫描时间范围内的日表。

调用记录日表在写入与查询前会 `ensureTraceTable`：新建表使用当前 schema；已存在的旧日表通过 `ALTER TABLE ... ADD COLUMN` 补齐缺失列（当前含 `session_id`、`outbound_url`），旧行对应字段读为空默认值。其它核心表仍不提供破坏性迁移；无法加法兼容的旧库需删除后重建。

`url` 为客户端入站请求路径（如 `/v1/messages`）；`outbound_url` 为实际上游完整 URL。管理端 quota／models 查询写入的记录两者均为上游地址。
