# `/home` 数据管理与占位数据改进计划

## 1. 目标与范围

本计划针对 `internal/service/static/home.html` 和 `internal/service/static/home.js`，解决两个问题：

1. 建立统一的服务器数据管理模块，集中负责请求、缓存、状态、刷新和数据变化订阅。
2. 页面首次加载时立即展示结构稳定的占位数据；服务器数据返回后，只更新对应数据区域，不阻塞整个应用首屏。

本次改造覆盖 `/home` 管理台前端状态与渲染方式，并新增独立的数据模块 `internal/service/static/home-data.js`。现有 `/api/me`、`/api/providers`、`/api/keys`、`/api/users`、`/api/calls` 和 Usage API 继续作为数据源；其中 `providers` 和 `users` 只由管理员加载。

普通用户页面如果需要显示订阅名称或平台信息，必须由 `/api/keys`、`/api/calls` 等用户可访问接口直接返回脱敏后的关联展示字段，不能为了补充展示信息而让普通用户请求 `/api/providers`。

## 2. 当前实现与问题

当前 `home.js` 的状态主要是模块闭包内的全局变量：

```js
var accounts = [];
var keys = [];
var users = [];
var usage = {};
var currentUser = null;
```

`loadData()` 负责一次性串行请求用户、订阅、Key、用户列表和调用记录，然后集中调用多个 `render*` 函数。该方式有以下问题：

- 任意一个主请求失败，都可能影响后续数据加载和整页展示。
- 页面组件无法订阅“某一类数据已更新”，只能依赖手动调用 `loadData()`。
- 新建、编辑、删除操作完成后通常重新加载全部数据，刷新范围过大。
- 首屏只能显示空数组对应的“暂无数据”，没有真实的加载占位状态。
- Usage、日志等按需数据与基础数据混在同一套刷新流程中。
- 请求返回顺序不可控时，旧请求可能覆盖较新的结果。

相关现状位置：`home.js` 的状态声明、`showApp()`、`navigate()`、`loadData()` 以及各个 `render*` 函数。

## 3. 总体方案

新增一个前端数据层，将“请求服务器”和“通知视图”分开：

```text
页面事件 / 定时刷新 / 写操作
              |
              v
        HomeDataStore
        - cache
        - request
        - loading/error
        - version
        - subscribe
              |
              v
       API endpoints

订阅者：个人总览、系统总览、订阅管理、API Key、用户管理、调用记录、Usage
```

`HomeDataStore` 从一开始就放在独立文件 `internal/service/static/home-data.js` 中。`home.js` 只负责页面事件、导航和业务编排，渲染函数可以继续放在 `home.js`。

`home.html` 的脚本加载顺序调整为：

```html
<script src="/static/home-data.js" defer></script>
<script src="/static/home.js" defer></script>
```

推荐由 `home-data.js` 暴露 `window.createHomeDataStore`，只暴露 Store 工厂和必要类型，不暴露内部 cache、请求序号或订阅者集合。

## 4. `HomeDataStore` 设计

### 4.1 数据 key

统一使用以下 key，避免组件直接操作散落的全局变量：

| Key | API | 说明 | 默认占位形态 |
| --- | --- | --- | --- |
| `me` | `GET /api/me` | 当前用户和服务版本 | 一个用户对象 |
| `providers` | `GET /api/providers` | 管理员可见的订阅 | 3 条订阅行；仅 admin 加载 |
| `keys` | `GET /api/keys` | 当前用户可见的 API Key | 3 条 Key 行 |
| `users` | `GET /api/users` | 管理员用户列表 | 4 条用户行；仅 admin 加载 |
| `recentCalls` | `GET /api/calls?page=1` | 个人总览最近调用 | 4 条活动行 |
| `logs` | `GET /api/calls?...` | 调用记录分页结果 | 当前页大小的日志行 |
| `usageBySubscription` | `/api/usage/subscriptions?...` | 按订阅统计 | 3 条统计行 |
| `usageByUser` | `/api/usage/users?...` | 按用户统计 | 3 条统计行 |

Usage 和日志需要把查询参数纳入 cache key，例如 `logs?page=1&page_size=100&q=foo`，不能与无参数数据共用一个 key。

### 4.2 对外接口

```js
const store = createHomeDataStore({ request });

store.get('providers');
// { data, status, error, updatedAt, version }

const unsubscribe = store.subscribe('providers', function(snapshot) {
  renderAccounts(snapshot);
});

await store.load('providers');
await store.refresh('providers');

store.mutate('providers', function(current) {
  return current.filter(function(item) { return item.id !== id; });
});

unsubscribe();
```

建议实现以下规则：

- `get(key)` 始终返回快照，不直接返回内部可变对象。
- `subscribe(key, listener)` 注册后立即收到一次当前快照，便于组件首次挂载。
- 每个 key 独立维护 `status`：`idle`、`loading`、`ready`、`refreshing`、`error`。
- 首次加载时 `data` 为占位数据，`status=loading`；已有真实数据刷新时保留旧数据，`status=refreshing`。
- 每次成功提交真实数据时递增 `version`，通知所有订阅者。
- `refresh(key)` 使用请求序号或 `AbortController` 丢弃过期响应。
- 订阅回调异常不能阻断其他订阅者；开发环境记录错误，用户界面显示统一错误状态。
- `destroy()` 取消未完成请求并清理所有订阅，登出或页面卸载时调用。

### 4.3 快照结构

```js
{
  key: 'providers',
  data: [],
  status: 'loading',
  error: null,
  isPlaceholder: true,
  updatedAt: null,
  version: 0,
  requestId: 1
}
```

`isPlaceholder` 必须和 `status` 分开：数据可能已经是旧的真实数据，但正在后台刷新，此时 `isPlaceholder=false`。

## 5. 请求与刷新策略

### 5.1 首屏加载

`showApp()` 不再等待所有请求完成后才显示应用：

1. 立即显示 `appView` 和当前导航骨架。
2. 初始化 Store，发布所有 key 的占位快照。
3. 先加载 `me`，并行加载普通用户需要的 `keys` 和 `recentCalls`。
4. `me` 返回后确定角色；只有 admin 才加载 `providers`、`users`、管理员总览和管理员 Usage 数据。
5. 每个请求完成后只通知对应订阅者，局部替换占位数据。
6. 基础数据失败时保留页面结构并显示局部错误与“重试”按钮，不清空其他已成功数据。

示意代码：

```js
function showApp() {
  document.documentElement.classList.remove('home-auth-pending');
  $('loginView').hidden = true;
  $('appView').hidden = false;
  renderAllPlaceholders();

  Promise.allSettled([
    store.load('me'),
    store.load('keys'),
    store.load('recentCalls')
  ]).then(function() {
    // me 的订阅回调在确认 admin 后再加载 providers、users 和管理员数据
  });
}
```

### 5.2 页面切换与按需数据

- 进入 `logs` 才加载当前日志页；搜索和分页只刷新 `logs:<query>`。
- 进入管理员 `overview` 才加载两类 Usage；切换时间范围只刷新对应 Usage key。
- `providers` 和 `keys` 变化后，自动刷新与之关联的筛选项和统计视图；其中 `providers` 只存在于管理员 Store 和管理员页面。
- 普通用户不创建、不加载 `providers` key；API Key 和调用记录中的订阅名称、平台等展示信息必须使用接口返回的脱敏关联字段。
- 页面切换不应重置 Store 中已有数据；已加载的数据直接显示，必要时后台刷新。

### 5.3 写操作后的局部更新

所有写操作统一遵循：

```text
提交写请求 -> 成功 -> 更新相关 cache -> 通知订阅者 -> 失败只更新错误状态
```

映射关系：

| 操作 | 成功后至少更新 |
| --- | --- |
| 新建/编辑/删除 Provider | `providers`、`keys` 的关联显示、Usage 筛选项 |
| 新建/删除/重置 Key | `keys`、个人总览 Key 统计 |
| 新建/编辑/删除用户 | `users`、按用户 Usage 的用户筛选项 |
| 修改当前密码 | `me` 可选刷新，不需要刷新列表 |
| Usage 时间范围变化 | 对应 Usage cache key |

如果后端响应没有返回完整资源，优先调用对应 `store.refresh(key)`，不要在前端猜测服务器生成字段。删除操作可以先做乐观移除，但必须在失败时回滚；第一版建议使用成功后刷新，逻辑更安全。

## 6. 占位数据与渲染规范

### 6.1 占位数据原则

占位数据只表达布局，不伪造业务事实：

- 文本使用 `—`、灰色块或固定长度的 skeleton，不显示看起来像真实账号、Key、用户的名称。
- 数字使用 `—` 或等宽 skeleton，不显示 `0`，避免和真实的零混淆。
- 操作按钮在占位阶段禁用，避免用户点击到无效 ID。
- 占位行数固定且接近正常布局，减少数据回来后的页面跳动。
- 不把占位数据写入 `localStorage`，也不参与筛选、排序、统计。

### 6.2 推荐 DOM/CSS 约定

在 `common.css` 增加通用样式：

```css
.skeleton {
  display: inline-block;
  min-width: 72px;
  height: 12px;
  border-radius: 6px;
  background: linear-gradient(90deg, #182333, #27354a, #182333);
  background-size: 200% 100%;
  animation: skeleton-shimmer 1.4s ease-in-out infinite;
}

@keyframes skeleton-shimmer {
  from { background-position: 200% 0; }
  to { background-position: -200% 0; }
}
```

建议组件根据快照统一分支：

```js
function renderAccounts(snapshot) {
  if (snapshot.status === 'loading' && snapshot.isPlaceholder) {
    return renderAccountSkeleton();
  }
  if (snapshot.status === 'error' && !snapshot.data.length) {
    return renderRetry('订阅加载失败', snapshot.error);
  }
  return renderAccountRows(snapshot.data, snapshot.status === 'refreshing');
}
```

已有真实数据进入 `refreshing` 时保留表格和内容，只在表格容器或标题处显示轻量“更新中”提示，避免闪回 skeleton。

### 6.3 空数据和错误必须区分

| 状态 | 展示 |
| --- | --- |
| 首次加载 | skeleton，占位按钮禁用 |
| 成功但为空 | “暂无数据”及下一步操作提示 |
| 已有数据刷新 | 原数据 + 更新中提示 |
| 首次加载失败 | 错误说明 + 重试 |
| 刷新失败且有旧数据 | 原数据 + 非阻塞错误提示 + 重试 |

## 7. 组件订阅边界

建议由页面级渲染函数订阅 Store，而不是让任意事件处理器直接改 DOM：

| 组件 | 订阅 |
| --- | --- |
| 个人总览 | `me`、`keys`、`recentCalls` |
| 系统总览 | `providers`、`usageBySubscription`、`usageByUser` |
| 订阅管理 | `providers` |
| API Key | `keys`；管理员额外订阅 `providers` |
| 用户管理 | `users`（仅管理员） |
| 调用记录 | 当前查询对应的 `logs` |

事件处理器只负责调用 Store 方法或触发刷新，例如删除订阅成功后调用 `store.refresh('providers')`，不再直接修改 `accounts` 数组并手动调用多个 `render*`。

## 8. 分阶段落地步骤

### Phase 1：拆分数据文件与权限加载，不改变视觉

- 新增 `internal/service/static/home-data.js`，实现 `createHomeDataStore()` 和订阅机制。
- 在 `home.html` 中先加载 `home-data.js`，再加载 `home.js`。
- 把现有 `request()` 以依赖注入方式接入 Store。
- 将 `accounts`、`keys`、`users`、`currentUser`、`personalRecentLogs` 迁移为 Store key，其中 `providers`、`users` 仅在 admin 确认后创建/加载。
- 保留现有 `render*` 函数，但改为接收 snapshot 或从 Store 读取只读数据。
- 将 `loadData()` 拆成角色确认、普通用户数据加载和管理员数据加载三部分。

完成标准：现有功能行为不变，写操作不再依赖全量 `loadData()`。

### Phase 2：占位数据和局部状态

- 为所有列表、统计卡片、活动记录增加 skeleton 渲染。
- `showApp()` 改为先展示应用壳和占位数据。
- 实现首次失败、局部重试、已有数据刷新三种状态。
- 在 `common.css` 增加 skeleton、刷新中和错误提示样式。

完成标准：弱网下首屏在请求完成前可见，数据返回后无整页白屏或明显布局跳动。

### Phase 3：订阅完善与竞态控制

- 所有页面初始化/卸载时注册和取消订阅。
- 日志搜索增加请求序号或 `AbortController`，旧查询不能覆盖新查询。
- Usage 的每组筛选条件使用独立 cache key。
- 增加刷新去重：同一 key 已有相同请求时复用 Promise。

完成标准：快速切换页面、筛选和时间范围时，最终显示内容与最后一次操作一致。

### Phase 4：清理与模块边界固化

- 删除旧的全局数组和重复的 `loadData()` 调用。
- 固化 `home-data.js` 只包含 Store、请求协调、缓存和订阅；`home.js` 只包含视图、事件和导航。
- 如代码量确实影响维护，再将渲染器拆成 `home-render.js`；这不是本次改造的前置条件。
- 更新 `docs/service-static.md`，补充新的前端状态约定。

## 9. 测试与验收清单

### Store 单元测试

- `subscribe()` 注册后收到当前快照。
- 首次 `load()` 先通知 loading/placeholder，再通知 ready/真实数据。
- 已有真实数据 `refresh()` 不会清空旧数据。
- 请求失败会进入 error，重试成功回到 ready。
- 两个并发请求按 requestId 丢弃旧响应。
- 取消订阅后不再收到通知；订阅者异常不影响其他订阅者。
- 相同 key 的重复请求能够去重。

### 页面集成测试

- `/home` 加载时先看到应用壳、统计卡片和表格 skeleton。
- `/api/me` 失败时能回到登录/显示认证错误；其他非认证接口失败只影响对应区域。
- 普通用户不会加载管理员用户和管理员 Usage 数据。
- Provider、Key、User 的增删改成功后，关联列表和统计自动更新。
- 搜索、分页和 Usage 时间范围快速连续变更后，最终结果对应最后一次请求。
- 无数据时显示空态，不显示 skeleton 或错误态。
- 登出后取消请求和订阅，不会在登录页继续写入旧页面 DOM。

### 非功能验收

- 不在占位数据中放入真实 Credential、API Key 或用户信息。
- 现有鉴权、错误翻译、XSS 转义规则保持不变。
- 对现有 API 无新增强依赖；如未来需要服务端推送，再在 Store 外层增加事件源适配器。
- 首屏不因 Usage、日志详情等非首屏数据阻塞。

## 10. 后续可扩展方向

如果以后需要“服务器数据变化订阅”不仅限于本页面内的请求结果，可以保持 Store 的订阅接口不变，在底层增加事件源：

```text
HTTP 初始快照 -> HomeDataStore
SSE/WebSocket 增量事件 -> HomeDataStore.applyEvent()
                         -> 通知现有 subscribers
```

事件建议统一为：

```json
{
  "type": "provider.updated",
  "resource": "providers",
  "id": "provider-id",
  "version": 12,
  "data": {}
}
```

第一阶段不建议直接引入 WebSocket/SSE：当前需求首先是前端数据变化订阅和一致的刷新机制，HTTP 请求完成后的 Store 通知已经可以覆盖页面内的数据联动；等后端具备事件广播和断线重连语义后，再增加推送适配器。

## 11. 推荐实施顺序

按以下顺序提交，便于回滚和定位问题：

1. `HomeDataStore` + Store 测试。
2. 基础数据迁移：普通用户使用 `me`、`keys`、`recentCalls`；管理员额外使用 `providers`、`users`。
3. 写操作局部刷新。
4. skeleton 和错误/空态渲染。
5. 日志、Usage 的按需 cache 与竞态控制。
6. 删除旧全量加载逻辑并更新静态页面设计文档。

最终目标是：页面组件只关心“如何展示一个 snapshot”，数据请求、缓存、刷新、错误恢复和变化通知全部由 `HomeDataStore` 负责。
