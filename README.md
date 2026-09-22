# UniSub

Go + SQLite AI 网关，统一管理 OpenAI/Codex、Anthropic/Claude、Grok 的订阅凭据和上游 API Key。React Dashboard 使用 Vite、Tailwind CSS、shadcn/ui **Base UI** 组件及 TanStack Query 数据管理层。

网页统一使用 shadcn/ui 的 `base-nova` 源码组件，交互基础为 `@base-ui/react`，不混用 Radix。下拉选择使用 Select，日期使用 Calendar + Popover，移动导航使用 Sheet；表单、表格、状态和确认弹窗也集中在 `src/components/ui/`。组件来源和维护约定见 [组件说明](src/components/ui/README.md)。

模型映射按实际处理请求的账号所属供应商规则改写客户端 `model`（及 Grok 覆盖头）；未配置或未命中则原样转发。

## 开发环境

- Go 1.27.1（以 `go.mod` 为准）
- Node.js 24、npm（依赖版本锁定在 `package-lock.json`）

首次启动前必须先构建前端；`internal/web/dist` 是被忽略的生成目录，不需要提交。

```sh
npm ci
npm run build
go run ./cmd/unisub
```

Windows PowerShell 若禁用脚本执行策略，请使用 `npm.cmd` 替代 `npm`。
默认监听 `http://localhost:8080`。开发初始账号为 `admin` / `admin12345`，可通过下表变量配置；同名账号已有密码散列时不会被启动配置覆盖；散列为空时会补上初始密码。

## DEV / PRD

| 配置 | 默认值 | 用途 |
| --- | --- | --- |
| `UNISUB_MODE` | `PRD` | `DEV` 读取本地构建目录；`PRD` 使用可执行文件内嵌资源（兼容 `PROD`） |
| `UNISUB_WEB_DIR` | `internal/web/dist` | DEV 资源根目录；相对路径以工作目录为基准，PRD 不读取此目录 |
| `DATABASE_URL` | `sqlite://./data/ai-unisub.db` | SQLite 数据库地址 |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `ADMIN_USERNAME` | `admin` | 初始管理员用户名 |
| `ADMIN_PASSWORD` | `admin12345` | 初始管理员密码 |
| `OAUTH_CALLBACK_BASE_URL` | 当前请求的 origin | OAuth 回调公开地址（可选） |

### DEV：由 Go 返回本地静态文件

在项目根目录的终端 A 运行：

```sh
npm run dev
```

终端 B（PowerShell）：

```powershell
$env:UNISUB_MODE = 'DEV'
go run ./cmd/unisub
```

Vite 监听 `src/` 和 `public/` 的变化并更新 `internal/web/dist`；刷新网页即可看到改动，无需重新编译 Go。
如需要 React HMR，可额外运行 `npm run dev:hmr` 并访问 Vite 输出的地址；开发代理默认转发到 `http://127.0.0.1:8080`，可用 `UNISUB_BACKEND_URL` 修改。

### PRD：单文件发布

```sh
npm run build:server
```

构建产物为 `bin/unisub`（Windows 为 `bin/unisub.exe`）。PRD 不依赖 Node.js、源码或工作目录中的静态文件。前端更改需要重新执行前端和 Go 构建。

```sh
docker compose -f deploy/docker/docker-compose.yml up --build -d
```

Docker 使用 Node 前端构建 → Go 编译 → Alpine 运行三阶段构建。

## 目录与模块

包依赖、Service 框架与 UniSub 应用模块之间的关系见 [系统结构](docs/architecture.md)。

```text
cmd/unisub/       服务入口
cmd/oauth/        OAuth 调试工具
cmd/dummy/        本地模拟工具
internal/common/ 通用 JSON 错误与公共错误消息
internal/database/ 统一数据库契约、SQLite 持久化与内部内存缓存
internal/oauth/  OAuth 会话、刷新和适配器
internal/aiprovider/ AI Provider、运行时账号和并发队列
internal/proxy/  代理对象、优先级调度、探测与统计
internal/service/ 模块框架、路由、身份认证与共享服务
internal/unisub/ 应用组装、静态文件、管理 API、OAuth 流程、网关
internal/web/    Vite 产物的 go:embed 入口
src/data/        请求、缓存、查询订阅和变更操作
src/pages/       React 页面
src/components/ui/ shadcn/ui 源码组件
public/          公共静态资源
```

AI 上游模块统一使用 `aiprovider` 包与 `AIProvider*` 类型；管理页面为 `src/pages/ai-providers.tsx`，接口为 `/api/ai-providers`。旧 API、JSON 字段和数据库列保留兼容，具体见 [AI Provider 设计](docs/ai-provider.md)。

代理能力位于独立的 `internal/proxy` 包，统一包含代理对象构造与内部 URL 校验、HTTP 传输配置和代理管理；`oauth` 显式依赖代理包，通过参数接收代理对象，不用 Context 隐式传递代理，`common` 不保留代理文件或工具。Service 注入代理管理能力，UniSub API 提供管理入口，AIProvider 通过窄接口调用。代理组配置沿用原存储表示，新增 `proxy_stats` 保存幂等的 10 分钟聚合；策略与接口见 [Proxy 设计](docs/proxy.md)。

四个业务模块均位于 `internal/unisub`：[Static](docs/unisub-static.md)、[API](docs/unisub-api.md)、[OAuthFlow](docs/unisub-oauthflow.md)、[Gateway](docs/unisub-gateway.md)。`service` 提供模块框架，不承载这些模块的业务归属。

`internal/service/auth.go` 的共享认证接口与实现行为见 [Service](docs/service.md)；登录、登出及管理接口的业务授权见 [UniSub API](docs/unisub-api.md)。

页面与认证契约：`/` 仅返回静态网页，前端在同一入口按会话状态显示登录界面或 Dashboard；登录、登出分别使用 API 模块的 `POST /api/login`、`POST /api/logout`，返回 JSON，不做页面重定向。旧 `/login`、`/home`、`/logout` 路径返回 404。

## 验证

```sh
npm run build
npm test
go test ./...
go vet ./...
npm run test:e2e
```

端到端测试自动使用独立的 SQLite 内存数据库（`sqlite::memory:`）和 `127.0.0.1:28080`，不会读取 `data/` 中的账号或凭据。这里是 SQLite 的存储模式，不是数据库模块中实现 `Database` 并包装 SQL 存储的 `MemoryDatabase`。默认在 Windows 上使用已安装的 Edge；其他系统默认使用 Playwright Chromium（首次需 `npx playwright install chromium`）。可通过 `PLAYWRIGHT_CHANNEL` 指定 `chrome`、`msedge` 等通道。

## API 调用

在控制台添加上游账号，再签发绑定该账号的 API Key。客户端继续使用官方路径，例如 `/v1/responses`、`/v1/messages`、`/v1/chat/completions`。上游由密钥绑定的账号决定，支持自定义 Base URL、上游 API Key 和 OAuth 凭据。

网关保持响应状态和流式字节；调用记录中的响应体最多保存前 1 MiB，完整响应仍发送给客户端。WebSocket upgrade 尚未实现。真实平台的账号可用性与模型能力需要使用对应账号验证；本地自动化测试使用模拟上游。

项目协作约定见 [AGENT.md](AGENT.md)。详细设计入口：[UniSub](docs/unisub.md)、[框架](docs/service.md)、[数据库](docs/database.md)、[AI Provider](docs/ai-provider.md)、[OAuth](docs/oauth.md)、[公共工具](docs/common.md)。
