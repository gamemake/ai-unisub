# 统计能力：数据库变化方案

本文说明若要在系统中统计「每日用量」「并发会话」等信息，**数据库需要做哪些变化**。  
只覆盖表结构、索引、迁移与保留策略；不展开 API / 前端实现。

相关现状文档：[`docs/database.md`](database.md)。

## 1. 结论

当前库**已经有明细**，但**没有面向统计的聚合表**：

| 能力 | 现状 | 要落库吗 |
| --- | --- | --- |
| 单次请求用量 | `request_logs_YYYYMMDD` 已记 Token / 状态 / 账号 / Key / model | 保留为事实来源 |
| 24h 用量 | `UsageSummary` **现查**日志分表聚合，无汇总表；旧 `usage_logs` 已删 | **要新增日聚合表**，否则跨天、跨保留期、多维统计会越来越慢 |
| 实时并发会话 | 内存 `Server.limiters`（每账号一个 buffered channel），DB 只有 `accounts.concurrency_limit` 配置 | **实时不必进 DB**；仪表盘读内存即可 |
| 历史并发峰值/分布 | **完全没有** | **要新增采样/日汇总表** |

数据库变化的核心是：在保留明细日志的前提下，增加 **「日用量聚合」** + **「并发历史」** 两类表；实时并发继续用内存。

## 2. 现状锚点

| 内容 | 位置 |
| --- | --- |
| 请求明细分表 | `internal/repository/request_logs.go` → `request_logs_YYYYMMDD` |
| 24h 汇总 | 同文件 `usageSummaryFromRequestLogs`（扫最近分表做 `COUNT/SUM`） |
| 并发闸门 | `internal/server/server.go` → `acquire`，`sync.Map` → `chan struct{}` |
| 旧用量表 | 迁移版本 7 已删除 `usage_logs` |

明细里已有可用维度：`account_id`、`api_key_id`、`provider`、`model`、`status_code`、`error_type`、各 Token、`duration_ms`、`started_at`。

## 3. 推荐分层

不要一上来一张「万能宽表」。SQLite 单写、WAL、单实例的前提下，**按查询粒度分表**更清晰，也方便不同保留期。

```text
request_logs_YYYYMMDD          ← 明细（保留短，如 30 天）
        │ 滚动聚合 / 收尾汇总
        ▼
usage_daily                    ← 账号 × 日（最小集，必做）
usage_daily_key                ← 账号 × Key × 日（个人/租户）
usage_daily_model              ← 账号 × provider × model × 日（细拆）
concurrency_samples            ← 实时采样落盘（历史曲线）
concurrency_daily              ← 账号 × 日峰值/均值（报表）
```

落地顺序建议：

1. **先做** `usage_daily`（账号 + 日）—— 系统总览、账号管理足够用。
2. **紧接着** `usage_daily_key`—— 个人总览、按 Key 审计。
3. **按需** `usage_daily_model`—— 表更大；model 空值要规范化（`''` 或 `'unknown'`）。
4. 并发：**内存实时 + `concurrency_samples` + `concurrency_daily`**。

## 4. 每日用量表

### 4.1 `usage_daily`（账号 × 日，最小集）

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `day` | TEXT | 本地日历日 `YYYY-MM-DD`（与日志分表同一 `time.Local`） |
| `account_id` | INTEGER NOT NULL | 上游账号 |
| `provider` | TEXT NOT NULL | 冗余，避免总览再 JOIN |
| `requests` | INTEGER NOT NULL DEFAULT 0 | 请求次数 |
| `success_2xx` | INTEGER NOT NULL DEFAULT 0 | 可选但很有用 |
| `client_error_4xx` | INTEGER NOT NULL DEFAULT 0 | 可选 |
| `server_error_5xx` | INTEGER NOT NULL DEFAULT 0 | 可选 |
| `rate_limited` | INTEGER NOT NULL DEFAULT 0 | `rate_limited` / 429 |
| `concurrency_limited` | INTEGER NOT NULL DEFAULT 0 | `concurrency_limited` |
| `input_tokens` … `total_tokens` | INTEGER NOT NULL DEFAULT 0 | 与现有 UsageSummary 对齐 |
| `duration_ms_sum` | INTEGER NOT NULL DEFAULT 0 | 平均耗时 = sum / requests |
| `duration_ms_max` | INTEGER NOT NULL DEFAULT 0 | 当日最大耗时 |
| `updated_at` | TEXT NOT NULL | 最后滚动更新时间 |

约束与索引：

- `PRIMARY KEY (day, account_id)` 或 `UNIQUE(day, account_id)`
- `INDEX (account_id, day DESC)` —— 账号近 N 天
- `INDEX (day)` —— 全站日汇总

### 4.2 `usage_daily_key`（账号 × Key × 日）

在 4.1 指标基础上增加：

| 列 | 说明 |
| --- | --- |
| `api_key_id` | 下游 Key；Key 删除后历史行仍保留（**不要 FK CASCADE**，与 request_logs 同策略） |

约束：`UNIQUE(day, api_key_id)`，或更严的 `UNIQUE(day, account_id, api_key_id)`。

### 4.3 `usage_daily_model`（账号 × provider × model × 日）

| 列 | 说明 |
| --- | --- |
| `model` | TEXT NOT NULL；空则存 `''` 或 `'unknown'`，必须稳定 |

约束：`UNIQUE(day, account_id, provider, model)`。

### 4.4 与明细的关系

- **不改**现有 `request_logs_*` 表结构也能做日用量（从明细聚合即可）。
- 可选增强（非必须）：明细增加 `queue_wait_ms`，便于统计排队；用现有 `ensureColumn` 幂等补齐即可。
- 聚合表保留期应 **长于** 日志（例如日志 30 天、用量 365 天）；清理按 `day` 删除，不依赖日志分表还在。

写入约定（库设计约束）：

- 请求结束时对当日行 `INSERT … ON CONFLICT DO UPDATE` 增量累加；或
- 整点 / 日终从当日 `request_logs_YYYYMMDD` 重算（实现简单、可自愈）。

可两者并用：**热路径增量 + 日终校正**。

## 5. 并发会话：实时 vs 历史

### 5.1 实时占用 —— 不必为「当前值」建业务表

当前闸门是内存 channel，进程重启后归零，这与「正在进行的会话」语义一致。  
仪表盘直接读内存的 `in_use / limit / waiting` 即可。

### 5.2 `concurrency_samples`（历史曲线）

定时（如每 10–30s）把各账号当前占用写入：

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `id` | INTEGER PK | 自增 |
| `sampled_at` | TEXT NOT NULL | 时间戳，全库时区约定统一即可 |
| `account_id` | INTEGER NOT NULL | |
| `in_use` | INTEGER NOT NULL | 当前占用槽位数 |
| `limit_value` | INTEGER NOT NULL | 采样时的 limit（配置可变） |
| `queued` | INTEGER NOT NULL DEFAULT 0 | 当前 channel 实现难精确；要准需先改内存闸门 |

索引：`(account_id, sampled_at DESC)`、`(sampled_at)`。  
清理：保留数天到数周即可，勿与年维度用量混一张表。

> 现有 `acquire` 用 buffered channel，**拿不到真实排队长度**。若 `queued` 要进库，需先改内存结构；表可以先留列，初期恒为 0。

### 5.3 `concurrency_daily`（峰值 / 分布报表）

| 列 | 说明 |
| --- | --- |
| `day` | `YYYY-MM-DD` |
| `account_id` | |
| `max_in_use` | 当日观察到的最大占用 |
| `max_limit` | 当日见过的最大 limit（或末日 limit） |
| `sample_count` | 样本数 |
| `in_use_sum` | 用于 `avg = in_use_sum / sample_count` |
| `acquire_ok` | 成功获得槽位次数（热路径计数） |
| `queue_timeouts` | `concurrency_limited` 次数 |
| `wait_ms_sum` / `wait_ms_max` | 可选；依赖热路径记录排队等待 |

约束：`UNIQUE(day, account_id)`。

若只要小时曲线，可另加 `concurrency_hourly`（`YYYY-MM-DDTHH`）；一般 **先 daily + samples 够用**。

## 6. 建议暂缓的表

这些都可以从明细或上面聚合派生，不必第一期建表：

| 需求 | 建议 |
| --- | --- |
| 错误类型分布 | 先看日志 / 用量表 limited 计数；量大再加 `usage_daily_error` |
| 延迟分位（P95/P99） | 先用 avg/max；或应用内 HDR 再定期刷表 |
| RPM 命中历史 | 内存计数器足够；要历史再加 `rate_limit_daily` |
| 上游配额曲线 | 已有 `accounts.quota_json` 最新值；要历史再加 `quota_snapshots` |

## 7. 迁移与兼容

贴合现有 `internal/database/database.go` 风格：

1. 新迁移版本（例如 **version 10**）：`CREATE TABLE IF NOT EXISTS` 上述固定表 + 索引。
2. **不要**给 `usage_*` / `concurrency_*` 挂 `REFERENCES … ON DELETE CASCADE`（账号/Key 删了，统计仍要能查；与 request_logs 一致）。
3. 可选回填：对仍存在的 `request_logs_*` 按天聚合写入 `usage_*`（一次性）；并发历史无法回填。
4. 同步更新 [`docs/database.md`](database.md) 的表清单与 ER。
5. 清理任务：在现有 request log retention 旁增加聚合表按 `day` / `sampled_at` 的删除。

**现有表几乎不用改列**即可支撑第一期；唯一值得考虑的明细增强是 `queue_wait_ms`。

## 8. 明确不做什么

- 不把实时 `in_use` 每请求同步写 DB（热路径太重，SQLite 单写会拖垮转发）。
- 不恢复旧 `usage_logs` 明细表；方向是 **聚合表**，不是第二份明细。
- 不做跨进程共享并发状态的 DB 锁（当前是单实例内存闸门）。

## 9. 建议落地顺序（仅库）

1. `usage_daily` + 索引 + 迁移 + 可选历史回填  
2. `usage_daily_key`  
3. `concurrency_daily` + `concurrency_samples`（`queued` 可先占位）  
4. 需要模型报表时再上 `usage_daily_model`  
5. 若要准确排队长度：先改内存闸门，再写真实 `queued` / `wait_ms_*`

## 10. 验收口径（库层面）

- 新库启动后上述表存在且约束正确。
- 旧库迁移可重复执行、不丢 `request_logs_*`。
- 删除账号/Key 后聚合行仍在。
- 日志分表按保留期删除后，更早的 `usage_daily*` 仍可查询。
- 文档与 `schema_migrations` 版本一致。
