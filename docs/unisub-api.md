# UniSub API 模块

UniSub API 模块负责登录、登出、用户、密码、AIProvider Account、API Key、调用记录、用量和代理管理，接口统一使用 `/api/` 前缀。普通管理接口使用 `AuthSession`；登录与登出使用下面定义的公开认证入口，不受该前缀的 Session 中间件拦截。

登录、登出与普通管理接口均由 `internal/unisub/api.go` 注册。代理管理通过独立 `proxy.Manager` 完成。

[Static](unisub-static.md) 仅返回 `/` 静态网页与资源；全部 `/api/oauth/*` 仍由 [OAuthFlow](unisub-oauthflow.md) 负责；`/v1/*` 由 [Gateway](unisub-gateway.md) 负责。

## 通用约定

AuthService、Session、API Key 校验和密码处理的实现见 [Service](service.md)。本文件维护登录、登出及管理接口的 HTTP 契约与业务授权。

受保护接口认证使用 `Cookie: session=<token>`。框架验证 Session 后将 Principal 放入 Context；Handler 校验角色和资源归属，不接受客户端指定当前用户。公开的登录与登出接口不要求已有有效 Session。

| 响应 | 格式 |
| --- | --- |
| 用户、AIProvider、Key、调用记录列表 | `{"items":[],"total":0}` |
| 代理组列表 | 直接返回数组；空结果可能为 `null`，前端按空列表处理 |
| 用量统计 | `{"data":[],"totals":{},"has_records":false}` |
| 通常的业务错误 | `{"error":"英文错误消息"}` |
| 删除成功 | `204`，无响应体 |

成功读取通常为 `200`，创建为 `201`；参数错误 `400`，未登录 `401`，无权限通常为 `403`，未找到 `404`，内部错误 `500`。部分未知路径或方法使用标准 `http.NotFound`，不是统一 JSON。非管理员访问管理接口统一返回 `403`。

## 登录与登出

本节定义 API，均返回 JSON，不返回 HTML 或 3xx 页面跳转。

| 方法 | 路径 | Body | 认证 | 成功响应 |
| --- | --- | --- | --- | --- |
| POST | `/api/login` | `username/password` | AuthNone | 200；`{"status":"ok","user":{...}}`，设置 Session Cookie |
| POST | `/api/logout` | 无 | AuthNone；如有 Cookie 则清除对应会话 | 200；`{"status":"ok"}`，清除 Session Cookie |

登录请求为 `{"username":"...","password":"..."}`，Body 上限 1 MiB。缺少字段或 JSON 无效返回 400；校验启用用户与密码后创建 Session，响应用户信息不包含密码散列。错误账号、密码或停用用户统一返回 401，不区分账号是否存在。

登出为幂等操作：存在会话则使其失效；没有 Cookie、会话已过期或已删除时仍返回成功，并发送清除 Cookie 的响应。不得因 Session 失效导致用户无法完成本地登出。GET 不触发登录或登出，非 POST 方法返回 405，并设置 `Allow: POST`。

两个接口的响应使用 `Cache-Control: no-store`。API 模块通过 AuthService 创建、删除会话和设置／清除 Cookie，通过 Database 校验用户；Static 不参与这些操作。

路由注册采用同属 API 模块的精确 `/api/login`、`/api/logout` 路由（AuthNone）和普通 `/api/` 前缀路由（AuthSession）。精确路径优先匹配，避免登录被 Session 认证拦截；不能为此把整个 `/api/` 改为公开访问。

前端在 `/` 中提交登录，成功后刷新当前用户与受保护数据并显示 Dashboard；登出成功后取消查询、清空用户缓存并显示登录界面。登出请求失败时展示错误，不能宣称服务端会话已失效。会话失效的 401 在原页面切换登录状态，不依赖旧页面路径。

## 普通用户权限

非管理员仅可见个人总览、API Key、调用记录、安全设置。直接访问管理页面地址不会渲染管理界面；后端独立拦截管理 API，包括兼容别名及 OAuth API。个人接口仅允许 `me`、`password`、`keys`、`calls`；Key 和调用记录仍按当前用户校验归属。登录、登出不受此限制。

`GET /api/keys/providers` 为个人页面提供绑定及显示选项，仅返回 `{items:[{id,name,provider,enabled}],total}`，不包含配置、URL、组成员或凭据；其他方法返回 405。

## 当前用户与密码

| 方法 | 路径 | Body | 权限与响应 |
| --- | --- | --- | --- |
| GET | `/api/me` | 无 | Session；返回 `id/name/role/server_version` |
| POST | `/api/password` | `old_password/new_password` | 当前用户；校验旧密码，新密码长度至少 8，成功返回 `{"status":"ok"}` |

## 用户管理

以下接口均要求管理员，用户响应不包含密码散列。

| 方法 | 路径 | Body／用途 |
| --- | --- | --- |
| GET | `/api/users` | 用户列表 |
| POST | `/api/users` | `name/password/role`；创建启用用户 |
| PUT | `/api/users/{id}` | 可选 `role/enabled` |
| POST | `/api/users/{id}/password` | `password`；重置目标用户密码 |
| DELETE | `/api/users/{id}` | 删除目标用户 |

创建时 `role` 必须为 `admin` 或 `user`，用户名非空且不能重复，密码长度至少 8。编辑时空 role 表示不更改。管理员不能删除自己、停用自己、将自己降为普通用户，或通过管理员重置接口重置自己的密码；本人修改密码使用 `/api/password`。

## AIProvider Account

`/api/providers` 是 `/api/ai-providers` 的兼容别名，子路径使用同一处理器。

| 方法 | 路径 | Body | 权限 |
| --- | --- | --- | --- |
| GET | `/api/ai-providers` | 无 | 管理员 |
| POST | `/api/ai-providers` | `name/provider/config` | 管理员 |
| PUT | `/api/ai-providers/{id}` | `name/provider/config` | 管理员 |
| DELETE | `/api/ai-providers/{id}` | 无 | 管理员 |

创建必须提供 provider 和有效 config；当前类型为 `codex`、`claude`、`grok`、`dummy`。更新不能改变 provider 类型。config 可以是 JSON 对象，兼容编码为字符串的 JSON 对象。

配置中的内联 `credential` 保存到凭据存储后，账号配置只保存 `credential_id`；提供已有 ID 时验证凭据存在。API Key 认证账号在编辑时省略 `api_key` 可保留原密钥。配置字段与运行时语义见 [AIProvider](ai-provider.md)。

`provider` 新增 `api` 与 `group`。API 支持可选 `supplier`，未填 `api_endpoint` 时按请求协议使用供应商目录的内置 URL；组通过 `config.members: [{id, weight}]` 引用已有非组账号，权重缺省 3，取整数 1～5。组和成员使用单值 `client_type`（Any、Anthropic、OpenAI、Grok），缺省 Any；数组不合法，旧 `client_types` 字段忽略且保存时移除。组必须为 Any 或与所有成员的有效客户端类型相同。组不保存独立凭据、供应商、上游 URL 或代理组；仍被组引用的成员不能删除。AIProvider 仅接受 `proxy_group_id`，旧直接 `proxy` 字段作为未知字段忽略。保存后的配置包含归一化的类型、供应商和权重，数据库写入失败时回退运行时配置。

### 模型供应商与映射

| 方法与路径 | 权限 | 语义 |
| --- | --- | --- |
| GET /api/ai-catalog | 管理员 | 返回 `{catalog: {suppliers}, builtin_suppliers}` |
| PUT /api/ai-catalog | 管理员 | 整体保存 `{suppliers}`；数据库成功后原子更新运行时目录 |
| GET /api/ai-catalog/{id} | 管理员 | 返回指定供应商 `{id, name, claude_url, codex_url, mappings}`；不存在返回 404 |
| PUT /api/ai-catalog/{id} | 管理员 | 仅更新当前供应商，body 为 `{id, name, claude_url, codex_url, mappings}`；路径与 body 的 id 必须一致，其他供应商配置不变；响应格式与目录 GET 一致 |

`suppliers` 和 `builtin_suppliers` 项为 `{id, name, claude_url, codex_url, mappings}`，必须保留 anthropic、openai、grok、deepseek、zhipu、kimi 六项，不能删除或重复。每个供应商内部的 `mappings` 项为 `{client, model, target}`，client 取 Anthropic、OpenAI、Grok；相同客户端、供应商和请求模型名不允许重复。数据库映射优先，删除覆盖项后回退 对应供应商的内置映射；无匹配项时保留请求模型名。

`claude_url`、`codex_url` 为代码维护的只读内置值，Grok 共用 `codex_url`。目录整体 PUT 和单供应商 PUT 修改 URL 均返回 400；模型映射仍可修改。加载数据库时恢复内置 URL，旧 `url` 字段不生效。

当前响应边界：

- config 递归移除 `access_token`、`refresh_token`、`api_key`、`raw`、`credential` 字段。
- 普通用户不能读取 AIProvider 管理列表。
- 管理员列表及创建／更新响应，会在能够加载凭据时附带顶层完整 `credential`。不能把当前行为描述成“所有管理响应均已脱敏”。
- 凭据存储接口本身不记录用户归属和 service 类型；现有 ID 校验不能被描述成凭据所有权校验。
- 删除前检查 API Key 引用；仍有 Key 绑定的账号不能删除。删除成功后移除运行时实例，并尝试清理没有其他账号引用的 Credential。
- 创建失败路径有运行时实例和新建凭据补偿，但账号与凭据更新不是跨实体事务，不能承诺所有失败路径原子回滚。

## API Key

| 方法 | 路径 | Body／用途 | 权限 |
| --- | --- | --- | --- |
| GET | `/api/keys` | 当前用户 Key 列表 | Session |
| POST | `/api/keys` | `name/account_id/valid_seconds` | Session |
| GET | `/api/keys/{id}` | 单个 Key | 仅所属用户 |
| DELETE | `/api/keys/{id}` | 删除 Key | 仅所属用户 |

name 去除首尾空白后必须为 1–64 个字符；account_id 必须存在；valid_seconds 不得为负，0 表示无固定过期时间。创建时使用当前用户 ID，不由请求体指定。

创建、列表和单个读取均返回明文 key，不是仅创建时返回一次。管理员也使用自己的 Key 范围，不能通过此接口读取其他用户的 Key。跨用户 ID 按不存在处理。

## 调用记录

| 方法 | 路径 | 参数 |
| --- | --- | --- |
| GET | `/api/calls` | Query：`q`、`account_id`、`code`、`range/from/to`、`page`、`page_size`、`mine` |
| GET | `/api/calls/{day}/{id}` | Path：UTC 日期 `YYYYMMDD` 与记录 ID |

默认 page=1、page_size=100；page 至少 1，page_size 接受 10–100 的整数。管理员默认查询全部记录，`mine=1` 限制为本人；普通用户始终限制为本人。

q 去除首尾空白后，精确匹配 IP、模型、session_id 或 request_id，任一字段相等即可；仅管理员额外精确匹配 username，界面不提示该能力。account_id 按账号 ID 匹配；code 省略或为空时查询全部；传入时按存储的 http_error_code 精确匹配，接受 0 或 100–599 的整数。成功记录目前存储为 0，不映射为 200。文本、账号、状态、时间以及归属限制之间使用 AND。

时间支持 1d、1w、1m、custom，与系统总览一致；custom 的 from/to 为 UTC 日期且包含结束日。省略 range 保留原接口的不限时间行为，调用记录页面默认使用 1d。非法 code 或时间范围返回 400。筛选先于分页执行，total 为全部匹配记录数。

列表摘要和详情均包含 session_id。网关仅按 [AIProvider 原生会话头适配](ai-provider.md) 读取 Claude、Codex、Grok 对应的原生 SessionID，不读取 X-Unisub-Session-ID；未匹配客户端或原生头时留空，不使用通用头或请求正文兜底。调用记录与组内粘性使用相同规则。这是客户端调用会话标识，不是 Dashboard 登录会话。旧记录没有该值时返回空字符串；组调用的 account_id/provider_type 记录实际执行的成员。

列表返回摘要，详情包含记录的请求／响应信息。响应前清空用于归属判断的 APIKey 字段；普通用户无权读取的详情返回 `404`。当前详情权限依赖仍存在的所属 Key 匹配，不能将它描述成独立的永久历史授权机制。

## 用量统计

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/usage/subscriptions` | 按账号聚合用量 |
| GET | `/api/usage/users` | 按用户聚合，可用 `subscription_id` 限制账号 |

两者均为管理员功能。Query 的 range 支持 `1d`（默认最近 24 小时）、`1w`（7 天）、`1m`（30 天）、`custom`；custom 使用 `from/to=YYYY-MM-DD`，按 UTC 包含结束日。

usage 包含 requests、input_tokens、output_tokens、cache_creation_tokens、cache_read_tokens、total_tokens。total_tokens 是四类 token 数之和；统计来自已记录调用，不代表上游账单或余额。

## 代理管理

以下接口均要求管理员，调用 `ModuleContext.Proxy()` 提供的 `proxy.Manager`。

| 方法 | 路径 | 输入／用途 |
| --- | --- | --- |
| GET | `/api/proxy-groups` | 查询代理组 |
| POST | `/api/proxy-groups` | 代理组 JSON；生成组 ID |
| GET | `/api/proxy-groups/{id}` | 读取单组 |
| PUT | `/api/proxy-groups/{id}` | 替换组配置，保留 ID 和创建时间 |
| DELETE | `/api/proxy-groups/{id}` | 删除组 |
| POST | `/api/proxy-groups/test` | `group_id/proxy_id` 或未保存的 `url` |
| POST | `/api/proxy-groups/{id}/test` | `proxy_id`；探测指定组内代理 |
| GET | `/api/proxy-groups/errors` | Query：必填 `group_id/proxy_id`；读取错误时间和桶 |

组名必填，max_retries 不得为负，代理 URL 必须通过校验。组列表和普通详情移除代理的 last_error_at、error_records；错误信息使用专门接口读取。探测响应中的 status 表示可用性，HTTP 请求成功不等于被探测代理可用。

独立包的领域设计、优先级调度及分层统计见 [Proxy](proxy.md)。

## 依赖与验证

API 使用 Database、AIProviderManager、代理管理和认证能力。浏览器登录与登出由本模块使用 AuthService 处理；OAuth Session 与结果操作仍属于 OAuthFlow，不在本模块内实现。

现有相关测试位于 `internal/unisub/api_test.go`、`auth_test.go`、`aiprovider_compatibility_test.go` 和 Dashboard 端到端测试。文档核对重点为角色差异、Key 归属、配置保存、列表包装、调用查询与代理接口，不将未执行的测试写成已通过。

登录与登出的验证包括公开路径优先级、普通 API 仍要求会话、登录错误、Cookie 设置与清除、重复登出、方法限制，以及所有认证交互不返回页面重定向。`auth_api_test.go` 验证上述 HTTP 契约。
