# UniSub Static 模块

Static 模块只负责返回 UniSub 的静态网页与资源，不判断登录状态、不处理登录或登出、不执行页面重定向。它实现 Service 的 Module 接口，但属于 UniSub 应用层。

本文定义页面入口的目标契约；当前 `internal/unisub/static.go` 和前端尚未按此契约调整。本次仅更新文档，不改变运行行为。登录与登出接口归属 [API 模块](unisub-api.md)。

## 路由

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET、HEAD | `/` | 始终返回同一个 Vite index.html，不查询 Session |
| GET、HEAD | `/assets/*` 及其他存在的公共资源 | 从注入的 fs.FS 返回资源 |

这些路由注册为 AuthNone。已登录、未登录或 Session 过期的请求获取相同静态网页；HEAD 仅返回响应头。根路径不接受登录提交，非 GET/HEAD 请求返回 405。

原 `/login` 与 `/home` 页面入口合并到 `/`，不再注册独立页面或兼容重定向；原 `/logout` 不作为静态模块入口。不存在的旧路径按 404 处理，不回退到应用网页。

## 资源与运行模式

应用层根据 `unisub.Config` 注入 fs.FS：

- DEV 使用本地 `UNISUB_WEB_DIR`，默认 `internal/web/dist`。
- PRD（兼容 PROD）使用 `internal/web/embed.go` 嵌入的前端产物。
- 初始化检查 index.html；缺少产物会失败，需先完成前端构建。
- `/` 返回 Vite index.html，不使用服务端业务模板，也不按登录状态生成不同 HTML。
- HTML 设置 no-store；资源设置 no-cache；均设置 X-Content-Type-Options=nosniff。

资源路径必须合法，拒绝反斜线、点文件、目录列表和直接读取 .html。不存在的资源返回 404，不把任意未知 URL 回退成应用 HTML。更具体的 /api/、/api/oauth/、/v1/ 路由优先于静态前缀。

## 前端数据边界

`src/data/client.ts` 统一发送同源 JSON 请求、解析错误与清理缓存；`src/data/store.ts` 提供查询和变更操作。页面通过 TanStack Query 订阅服务端状态，组件规范见 [UI 说明](../src/components/ui/README.md)。

目标交互始终使用 `/` 作为页面入口：前端通过 `/api/me` 确定会话状态，未登录时显示登录界面，已登录时显示 Dashboard。登录、登出及会话失效后的状态切换由前端数据层完成，不跳转到独立登录或主页路径。

非登录请求收到 401 时取消受保护查询、清空用户缓存并在当前页面显示登录界面；登录 401 只展示登录错误。网络故障、5xx 或无法解析的响应显示错误，不直接判定为未登录。接口请求与响应以 [API 模块](unisub-api.md) 为准。

## 依赖与验证

目标实现只使用注入的 fs.FS 及框架路由能力，不依赖 AuthService 或 Database，不直接调用其他模块实例。

验证边界为根路径对不同会话返回相同 HTML、GET/HEAD 行为、资源路径限制、不支持方法和旧页面路径的 404。登录、登出与会话验证属于 API 和前端测试范围。当前 `internal/unisub/static_test.go` 及端到端测试仍对应现有实现，不代表目标契约已验证。
