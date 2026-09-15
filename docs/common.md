# Common 公共工具

`internal/common` 提供跨模块的基础机制，不依赖 database、oauth、aiprovider、service 或 unisub。

| 文件 | 能力 |
| --- | --- |
| `errors.go` | 通用客户端错误消息和 JSON 输出 |

## JSON 错误

```go
func WriteError(w http.ResponseWriter, status int, message string)
```

WriteError 设置 `Content-Type: application/json; charset=utf-8`，写入指定 HTTP 状态并编码单字段响应：

```json
{"error":"invalid provider config"}
```

Message 常量提供通用英文文本，例如 MessageUnauthorized、MessageInvalidAIProviderConfig、MessageOAuthUpstreamFailed。调用方负责将内部错误映射为可公开消息，Common 不自动分类数据库或上游错误。

当前 Handler 仍有字符串字面量、err.Error() 和标准 http.NotFound 路径，不能声称全部错误都经过统一常量或 JSON 包装。前端只对部分错误做中文映射，其他显示原文，见 [Static](unisub-static.md)。

## 验证边界

验证 HTTP 状态、Content-Type、单字段 JSON 格式和公共错误消息。通用错误输出不代表业务权限已经校验，也不自动保证调用方传入的错误消息适合公开。
