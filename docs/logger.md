# Logger 统一日志

`internal/logger` 提供全局日志初始化、级别过滤和结构化输出。它不解释业务语义，也不持有业务 error、客户端错误消息或 HTTP JSON 错误响应。

| 文件 | 能力 |
| --- | --- |
| `log.go` | 日志配置、模块 Logger、结构化输出与标准库 Logger 适配 |
| `errors.go` | logger 包自身的错误声明 |

## 初始化与接口

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

- 默认级别为 `INFO`，默认向标准错误输出单行 JSON；`LogConfig{}` 使用该默认配置。
- 只接受 `DEBUG`、`INFO`、`WARN`、`ERROR` 四个标准级别。非法级别返回错误，并保留原配置。
- `Output` 由调用方管理，logger 不关闭它。初始化会原子替换共享配置，已经创建的 Logger 也使用新配置。
- 空白模块名及零值 Logger 使用 `unknown`，空白事件名使用 `message`。
- 输出支持并发调用，每条日志保持为完整的单行 JSON。输出失败不改变调用方控制流，也不递归记录或触发 panic。
- `StandardLogger` 用于适配要求 `*log.Logger` 的接口；它仍通过同一输出链路写入 JSON。

## 模块接入约束

所有 Go 模块都必须使用 `internal/logger`，并在模块包的 `logger.go` 中集中声明唯一的 `ModuleLogger`：

```go
package oauth

import "ai-unisub/internal/logger"

var ModuleLogger = logger.ModuleLogger("oauth")
```

包内其他文件直接调用该变量：

```go
ModuleLogger.InfoAttrs("authorization_completed",
    slog.String("service", service),
)
```

不得在其他文件重复调用 `logger.ModuleLogger`，不得直接使用 `slog` 的包级输出函数，也不得为模块另建独立日志链路。模块标识必须稳定，不能包含请求、用户或凭据等动态数据。

## Error 归属

logger 包只声明日志设施自身产生的 error。每个业务包的 package-level error、客户端安全错误消息和本包错误响应辅助函数必须放在该包自己的 `errors.go` 中；调用点可以通过 `%w` 增加上下文。业务错误不得放入 `internal/logger`，也不建立跨领域的公共错误消息包。

## 日志字段

每条日志至少包含以下基础字段；`*Attrs` 方法接收的 `slog.Attr` 作为额外 JSON 字段输出。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `time` | string | RFC 3339 格式的 UTC 时间 |
| `level` | string | `DEBUG`、`INFO`、`WARN` 或 `ERROR` |
| `module` | string | Logger 绑定的稳定模块标识 |
| `event` | string | 稳定、机器可读的事件名 |
| `msg` | string | 面向阅读者的事件描述；Attrs 方法未传消息时为空字符串 |

字段顺序不作为契约。普通方法将说明写入 `msg`；需要查询、筛选或聚合的信息应使用 `*Attrs` 方法传入类型明确的字段。

```jsonl
{"time":"2026-09-16T08:00:00Z","level":"INFO","msg":"service started","module":"service","event":"started"}
{"time":"2026-09-16T08:01:00Z","level":"WARN","msg":"","module":"oauth","event":"authorization_failed","service":"openai"}
```

## 能力边界与验证

- 日志只负责记录，不自动重试、恢复、退出进程或触发 panic。
- 不自动采集业务上下文；敏感字段是否可记录由调用包负责判断。
- 不提供文件轮转、持久化存储或远程采集。
- 测试覆盖默认配置、级别过滤、模块标识、UTC 时间、结构化字段、并发输出、配置切换、标准库适配及输出失败行为。
