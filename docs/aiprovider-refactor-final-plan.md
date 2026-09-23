# `internal/aiprovider` 最终重构方案

> 状态：临时设计文档
>
> 目标：在不改变现有 API、持久化格式和运行行为的前提下，明确 `aiprovider` 的层次，降低 Manager 和 OAuth Provider 的职责密度。

## 一、最终目标

重构后按职责划分为以下层次：

```text
contract       公共数据结构、接口、错误和调用 Trace
supplier       Supplier、overlay、模型映射、订阅计划
quota          配额查询、解析、校验、缓存和订阅用量
transport      HTTP 转发、代理、重试、响应流和认证恢复
adapter        Claude、Codex、Grok、Dummy 等具体 Provider
runtime        Manager、Account、Group、实例路由和健康状态
```

依赖方向：

```text
contract
  ↑
supplier  quota   transport
      ↑      ↑       ↑
             adapter
                ↑
              runtime
```

底层模块不能反向依赖 `runtime`，`quota` 不负责 HTTP 转发，`transport` 不知道 Supplier 持久化细节。

## 二、目标目录

第一阶段不强制一次性移动所有代码，先按以下目录建立最终边界：

```text
internal/aiprovider/
├── contract/
│   ├── config.go          # AIProviderConfig、ProviderData
│   ├── state.go           # AIProviderState、AIProviderQuota
│   ├── provider.go        # AIProvider、AIProviderFactory
│   ├── trace.go           # AIProviderCallTrace、APICallRecorder
│   └── errors.go          # 对外错误
├── supplier/
│   ├── supplier.go        # Catalog、Supplier
│   ├── overlay.go         # overlay 合并、diff、校验和存储接口
│   ├── models.go          # Supplier 模型及模型映射
│   └── plans.go           # subscription plan 和 usage weight
├── quota/
│   ├── service.go         # QuotaService
│   ├── cache.go           # quota cache
│   ├── query.go           # 查询流程和认证恢复
│   ├── parse.go           # 各 Supplier 响应解析
│   ├── schema.go          # wire schema 校验
│   └── subscription.go    # subscription header/body 用量
├── transport/
│   ├── forwarder.go       # HTTP 请求执行
│   ├── proxy.go            # ProxyResolver 适配
│   ├── retry.go            # 重试和错误分类
│   ├── response.go         # 流式响应和响应头转发
│   └── auth.go             # TokenSource 和认证恢复
├── adapter/
│   ├── claude.go
│   ├── codex.go
│   ├── grok.go
│   └── dummy.go
└── runtime/
    ├── manager.go          # 注册、创建、查询、删除和更新
    ├── account.go          # 并发额度和 FIFO 等待队列
    ├── group.go            # Provider group 和关系校验
    ├── routing.go          # provider 选择、affinity、health
    └── models.go           # group model 聚合
```

现有顶层 `aiprovider` 包可以暂时保留为兼容 facade，逐步转发到这些子包，避免一次性修改大量调用方。

## 三、各层职责

### 1. `contract`

只放跨层共享的稳定契约：

- Provider 配置、状态和持久化输入输出结构；
- `AIProvider` 和 `AIProviderFactory` 接口；
- API 调用 Trace；
- 对外错误，例如 quota 不支持、models 不支持、队列超时。

该层不能导入数据库、OAuth 实现、HTTP 代理实现或具体 Provider。

### 2. `supplier`

负责 Supplier 的纯领域逻辑：

- builtin Supplier；
- overlay merge/diff；
- Catalog 校验；
- Supplier URL 和支持的 ClientType；
- ModelMapping；
- SubscriptionPlan 和 UsageWeight。

`SupplierOverlayStore` 只定义接口，不实现数据库访问。`supplier` 不持有 `AIProviderManager`。

### 3. `quota`

负责把上游配额转换成统一的 `Quota`：

- quota endpoint 计算；
- Supplier 响应解析和 schema 校验；
- quota cache；
- subscription header/body 更新；
- cache invalidation 和持久化回调。

建议定义窄接口：

```go
type QuotaService interface {
    Fetch(context.Context) (*Quota, error)
    Cached() *Quota
    Invalidate()
}
```

`QuotaService` 可以依赖一个抽象的请求执行器，但不能直接操作 Provider 的 `http.Client` 字段。

### 4. `transport`

负责一次 HTTP 请求的基础设施行为：

- 选择直连或代理；
- 取得和刷新 token；
- 设置认证头；
- 处理 redirect、重试和 dial error；
- 转发普通响应和 SSE 流；
- 捕获请求/响应 Trace。

建议抽出以下接口：

```go
type TokenSource interface {
    Token(context.Context, RequestInfo) (string, error)
    Recover(context.Context, RequestInfo, string) (string, error)
}

type Forwarder interface {
    Do(context.Context, *http.Request) (*http.Response, error)
}
```

### 5. `adapter`

具体 Adapter 只描述协议差异：

- 服务名称和默认 endpoint；
- 配置解码；
- 请求入口；
- quota parser；
- models 查询方式；
- Claude/Codex/Grok 的特殊 header 或认证规则。

公共的配置、认证、重试、响应复制逻辑不得继续复制到 `claude.go`、`codex.go` 和 `grok.go`。

### 6. `runtime`

负责运行时对象关系：

- Provider factory 注册；
- 实例创建、删除和更新；
- Account 并发控制；
- Group 成员关系；
- 路由、affinity 和 health；
- Manager 与外部持久化回调的绑定。

`AIProviderManager` 不再直接实现 Supplier/Catalog 领域算法，也不应该包含 HTTP 转发细节。

## 四、现有文件迁移映射

| 当前文件 | 目标位置 |
|---|---|
| `aiprovider.go` | `contract/` |
| `config.go` | `contract/` 或 `adapter/` 的配置解码部分 |
| `catalog.go` | `supplier/`，其中 Manager 方法改为 SupplierService |
| `catalog_defaults.go` | `supplier/` |
| `clients.go` | `supplier/` 或 `runtime/routing.go` |
| `models.go`、`model_mapping.go` | `supplier/` |
| `subscription_plan.go` | `supplier/plans.go` |
| `quota_*.go` | `quota/` |
| `oauth_aiprovider.go` | 拆入 `adapter/`、`transport/`、`quota/` |
| `claude.go`、`codex.go`、`grok.go` | `adapter/` |
| `dummy.go` | `adapter/` 或 `testsupport/` |
| `aiprovider_manager.go` | `runtime/manager.go` |
| `account.go` | `runtime/account.go` |
| `group.go` | `runtime/group.go`、`runtime/routing.go` |
| `group_models.go` | `runtime/models.go` |
| `call_recorder.go`、`usage.go` | `contract/trace.go` 或 `transport/response.go` |
| `response.go` | `transport/response.go` |

## 五、实施顺序

### 阶段 0：建立基线

- 保留现有行为和 JSON 字段；
- 执行 `go test ./...`；
- 确认 quota、group、routing、streaming 和 OAuth 测试均通过；
- 不在本阶段改动持久化格式。

### 阶段 1：提取契约

- 固化 `contract` 中的接口和数据结构；
- 保留顶层类型别名，兼容旧调用方；
- 将错误定义集中管理；
- 增加接口级测试，而不是只测试具体实现。

### 阶段 2：提取 Supplier

- 先移动纯函数：merge、diff、validate、model mapping；
- 再抽出 overlay store 接口；
- 最后把 Manager 上的 Supplier 方法迁移到 `SupplierService`；
- 保证现有 overlay JSON 完全兼容。

### 阶段 3：提取 Quota

- 将 cache 与 parser 分离；
- 让 quota 通过抽象请求执行器访问上游；
- 保持 quota snapshot 的 state JSON 不变；
- 保留现有 quota 测试，并增加失效、并发和晚到响应测试。

### 阶段 4：拆分 OAuth Provider

- 先抽 `TokenSource`；
- 再抽 `Forwarder`、retry 和 response streaming；
- 将 `oauthAIProvider.handle` 缩减为流程编排；
- 最后把 Claude/Codex/Grok 的差异收敛到 Adapter。

### 阶段 5：收缩 Runtime Manager

- 把 group、routing、health 和 model aggregation 从 Manager 拆出；
- Manager 只保留生命周期和注册职责；
- 删除不再需要的跨层回调和类型断言；
- 使用构造函数注入依赖，减少运行时 `interface{}` 判断。

### 阶段 6：清理兼容层

- 更新所有内部调用方到新包路径；
- 保留必要的类型别名和 facade；
- 确认没有循环依赖后，再删除旧的顶层实现；
- 更新文档和架构图。

## 六、必须保持的兼容性

- Provider 配置 JSON 字段和默认值不变；
- Provider state 和 quota snapshot JSON 不变；
- Supplier overlay 的读写格式不变；
- `AIProviderManager` 的实例 ID 仍使用 `int`；
- API 请求、认证头、redirect 策略和 SSE 行为不变；
- OAuth 失败恢复、代理重试和 quota cache 失效行为不变；
- Group 的成员选择、权重、affinity 和 client intersection 行为不变。

## 七、验收标准

重构完成后至少满足：

1. `go test ./...` 全部通过；
2. `go vet ./...` 无新增问题；
3. `go test -race ./internal/aiprovider/...` 通过；
4. `runtime` 不包含 HTTP 响应解析代码；
5. `supplier` 不依赖数据库实现和 OAuth 实现；
6. `quota` 不直接修改 Provider 的 HTTP client；
7. `adapter` 不重复实现代理、重试和流式复制；
8. 配置、state、overlay 和 quota 的旧 JSON fixture 无需修改；
9. 关键路径具备单元测试：认证恢复、重试、SSE、quota 并发失效、group 路由和 overlay merge。

## 八、明确不做的事情

- 不为了目录好看而拆成大量只有一个文件的 package；
- 不引入通用 DI 框架；
- 不把所有类型都改成 interface；
- 不在重构过程中修改数据库 schema；
- 不把 Provider 特有协议强行抽象成一个复杂的万能配置；
- 不一次性重写所有 Adapter。

## 九、优先级结论

最先处理的两个问题是：

1. 将 `catalog.go` 从 `AIProviderManager` 中独立出来，归入 `supplier` 包；
2. 将 `oauth_aiprovider.go` 拆成 `transport`、`quota` 和 Adapter 编排层。

完成这两步后，整个包的核心层次会从“Manager 调用所有东西”变为“Runtime 组合 Supplier、Quota、Transport 和 Adapter”，后续拆分可以持续小步进行。
