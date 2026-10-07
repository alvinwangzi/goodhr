# Boss直聘适配器

<cite>
**本文引用的文件**   
- [entry.go](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go)
- [runtime.go](file://goodhr5/local-agent-go/internal/platforms/boss/runtime.go)
- [navigation.go](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go)
- [position.go](file://goodhr5/local-agent-go/internal/platforms/boss/position.go)
- [candidate.go](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go)
- [detail.go](file://goodhr5/local-agent-go/internal/platforms/boss/detail.go)
- [greet.go](file://goodhr5/local-agent-go/internal/platforms/boss/greet.go)
- [helpers.go](file://goodhr5/local-agent-go/internal/platforms/boss/helpers.go)
- [followup.go](file://goodhr5/local-agent-go/internal/platforms/boss/followup.go)
- [auto_reply.go](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go)
- [reply.go](file://goodhr5/local-agent-go/internal/platforms/boss/reply.go)
- [re_greet.go](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go)
- [index.js](file://goodhr5/local-agent-go/worker-node/src/index.js)
- [boss-scroll-anchor.js](file://goodhr5/local-agent-go/worker-node/src/boss-scroll-anchor.js)
- [boss-scroll-diagnostic.js](file://goodhr5/local-agent-go/worker-node/src/boss-scroll-diagnostic.js)
</cite>

## 更新摘要
**变更内容**   
- 新增ReGreet复打招呼功能模块，包含LocateReplyConversation、StageReGreet、SendReGreet、ConfirmReGreet四个核心方法
- 增强候选人后续跟进工作流，支持自动化候选人的二次沟通
- 集成云端候选名单拉取与AI生成复打消息功能
- 完善Boss平台聊天会话搜索与定位能力

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构总览](#架构总览)
5. [详细组件分析](#详细组件分析)
6. [ReGreet复打招呼系统](#regreet复打招呼系统)
7. [依赖关系分析](#依赖关系分析)
8. [性能与反爬策略](#性能与反爬策略)
9. [故障排查指南](#故障排查指南)
10. [结论](#结论)
11. [附录：配置项与调试建议](#附录配置项与调试建议)

## 简介
本文件面向Boss直聘平台适配器的实现，系统性解析其页面结构特点、DOM元素定位策略、用户交互模拟方法，以及岗位搜索、候选人列表获取、简历详情解析、自动问候、聊天框信息索要等核心流程。**最新更新**增加了ReGreet复打招呼功能，支持对之前打过招呼但未回复的候选人进行自动化二次沟通。文档同时覆盖Boss直聘特有的反爬虫应对、页面加载等待策略、异常处理方案、特殊配置选项、性能优化技巧、调试方法与数据同步要点。

## 项目结构
Boss直聘适配器位于本地Agent的"平台实现"模块中，Go侧负责编排业务逻辑与Worker通信，Node侧提供浏览器端滚动安全判断与诊断能力。关键文件职责如下：
- entry.go：入口页打开、确认弹框处理、入口页判定
- runtime.go：运行时基础能力、候选人可见性通用参数、年龄提取与ID规范化
- navigation.go：入口页匹配、岗位名称规范化、搜索关键词生成、列表项选择器合并
- position.go：当前岗位读取、岗位切换（搜索优先+回退滚动查找）
- candidate.go：候选人列表提取、滚动、可见性保证、筛选文本与指纹
- detail.go：候选人详情提取、截图拼接、详情关闭与文本清洗
- greet.go：打招呼流程
- followup.go：打招呼后聊天框复用与信息索要流程
- auto_reply.go：**新增** ReGreet复打招呼核心方法、聊天会话搜索、面板上下文读取
- reply.go：**新增** Worker端Boss聊天搜索接口封装
- re_greet.go：**新增** 复打招呼任务编排、云端候选名单处理、AI消息生成
- helpers.go：云端配置到Worker协议转换、通用工具函数
- screenshot.go：分段截图拼接为长图、临时文件清理
- boss-scroll-anchor.js：滚动锚点安全区判断、自适应滚轮距离、滚动预算扩展
- boss-scroll-diagnostic.js：滚动失败诊断汇总与中文结论

```mermaid
graph TB
subgraph "Go平台层"
E["entry.go<br/>入口页处理"]
R["runtime.go<br/>运行时基础"]
N["navigation.go<br/>导航与选择器"]
P["position.go<br/>岗位操作"]
C["candidate.go<br/>候选人操作"]
D["detail.go<br/>详情与截图"]
G["greet.go<br/>打招呼"]
F["followup.go<br/>聊天框与信息索要"]
A["auto_reply.go<br/>复打招呼核心"]
Y["reply.go<br/>聊天搜索封装"]
H["helpers.go<br/>配置与工具"]
S["screenshot.go<br/>截图拼接"]
end
subgraph "任务编排层"
RG["re_greet.go<br/>复打任务编排"]
end
subgraph "Node Worker脚本"
I["index.js<br/>聊天搜索实现"]
AJS["boss-scroll-anchor.js<br/>滚动安全与预算"]
BJS["boss-scroll-diagnostic.js<br/>滚动诊断"]
end
E --> H
P --> N
P --> H
C --> H
D --> H
D --> S
G --> H
F --> H
A --> Y
A --> H
RG --> A
Y --> I
C --> AJS
C --> BJS
D --> AJS
D --> BJS
```

**图表来源** 
- [entry.go:12-72](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L12-L72)
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [reply.go:116-126](file://goodhr5/local-agent-go/internal/platforms/boss/reply.go#L116-L126)
- [re_greet.go:23-48](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L23-L48)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

**章节来源**
- [entry.go:12-72](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L12-L72)
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [reply.go:116-126](file://goodhr5/local-agent-go/internal/platforms/boss/reply.go#L116-L126)
- [re_greet.go:23-48](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L23-L48)

## 核心组件
- 入口与导航
  - 入口页打开与确认弹框处理：通过Worker接口提取弹窗文本并点击确认按钮，随后延迟等待关闭。
  - 入口页判定：列出当前页面，匹配配置中的入口URL或前缀。
  - 岗位名称规范化与搜索关键词生成：去除括号说明、统一空格，提高搜索命中率。
- 岗位操作
  - 当前岗位读取：按配置选择器提取文本，为空时记录详细日志并报错。
  - 岗位切换：优先使用岗位搜索框输入精简关键词；若不可用则回退到完整列表滚动查找；支持元素引用点击与序号回退。
- 候选人操作
  - 候选人列表提取：调用Worker专用接口，返回结构化卡片数据，附带耗时统计。
  - 候选人滚动与可见性保证：小步滚轮滚动，结合安全边距与自适应步长，确保卡片完全进入可视区域。
  - 候选人指纹：基于姓名与年龄生成稳定ID，用于去重。
- 详情解析
  - 详情提取：根据模式（DOM/OCR/AI）触发不同Worker路径，附带截图与滚动参数。
  - 截图拼接：将分段截图纵向拼接为长图，输出固定路径，清理临时分段。
  - 详情关闭：通过快捷键关闭详情面板。
- 打招呼与信息索要
  - 打招呼：先确保候选人可见，再调用打招呼接口。
  - 聊天框复用与信息索要：打招呼后优先复用已打开聊天框，否则主动打开；支持手机号、微信、简历三类动作与追加问候语发送。
- **新增** ReGreet复打招呼
  - 候选人会话定位：通过搜索框按姓名定位候选人并打开聊天面板。
  - 复打消息输入：核对身份与空草稿后输入复打文本。
  - 复打消息发送：核对草稿一致性后点击发送。
  - 发送结果确认：验证面板新增了本次出站文本。

**章节来源**
- [entry.go:12-72](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L12-L72)
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [re_greet.go:152-237](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L152-L237)

## 架构总览
Boss直聘适配器采用"Go编排 + Node Worker执行"的分层架构。Go侧负责业务编排、配置解析与日志记录；Node侧在浏览器环境中执行DOM查询、滚动、截图与诊断。两者通过统一的Worker API进行通信。**新增的ReGreet功能**通过云端候选名单拉取、AI消息生成、平台页面操作的完整流水线，实现对未回复候选人的自动化二次沟通。

```mermaid
sequenceDiagram
participant Runner as "岗位运行器"
participant Boss as "Boss适配器(Go)"
participant Cloud as "云端API"
participant Worker as "浏览器Worker(Node)"
participant Page as "Boss页面"
Note over Runner,Page : 常规候选人扫描流程
Runner->>Boss : 请求候选人列表
Boss->>Worker : POST /api/v1/boss/candidates/extract
Worker->>Page : 查询候选人卡片DOM
Page-->>Worker : 返回候选数据结构
Worker-->>Boss : 返回候选人数组与耗时
Boss-->>Runner : 返回候选人列表
Note over Runner,Page : ReGreet复打招呼流程
Runner->>Cloud : 拉取复打候选名单
Cloud-->>Runner : 返回候选人名册
Runner->>Boss : LocateReplyConversation(按姓名定位)
Boss->>Worker : POST /api/v1/boss/chat/search-session
Worker->>Page : 搜索候选人会话
Page-->>Worker : 返回搜索结果
Worker-->>Boss : 返回面板姓名与状态
Boss-->>Runner : 返回聊天会话对象
Runner->>Boss : StageReGreet/SendReGreet(输入发送)
Boss->>Worker : 操作聊天框与发送按钮
Worker->>Page : 输入文本并点击发送
Page-->>Worker : 返回操作结果
Worker-->>Boss : 返回发送状态
Boss-->>Runner : 返回复打结果
Runner->>Cloud : 上报复打结果
```

**图表来源**
- [candidate.go:15-48](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go#L15-L48)
- [re_greet.go:125-237](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L125-L237)
- [auto_reply.go:972-1067](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L972-L1067)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

## 详细组件分析

### 入口与导航组件
- 入口页打开与确认弹框
  - 打开入口页：校验配置URL，调用Worker打开页面。
  - 确认弹框：提取弹窗体文本，若存在则点击确认按钮，延迟等待关闭。
- 入口页判定
  - 列出当前页面，取默认页并与配置入口URL进行匹配（精确/前缀/包含）。
- 岗位名称规范化与搜索关键词
  - 去除中英文括号说明，统一空格，生成适合Boss搜索框的精简关键词。
- 列表项选择器合并
  - 合并列表容器与列表项的配置，支持父类与目标类叠加，提升定位稳定性。

```mermaid
flowchart TD
Start(["开始"]) --> CheckEntry["检查入口URL是否有效"]
CheckEntry --> OpenPage["打开入口页面"]
OpenPage --> ExtractDialog["提取弹窗文本"]
ExtractDialog --> HasDialog{"发现确认弹框？"}
HasDialog --> |是| ClickConfirm["点击确认按钮"]
HasDialog --> |否| SkipDialog["跳过"]
ClickConfirm --> WaitClose["等待弹框关闭"]
SkipDialog --> ListPages["列出当前页面"]
WaitClose --> ListPages
ListPages --> MatchEntry["匹配入口URL"]
MatchEntry --> End(["结束"])
```

**图表来源**
- [entry.go:14-52](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L14-L52)
- [entry.go:57-71](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L57-L71)
- [navigation.go:14-38](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L14-L38)
- [navigation.go:55-71](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L55-L71)
- [navigation.go:79-93](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L79-L93)
- [navigation.go:95-122](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L95-L122)

**章节来源**
- [entry.go:14-71](file://goodhr5/local-agent-go/internal/platforms/boss/entry.go#L14-L71)
- [navigation.go:14-122](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L14-L122)

### 岗位操作组件
- 当前岗位读取
  - 按配置选择器提取文本，为空时记录详细诊断日志并返回错误。
- 岗位切换
  - 优先通过岗位搜索框输入精简关键词，多次刷新检查匹配项；未命中则清空搜索框并回退到完整列表滚动查找。
  - 找到匹配项后，尝试滚动到可视区域并点击；若无元素引用则按序号点击。
  - 滚动参数包括距离、等待时间、最大尝试次数、视口边距与仅垂直滚动。

```mermaid
flowchart TD
S(["开始"]) --> ReadCurrent["读取当前岗位名称"]
ReadCurrent --> SwitchBtn["点击岗位切换按钮"]
SwitchBtn --> SearchInput{"是否存在搜索框？"}
SearchInput --> |是| TypeQuery["输入精简关键词"]
TypeQuery --> RefreshCheck["多次刷新检查匹配项"]
RefreshCheck --> FoundSearch{"搜索命中？"}
FoundSearch --> |是| ScrollAndClick["滚动到可视区域并点击"]
FoundSearch --> |否| ClearSearch["清空搜索框"]
ClearSearch --> FallbackList["回退到完整列表查找"]
SearchInput --> |否| FallbackList
FallbackList --> FindItems["查找所有岗位项"]
FindItems --> MatchName["匹配岗位名称"]
MatchName --> VisibleOrIndex{"有元素引用？"}
VisibleOrIndex --> |是| EnsureVisible["确保可见并点击"]
VisibleOrIndex --> |否| ClickByIndex["按序号点击"]
EnsureVisible --> E(["结束"])
ClickByIndex --> E
```

**图表来源**
- [position.go:14-29](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L14-L29)
- [position.go:34-118](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L34-L118)
- [position.go:121-199](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L121-L199)

**章节来源**
- [position.go:14-199](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L14-L199)

### 候选人操作组件
- 候选人列表提取
  - 调用Worker专用接口，返回候选人数组与耗时统计；对每个候选人计算指纹并写入ID字段。
- 候选人滚动与可见性保证
  - 使用小步滚轮滚动，结合安全边距与自适应步长，确保卡片完全进入可视区域。
  - 滚动预算根据初始剩余距离动态扩展，避免远距离目标提前耗尽重试。
- 候选人指纹
  - 基于姓名与年龄生成稳定ID，用于跨任务去重。

```mermaid
flowchart TD
Start(["开始"]) --> ExtractCandidates["提取候选人列表"]
ExtractCandidates --> ForEach["遍历候选人"]
ForEach --> ComputeFingerprint["计算姓名+年龄指纹"]
ComputeFingerprint --> AssignID["写入候选人ID"]
AssignID --> Next["下一个候选人"]
Next --> Done(["完成"])
```

**图表来源**
- [candidate.go:15-48](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go#L15-L48)
- [candidate.go:76-86](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go#L76-L86)
- [boss-scroll-anchor.js:124-170](file://goodhr5/local-agent-go/worker-node/src/boss-scroll-anchor.js#L124-L170)

**章节来源**
- [candidate.go:15-86](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go#L15-L86)
- [boss-scroll-anchor.js:14-170](file://goodhr5/local-agent-go/worker-node/src/boss-scroll-anchor.js#L14-L170)

### 详情解析组件
- 详情提取
  - 根据模式（DOM/OCR/AI）决定是否启用截图；传递候选人索引、元素引用、滚动参数与截图目录。
  - 返回详情文本与截图信息；若截图为空则记录警告。
- 截图拼接
  - 将分段截图纵向拼接为长图，输出固定路径；单分段直接复制；多分段像素级拼接并清理临时文件。
- 详情关闭
  - 通过快捷键关闭详情面板，记录日志。

```mermaid
sequenceDiagram
participant Boss as "Boss适配器"
participant Worker as "浏览器Worker"
participant FS as "文件系统"
Boss->>Worker : 请求详情(含截图开关)
Worker-->>Boss : 返回详情文本与分段截图
Boss->>FS : 创建输出目录
Boss->>FS : 读取分段PNG并解码
Boss->>Boss : 像素拼接为长图
Boss->>FS : 编码写入固定路径
Boss->>FS : 删除临时分段
Boss-->>Caller : 返回详情与长图路径
```

**图表来源**
- [detail.go:16-63](file://goodhr5/local-agent-go/internal/platforms/boss/detail.go#L16-L63)
- [screenshot.go:19-167](file://goodhr5/local-agent-go/internal/platforms/boss/screenshot.go#L19-L167)

**章节来源**
- [detail.go:16-107](file://goodhr5/local-agent-go/internal/platforms/boss/detail.go#L16-L107)
- [screenshot.go:19-167](file://goodhr5/local-agent-go/internal/platforms/boss/screenshot.go#L19-L167)

### 打招呼与信息索要组件
- 打招呼
  - 先确保候选人可见，再调用打招呼接口。
- 聊天框复用与信息索要
  - 打招呼后聊天框通常自动打开，先短轮询复用；否则主动打开聊天框。
  - 支持手机号、微信、简历三类动作与确认浮层；可追加发送问候语。
  - 兜底关闭聊天框，防止残留。

```mermaid
sequenceDiagram
participant Boss as "Boss适配器"
participant ChatFlow as "聊天流工具"
participant Worker as "浏览器Worker"
Boss->>Worker : 确保候选人可见
Boss->>Worker : 打开聊天框(如需)
Worker-->>Boss : 聊天框状态
Boss->>ChatFlow : 复用或打开聊天框
ChatFlow->>Worker : 点击索要动作(手机/微信/简历)
Worker-->>ChatFlow : 确认浮层
ChatFlow->>Worker : 发送追加问候语(可选)
ChatFlow->>Worker : 关闭聊天框(兜底)
```

**图表来源**
- [greet.go:13-22](file://goodhr5/local-agent-go/internal/platforms/boss/greet.go#L13-L22)
- [followup.go:45-116](file://goodhr5/local-agent-go/internal/platforms/boss/followup.go#L45-L116)

**章节来源**
- [greet.go:13-22](file://goodhr5/local-agent-go/internal/platforms/boss/greet.go#L13-L22)
- [followup.go:45-116](file://goodhr5/local-agent-go/internal/platforms/boss/followup.go#L45-L116)

## ReGreet复打招呼系统

### 概述
ReGreet复打招呼系统是Boss直聘适配器的核心新功能，专门针对之前打过招呼但未回复的候选人进行自动化二次沟通。该系统通过云端候选名单拉取、AI消息生成、平台页面操作的完整流水线，实现了智能化的候选人跟进工作流。

### 核心方法

#### LocateReplyConversation - 候选人会话定位
通过Boss平台的搜索框功能，按候选人姓名定位并打开聊天面板。该方法实现了完整的身份验证机制，确保定位到的确实是目标候选人。

**工作流程**：
1. 调用Worker端的`/api/v1/boss/chat/search-session`接口
2. 在搜索框中输入候选人姓名
3. 从搜索结果列表中匹配目标候选人
4. 点击对应条目跳转到聊天面板
5. 验证面板显示的候选人姓名与搜索姓名一致

```mermaid
flowchart TD
Start(["开始"]) --> ValidateName["验证候选人姓名"]
ValidateName --> CallSearch["调用搜索接口"]
CallSearch --> InputName["输入候选人姓名"]
InputName --> FindMatch["匹配搜索结果"]
FindMatch --> ClickItem["点击匹配条目"]
ClickItem --> VerifyPanel["验证面板姓名"]
VerifyPanel --> NameMatch{"姓名匹配？"}
NameMatch --> |是| ReturnConv["返回聊天会话对象"]
NameMatch --> |否| Error["返回错误"]
ReturnConv --> End(["结束"])
Error --> End
```

**图表来源**
- [auto_reply.go:972-997](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L972-L997)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

#### StageReGreet - 复打消息输入
在执行发送前进行严格的安全检查，确保面板身份正确且草稿为空，然后将AI生成的复打消息输入到聊天框。

**安全检查**：
- 验证当前面板身份与目标候选人一致
- 确保聊天框草稿为空
- 验证复打消息长度不超过1000字符
- 检查上下文是否有效

#### SendReGreet - 复打消息发送
在确认草稿内容与预期文本完全一致后，点击发送按钮完成消息发送。

**发送验证**：
- 验证复打消息不为空
- 比对草稿内容与预期文本的一致性
- 点击发送按钮

#### ConfirmReGreet - 发送结果确认
发送后进行最终验证，确认面板中确实新增了本次出站消息，避免将历史同文误判为本次发送成功。

**确认逻辑**：
- 检查最后一条消息是否为出站文本
- 验证消息内容与发送文本一致
- 对比发送前后的消息数量变化
- 处理边界情况（如发送前末条为空）

### 任务编排流程

```mermaid
sequenceDiagram
participant Runner as "任务运行器"
participant Cloud as "云端API"
participant Boss as "Boss适配器"
participant AI as "AI生成器"
participant Worker as "浏览器Worker"
Note over Runner,Worker : ReGreet任务完整流程
Runner->>Cloud : 拉取复打候选名单
Cloud-->>Runner : 返回候选人名册
loop 遍历每个候选人
Runner->>Boss : LocateReplyConversation(定位候选人)
Boss->>Worker : 搜索候选人会话
Worker-->>Boss : 返回会话对象
Boss-->>Runner : 返回定位结果
Runner->>Boss : ReadOpenedReplyContext(读取上下文)
Boss->>Worker : 读取聊天历史
Worker-->>Boss : 返回消息列表
Boss-->>Runner : 返回上下文
Runner->>AI : GenerateReGreet(生成复打消息)
AI-->>Runner : 返回决策与消息
alt AI决定发送
Runner->>Boss : StageReGreet(输入消息)
Boss->>Worker : 输入复打文本
Worker-->>Boss : 返回输入结果
Runner->>Boss : SendReGreet(发送消息)
Boss->>Worker : 点击发送按钮
Worker-->>Boss : 返回发送结果
Runner->>Boss : ConfirmReGreet(确认发送)
Boss->>Worker : 验证面板状态
Worker-->>Boss : 返回确认结果
Boss-->>Runner : 返回确认结果
alt 发送成功
Runner->>Cloud : 上报成功结果
else 发送失败
Runner->>Cloud : 上报失败原因
end
else AI决定跳过
Runner->>Cloud : 上报跳过原因
end
end
```

**图表来源**
- [re_greet.go:144-249](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L144-L249)

**章节来源**
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [reply.go:116-126](file://goodhr5/local-agent-go/internal/platforms/boss/reply.go#L116-L126)
- [re_greet.go:23-367](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L23-L367)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

## 依赖关系分析
- Go层内部依赖
  - helpers.go提供配置分区读取、元素定位转换与通用工具函数，被其他模块广泛复用。
  - runtime.go提供候选人可见性通用参数与年龄提取逻辑，供candidate.go与greet.go复用。
  - navigation.go提供入口页匹配与岗位名称规范化，被position.go与entry.go复用。
  - screenshot.go依赖文件系统与图像库，被detail.go调用。
  - **新增** auto_reply.go依赖reply.go提供的聊天搜索封装，被re_greet.go任务编排调用。
  - **新增** re_greet.go依赖platformcore.ReGreetRuntime接口，协调云端API、AI生成器与平台操作。
- Node层依赖
  - boss-scroll-anchor.js提供滚动安全判断、自适应步长与滚动预算扩展，被Go层通过Worker接口间接使用。
  - boss-scroll-diagnostic.js汇总滚动轨迹并生成诊断结论，辅助定位滚动失败原因。
  - **新增** index.js中的searchBossChatSession函数实现Boss平台聊天搜索的具体逻辑。

```mermaid
graph LR
H["helpers.go"] --> P["position.go"]
H --> C["candidate.go"]
H --> D["detail.go"]
H --> G["greet.go"]
H --> F["followup.go"]
R["runtime.go"] --> C
R --> G
N["navigation.go"] --> P
D --> S["screenshot.go"]
A["auto_reply.go"] --> Y["reply.go"]
RG["re_greet.go"] --> A
C --> AJS["boss-scroll-anchor.js"]
C --> BJS["boss-scroll-diagnostic.js"]
D --> AJS
D --> BJS
Y --> I["index.js"]
```

**图表来源**
- [helpers.go:112-180](file://goodhr5/local-agent-go/internal/platforms/boss/helpers.go#L112-L180)
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [re_greet.go:23-48](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L23-L48)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

**章节来源**
- [helpers.go:112-180](file://goodhr5/local-agent-go/internal/platforms/boss/helpers.go#L112-L180)
- [auto_reply.go:969-1072](file://goodhr5/local-agent-go/internal/platforms/boss/auto_reply.go#L969-L1072)
- [re_greet.go:23-48](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L23-L48)
- [index.js:1795-1867](file://goodhr5/local-agent-go/worker-node/src/index.js#L1795-L1867)

## 性能与反爬策略
- 页面加载等待策略
  - 入口弹框确认后延迟等待关闭，避免后续操作误触。
  - 岗位搜索结果刷新采用多次轮询与短暂延迟，提升匹配成功率。
  - **新增** ReGreet任务间随机间隔（30-50分钟），模拟人工节奏避免检测。
- 滚动与可见性优化
  - 小步滚轮滚动，结合安全边距与自适应步长，减少无效滚动与越界抖动。
  - 滚动预算根据初始剩余距离动态扩展，避免远距离目标提前耗尽重试。
- 反爬虫应对
  - 通过Worker接口执行DOM操作与截图，降低直接HTTP请求风险。
  - 使用稳定的选择器与父类/目标类组合，增强抗变更能力。
  - 详情文本清洗移除平台附加内容，避免干扰下游AI处理。
  - **新增** ReGreet复打招呼通过真实浏览器交互，避免API调用检测。
  - **新增** 候选人搜索使用平台内置搜索功能，而非直接DOM遍历。
- 性能特性
  - 候选人列表提取返回耗时统计，便于监控与调优。
  - 截图拼接过程记录内存与耗时，便于定位瓶颈。
  - **新增** ReGreet任务统计（总数、发送数、跳过数、失败数），支持进度跟踪。

[本节为通用指导，不直接分析具体文件]

## 故障排查指南
- 候选人滚动定位失败
  - 查看滚动诊断结论代码与中文解释，重点关注"滚轮无效""方向振荡""目标未接近""目标不可测"。
  - 核对视口来源、DPR、可视缩放、滚动容器与滚轮落点。
  - 调整滚动距离、最大尝试次数与安全边距，必要时扩大滚动预算。
- 详情截图缺失或拼接失败
  - 检查分段截图路径是否存在、文件大小与解码是否成功。
  - 确认输出目录权限与磁盘空间，观察拼接过程中的内存分配与编码写入日志。
- 岗位搜索未命中
  - 检查岗位名称规范化与搜索关键词生成是否正确。
  - 确认搜索框是否可用，必要时回退到完整列表查找。
- 聊天框未打开或信息索要失败
  - 确认打招呼后聊天框是否自动打开，必要时主动打开。
  - 核对三类动作的选择器与确认浮层选择器是否匹配。
- **新增** ReGreet复打招呼问题
  - 候选人定位失败：检查候选人姓名是否正确，确认Boss平台搜索功能是否正常。
  - 消息输入失败：验证聊天框是否处于激活状态，检查文本长度限制。
  - 发送确认失败：核对发送前后消息列表变化，确认面板状态同步正常。
  - 云端同步失败：检查网络连接、Token有效性及云端API响应。

**章节来源**
- [boss-scroll-diagnostic.js:86-250](file://goodhr5/local-agent-go/worker-node/src/boss-scroll-diagnostic.js#L86-L250)
- [screenshot.go:19-167](file://goodhr5/local-agent-go/internal/platforms/boss/screenshot.go#L19-L167)
- [position.go:121-199](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L121-L199)
- [followup.go:45-116](file://goodhr5/local-agent-go/internal/platforms/boss/followup.go#L45-L116)
- [re_greet.go:152-237](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L152-L237)

## 结论
Boss直聘适配器通过Go编排与Node Worker执行的协作，实现了从入口页处理、岗位搜索与切换、候选人列表提取与可见性保证、详情解析与截图拼接，到打招呼与信息索要的完整自动化流程。**最新增强的ReGreet复打招呼系统**进一步扩展了平台能力，支持对未回复候选人的智能化二次沟通。该系统通过云端候选名单拉取、AI消息生成、平台页面操作的完整流水线，配合严格的身份验证与安全检查，有效提升了招聘效率与用户体验。其设计强调稳定性与可观测性：通过稳定的选择器组合、滚动安全判断与诊断、详细的日志与耗时统计，有效应对Boss直聘的反爬机制与页面变化。在实际使用中，建议结合配置项与调试方法持续优化滚动参数与选择器，以提升成功率与性能。

[本节为总结性内容，不直接分析具体文件]

## 附录：配置项与调试建议
- 配置项要点
  - 入口页配置：包含URL与匹配规则（精确/前缀/包含），用于打开与判定入口页。
  - 岗位配置：包含当前岗位选择器、切换按钮、列表容器、列表项、列表项文字、搜索框等。
  - 候选人配置：包含卡片字段定位、可见性参数（距离、等待时间、尝试次数、最大距离、视口边距）。
  - 详情配置：包含截图开关、滚动参数、截图目录与文件名。
  - 聊天框配置：包含全局聊天框、姓名、关闭按钮、输入框、发送按钮与三类动作的选择器。
  - **新增** ReGreet配置：包含复打提示词、时间范围、间隔设置、最大数量等。
- 调试建议
  - 启用Worker日志与岗位运行日志，关注耗时统计与诊断结论。
  - 针对滚动问题，优先检查视口来源、DPR、可视缩放与滚动容器。
  - 针对截图问题，检查分段截图路径、文件大小与解码日志。
  - 针对岗位搜索问题，检查岗位名称规范化与搜索关键词生成。
  - 针对聊天框问题，核对选择器与确认浮层选择器，必要时主动打开聊天框。
  - **新增** ReGreet调试：检查候选人搜索功能、聊天面板状态、消息输入输出、云端API连接。
  - **新增** 性能监控：关注ReGreet任务执行时间、成功率统计、云端同步状态。

**章节来源**
- [navigation.go:14-38](file://goodhr5/local-agent-go/internal/platforms/boss/navigation.go#L14-L38)
- [position.go:34-118](file://goodhr5/local-agent-go/internal/platforms/boss/position.go#L34-L118)
- [candidate.go:15-48](file://goodhr5/local-agent-go/internal/platforms/boss/candidate.go#L15-L48)
- [detail.go:16-63](file://goodhr5/local-agent-go/internal/platforms/boss/detail.go#L16-L63)
- [followup.go:16-31](file://goodhr5/local-agent-go/internal/platforms/boss/followup.go#L16-L31)
- [re_greet.go:255-288](file://goodhr5/local-agent-go/internal/positionrunner/re_greet.go#L255-L288)