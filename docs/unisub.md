# UniSub 应用层

系统分层、包依赖与共享状态归属见 [系统结构](architecture.md)。本文维护应用组装、模块职责和接口索引。

`cmd/unisub/main.go` 处理进程配置和启动；`internal/unisub.New` 创建 Service、确保初始管理员、恢复数据库中的 AIProvider 实例，并注册应用模块。初始化失败时关闭已创建的服务。

## 应用组装

1. 按 unisub.Config.Mode 选择静态资源：DEV 读取本地构建目录，PRD/PROD 使用嵌入资源；检查 index.html。
2. 创建 Service 与数据库、OAuth、AIProvider、认证及代理管理依赖。
3. EnsureAdmin 确保配置中的管理员账号存在；同名账号已有密码散列时不覆盖，散列为空时补上初始密码。
4. 从数据库恢复账号配置并创建运行时实例；恢复失败则初始化失败。
5. 按 static、api、oauthflow、gateway 顺序注册模块，返回 Service 的 HTTP Handler 给入口监听。

模块通过 `service.ModuleContext` 获取共享能力。框架不导入应用层，AIProvider 不导入数据库或 Web 页面。四个模块均属于 `internal/unisub`，文档使用 unisub-* 命名；Service 只提供模块机制。

页面入口统一为 `/`：Static 只返回静态网页和资源，API 负责登录与登出。

## 模块职责

| 模块 | 实现文件 | 职责 | 文档 |
| --- | --- | --- | --- |
| static | `internal/unisub/static.go` | 仅返回根路径静态网页与构建资源 | [Static](unisub-static.md) |
| api | `internal/unisub/api.go` | 登录／登出接口、普通管理 JSON API、用量与代理组 | [API](unisub-api.md) |
| oauthflow | `internal/unisub/oauthflow.go` | OAuth JSON API、公开回调和结果交接 | [OAuthFlow](unisub-oauthflow.md) |
| gateway | `internal/unisub/gateway.go` | 基于 Key 绑定账号的 /v1/ 转发 | [Gateway](unisub-gateway.md) |

路由采用最长匹配：/api/login、/api/logout 精确路径与 /api/oauth/ 前缀优先于 /api/，各业务前缀优先于静态模块的 /。登录与登出由 API 模块注册为 AuthNone；方法和资源权限在具体 Handler 内检查。

## API 模块接口索引

共享认证接口与实现见 [Service](service.md)，各接口的业务授权由对应应用模块文档维护。

除登录／登出接口外，以下接口均先要求 Session；管理员标记表示额外角色限制。Body、响应和边界行为以 [API](unisub-api.md) 为准。

| 方法 | 路径 | 用途 | 额外权限 |
| --- | --- | --- | --- |
| POST | `/api/login` | 登录，创建 Session 并返回 JSON | AuthNone |
| POST | `/api/logout` | 幂等登出，清除 Session 并返回 JSON | AuthNone，无有效会话也成功 |
| GET | `/api/me` | 当前用户及服务版本 | 本人 |
| POST | `/api/password` | 修改密码 | 本人 |
| GET、POST | `/api/users` | 列出／创建用户 | 管理员 |
| PUT、DELETE | `/api/users/{id}` | 编辑／删除用户 | 管理员 |
| POST | `/api/users/{id}/password` | 重置密码 | 管理员，不能重置本人 |
| GET | `/api/accounts` | 列出账号 | 管理员 |
| POST | `/api/accounts` | 创建账号 | 管理员 |
| PUT、DELETE | `/api/accounts/{id}` | 编辑／删除账号 | 管理员 |
| GET、POST | `/api/keys` | 列出／创建 Key | 本人 |
| GET、DELETE | `/api/keys/{id}` | 读取／删除 Key | Key 所属用户 |
| GET | `/api/calls` | 调用摘要列表 | 普通用户仅本人；管理员可查全部 |
| GET | `/api/calls/{day}/{id}` | 调用详情 | 归属校验或管理员 |
| GET | `/api/usage/subscriptions` | 按账号统计用量 | 管理员 |
| GET | `/api/usage/users` | 按用户统计用量 | 管理员 |
| GET、POST | `/api/proxy-groups` | 列出／创建代理组 | 管理员 |
| GET、PUT、DELETE | `/api/proxy-groups/{id}` | 读取／替换／删除组 | 管理员 |
| POST | `/api/proxy-groups/test` | 测试组内代理或 URL | 管理员 |
| POST | `/api/proxy-groups/{id}/test` | 测试组内代理 | 管理员 |
| GET | `/api/proxy-groups/errors` | 读取代理错误记录 | 管理员 |

账号与供应商分别使用 `/api/accounts` 和 `/api/suppliers`，不再以 provider 混称管理资源。

## OAuthFlow 接口索引

| 方法 | 路径 | 用途 | 认证 |
| --- | --- | --- | --- |
| POST | `/api/oauth/{service}/start` | 启动授权 | Session |
| GET | `/api/oauth/{service}/status/{session}` | 查询状态与 PKCE 结果 ID | Session + 归属 |
| POST | `/api/oauth/{service}/complete/{session}` | 提交 PKCE code/state | Session + 归属 |
| POST | `/api/oauth/{service}/poll/{session}` | Device Flow 轮询 | Session + 归属 |
| GET | `/api/oauth/results/{id}` | 一次性读取 Credential | Session + 归属 |
| GET | `/auth/callback`、`/callback`、`/oauth/code/callback` | 公开回调 | OAuth state 校验 |

这些 JSON 路径虽然以 /api/ 开头，仍由 OAuthFlow 注册和处理，不属于普通 API 模块。

## Static 与 Gateway 入口

| 方法 | 路径 | 模块与行为 |
| --- | --- | --- |
| GET、HEAD | `/` | Static；始终返回相同的静态网页，不判断会话或重定向 |
| GET、HEAD | `/assets/*` 等存在的公共资源 | Static；公开静态文件 |
| 上游支持的方法 | `/v1/*` | Gateway；Bearer auth token 或 X-Api-Key 认证，转发到绑定账号 |

网关不声明所有 /v1/ 能力都可用，模型与媒体端点由实际账号和上游决定；当前不支持 WebSocket upgrade。

## 代理领域边界

**采用独立 `internal/proxy` 包**：代理包拥有代理组、调度、探测和状态统计；Service 负责构造与注入；API 模块只处理 HTTP 与管理员权限；AIProvider 使用窄接口选择代理和报告结果。

代理优先级、全局地址状态、应用维度与历史统计由 [Proxy](proxy.md) 定义并实现。

## 前端与持久化

React 页面通过 `src/data/` 访问接口和订阅缓存，shadcn/ui Base UI 组件承载交互。页面入口统一为 `/`，前端通过 `/api/me` 判断会话，在同一页面显示登录界面或 Dashboard；登录、登出和会话失效不跳转到独立页面路径。Static 不依赖认证与数据库；API、OAuthFlow 和 Gateway 按各自契约使用共享能力。

用户、账号、Key、Credential 和调用记录由 Database 保存；浏览器 Session、OAuth Session、OAuth 临时结果及账号队列是内存状态。配置和启动命令见 [README](../README.md)，框架生命周期见 [Service](service.md)。
