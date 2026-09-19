# 临时文档：模型价格、单次花费公式与快速模式

> 状态：**临时调研笔记**，非正式产品文档。  
> 整理日期：2026-09-19  
> 模型列表来源：本地库 `data/ai-unisub.db` → `configs`（`type=supplier`，`name ∈ {openai, grok, deepseek}`）  
> 价格来源：[LiteLLM `model_prices_and_context_window.json`](https://raw.githubusercontent.com/BerriAI/litellm/refs/heads/main/model_prices_and_context_window.json)  
> Codex 速度档来源：[openai/codex `models.json`](https://raw.githubusercontent.com/openai/codex/refs/heads/main/codex-rs/models-manager/models.json)  
> OpenAI Fast mode 文档：[Fast mode](https://platform.openai.com/docs/guides/priority-processing.md)、[Flex](https://platform.openai.com/docs/guides/flex-processing.md)

## 1. 库内现状

- 供应商 overlay 只存 `{"models":[...]}`，**没有**单价字段。
- 代码 builtin 默认模型已被 DB overlay 替换为下表名称。
- 下表 $/token 适合 **API Key 按量** 估算；**ChatGPT / Codex / SuperGrok 订阅**多为额度制，Fast 在订阅侧表现为 **increased usage**，不是单独再开一张 API 账单。

### 1.1 DB 模型列表快照

| 供应商 | 模型 |
| --- | --- |
| openai | `codex-auto-review`, `gpt-5.4`, `gpt-5.5`, `gpt-5.6-luna`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-6-astra`, `gpt-daybreak-blue-latest`, `gpt-daybreak-red-latest` |
| grok | `grok-4.20-0309-non-reasoning`, `grok-4.20-0309-reasoning`, `grok-4.20-multi-agent-0309`, `grok-4.3`, `grok-4.5`, `grok-4.6`, `grok-build-0.1`, `grok-imagine-image`, `grok-imagine-image-2.0`, `grok-imagine-image-quality`, `grok-imagine-video`, `grok-imagine-video-1.5` |
| deepseek | `deepseek-flash`, `deepseek-v4-pro` |

---

## 2. 符号与通用花费公式

| 符号 | 含义 | UniSub `call_traces` 字段 |
| --- | --- | --- |
| \(I\) | input tokens（不含 cache） | `input_tokens` |
| \(O\) | output tokens | `output_tokens` |
| \(C_r\) | cache read tokens | `cache_read_tokens` |
| \(C_w\) | cache creation / write tokens | `cache_creation_tokens` |
| \(N_{img}\) | 图像张数 | （trace 无现成字段） |
| \(T\) | 视频秒数 | （trace 无现成字段） |
| \(P_{in}, P_{out}, P_{cr}, P_{cw}\) | 单价（**USD / 1M tokens**） | — |

**文本模型（Standard）：**

\[
\mathrm{cost} = \frac{I\cdot P_{in} + O\cdot P_{out} + C_r\cdot P_{cr} + C_w\cdot P_{cw}}{10^6}
\]

缺项按 0。伪代码：

```text
cost_usd = (I*P_in + O*P_out + C_r*P_cr + C_w*P_cw) / 1_000_000
```

**Fast / Priority（API 按量）：** 同上，改用 priority 单价 \(P^{pri}\)。

**图像：** \(\mathrm{cost} = P_{img}\times N_{img}\)  
**视频：** \(\mathrm{cost} = P_{img}\times N_{img} + P_{sec}(res)\times T\)

---

## 3. OpenAI / Codex 模型价格（含快速模式）

单位：USD / 1M tokens，除非另注。  
LiteLLM key 与模型 slug 同名（无前缀）。  
`codex-auto-review`：**LiteLLM 无价目**。

### 3.1 Codex 模型目录中的速度档（订阅 UI / 配置）

| 模型 | Fast 档（`service_tiers`） | 说明（models.json 原文） | `additional_speed_tiers` |
| --- | --- | --- | --- |
| `gpt-6-astra` | `priority` → 显示名 **Fast** | **2× speed, increased usage** | `["fast"]` |
| `gpt-5.6-sol` | `priority` → **Fast**；另有 **`ultrafast` → Ultrafast** | Fast：**1.5× speed, increased usage**；Ultrafast：最低延迟 | `["fast"]` |
| `gpt-5.6-terra` | `priority` → Fast | 1.5×，用量增加 | `["fast"]` |
| `gpt-5.6-luna` | `priority` → Fast | 1.5×，用量增加 | `["fast"]` |
| `gpt-5.5` | `priority` → Fast | 1.5×，用量增加 | `["fast"]` |
| `gpt-5.4` | `priority` → Fast | 1.5×，用量增加 | `["fast"]` |
| `codex-auto-review` | `priority` → Fast | 1.5×，用量增加 | `["fast"]` |
| `gpt-daybreak-blue-latest` | **无** | — | `[]` |
| `gpt-daybreak-red-latest` | **无** | — | `[]` |

Codex 开启方式：

| 方式 | 做法 |
| --- | --- |
| TUI | 快捷键 **Turn Fast mode on or off** |
| 配置 | `~/.codex/config.toml` → `service_tier` |
| CLI | `codex -c service_tier='"priority"'`（或 `ultrafast` 等） |

与 **`model_reasoning_effort = low`** 不同：后者是少推理，不是 service tier。

### 3.2 Standard / Fast(Priority) / Flex / Batch 单价

| 模型 | Standard in/out | Fast/Priority in/out | Flex in/out | Batch in/out | Cache read Std / Pri | Cache write Std / Pri | max in / out |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| `codex-auto-review` | — | — | — | — | — | — | 无 LiteLLM 价 |
| `gpt-5.4` | 2.5 / 15 | **5 / 30** | 1.25 / 7.5 | 1.25 / 7.5 | 0.25 / 0.5 | — | 1.05M / 128K |
| `gpt-5.5` | 5 / 30 | **12.5 / 75** | 2.5 / 15 | 2.5 / 15 | 0.5 / 1.25 | — | 1.05M / 128K |
| `gpt-5.6-luna` | 0.2 / 1.2 | **0.4 / 2.4** | 0.1 / 0.6 | 0.1 / 0.6 | 0.02 / 0.04 | 0.25 / 0.5 | 922K / 128K |
| `gpt-5.6-sol` | 4 / 20 | **8 / 40** | 2 / 10 | 2 / 10 | 0.4 / 0.8 | 5 / 10 | 922K / 128K |
| `gpt-5.6-terra` | 2 / 12 | **4 / 24** | 1 / 6 | 1 / 6 | 0.2 / 0.4 | 2.5 / 5 | 922K / 128K |
| `gpt-6-astra` | 10 / 50 | **20 / 100** | 5 / 25 | 5 / 25 | 1 / 2 | 12.5 / 25 | 922K / 128K |
| `gpt-daybreak-blue-latest` | 4 / 20 | LiteLLM **无** priority 字段 | — | — | 0.4 / — | 5 / — | 922K / 128K |
| `gpt-daybreak-red-latest` | 12.5 / 75 | LiteLLM **无** priority 字段 | — | — | 1.25 / — | 15.625 / — | 400K / 128K |

官方对 **GPT-5.6 Sol Fast**：Standard 的 **2×**（短上下文 $8 / $40；长上下文 $16 / $60）。与 LiteLLM priority 列一致。

API 侧 Fast 常见约 **2× Standard**；Codex 订阅目录对多数 5.x 写 **1.5× usage**，Astra 写 **2× usage**——**订阅扣额倍率与 API 美元倍率不要混用**。

### 3.3 长上下文（>272K）加价（LiteLLM，节选）

部分模型在 input 超过约 272K tokens 时另有更高单价；Fast 再叠 priority 系数。示例 `gpt-5.6-sol`：

| 档 | input | output | cache read | cache write |
| --- | ---: | ---: | ---: | ---: |
| Standard ≤272K | 4 | 20 | 0.4 | 5 |
| Standard >272K | 8 | 30 | 0.8 | 10 |
| Priority ≤272K | 8 | 40 | 0.8 | 10 |
| Priority >272K | 16 | 60 | 1.6 | 20 |
| Flex ≤272K | 2 | 10 | 0.2 | 2.5 |

### 3.4 OpenAI 单次花费公式（含 Fast）

| 模型 | Standard | Fast / Priority |
| --- | --- | --- |
| `codex-auto-review` | 无法计算 | 无法计算 |
| `gpt-5.4` | \((2.5I + 15O + 0.25C_r)/10^6\) | \((5I + 30O + 0.5C_r)/10^6\) |
| `gpt-5.5` | \((5I + 30O + 0.5C_r)/10^6\) | \((12.5I + 75O + 1.25C_r)/10^6\) |
| `gpt-5.6-luna` | \((0.2I + 1.2O + 0.02C_r + 0.25C_w)/10^6\) | \((0.4I + 2.4O + 0.04C_r + 0.5C_w)/10^6\) |
| `gpt-5.6-sol` | \((4I + 20O + 0.4C_r + 5C_w)/10^6\) | \((8I + 40O + 0.8C_r + 10C_w)/10^6\) |
| `gpt-5.6-terra` | \((2I + 12O + 0.2C_r + 2.5C_w)/10^6\) | \((4I + 24O + 0.4C_r + 5C_w)/10^6\) |
| `gpt-6-astra` | \((10I + 50O + 1C_r + 12.5C_w)/10^6\) | \((20I + 100O + 2C_r + 25C_w)/10^6\) |
| `gpt-daybreak-blue-latest` | \((4I + 20O + 0.4C_r + 5C_w)/10^6\) | 无 LiteLLM priority 价 |
| `gpt-daybreak-red-latest` | \((12.5I + 75O + 1.25C_r + 15.625C_w)/10^6\) | 无 LiteLLM priority 价 |

示例：`gpt-5.4`，\(I=10000\)，\(O=2000\)，无 cache  
- Standard：\((2.5\times10000 + 15\times2000)/10^6 = 0.055\) USD  
- Fast：\((5\times10000 + 30\times2000)/10^6 = 0.11\) USD  

### 3.5 OpenAI `service_tier` 光谱

| `service_tier` | 速度 | 价格（相对 Standard） | 场景 |
| --- | --- | --- | --- |
| `fast` / `priority` | 更快更稳（文档称最高约 2.5×，视模型） | 更贵（API 多为 ~2×） | 延迟敏感 |
| `ultrafast` | 最低延迟（主要 `gpt-5.6-sol`） | 更高 / 受控访问 | 极致延迟 |
| `auto` / 缺省 → 常为 `default` | 正常 | 基准 | 一般在线 |
| `flex` | 更慢，可能 429 | ~Batch，约 0.5× | 评测、异步 |
| Batch API | 异步（可至 24h） | ~0.5× | 大批量离线 |

- Priority processing 已更名为 **Fast mode**（文档称 2026-07-30）；请求仍可用 `priority` 或 `fast`。  
- Fast 与 Standard **共用**该模型 rate limit；另有 **ramp rate**：TPM 爬升过猛可能降回 `default` 并按标准计费。  
- 与 **Scale Tier**（预购容量）是另一套。  
- 一般不支持微调模型、embeddings；部分数据驻留组合有限制。

---

## 4. Grok / xAI 价格

LiteLLM provider：`xai`，key 形如 `xai/<slug>`。  
**无** OpenAI 式 `service_tier` Fast 字段。

### 4.1 文本

| 模型 | input | output | cache read | max in/out | 单次公式（标准档） |
| --- | ---: | ---: | ---: | --- | --- |
| `grok-4.20-0309-non-reasoning` | 1.25 | 2.5 | 0.2 | 1M / 1M | \((1.25I + 2.5O + 0.2C_r)/10^6\) |
| `grok-4.20-0309-reasoning` | 1.25 | 2.5 | 0.2 | 1M / 1M | 同上 |
| `grok-4.20-multi-agent-0309` | 1.25 | 2.5 | 0.2 | 1M / 1M | 同上（mode=responses） |
| `grok-4.3` | 1.25 | 2.5 | 0.2 | 1M / 1M | 同上 |
| `grok-4.5` | 2.0 | 6.0 | 0.3 | 500K / 500K | \((2I + 6O + 0.3C_r)/10^6\) |
| `grok-4.6` | 2.0 | 6.0 | 0.5 | 500K / 500K | \((2I + 6O + 0.5C_r)/10^6\) |
| `grok-build-0.1` | 1.0† | 2.0† | 0.2† | 256K / 256K | 见下阶梯 |

† **`grok-build-0.1` 与多款 grok-4.x：LiteLLM 含 `*_above_200k_tokens` 翻倍档**（以官方是否分段/整单升档为准）：

| | ≤200K | >200K |
| --- | ---: | ---: |
| input | 1.0 | 2.0 |
| output | 2.0 | 4.0 |
| cache read | 0.2 | 0.4 |

（`grok-4.5` / `4.6` 等 >200K 为 input 4 / output 12 等，见 LiteLLM 原字段。）

### 4.2 图像 / 视频

| 模型 | 计价 | 单次公式 |
| --- | --- | --- |
| `grok-imagine-image` | $0.02 / 图 | \(0.02 N_{img}\) |
| `grok-imagine-image-2.0` | $0.06 / 图 | \(0.06 N_{img}\) |
| `grok-imagine-image-quality` | $0.05 / 图 | \(0.05 N_{img}\) |
| `grok-imagine-video` | 输入图 $0.002；输出 480p $0.05/s，720p $0.07/s | \(0.002 N_{img} + P_{sec}T\) |
| `grok-imagine-video-1.5` | 输入图 $0.01；480p $0.08/s，720p $0.14/s，1080p $0.25/s | \(0.01 N_{img} + P_{sec}T\) |

示例：`grok-imagine-video-1.5`，1 张参考图，720p 6 秒 → \(0.01 + 0.14\times6 = 0.85\) USD。

---

## 5. DeepSeek 价格

| 模型 | input | output | cache read | cache write | max in/out | 单次公式 |
| --- | ---: | ---: | ---: | ---: | --- | --- |
| `deepseek-flash` | 0.30 | 1.20 | 0.006 | 0 | 1M / 393K | \((0.3I + 1.2O + 0.006C_r)/10^6\) |
| `deepseek-v4-pro` | 1.32 | 3.96 | 0.044 | 0 | 1M / 393K | \((1.32I + 3.96O + 0.044C_r)/10^6\) |

LiteLLM 另有同价别名 `deepseek-v4-flash`。无 priority/flex 字段。

示例：`deepseek-v4-pro`，\(I=50000\)，\(O=5000\)，\(C_r=20000\)  
→ \((1.32\times50000 + 3.96\times5000 + 0.044\times20000)/10^6 = 0.08668\) USD。

---

## 6. HTTP 如何检查是否快速模式

**看 JSON body 的 `service_tier`，不要看普通 HTTP Header。**

适用：`POST /v1/responses`、`POST /v1/chat/completions`。

### 6.1 请求（意图）

```json
{
  "model": "gpt-5.6-sol",
  "input": "...",
  "service_tier": "fast"
}
```

| 请求值 | 含义 |
| --- | --- |
| `fast` / `priority` | 申请 Fast mode（Codex Fast 的 id 常为 `priority`） |
| `ultrafast` | 申请 Ultrafast |
| `default` | 明确 Standard |
| `flex` | Flex |
| `auto` / **缺省** | 跟项目默认；项目若默认 Fast，body 可无字段仍走 Fast |

```bash
jq -r '.service_tier // "auto(缺省)"' request.json
```

UniSub：看 call trace 的 `request_body` 顶层 `service_tier`。

### 6.2 响应（实际结果，以这个为准）

官方：响应 `service_tier` = **真正处理该请求的档位**，可与请求不同（ramp 降档 → `default`）。

| 响应值 | 是否快速模式 |
| --- | --- |
| `priority` / `fast` | 是 Fast |
| `ultrafast` | 是 Ultrafast |
| `default` | 否（含申请了 Fast 但被降档） |
| `flex` | 否 |
| 缺失 | 未声明，不能当 Fast |

注意：

- 请求写 `fast` 或 `priority` 时，多数 GPT-5.6 及更早模型响应常统一为 **`priority`**。  
- 流式：看**完成 chunk**，不要只扫中间 delta。

```bash
jq -r '.service_tier // empty' response.json
```

### 6.3 不要误判

| 现象 | 是否 Fast |
| --- | --- |
| Header `Priority: u=4, i` | **否**（HTTP 优先级；与 OpenAI Fast 无关） |
| `reasoning_effort: low` | **否** |
| 模型名含 `fast` | **否** |
| 仅请求有 `service_tier=fast` | 只表示意图；计费/额度看**响应** |

### 6.4 检查清单

```text
1. request.service_tier ∈ {fast, priority, ultrafast}?  → 客户端申请了快速档
2. response.service_tier ∈ {fast, priority, ultrafast}? → 实际上是快速档
3. 请求要了 fast/priority，响应 default                 → 申请了但降到标准档
4. 请求无字段，响应 priority/fast                       → 多半项目默认 Fast
```

```python
req_tier = request_json.get("service_tier")   # None → auto
resp_tier = response_json.get("service_tier")
requested_fast = req_tier in ("fast", "priority", "ultrafast")
actual_fast = resp_tier in ("fast", "priority", "ultrafast")
# 计费/额度是否按快速档：用 actual_fast
```

---

## 7. 覆盖率摘要

| 供应商 | DB 模型数 | LiteLLM 有价 | 备注 |
| --- | ---: | ---: | --- |
| openai | 9 | 8 | 缺 `codex-auto-review`；Daybreak 无 priority 价列 |
| grok | 12 | 12 | 图像/视频非 token 计价 |
| deepseek | 2 | 2 | — |

---

## 8. 来源与局限

1. 单价来自 LiteLLM 聚合表，不是 UniSub DB 字段，也非实时官方 API。  
2. 订阅账号（Codex / SuperGrok）实际多为套餐额度；表上 $/token 更适合 API Key。  
3. Codex Fast 的「1.5× / 2× usage」是**额度消耗**描述；API Fast 的 ~2× 是**美元单价**。  
4. reasoning 的思考 token 若已计入 `output_tokens`，公式中的 \(O\) 已包含。  
5. 图像/视频当前 trace 若只有 token 列，无法直接套文本公式。  
6. 本文件为临时笔记；落地计费或供应商配置前需再对官方定价页与实抓响应校验。

## 9. 相关链接

- LiteLLM 价目：https://raw.githubusercontent.com/BerriAI/litellm/refs/heads/main/model_prices_and_context_window.json  
- Codex models.json：https://raw.githubusercontent.com/openai/codex/refs/heads/main/codex-rs/models-manager/models.json  
- OpenAI Fast mode：https://platform.openai.com/docs/guides/priority-processing.md  
- OpenAI Flex：https://platform.openai.com/docs/guides/flex-processing.md  
- OpenAI Pricing（Fast 筛选）：https://developers.openai.com/api/docs/pricing?latest-pricing=fast  
