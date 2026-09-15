# UniSub OAuthFlow 模块

`internal/unisub/oauthflow.go` 实现 UniSub 的 OAuth Web 交互，注册全部 OAuth JSON API 和公开回调。协议由 [OAuth 模块](oauth.md) 完成；账号和凭据的最终保存由 [API 模块](unisub-api.md) 完成。

## 路由与认证

| 方法 | 路径 | 权限 | 作用 |
| --- | --- | --- | --- |
| POST | `/api/oauth/{service}/start` | 管理员 Session | 开始授权，可提交 `{"proxy":"..."}` 或空 Body |
| GET | `/api/oauth/{service}/status/{session}` | 管理员 Session + 归属 | 查询 PKCE 待完成状态或结果 ID |
| POST | `/api/oauth/{service}/complete/{session}` | 管理员 Session + 归属 | 提交 `code/state` 完成 PKCE |
| POST | `/api/oauth/{service}/poll/{session}` | 管理员 Session + 归属 | 轮询 Device Flow |
| GET | `/api/oauth/results/{id}` | 管理员 Session + 归属 | 一次性读取结果 |
| GET | `/auth/callback` | 无浏览器认证；校验 state | Codex 回调路径 |
| GET | `/callback` | 无浏览器认证；校验 state | 默认兼容回调路径 |
| GET | `/oauth/code/callback` | 无浏览器认证；校验 state | 额外兼容回调路径 |

service 支持 codex、claude、grok、dummy。公开回调不使用 URL 参数中的 service/subject 作为归属依据，而是通过 state 找到原 Session。

回调基址优先使用 `OAuthCallbackBaseURL`，否则使用请求的 TLS 状态与 Host。Codex 拼接 `/auth/callback`，其他类型拼接 `/callback`。模块不根据转发头自动推导公开域名。

## Session 与结果

Web 启动使用当前登录用户 ID 作为 SubjectID。Session 保存 service、subject、redirect URI、state、PKCE verifier 或 device code、proxy 和过期时间。

PKCE Session 默认有效期 10 分钟；Device Flow 使用 Adapter 返回的过期时间（非零时覆盖默认值）。OAuthResultStore 的结果有效期固定为 10 分钟。二者都在进程内存中，重启后失效，不写入数据库。

`OAuthResult` 包含 SessionID、SubjectID、Service 和完整 Credential：

- `Put` 要求非空 SubjectID 和 Service，生成随机结果 ID。
- `FindSession` 按 session、service、subject 匹配，供 status 交接结果。
- `Take` 在锁内完成有效期、归属校验和删除；并发读取同一结果最多一人成功。
- 跨用户读取不消费他人的有效结果；不存在、过期和跨用户读取统一返回 `404`。
- result ID 与 credential_id 不同，不能将临时结果 ID 保存到账号配置作为凭据引用。

## PKCE 交互

1. start 返回 session_id、authorization_url 和 expires_at；verifier 仅保存在服务端。
2. 用户完成上游授权，通过公开 callback 或已登录的 complete 接口提交 code/state。
3. callback 使用 `SessionForState(state)` 找回 service/subject；complete 先使用 `SessionForSubject` 验证当前用户。
4. Manager 校验 state 后先消费 Session，再进行 token exchange。交换失败需要重新开始授权，不能重放原 Session。
5. 成功后保存携带 SessionID 的短期结果。
6. 前端通过 status 得到 `{"status":"complete","result_id":"..."}`；待完成时返回 `{"status":"pending"}`。complete 成功直接返回 result_id。
7. 前端调用 results 读取 Credential，然后在管理员保存账号时通过 API 持久化。

公开 callback 成功时还设置 `X-OAuth-Result-ID` 响应头，供程序化客户端使用。浏览器页面使用已认证的 status 交接，不需要从 callback HTML 或跨窗口响应头提取数据。

callback 只接受 GET。成功与授权失败页面均返回 HTTP 200、`Cache-Control: no-store` 和固定 HTML 文本，不显示 token、code、state 或结果 ID。上游携带 error 时丢弃匹配的 Session；缺少 state 或 code 返回失败页，缺少 code 本身不消费 Session。

## Device Flow 交互

start 额外返回 user_code、verification_uri，authorization_url 同时使用验证地址。用户在上游完成验证后，前端调用 poll。

| 结果 | HTTP | 响应 |
| --- | --- | --- |
| 等待或减速 | 202 | status=pending、error=authorization_pending/slow_down、interval_seconds=5、expires_at |
| 成功 | 200 | status=complete、result_id |
| Session 不存在或过期 | 400 | 通用 OAuth Session 错误 |
| 其他上游错误 | 502 | 通用 OAuth 上游失败消息 |

当前 pending 和 slow_down 都返回固定的 5 秒间隔，不能描述成服务端自动递增间隔。poll 成功直接交接 result_id，结果未设置 SessionID，因此不通过 PKCE 的 status 结果查找路径交接。Manager 在轮询成功后消费 Session，但并发 poll 不具备与结果 Take 相同的原子单次消费保证。

## 结果响应与保存

`GET /api/oauth/results/{id}` 成功设置 `Cache-Control: no-store` 并返回：

```json
{
  "result": {
    "access_token": "...",
    "refresh_token": "...",
    "token_type": "Bearer"
  }
}
```

标准化字段还可包含 expires_at、account_id、account_name、email。该接口只消费短期结果，不自动创建账号或长期凭据。最终保存时将 Credential 交给 AIProvider 管理接口，存储层生成凭据引用。

当前管理员 AIProvider 响应也可能包含完整 Credential；不能声称一次性 result 是系统唯一返回 Credential 的接口。实际响应边界见 [API](unisub-api.md)。

## 错误与验证边界

未登录 JSON 请求返回 401；错误 state、无效 Session、不支持的 flow 通常为 400；上游失败为 502；结果不存在为 404。部分显式方法检查返回 405，其余未知路径或方法可能返回 404。

回调不能用用户参数覆盖 Session 归属；错误响应不回显 token exchange 原始内容。OAuth state、verifier、code、device code 和 token 不应写入普通日志。

相关测试包括 `internal/unisub/oauthflow_test.go`、`oauth_complete_test.go`、`internal/service/oauth_result_store_test.go`，覆盖 Web 边界与结果交接；协议行为由 OAuth 测试覆盖。本地模拟结果不代表真实账号完成认证。
