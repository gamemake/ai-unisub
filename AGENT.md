# UniSub 项目协作说明

UniSub 是 Go + SQLite AI 网关，提供多用户管理、上游订阅凭据与 API Key 管理、官方路径形式的请求转发，以及 React Dashboard。

## 技术与目录

- 服务端：Go，版本以 `go.mod` 为准；SQLite 使用 `modernc.org/sqlite`，无需 CGO。
- Web：Vite + React + TypeScript + Tailwind CSS；shadcn/ui 使用 Base UI 的 `base-nova` 组件，不混用 Radix。
- 数据层：`src/data/` 统一处理请求、TanStack Query 缓存、查询与变更；页面通过数据层更新界面。

| 路径 | 职责 |
| --- | --- |
| `cmd/unisub/` | 服务进程配置和 HTTP 启动 |
| `cmd/oauth/`、`cmd/dummy/` | 独立 OAuth CLI、本地模拟工具 |
| `internal/common/` | 通用 JSON 错误与公共错误消息；不承担代理职责 |
| `internal/database/` | 统一数据库契约、SQLite 持久化与内部内存缓存结构 |
| `internal/oauth/` | OAuth Manager、Session、凭据刷新和协议适配器 |
| `internal/aiprovider/` | AIProvider 工厂、运行时 Account、并发队列和上游调用 |
| `internal/service/` | 模块框架、路由、认证及共享依赖 |
| `internal/unisub/` | 应用组装及 static、api、oauthflow、gateway 模块 |
| `internal/web/` | `go:embed all:dist` 入口；`dist/` 为忽略提交的前端产物 |
| `src/`、`public/` | Web 源码与公共资源 |
| `tests/e2e/` | Dashboard 端到端测试 |

## 模块边界

- `service` 不导入 `unisub`；应用模块通过 `ModuleContext` 使用共享依赖。
- `oauth` 不依赖 Web Handler 或具体 AIProvider；Web 与 CLI 复用协议层。
- `aiprovider` 不依赖数据库或页面，持久化转换由应用层负责。
- 模块职责：`static` 仅通过 `/` 返回静态网页并提供资源，不判断会话或重定向；`api` 负责 `/api/login`、`/api/logout` 与普通管理 API；`oauthflow` 负责全部 OAuth JSON API 与回调；`gateway` 负责 `/v1/` 转发。四者均为 UniSub 应用模块，不是 Service 框架内置模块。
- 页面入口与登录／登出归属已对齐；前端在 `/` 中按会话状态切换登录界面和 Dashboard。
- **代理能力位于独立的 `internal/proxy`**，统一负责代理对象构造与内部 URL 校验、HTTP 传输配置、代理组、调度、探测、状态和统计。`oauth` 显式依赖 `proxy` 并通过参数接收代理对象，不用 Context 隐式传递代理。代理包通过存储接口接入数据库，不依赖 `service`、`unisub`、`oauth` 或具体 AIProvider；`common` 下不包含 `proxy.go`。
- 代理管理由 `internal/proxy` 持有；接口、策略和统计存储见 [Proxy 设计](docs/proxy.md)。

## 开发与验证

首次运行 Go 服务前先执行 `npm ci`、`npm run build`，以满足嵌入资源要求。PowerShell 禁用脚本执行时使用 `npm.cmd`。

| 命令 | 用途 |
| --- | --- |
| `npm run dev` | 监听并构建前端；配合 `UNISUB_MODE=DEV` 的 Go 服务 |
| `npm run dev:hmr` | Vite HMR 开发服务器 |
| `go run ./cmd/unisub` | 启动服务，默认 PRD 模式 |
| `npm run build:server` | 构建前端与可执行文件 |
| `npm test` | 前端单元测试 |
| `go test ./...`、`go vet ./...` | Go 测试与静态检查 |
| `npm run test:e2e` | 构建前端并使用独立的 SQLite 内存数据库运行端到端测试 |

DEV 读取本地前端构建目录；PRD 使用可执行文件中的嵌入资源。配置与运行步骤见 [README](README.md)。检查范围应匹配改动：纯文档更新检查链接、事实和差异，无需生成构建产物。

## 文档维护约定

- 文档以模块职责、接口契约、数据结构和运行机制组织，不承载开发排期、任务清单或历史改造叙述。
- 区分当前实现与目标设计，不将目标能力写成已实现功能，也不以模拟测试替代真实平台验证。
- HTTP 方法、路径、权限和响应以实现为依据；详细接口集中在模块文档，应用总览提供索引。
- 保留未提交的用户修改；只更新当前任务授权的文件，不提交数据库、凭据和生成产物。

## 文档索引

| 主题 | 文档 |
| --- | --- |
| 系统结构与包／模块关系 | [Architecture](docs/architecture.md) |
| 应用与接口总览 | [UniSub](docs/unisub.md) |
| 框架与共享认证实现 | [Service](docs/service.md) |
| 独立代理包设计 | [Proxy](docs/proxy.md) |
| 持久化 | [Database](docs/database.md) |
| OAuth 协议与 CLI | [OAuth](docs/oauth.md) |
| AI 上游与运行时账号 | [AIProvider](docs/ai-provider.md) |
| 公共工具 | [Common](docs/common.md) |
| UniSub 应用模块 | [Static](docs/unisub-static.md)、[API](docs/unisub-api.md)、[OAuthFlow](docs/unisub-oauthflow.md)、[Gateway](docs/unisub-gateway.md) |
| UI 组件 | [组件说明](src/components/ui/README.md) |
