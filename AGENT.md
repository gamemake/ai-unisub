# UniSub

UniSub - AI 网关服务，主要功能：
* 订阅转 api， 支持 OpenAI Anthropic Grok。
* 支持指定 api_key 和 base url。
* 支持多用户

开发环境：
* 服务器使用 GO 语言开发，数据库用 sqlite。
* Web 客户端 shadcn/ui + Vite + React + Tailwind CSS

## 目录结构

| 目录 | 说明 |
| --- | --- |
| cmd/unisub | 服务程序入口 |
| internal/ | GO 实现的服务器库 |
| internal/web/embed.go | //go:embed all:dist |
| internal/web/dist/ | Vite 构建产物，gitignore |
| src/ | Web 端代码 |
| public/ | 公共文件 |

## 服务器

服务器采用包和模块化设计：
* 整体分割为几个独立的根业务无关的包
* unisub 基于 service 框架又分割为几个独立的模块
    * oauth 认证流程
    * 静态文件
    * api 请求

| 名字 | 说明 | 文档 |
| --- | --- | --- |
| common | 集中定义了 http 错误码 | docs/common-design.md |
| database | 数据库模块 | docs/database-design.md |
| proxy | 网络代理管理模块 | docs/proxy-design.md |
| oauth | OAuth 认证模块，支持 Anthropic OpenAI Grok | docs/oauth-design.md |
| aiprovider | 网关 AI 服务提供者，支持订阅和 api_key 两种方式 | docs/aiprovider_design.md |
| service | 服务器框架。整合 database oauth aiprovider 模块，同时提供 http url 路由服务和支持模块化扩展模式。| docs/service_design.md |
| unisub | UniSub 服务端实现 | /docs/unisub_design.md |

## 客户端

Dashboard 风格的网页。

服务器两种运行模式：
* DEV 开发模式 - 服务器返回的静态页面有本从项目目录里读取。
* PRD 产品模式 - 静态页面整合在服务器可执行文件中。

客户端的技术要求：
* 独立的数据管理模块负责管理来自于服务器的数据，从数据管理模块刷新界面。
