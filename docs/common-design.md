# Common 设计

## 1. 目标

`internal/common` 提供多个内部模块都会使用、但不属于某一个业务域的基础能力。目前包含两部分：

- HTTP Proxy：在请求 Context 中传递代理配置，并校验代理 URL；
- HTTP Error：输出统一的 JSON 错误响应。

Common 不依赖 `oauth`、`provider`、`service` 或 `database`，业务包可以依赖 Common，而 Common 不反向依赖业务包，从而避免横向依赖和循环 import。

目录结构如下：

```text
internal/common/
├── errors.go   # 统一 HTTP JSON 错误响应
└── proxy.go    # HTTP Proxy Context 与 URL 校验
```

## 2. Proxy

### 2.1 Context 传递

```go
func WithHTTPProxy(ctx context.Context, proxy string) context.Context
func HTTPProxyFrom(ctx context.Context) string
```

`WithHTTPProxy` 会去除代理字符串首尾空白。Context 为 `nil` 时使用 `context.Background()`；代理为空时返回原 Context，不写入空值。`HTTPProxyFrom` 在 Context 为空、没有代理或值类型不正确时返回空字符串。

代理配置通过不可导出的 Context key 保存，避免与其他包的 key 冲突。调用方不应直接构造或依赖 key，而应使用上述两个函数。

典型流程是：HTTP handler 解析用户输入后把代理写入 Context，OAuth Manager 将代理保存到短期 Session，后续 callback 或 Device Flow poll 再恢复到 outbound request Context。

```text
请求 proxy
    │
    ▼
ParseHTTPProxy ── invalid ──> 400 JSON error
    │ valid
    ▼
WithHTTPProxy(request.Context(), proxy)
    │
    ▼
HTTP/OAuth client 读取 HTTPProxyFrom(ctx)
```

### 2.2 URL 校验

```go
func ParseHTTPProxy(proxy string) (*url.URL, error)
```

空字符串表示不使用代理，返回 `(nil, nil)`。非空值必须同时具备 Scheme 和 Host，并且 Scheme 只能是 `http`、`https`、`socks5` 或 `socks5h`。

不支持的 Scheme、缺少 Host 或无法解析的值统一返回 `invalid proxy`。校验函数只负责语法和 Scheme 白名单，不负责连接代理或验证代理是否可达；实际网络失败由 outbound client 返回。

## 3. HTTP Error

### 3.1 响应模型

错误响应不定义额外的公开结构体，使用固定的单字段 JSON：

```json
{"error":"invalid provider config"}
```

`error` 字段是稳定的英文错误消息，同时作为前端多语言资源的 key 和找不到翻译时的英文兜底。修改已发布的错误消息等同于修改 API 契约。Common 不负责把内部 `error` 自动暴露给客户端，调用方应先将数据库、上游服务和认证错误映射为安全的公共消息。

### 3.2 输出函数

```go
func WriteError(w http.ResponseWriter, status int, message string)
```

`WriteError` 设置 `Content-Type: application/json; charset=utf-8`，写入 HTTP status，然后通过内部匿名结构体编码固定的 `error` 字段。

### 3.3 公共错误消息

所有 Web handler 使用的客户端可见错误消息都定义在 `common/errors.go` 的 `Message...` 常量中，例如 `MessageUnauthorized`、`MessageInvalidProviderConfig` 和 `MessageOAuthUpstreamFailed`。模块只引用这些常量，不在 handler 中新增字符串字面量。

内部错误不能直接通过 `err.Error()` 写入响应；应根据错误类型映射到 Common 的安全公共消息。这样可以避免把数据库细节、Provider 配置、上游响应或其他敏感信息泄漏给 Web 客户端，同时保证相同语义的错误使用稳定文本。

## 4. 包边界与兼容入口

新代码应直接依赖 `ai-unisub/internal/common`。现有 `oauth` 的 Proxy 函数保留为兼容转发入口；Service handler 直接调用 Common。新的跨模块代码不应继续把通用能力绑定到 OAuth 或 Service 包。

Common 只提供机制，不提供业务错误码、OAuth 错误或 Provider 错误。业务包继续拥有自己的领域错误，并在 HTTP 边界调用 Common 完成安全映射和输出。

## 5. 测试要求

- Proxy：覆盖空值、首尾空白、四种支持的 Scheme、缺少 Scheme/Host、非法 URL，以及 Context 读写和 nil Context；
- Error：覆盖单字段 JSON、Content-Type 和 HTTP status；
- 集成：OAuth callback、Device Flow 和 Provider outbound request 均应使用同一套 Common Proxy 语义。
