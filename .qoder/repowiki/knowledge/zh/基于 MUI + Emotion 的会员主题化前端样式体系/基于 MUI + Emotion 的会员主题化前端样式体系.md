---
kind: frontend_style
name: 基于 MUI + Emotion 的会员主题化前端样式体系
category: frontend_style
scope:
    - '**'
source_files:
    - goodhr5/cloud/frontend-next/package.json
    - goodhr5/cloud/frontend-next/next.config.ts
    - goodhr5/cloud/frontend-next/app/globals.css
    - goodhr5/cloud/frontend-next/app/theme.ts
    - goodhr5/cloud/frontend-next/app/providers.tsx
    - goodhr5/cloud/frontend-next/app/admin-theme-colors.test.mjs
    - goodhr5/cloud/frontend-next/app/theme.test.mjs
---

## 1. 系统与方法

GoodHR 新版前端位于 `goodhr5/cloud/frontend-next`，采用 **Next.js App Router**（Next 16）+ **React 19** 构建，样式方案以 **MUI v9** 为核心 UI 组件库，配合 **Emotion**（`@emotion/react`、`@emotion/styled`、`@emotion/cache`）完成主题注入与 CSS-in-JS 渲染。通过 `@mui/material-nextjs` 的 `AppRouterCacheProvider` 在服务端缓存样式，避免首屏闪烁。

项目不使用 Tailwind/SCSS/Sass 等原子或预处理方案，而是以 MUI 主题系统为唯一视觉来源；全局基础样式集中在 `app/globals.css`，仅定义 CSS 变量、重置、滚动行为、选择器高亮色以及少量关键帧动画（`signalFlow`、`workflowProgressX/Y`、`workflowNode`），并通过 `prefers-reduced-motion` 媒体查询尊重无障碍偏好。

## 2. 核心文件与包

- `package.json`：声明 Next、React、MUI、Emotion、CodeMirror、WangEditor、QRCode 等依赖。
- `next.config.ts`：根据环境变量 `GOODHR_STATIC_EXPORT=1` 切换静态导出模式，关闭 `poweredByHeader`，并配置 `.html` 到路由的重定向。
- `app/globals.css`：全局 CSS 变量 `--goodhr-green`、`--goodhr-ink`，基础 reset、smooth scroll、selection 高亮、动画 keyframes 及无障碍动效降级。
- `app/theme.ts`：定义 `MembershipTheme = "free" | "plus" | "max"` 三种会员等级主题，维护 `membershipPalettes` 映射表，通过 `createGoodHRTheme()` 生成统一浅色 MUI 主题，覆盖 primary/secondary/background/text/divider/action/success/warning/error、圆角、字体族、按钮最小高度、输入框圆角等。
- `app/providers.tsx`：在根布局中用 `AppRouterCacheProvider` + `ThemeProvider` + `CssBaseline` 包裹应用，暴露 `useMembershipTheme` Context 供子树动态切换会员主题。
- `components/admin/*`、`components/payment/*`、`components/*.tsx`：业务组件，全部通过 MUI 组件和主题变量获取样式，不直接写颜色硬编码。
- `app/admin-theme-colors.test.mjs`：CI 级测试，扫描 `app/admin` 与 `components/admin` 下所有 `.ts/.tsx/.js/.jsx`，禁止出现旧版固定绿色色值（如 `#0f754a`、`#15945f` 等 40+ 个色值）及对应阴影，强制使用主题变量。
- `app/theme.test.mjs`：验证 `resolveMembershipTheme` 对非激活/未知类型回退到 `free`，Plus/Max 保持 `light` 模式且主色正确，success 等业务状态色不被主题覆盖。

## 3. 架构与设计约定

- **单一主题入口**：所有视觉配色必须经由 `theme.ts` 中的 `membershipPalettes` 与 `createGoodHRTheme` 产出，禁止在组件内硬编码十六进制颜色。
- **会员分级主题**：`free`（品牌绿 `#159a62`）、`plus`（深灰黑 `#242424`）、`max`（金棕 `#8a6518`）三套浅色主题共享同一套 MUI 结构，仅替换强调色与背景色板。
- **服务端样式缓存**：通过 `AppRouterCacheProvider options={{ key: "goodhr" }}` 实现 SSR 样式去重，保证多页面切换时样式一致。
- **CSS 变量作为轻量扩展点**：`globals.css` 暴露 `--goodhr-green`、`--goodhr-ink`，供非 MUI 场景（如 selection 高亮、自定义动画）复用品牌色。
- **响应式与可访问性**：全局提供 `prefers-reduced-motion` 降级，将滚动行为恢复为 `auto`，并将所有动画时长压缩至 `0.01ms`。
- **构建产物策略**：生产默认 `standalone` 输出，可通过 `GOODHR_STATIC_EXPORT=1` 切换为静态导出（同时禁用图片优化），便于部署到 CDN。

## 4. 约定与约束

- **禁止硬编码品牌色**：`admin-theme-colors.test.mjs` 明确列出被禁用的旧版绿色色值集合与阴影字符串，任何在后台源码中出现这些字面量都会导致测试失败——这是仓库内对该风格约束的**强制执行手段**。
- **主题模式固定为 light**：`createGoodHRTheme` 始终设置 `palette.mode = "light"`，项目当前未实现暗色主题。
- **组件样式来源限制**：业务组件应通过 MUI 组件 + 主题变量获取样式；如需自定义样式，优先使用 Emotion 的 `styled` 或 CSS 变量，而非直接写色值。
- **字体栈统一**：主题中统一指定 `Inter, "SF Pro Display", "PingFang SC", "Microsoft YaHei", Arial, sans-serif`，新组件不应引入额外字体。
- **按钮与输入框规范**：主题层已强制 `MuiButton` 最小高度 44px、全圆角、无边框阴影；`MuiOutlinedInput` 最小高度 56px、圆角 18px；新增表单控件应沿用这些基线。
- **业务语义色不受主题影响**：`success`、`warning`、`error` 在主题中被固定为业务含义色，主题切换不得改变其语义表达（由 `theme.test.mjs` 断言保障）。
- **动画需尊重用户偏好**：新增动画必须考虑 `prefers-reduced-motion` 下的降级路径，避免强制触发长耗时动画。

总体而言，该仓库的前端样式体系围绕 **MUI 主题 + Emotion** 构建，以“会员等级驱动主题”为核心设计决策，并通过 Node 测试脚本对“禁止硬编码旧版绿色”这一约束进行工程化强制，确保后台界面视觉一致性。