---
kind: frontend_style
name: HRPlus Next.js 前端样式体系（MUI + Emotion + CSS 变量）
category: frontend_style
scope:
    - '**'
source_files:
    - goodhr5/cloud/frontend-next/package.json
    - goodhr5/cloud/frontend-next/app/theme.ts
    - goodhr5/cloud/frontend-next/app/providers.tsx
    - goodhr5/cloud/frontend-next/app/globals.css
    - goodhr5/cloud/frontend-next/next.config.ts
---

## 1. 使用的系统与工具

- **框架**：Next.js 16.2.9（App Router），React 19.2.0。
- **组件库**：@mui/material 9.1.1 + @mui/icons-material 9.1.1，通过 `@mui/material-nextjs` 的 `AppRouterCacheProvider` 集成到 App Router。
- **样式引擎**：Emotion（`@emotion/react`、`@emotion/styled`、`@emotion/cache`），由 MUI 内部使用；全局样式与少量自定义动画写在原生 CSS。
- **构建输出**：默认 `standalone`，可通过环境变量 `GOODHR_STATIC_EXPORT=1` 切换为静态导出模式（`next.config.ts` 中判断）。

## 2. 关键文件

- `goodhr5/cloud/frontend-next/app/theme.ts` — 唯一主题定义，集中声明调色板、字体、圆角、组件覆盖。
- `goodhr5/cloud/frontend-next/app/providers.tsx` — 根 Provider，注入 `ThemeProvider` + `CssBaseline` + `AppRouterCacheProvider`。
- `goodhr5/cloud/frontend-next/app/globals.css` — 全局基础样式、CSS 自定义属性、关键帧动画、`prefers-reduced-motion` 无障碍适配。
- `goodhr5/cloud/frontend-next/next.config.ts` — 构建期样式相关配置（静态导出时关闭图片优化、禁用 `X-Powered-By`）。
- `goodhr5/cloud/frontend-next/components/admin/*.tsx` — 管理后台 UI 组件，全部基于 MUI 组件组合。

## 3. 架构与约定

### 3.1 主题系统（单一明亮蓝色主题）

`createHRPlusTheme()` 在 `app/theme.ts` 中通过 `createTheme` 生成统一主题，全站只使用这一套浅色蓝色主题（`mode: "light"`）：

- 品牌主色 `#0052CC`，深色 `#003D99`，浅背景 `#EBF0FF`。
- 文字色 `#1A1F36`，辅助色 `#5E6580`，分割线 `#D6DCF0`。
- 语义色：成功 `#238653`，警告 `#c47a1a`，错误 `#c83f49`。
- 全局圆角 `borderRadius: 8`，按钮统一 `minHeight: 44`、`borderRadius: 999`（胶囊形）、无阴影、`paddingInline: 20`。
- 输入框统一 `variant: "outlined"`，`MuiOutlinedInput` 根节点 `minHeight: 56`、`borderRadius: 18`。
- 字体栈：`Inter, "SF Pro Display", "PingFang SC", "Microsoft YaHei", Arial, sans-serif`。
- 组件覆盖集中在 `components.MuiButton` / `MuiPaper` / `MuiTextField` / `MuiOutlinedInput` 的 `styleOverrides` 或 `defaultProps` 中。

该主题在 `providers.tsx` 中实例化一次并作为 `ThemeProvider` 的 theme 传入，所有页面通过 MUI 的 `useTheme` / styled API 消费。没有暗色模式实现。

### 3.2 全局 CSS 与 CSS 变量

`globals.css` 定义了站点级 CSS 变量：

- `--goodhr-brand: #0052CC`（与 MUI primary.main 一致）
- `--goodhr-ink: #1A1F36`

并通过 `color-scheme: light` 声明亮色方案。选择器高亮、滚动行为、body 溢出隐藏、链接继承颜色等基础重置也在此处完成。

### 3.3 动画策略

所有动效以原生 `@keyframes` 定义在 `globals.css` 中，包括：

- `signalFlow`、`workflowProgressX/Y`、`workflowNode` — 工作流可视化动效。
- `auroraShift` — 登录页极光渐变背景。
- `fadeInUp` — 登录卡片入场动画，配合 `.login-fade-in` / `.login-fade-in-delay` 两个类名使用。

通过 `@media (prefers-reduced-motion: reduce)` 将动画时长强制为 `0.01ms` 且迭代次数为 1，满足无障碍要求。

### 3.4 组件组织

- 公共展示型组件放在 `components/`（如 `BrandMark.tsx`、`SiteHeader.tsx`、`SiteFooter.tsx`、`LoginForm.tsx` 等）。
- 管理后台专用组件放在 `components/admin/`，全部基于 MUI 组件组合，未引入独立的设计系统或业务组件库。
- 页面路由位于 `app/` 下（`admin/`、`login/`、`pricing/`、`contact/`、`videos/`、`download/`、`features/` 等），遵循 Next.js App Router 约定。

### 3.5 第三方富文本与编辑器

- CodeMirror：`@uiw/react-codemirror` + `@codemirror/lang-json`，用于 JSON 编辑场景（见 `components/admin/JsonEditor.tsx`）。
- WangEditor：`@wangeditor/editor` + `@wangeditor/editor-for-react`，用于邮件模板编辑（见 `components/admin/MailEditor.tsx`）。

这些编辑器自带样式，未被纳入 MUI 主题覆盖范围。

## 4. 约定与约束

- **主题来源唯一**：全站视觉通过 `app/theme.ts` 中的 `createHRPlusTheme()` 产出，`providers.tsx` 是唯一注入点；新增颜色应优先修改该函数而非在组件内硬编码。
- **颜色一致性**：CSS 变量 `--goodhr-brand` 与 MUI `primary.main` 同为 `#0052CC`，二者应保持同步（当前已一致）。
- **按钮形态**：MUI Button 被全局覆盖为胶囊形（`borderRadius: 999`）、最小高度 44px、无阴影，这是通过 `components.MuiButton.styleOverrides.root` 实现的站点级约定。
- **输入框形态**：所有 `TextField` 默认使用 `outlined` 变体，`OutlinedInput` 根节点最小高度 56px、圆角 18px。
- **响应式与无障碍**：仅通过 `@media (prefers-reduced-motion: reduce)` 提供减少动效支持；未见断点媒体查询，布局主要依赖 MUI 栅格与 Flexbox。
- **构建产物**：非静态导出模式下禁用 `X-Powered-By` 头（`poweredByHeader: false`）；静态导出模式会开启 `images.unoptimized: true`。
- **无 Tailwind / Sass / Less**：仓库未引入任何原子 CSS 或预处理器，样式仅通过 MUI + Emotion + 原生 CSS 实现。
- **无暗色模式**：主题固定为 `mode: "light"`，未实现主题切换逻辑。

## 5. 适用性说明

本仓库的前端样式体系集中在 `goodhr5/cloud/frontend-next/` 子目录，采用 Next.js + MUI + Emotion 的组合，辅以少量原生 CSS 变量和关键帧动画。其余模块（Go 后端、本地 Agent、worker-node）均不包含前端样式代码。