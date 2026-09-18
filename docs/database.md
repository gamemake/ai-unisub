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

实体 ID 包括账号、用户、API Key、代理组和调用记录 ID，以及 `UserID`、`AccountID`、`GroupID` 等关联字段。

## MemoryDatabase 缓存

`NewMemoryDatabase` 只用于构造 SQLite 内部缓存，不应作为 `Database` 传给业务层。缓存内容包括：

- 账号、用户、API Key 和代理组；
- Module Config；
- Credential。

SQLite 启动时从持久化表加载实体缓存。Module Config 和 Credential 在成功从 SQLite 读取后写入缓存；写入或删除 SQLite 成功后同步更新缓存。调用记录和代理日志不进入缓存，始终直接使用 SQLite。

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

## 调用记录与代理日志

调用记录和代理日志按 UTC 日期写入日表：

- `call_traces_YYYYMMDD`
- `proxy_logs_YYYYMMDD`

`QueryCallTraces` 和 `QueryProxyLogs` 支持分页与过滤，过滤条件中的 `TimeRange` 必须同时填写起止时间，且开始时间不得晚于结束时间；数据库不限制该范围是否落在最近 30 天。`GetCallTrace` 使用 UTC 日期和 `int` 类型记录 ID 查询完整内容。

当前实现不提供旧数据库结构迁移。使用旧 schema 的数据库文件需要删除后重新创建。
