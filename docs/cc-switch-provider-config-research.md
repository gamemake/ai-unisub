# CC Switch 供应商参数（UniSub 相关）

> 临时笔记。仅列 UniSub 需要处理的字段：`name`、`url`、`api`、`api_backend`、模型。  
> 来源：CC Switch v3.20.x 与 [farion1231/cc-switch](https://github.com/farion1231/cc-switch)。

字段在各客户端中的对应名不同，下表「CC Switch 侧」为实际写入名，「Deep link」为 `ccswitch://v1/import` 查询参数名（有则写出）。

---

## Claude Code（`app=claude`）

写入 `settings_config.env`。

| 逻辑字段 | CC Switch 侧 | Deep link | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| name | provider `name` | `name` | 是 | 供应商显示名 |
| url | `ANTHROPIC_BASE_URL` | `endpoint` | 是 | API 根前缀，**不带** `/v1/messages`（客户端自拼） |
| api | `ANTHROPIC_AUTH_TOKEN`（或 `ANTHROPIC_API_KEY`） | `apiKey` | 是 | Deep link 默认写入 `ANTHROPIC_AUTH_TOKEN` |
| api_backend | — | — | — | 无此字段；固定 Anthropic Messages |
| 模型 | `ANTHROPIC_MODEL` | `model` | 否 | 主模型 |
| 模型 | `ANTHROPIC_DEFAULT_SONNET_MODEL` | `sonnetModel` | 否 | Sonnet 默认 |
| 模型 | `ANTHROPIC_DEFAULT_OPUS_MODEL` | `opusModel` | 否 | Opus 默认 |
| 模型 | `ANTHROPIC_DEFAULT_HAIKU_MODEL` | `haikuModel` | 否 | Haiku 默认 |

---

## Claude Desktop（`app_type=claude-desktop`，直连）

与 Claude Code 分面板。无 provider deep link（`app=claude-desktop` 不在白名单）。UniSub 按**直连**处理：模型不填，Desktop 自行读上游 `/v1/models`。

| 逻辑字段 | CC Switch 侧 | Deep link | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| name | provider `name` | — | 是 | 供应商显示名 |
| url | `ANTHROPIC_BASE_URL` | — | 是 | 上游接口地址 |
| api | `ANTHROPIC_AUTH_TOKEN`（或 `ANTHROPIC_API_KEY`） | — | 是 | 默认 AUTH_TOKEN |
| api_backend | — | — | — | 无此字段；直连固定 Anthropic Messages |
| 模型 | — | — | — | 直连不填；由 Desktop 发现模型 |

---

## Codex（`app=codex`）

写入 `settings_config.auth` + `settings_config.config`（toml）。

| 逻辑字段 | CC Switch 侧 | Deep link | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| name | provider 段 `name`；及列表显示名 | `name` | 是 | Deep link 同时用作供应商名与 toml `name` |
| url | `[model_providers.*].base_url` | `endpoint` | 是 | 通常含 `/v1` |
| api | `auth.OPENAI_API_KEY` | `apiKey` | 是 | |
| api_backend | `wire_api` | —（生成时固定） | 是* | Deep link 固定 `responses` |
| 模型 | 顶层 `model` | `model` | 是* | Deep link 缺省 `gpt-5-codex` |

\* Deep link 未传 `model` 时用默认值；面板自定义仍应视为必填。

---

## Grok Build（`app=grokbuild`）

写入 `settings_config.config`（`~/.grok/config.toml` 片段）。

| 逻辑字段 | CC Switch 侧 | Deep link | 必填 | 说明 |
| --- | --- | --- | --- | --- |
| name | `[model."…"].name` | `name` | 是 | 模型表内显示名 |
| url | `[model."…"].base_url` | `endpoint` | 是 | 通常含 `/v1` |
| api | `[model."…"].api_key`（或 `env_key`） | `apiKey` | 是 | Deep link 写 `api_key` |
| api_backend | `[model."…"].api_backend` | —（生成时固定） | 是 | Deep link 固定 `responses` |
| 模型 | `[models].default` 与 `[model."…"].model` | `model` | 是* | Deep link 缺省 `grok-4.5`；profile 名与上游 model 可相同 |

\* Deep link 未传 `model` 时用默认值。

---

## UniSub 侧对照摘要

| 客户端 | name | url | api | api_backend | 模型 |
| --- | --- | --- | --- | --- | --- |
| Claude Code | Key 名称 | 公网根（无 `/v1`） | API Key | 无 | 主模型；可选三档 |
| Claude Desktop | 同上 | 同上或上游 base | 同上 | 无 | 不填（直连） |
| Codex | 同上 | 公网根 + `/v1` | 同上 | `responses` | 必填（或接受对方默认） |
| Grok Build | 同上 | 公网根 + `/v1` | 同上 | `responses` | 必填（或接受对方默认） |
