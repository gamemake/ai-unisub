# UniSub 应用层

`cmd/unisub/main.go` 处理进程配置和启动；`internal/unisub.New` 创建 service、确保初始管理员、恢复数据库中的 AIProvider 实例，并注册应用模块。初始化失败关闭已创建的服务。

| 模块 | 文件 | 路由 |
| --- | --- | --- |
| static | `internal/unisub/static.go` | `/`、`/login`、`/home`、`/logout`、`/api/login`、Vite 资源 |
| api | `internal/unisub/api.go` | `/api/me`、`/api/ai-providers`、`/api/keys`、`/api/users`、`/api/password`、`/api/calls`、`/api/usage/*`、`/api/proxy-groups` |
| oauthflow | `internal/unisub/oauthflow.go` | `/api/oauth/*` 及 OAuth 回调 |
| gateway | `internal/unisub/gateway.go` | `/v1/*` |

模块只通过 `service.ModuleContext` 获取共享能力。框架不导入应用层，AIProvider 不导入数据库或 Web 页面。

## Web 资源

Vite 输出 `internal/web/dist`，`internal/web/embed.go` 使用 `//go:embed all:dist`。DEV 注入 `os.DirFS`，每次请求读取最新构建；PRD 注入嵌入的 FS，完全忽略磁盘资源目录。入口检查 `index.html` 是否存在，缺少构建时明确报错。

`/login` 和 `/home` 返回同一个 React 入口，服务端先检查会话并重定向。仅登录用户能进入 `/home`；管理 JSON API 自行鉴权，不信任前端菜单隐藏。静态资源不做目录列表，也不会把未知 API 地址重写成 SPA HTML。

## 客户端状态

`src/data/client.ts` 处理会话请求、JSON、错误转换和 401；`store.ts` 统一提供 query hooks 和 mutation actions。TanStack Query 去重请求、缓存资源并在变更后使对应查询失效，组件根据查询状态刷新。UI 不直接 `fetch`。

页面包括登录、个人总览、API Key、调用记录、安全设置、系统总览、订阅管理、用户管理、代理管理。管理员菜单只对管理员显示，权限仍由后端强制验证。对话框使用 Radix/shadcn 的焦点管理与键盘操作。

## 网关

`Authorization: Bearer` → 验证 User/Key/Account → AIProviderManager 的运行时 Account → FIFO 并发队列 → AIProvider 转发 → 调用记录。启动时加载已有账号配置，故重启后不需要重新添加订阅。

请求体最多 64 MiB；默认每账号最多 100 个排队请求、180 秒排队超时，默认请求总超时 5 分钟。流式响应直接写回，响应日志截取前 1 MiB。日志展示的认证请求头被遮蔽。WebSocket upgrade 尚未实现。
