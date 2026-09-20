---
kind: external_dependency
name: Playwright 浏览器控制
slug: playwright
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - goodhr5/local-agent-go/worker-node/package.json
---

### Playwright
- 角色：Node Worker 中用于控制 CloakBrowser 实例的浏览器自动化框架，负责页面导航、事件模拟和 DOM 操作。
- 集成方式：通过 `playwright-core` 引入，与 CloakBrowser 配合使用。
- 稳定约束：Playwright 版本升级可能改变 API 行为，需关注与 CloakBrowser 的兼容性。