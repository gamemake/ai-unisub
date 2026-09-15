# shadcn/ui Base UI 组件

本目录从 shadcn/ui 官方 `base-nova` registry 引入（2026-09-15），不是 Radix 版本。源码随项目维护。

- 官方文档：https://ui.shadcn.com/docs/components/base
- 源码 registry：`https://ui.shadcn.com/r/styles/base-nova/{name}.json`
- 上游仓库：https://github.com/shadcn-ui/ui （MIT）
- `components.json` 的 `style` 保持 `base-nova`；新增控件也应选 Base UI 版本。
- registry 模板的 `cn` 已映射为 `@/lib/utils`，图标占位符已展开为 Lucide。
- 业务页面不得直接编写原生交互控件；使用这里的源码组件，必要时在 `src/components/` 中组合业务适配器。
- 静态组件内部使用 HTML 是官方实现的一部分：例如 Table、Label、Textarea。Calendar 使用官方 React Day Picker，日期和月份按钮使用 Base UI Button。
- 不使用 Radix `asChild`；链接按钮使用 Base UI `render` 和 `nativeButton={false}`。
- 本地适配包括按钮/输入框高度、表格间距、中文关闭标签、Card 标题语义和移动端宽度、Calendar 焦点 ref。主题令牌统一在 `src/index.css`。

`AppSelect` 负责领域字符串值、必填占位和标签映射；`DatePicker` 负责日期格式、范围约束与中文日历；`shared.tsx` 组合 Field、Dialog、AlertDialog 等组件，不实现另一套交互基础。

验证：`npm run build`、`npm test`、`npm run test:e2e`。
