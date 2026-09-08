# mitmproxy / HTTP 嗅探 方案

本文描述在 `ai-unisub` 中增加 **mitmproxy 出站调试** 与管理员 **HTTP 嗅探** 页面的设计方案。实现以本文为准；落地后需同步更新 [`api.md`](api.md)、[`database.md`](database.md)，并在 [`README.md`](../README.md) 中增加入口链接。

相关现状文档：[`http-headers.md`](http-headers.md)（头剥离/注入与调用日志头规则）、现有「调用记录」API（`GET /api/request-logs`）。

---

## 1. 目标

1. 支持将上游出站流量挂到外部 **mitmproxy**（通常为 `mitmweb`），便于解密查看真实 HTTPS 调用。
2. 管理员在控制台增加 **HTTP 嗅探** 页面，用于：
   - 启用/关闭与配置 mitmproxy 出站代理；
   - 查看调用记录（复用现有请求日志）；
   - 阅读本地启动 mitmproxy、信任 CA 的操作说明。
3. 默认关闭；仅 `role=admin` 可配置与进入该页面。

---

## 2. 背景与现状

| 已有能力 | 说明 |
| --- | --- |
| 网关转发 | [`internal/server/proxy.go`](../internal/server/proxy.go)：鉴权、选号、头剥离/注入、SSE |
| 账号出站代理 | [`account_proxy.go`](../internal/server/account_proxy.go)：每账号可选 `http://` / `socks5(h)://`，客户端缓存在 `subscriptionClients` |
| 调用记录 | [`httplog.go`](../internal/server/httplog.go) + 每日表 `request_logs_YYYYMMDD`：原文头 + 截断 body；成员看自己的，管理员可看全部 |
| 管理端 | 嵌入式 SPA：[`web/admin.html`](../internal/server/web/admin.html) / [`admin.js`](../internal/server/web/admin.js)，`data-page` + `isAdmin()` |

**当前没有**：进程内 TLS MITM、mitmproxy 进程托管、独立嗅探页、全局调试代理开关。

现有「调用记录」是 **应用层** 视图（经网关处理后的请求/响应）；mitmproxy 是 **链路层** 视图（真实出站 TLS/HTTP）。二者互补，不互相替代。

---

## 3. 推荐架构

采用 **外部 mitmproxy + UniSub 出站挂接**，不把 Python mitmproxy 嵌进 Go 二进制，v1 也不由 UniSub 拉起/杀死 mitmproxy 子进程。

```text
客户端 ──► UniSub 网关（鉴权 / 选号 / 头注入 / 写 request_logs）
              │
              │  若管理员启用 mitm：
              │  HTTP CONNECT / Proxy → mitmproxy(:8080)
              ▼
           mitmproxy / mitmweb（解密、过滤、导出）
              │
              ▼
           上游 Provider（Claude / Codex / Grok）
```

运维在本机或旁路容器自行运行 `mitmweb`；UniSub 只负责把上游 `http.Client` 的代理指过去。

### 3.1 与账号 `proxy_url` 的关系

全局设置增加 `apply_mode`：

| 模式 | 行为 |
| --- | --- |
| `force`（启用时推荐） | 全部上游 HTTP（转发、用量探测、OAuth 换票）改走 mitm URL，**暂时覆盖**账号 `proxy_url` |
| `when_no_account_proxy` | 仅对未配置账号代理的订阅生效；已有 SOCKS/HTTP 代理的账号保持原样 |

v1 **不做**「账号 SOCKS → 再链式进入 mitm」。页面需明确提示：嗅探为 `force` 时账号代理不生效。

变更 mitm 配置后必须 **清空全部** `subscriptionClients` 缓存，使新 Transport 立即生效。

### 3.2 方案取舍

| 候选 | 结论 |
| --- | --- |
| A. 外部 mitmproxy + UniSub 出站挂接 | **采用**：贴合「设置 mitmproxy + 看记录」、改动面可控、能看到真实上游 TLS |
| B. 仅增强应用内抓包、不接 mitm | 不满足 mitmproxy 需求；现有调用记录已覆盖大半应用层场景 |
| C. UniSub 内嵌/托管 mitmproxy 进程 | 运维复杂（Python 运行时、CA、权限），v1 不做 |

---

## 4. 功能范围

### 4.1 纳入 v1

- DB 持久化全局 mitm 设置（默认关闭）
- Admin-only API：`GET /api/mitm`、`PUT /api/mitm`
- 出站客户端按设置选择代理（凡走 `clientForSubscription` 的路径自动生效）
- 管理端新页面 **HTTP 嗅探**（仅管理员可见）
  - 开关、代理 URL、应用模式
  - 本地 mitmproxy 启动与 CA 信任说明（静态文案）
  - 调用记录列表/详情（复用现有 request-logs API）
- 配置校验：代理 URL 仅允许 `http://`（mitmproxy 常规入口）；必须含 host + port；禁止 path / query / fragment（与账号代理规则一致）
- 安全：默认关闭；GET 对 URL 中的 userinfo 脱敏；调用记录头按原文落库

### 4.2 明确不做（v1）

- 内嵌 / iframe mitmweb
- UniSub 管理 mitmproxy 进程生命周期
- 透明代理 / iptables / 系统级抓包
- 修改「调用记录」对普通成员的可见范围
- 在 `request_logs` 中重复存一份嗅探明文（调用记录已存原文头，避免再扩大无关字段）
- 账号代理与 mitm 的 SOCKS 链式组合

---

## 5. 数据模型

新增表 `system_settings`（通用 KV，便于后续其它全局开关复用）：

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `key` | TEXT | 主键，mitm 配置使用 `mitm` |
| `value_json` | TEXT | JSON 文本 |
| `updated_at` | TEXT | UTC RFC3339Nano |

`mitm` 的 `value_json` 形状：

```json
{
  "enabled": false,
  "proxy_url": "http://127.0.0.1:8080",
  "apply_mode": "force"
}
```

| 字段 | 说明 |
| --- | --- |
| `enabled` | 是否启用出站挂接；缺省 `false` |
| `proxy_url` | mitmproxy 监听地址，仅 `http://host:port` |
| `apply_mode` | `force` 或 `when_no_account_proxy` |

迁移：在 [`internal/database`](../internal/database) 增加一版（SQLite + PostgreSQL）。无行或解析失败时视为 `enabled=false`。

---

## 6. API 设计

均挂在现有 `/api` JWT 组下，并额外 `requireRole(RoleAdmin)`。

### 6.1 `GET /api/mitm`

返回当前设置。若存储的 URL 含用户名密码，响应中的 `proxy_url` 脱敏（或仅暴露 host:port），并带 `proxy_configured`。

```json
{
  "mitm": {
    "enabled": true,
    "proxy_url": "http://127.0.0.1:8080",
    "proxy_configured": true,
    "apply_mode": "force",
    "updated_at": "2026-09-08T12:00:00.000000000Z"
  }
}
```

非管理员：`403 forbidden`。

### 6.2 `PUT /api/mitm`

请求体采用 **整对象覆盖**（降低部分更新歧义）：

```json
{
  "enabled": true,
  "proxy_url": "http://127.0.0.1:8080",
  "apply_mode": "force"
}
```

校验失败：`400 invalid_request`。成功后：

1. 写入 `system_settings`
2. 调用 `invalidateAllSubscriptionClients()`
3. 返回与 GET 相同形状的最新设置

### 6.3 调用记录

不新增专用日志 API。嗅探页直接复用：

- `GET /api/request-logs`
- `GET /api/request-logs/:day/:id`

管理员已可按订阅等条件筛选；成员规则不变。

---

## 7. 后端改动要点

| 位置 | 改动 |
| --- | --- |
| `internal/model` | 增加 `MitmSettings` |
| `internal/repository` | Get/Put `system_settings` |
| `internal/database/schema.go` | 建表 + migration version |
| `internal/server/account_proxy.go` | `effectiveProxyURL`；`clientForSubscription` 合并全局 mitm；`invalidateAllSubscriptionClients` |
| `internal/server/mitm.go`（新） | `getMitm` / `putMitm` |
| `internal/server/server.go` | 注册路由 |
| 测试 | 模式矩阵、缓存失效、非管理员 403、URL 校验 |

出站代理选择伪代码：

```go
func (s *Server) effectiveProxyURL(account model.Subscription) (string, error) {
    mitm := s.loadMitmSettings() // 可读缓存，写配置时失效
    accountProxy := ""
    if account.ProxyConfigured {
        // 从仓库读取账号 proxy_url
    }
    if mitm.Enabled && mitm.ProxyURL != "" {
        if mitm.ApplyMode == "force" || accountProxy == "" {
            return mitm.ProxyURL, nil
        }
    }
    return accountProxy, nil
}
```

主转发、用量探测、OAuth 换票凡使用 `clientForSubscription` 的路径无需逐处修改。

---

## 8. 管理端 UI

沿用嵌入式控制台模式：

1. [`admin.html`](../internal/server/web/admin.html)：侧栏增加 `id="sniffNav"`、`data-page="sniff"`，默认 `hidden`
2. `section#page-sniff`：两块卡片
   - **mitmproxy 设置**：开关、URL、模式、保存；状态与风险提示
   - **调用记录**：筛选 + 表格 + 复用现有日志详情弹窗
3. [`admin.js`](../internal/server/web/admin.js)：`isAdmin()` 控制显示；`navigate` 将 `sniff` 列为管理员页；`loadMitm` / `saveMitm`
4. [`admin_page_test.go`](../internal/server/admin_page_test.go)：断言页面标记字符串存在

页面文案需强调：

- 仅建议在调试环境开启；生产环境慎用
- 需在运行 mitmproxy 的环境信任其 CA，才能解密 HTTPS
- `force` 模式会覆盖账号级 `proxy_url`

---

## 9. 运维使用流程

1. 安装 [mitmproxy](https://mitmproxy.org/)，例如启动：

   ```sh
   mitmweb --listen-host 127.0.0.1 --listen-port 8080
   ```

2. 浏览器打开 mitmweb（默认 UI 端口多为 `8081`），按需下载并信任 CA。
3. 管理员登录 UniSub → **HTTP 嗅探** → 填写 `http://127.0.0.1:8080` → 启用 → 选择 `force` → 保存。
4. 使用下游 `unisub_*` Key 发起一笔 `/v1/...`（或对应 Provider 路径）请求。
5. 在 mitmweb 查看解密后的上游流量；在 UniSub 嗅探页 / 「调用记录」查看应用层日志。

### 9.1 Docker 注意

若 UniSub 在容器内、mitmproxy 在宿主机，代理 URL 需使用容器可达的宿主机地址（例如 Docker Desktop 上的 `http://host.docker.internal:8080`），不能写容器视角下的 `127.0.0.1`（除非 mitm 与 UniSub 共用 network namespace）。

---

## 10. 安全与合规

- 默认 `enabled=false`
- 仅 `role=admin` 可读写设置与进入嗅探页
- 不改变成员「只能看自己的调用记录」规则
- 调用记录头按原文落库（见 [`http-headers.md`](http-headers.md)）；注意管理入口访问控制
- MITM 可看到上游 Token，等同高权限调试能力；文档与 UI 必须警示

---

## 11. 实现拆分建议

| 顺序 | 内容 |
| --- | --- |
| PR1 | `system_settings` + mitm API + `clientForSubscription` 挂接 + 单测 |
| PR2 | 管理端 HTTP 嗅探页（设置 + 调用记录）+ `admin_page_test` |
| PR3（可选） | `api.md` / `database.md` / README 交叉链接；Docker 网络说明；可选 TCP 拨测 mitm 端口（非必须） |

---

## 12. 后续可选（非 v1）

- 在嗅探开启时，将 **注入后的上游请求头** 写入日志扩展字段，便于不装 mitm 时对比头策略
- 只读状态探测（拨测 `proxy_url` 端口是否可连）
- 按订阅白名单启用 mitm，而不是全局 `force`

---

## 13. 实现前检查清单

- [ ] `system_settings` 迁移（SQLite + PostgreSQL）
- [ ] `GET/PUT /api/mitm` + 管理员鉴权
- [ ] `effectiveProxyURL` + 全量客户端缓存失效
- [ ] 管理端 `sniff` 页仅管理员可见
- [ ] 调用记录复用现有 API，无明文扩权
- [ ] 单测覆盖模式、校验、403、缓存失效
- [ ] 实现后更新 `api.md`、`database.md`、README 链接
