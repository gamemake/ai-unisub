# OAuth 模块

协议实现位于 `internal/oauth` 及 `adapters`，支持 Codex、Claude 的授权码流程、Grok 的设备授权流程，以及用于本地验证的 Dummy。OAuthManager 管理短期授权会话、state、PKCE、凭据刷新及代理上下文。

应用层 `internal/unisub/oauthflow.go` 提供：

- `POST /api/oauth/{service}/start`：创建绑定当前用户的授权会话。
- `POST /api/oauth/{service}/poll/{sessionID}`：轮询设备授权。
- `POST /api/oauth/{service}/complete/{sessionID}`：提交授权码与 state，必须属于当前用户。
- `GET /api/oauth/{service}/status/{sessionID}`：检查浏览器回调是否已完成。
- `GET /api/oauth/results/{id}`：当前用户一次性读取授权结果。
- `/callback`、`/auth/callback`、`/oauth/code/callback`：平台回调。

授权结果仅在内存保存 10 分钟，凭据不嵌入回调 HTML；前端通过鉴权接口获取，再绑定 AIProvider。普通成员不能通过会话 ID 或结果 ID 读取其他用户的凭据。管理员可查看和管理已有订阅凭据。

更详细的协议设计见 [oauth-design.md](oauth-design.md)。
