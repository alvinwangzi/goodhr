---
kind: external_dependency
name: CloakBrowser 反检测浏览器
slug: cloakbrowser
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - goodhr5/local-agent-go/worker-node/package.json
---

### CloakBrowser
- 角色：本地 Agent 的浏览器自动化底层，提供带指纹伪装能力的浏览器实例，用于操作 Boss 直聘、猎聘等招聘网站页面。
- 集成方式：作为 Node Worker（`goodhr5/local-agent-go/worker-node`）的依赖，配合 Playwright Core 执行点击、输入、滚动、截图、DOM 提取等操作。
- 稳定约束：版本升级可能影响页面自动化稳定性，需随招聘平台 DOM 变化同步回归测试。