# AIProvider 订阅套餐与用量权重

> 账号 `subscription_plan`、供应商 `subscription_plan_weights`、`UsageWeight*` 已实现；`Select` 同档加权随机仍是后续能力。总览见 [ai-provider.md](ai-provider.md)。

## 1. 目标

1. 在 `AIProviderConfig` 中记录订阅账号的**最高套餐档**（`subscription_plan`）。
2. 在**模型供应商**配置中维护各套餐的**用量权重**，供将来同优先级档内负载均衡。
3. 套餐身份只相信账号配置；权重只相信供应商配置（合并 builtin + overlay），不从上游／quota 推断。

不改变 `kind`（subscription / api / group）语义；不改变现有 `members[].weight` 优先级档语义。

## 2. 账号字段：`subscription_plan`

| 项 | 约定 |
| --- | --- |
| 字段名 | `subscription_plan` |
| JSON | `"subscription_plan": "codex_plus"` |
| Go | `SubscriptionPlan string \`json:"subscription_plan,omitempty"\`` |
| 取值 | 小写稳定 ID，**带平台前缀**；空表示「待补缺省」 |
| 适用范围 | 仅 `kind=subscription` |
| 语义 | 该账号**当前生效的最高套餐档** |
| 信任来源 | **只相信配置值** |

### 2.1 ID 前缀与目录

| 平台／适配器 | 供应商 id | ID 前缀／形态 | 合法 ID | 缺省 |
| --- | --- | --- | --- | --- |
| Codex | `openai` | `codex_` | `codex_plus`、`codex_pro_5x`、`codex_pro_20x` | `codex_plus` |
| Claude | `anthropic` | `claude_` | `claude_pro`、`claude_max_5x`、`claude_max_20x` | `claude_pro` |
| Grok | `grok` | `super_grok*` | `super_grok`、`super_grok_plus`、`super_grok_heavy` | `super_grok` |
| Dummy | 读 anthropic 权重 | 同 Claude | 同 Claude | `claude_pro` |

- **不提供** `free` / `team` / `enterprise`、无前缀 ID、独立 `codex_pro`、`codex_pro_10x`。
- API／组不得携带 `subscription_plan`。
- 加载时空值补缺省；非法 ID 拒绝保存。

## 3. 供应商字段：`subscription_plan_weights`（摊开存储）

### 3.1 位置与形状

用量权重属于**模型供应商**，不属于账号：

```json
{
  "name": "Anthropic",
  "models": ["…"],
  "subscription_plan_weights": {
    "claude_pro": 1,
    "claude_max_5x": 5,
    "claude_max_20x": 20
  }
}
```

| 项 | 约定 |
| --- | --- |
| 字段名 | `subscription_plan_weights` |
| 存储形态 | **摊开**：一层 JSON 对象 / Go `map[string]int`，键 = plan_id，值 = 正整数权重 |
| 不采用 | `[{id, weight}]` 数组；嵌套 `plans[].capacity`；写在 `AIProviderConfig` 上的第二套权重 |
| 持久化 | 与其它可配置项相同：builtin 缺省在代码；相对缺省的差异进 `PersistedConfig`（`type=supplier`, `name=<id>`）的 overlay |
| Overlay | 仅存与 builtin **不同**的 plan→weight 项（或整表 diff 策略与现有 map 类字段一致）；全部回到缺省则去掉该字段／配置行 |

「摊开」含义：权重表在供应商 JSON 里是**扁平键值**，每个套餐 ID 一列权重，便于读改与 diff，不必再解一层结构体列表。

### 3.2 代码缺省权重

| 供应商 | plan_id | 缺省用量权重 | 说明 |
| --- | --- | --- | --- |
| openai | `codex_plus` | **1** | 基线 |
| openai | `codex_pro_5x` | **5** | 约 5× |
| openai | `codex_pro_20x` | **20** | 约 20× |
| anthropic | `claude_pro` | **1** | 基线 |
| anthropic | `claude_max_5x` | **5** | **5×**（相对 Pro） |
| anthropic | `claude_max_20x` | **20** | **20×**（相对 Pro） |
| grok | `super_grok` | **1** | 基线 |
| grok | `super_grok_plus` | **3** | 中档 |
| grok | `super_grok_heavy` | **10** | 最高档 |
| deepseek / zhipu / kimi | — | 空 | 无订阅 plan 目录 |

管理员可在供应商 PUT 中覆盖上述数值；不能增删 plan 键集合以外的 ID（未知键拒绝）。

### 3.3 校验

- 键 ∈ 该供应商允许的 plan 目录（与账号 `subscription_plan` 目录一致）。
- 值：整数 **≥ 1**（禁止 0 与负数，避免加权随机除零／不可选）。
- 非订阅供应商提交非空 map：拒绝，或规范化为空（实现时与「无目录」一致即可）。
- Dummy 无独立供应商行：权重读 **anthropic** 合并后的表。

## 4. 用量权重 vs 成员优先级

| 概念 | 位置 | 含义 | 调度角色 |
| --- | --- | --- | --- |
| **调度优先级** | 组 `members[].weight`（1～5） | 先用哪一档成员 | 现行：取最高档 |
| **用量权重** | 供应商 `subscription_plan_weights[plan]` | 同档内相对用量／流量份额 | 将来：同档加权随机 |

二者正交。禁止用 plan 自动改写 `members[].weight`。

## 5. 计算函数（目标 API）

```go
// 读合并后的供应商表；plan 空则 DefaultSubscriptionPlan(adapter)。
// 未知 plan 回退缺省 plan 的权重，返回值恒 ≥ 1。
func UsageWeight(supplierID, plan string) int

// subscription → UsageWeight(账号所属供应商, config.SubscriptionPlan)
//   订阅供应商：codex→openai，claude/dummy→anthropic，grok→grok
// api → 1
// group → 不参与（调用方不应传入）
func UsageWeightFromConfig(adapter string, c AIProviderConfig) int
```

- **只读配置**（供应商权重表 + 账号 plan）；不读 quota、Header、上游 `plan_type`。
- 实现时从 `AIProviderManager` 的 catalog／`Supplier` 视图取表，而不是写死第二套与文档不一致的常量；**builtin 缺省**与上表一致，可作无 catalog 时的回退。

## 6. 将来接入 Select（不改热路径直至实现）

```text
1. 过滤不可用成员
2. 取最高 members[].weight 档
3. Session 粘性命中且仍在该档 → 复用
4. 否则按 UsageWeightFromConfig 对 choices 加权随机（替代均匀 rand）
```

## 7. 与现有模型关系

```text
kind=subscription
supplier / adapter
subscription_plan              ← 账号：哪一档（配置）
Supplier.subscription_plan_weights[plan]  ← 供应商：该档用量权重（摊开 map）
members[].weight               ← 组：优先级档
quota.subscription[]           ← 用量展示，不反推 plan／权重
```

## 8. 账号侧实现状态（已完成）

- Go 字段、目录校验、加载空值缺省、API 列表反映运行时 config。
- 前端订阅表单／列表展示套餐。
- 详见 [ai-provider.md](ai-provider.md) 配置表。

## 9. 供应商权重实现切分（备忘）

| 阶段 | 内容 |
| --- | --- |
| P0 文档 | 本约定 + ai-provider 正式摘要 |
| P1 | Supplier／Overlay／Diff／Validate、`UsageWeight*`、单测、catalog API、供应商 UI **已完成** |
| P2 | `Select` 同档加权随机（未做） |

## 10. 已定结论

1. Codex 倍率档 `codex_pro_5x` / `codex_pro_20x`；无独立 `codex_pro`。
2. **`claude_max_5x` / `claude_max_20x` 用量权重分别为 5 / 20**（相对 `claude_pro` = 1）。
3. 套餐 ID 带平台前缀；只相信账号配置的 plan。
4. Dummy 套餐与权重规则同 Claude（权重表用 anthropic 供应商）。
5. **用量权重放在模型供应商配置**；**摊开存储**为 `subscription_plan_weights: { plan_id: weight, … }`。
6. 用量权重 ≠ `members[].weight`；调度接入后置。
