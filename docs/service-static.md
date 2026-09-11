# `static` Service Module

`static` 是负责浏览器 Web UI 入口的 Service Module，处理静态资源、HTML 页面、登录和登出，不处理 JSON API、OAuth 协议交换或 AI Provider 调用。

## 路由与认证

| 路径 | 说明 | 认证 |
| --- | --- | --- |
| `/static/*` | JavaScript、CSS 和嵌入式资源 | `AuthNone` |
| `/login` | 返回静态登录网页和登录提交脚本；如果已经登录则重定向到 `/home` | `AuthNone` |
| `/` | 根据登录状态重定向到 `/login` 或 `/home` | `AuthNone` |
| `/home` | 返回静态 Web 应用网页；如果没有登录则重定向到 `/login` | `AuthNone`，由 Handler 判断 |
| `/logout` | 清除当前 Session，完成后重定向到 `/login` | Session 或无 Session 均可安全调用 |

系统只保留 `/login` 和 `/home` 两个静态 HTML 页面。页面本身由嵌入的静态网页资源提供，不使用服务端模板渲染；登录状态只用于决定是否重定向。页面中的登录、用户、Provider、API Key、调用记录等操作通过公开的 JSON API 完成。`/` 是根据登录状态进行跳转的统一入口，不直接渲染页面；`/logout` 是 Session 操作入口，不是 HTML 页面。

界面通过统一请求函数解析 API 响应。失败响应读取 JSON 的 `error` 字段，将稳定的英文消息作为多语言 key 映射为当前界面语言；没有翻译时显示英文原文。登录与其他 API 共用同一套解析逻辑，`401` 在展示错误后清理本地登录状态。网络失败和无法解析的响应使用界面自己的兜底消息，所有错误都通过 `textContent` 或等价的转义方式展示。

页面跳转规则如下：

1. 请求 `/`：已登录重定向 `/home`，未登录重定向 `/login`；
2. 请求 `/login`：已登录重定向 `/home`，未登录返回静态登录网页；
3. 请求 `/home`：已登录返回静态 Web 应用网页，未登录重定向 `/login`；
4. 请求 `/logout`：清除 Session 后重定向 `/login`。

## 依赖与边界

可依赖 `ModuleContext` 提供的模板、嵌入式资源、`AuthService` 和数据库只读能力。不得依赖 `oauthflow` 或 `gateway` 的具体类型；OAuth 页面交互通过公开的 `/api/oauth/*` 接口完成。

登录失败不能泄漏用户是否存在，静态资源不能暴露嵌入 FS 之外的本地文件。测试应覆盖资源访问、登录成功/失败、登录态重定向、登出后的 Session 清除和未认证重定向。
