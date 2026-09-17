# Common 公共工具

`internal/common` 提供公共错误输出与统一日志能力，不依赖具体业务实现。

| 文件 | 能力 |
| --- | --- |
| `errors.go` | 通用客户端错误消息和 JSON 输出 |
| `log.go` | 统一日志初始化、级别过滤与结构化输出 |

## JSON 错误

```go
func WriteError(w http.ResponseWriter, status int, message string)
```

WriteError 设置 `Content-Type: application/json; charset=utf-8`，写入指定 HTTP 状态并编码单字段响应：

```json
{"error":"invalid request"}
```

Message 常量提供通用英文错误文本，例如 MessageUnauthorized。`WriteError` 使用传入的状态码和消息，不自动分类错误，也不自动记录日志。

## 统一日志

Common 提供统一的日志基础设施，负责级别过滤、格式化与输出。模块标识、事件名和消息内容由调用方提供，Common 不解释业务语义。

### 初始化与输出

```go
type LogConfig struct {
    Level  slog.Level
    Output io.Writer
}

func InitLogging(config LogConfig) error
func ModuleLogger(module string) Logger

func (l Logger) Debug(event, msg string)
func (l Logger) Info(event, msg string)
func (l Logger) Warn(event, msg string)
func (l Logger) Error(event, msg string)
func (l Logger) DebugAttrs(event string, attrs ...slog.Attr)
func (l Logger) InfoAttrs(event string, attrs ...slog.Attr)
func (l Logger) WarnAttrs(event string, attrs ...slog.Attr)
func (l Logger) ErrorAttrs(event string, attrs ...slog.Attr)
func (l Logger) StandardLogger(level slog.Level, event string) *log.Logger
```

`DebugAttrs`、`InfoAttrs`、`WarnAttrs` 和 `ErrorAttrs` 与对应的普通日志方法使用相同的级别过滤和输出链路，但额外接收 `slog.Attr`，将调用方提供的字段作为额外 JSON 字段输出。适合记录 `provider`、`operation`、`status`、`duration_ms` 等可查询的结构化信息。

例如：

```go
common.ModuleLogger("oauth").InfoAttrs("http_request_completed",
    slog.String("provider", "grok"),
    slog.String("operation", "poll_device_token"),
    slog.Int("status", 200),
    slog.Int64("duration_ms", 183),
)
```

- 以标准库 `log/slog` 为基础，提供统一初始化和取得模块 Logger 的入口；初始化时接收日志级别与输出目标配置。
- 默认级别为 `INFO`，默认向标准错误输出单行 JSON；初始化前使用相同的默认配置。
- `LogConfig{}` 使用默认配置，`Output == nil` 表示标准错误。只接受四个标准级别；非法级别返回错误并保留原配置。输出目标由调用方管理，Common 不关闭它。
- 初始化原子替换共享配置，已创建的 Logger 同样使用新配置。
- Logger 绑定固定的模块标识。普通日志方法接收级别、事件名和消息内容；`*Attrs` 方法还可接收 `slog.Attr`，用于输出可查询的结构化字段，不修改已绑定的模块标识。
- 空白模块标识及零值 Logger 使用 `unknown`，空白事件名使用 `message`；`msg` 按传入内容记录。
- 输出支持并发调用，保证每条日志为完整的单行 JSON，不与其他日志交错。
- `StandardLogger` 用于适配要求 `*log.Logger` 的接口，绑定级别与事件名，并通过同一输出链路写入 JSON；级别须为四个标准级别之一。Common 本身不调用适配器的 `Fatal` 或 `Panic` 方法。
- Logger 不主动退出进程或触发 panic；不提供文件轮转、持久化存储或远程采集能力。

### 日志级别与字段

| 级别 | 含义 |
| --- | --- |
| `DEBUG` | 排障细节与操作过程，默认关闭 |
| `INFO` | 正常运行信息 |
| `WARN` | 需要关注但可恢复的异常 |
| `ERROR` | 操作失败或严重异常 |

仅输出不低于配置级别的日志，级别顺序为 `DEBUG` < `INFO` < `WARN` < `ERROR`。

每条日志至少包含以下五个基础字段，全部必填。通过 `*Attrs` 方法传入的属性会作为额外 JSON 字段输出，字段类型遵循 `slog.Attr`。

| 字段 | 类型 | 说明 | 示例 |
| --- | --- | --- | --- |
| `time` | string | 日志记录时间，使用 RFC 3339 格式的 UTC 时间，以 `Z` 结尾 | `2026-09-16T08:00:00.000Z` |
| `level` | string | 日志级别：`DEBUG`、`INFO`、`WARN`、`ERROR` | `INFO` |
| `module` | string | 调用方提供并由 Logger 绑定的模块标识 | `example` |
| `event` | string | 调用方提供的稳定、机器可读的事件名 | `operation_completed` |
| `msg` | string | 面向阅读者的事件描述，包含必要的操作详情与错误原因 | `Operation completed in 25 ms` |

普通日志方法的具体操作信息按需写入 `msg`；需要机器查询的字段应通过 `*Attrs` 方法传入。`*Attrs` 方法传入的字段会作为额外 JSON 字段输出，字段类型遵循 `slog.Attr`。字段顺序不作为契约。

### 日志输出内容样例

以下为输出格式样例，`example` 是示例模块标识，不对应具体业务模块。每行是独立的 JSON 对象；第一条仅在启用 `DEBUG` 时输出。

```jsonl
{"time":"2026-09-16T08:00:00.000Z","level":"DEBUG","module":"example","event":"operation_started","msg":"Starting operation"}
{"time":"2026-09-16T08:00:00.025Z","level":"INFO","module":"example","event":"operation_completed","msg":"Operation completed in 25 ms"}
{"time":"2026-09-16T08:01:00.000Z","level":"WARN","module":"example","event":"operation_retry","msg":"Attempt 1 failed; retry in 500 ms"}
{"time":"2026-09-16T08:01:00.500Z","level":"ERROR","module":"example","event":"operation_failed","msg":"Operation failed: timeout"}
```

### 能力边界

- 日志仅负责记录，不改变调用方的控制流程，不自动重试或恢复操作。
- 不自动采集业务上下文，不判断哪些事件应当记录，也不对重复调用进行去重。
- 日志输出失败不得触发递归日志或 panic；不承诺异常退出时零丢失。
- JSON 错误输出与日志输出相互独立，调用 `WriteError` 不会自动产生日志。

## 验证边界

- JSON 错误：验证 HTTP 状态、Content-Type、单字段 JSON 格式及消息内容。
- 日志：验证默认配置、级别过滤、模块标识绑定、UTC 时间格式、基础字段，以及 `*Attrs` 方法输出结构化属性。
- 输出：验证消息中的引号与换行正确进行 JSON 编码，并发输出不交错，输出失败不会递归记录或触发 panic。
