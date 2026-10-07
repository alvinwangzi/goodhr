/** 本文件负责官网功能介绍页及其 SEO 内容。 */

import AutoAwesomeRoundedIcon from "@mui/icons-material/AutoAwesomeRounded";
import ChatRoundedIcon from "@mui/icons-material/ChatRounded";
import DownloadRoundedIcon from "@mui/icons-material/DownloadRounded";
import ManageAccountsRoundedIcon from "@mui/icons-material/ManageAccountsRounded";
import PsychologyRoundedIcon from "@mui/icons-material/PsychologyRounded";
import QueryStatsRoundedIcon from "@mui/icons-material/QueryStatsRounded";
import SmartToyRoundedIcon from "@mui/icons-material/SmartToyRounded";
import TuneRoundedIcon from "@mui/icons-material/TuneRounded";
import { Box, Container, Typography } from "@mui/material";
import type { Metadata } from "next";
import MarketingShell from "@/components/MarketingShell";
import { createPageMetadata } from "@/lib/seo";

export const metadata: Metadata = createPageMetadata({ title: "HRPlus 招聘自动化功能 - 筛选、打招呼、复打与自动回复", description: "了解 HRPlus 的关键词筛选、AI 匹配分析、自动打招呼、BOSS 复打招呼与 AI 自动回复，以及简历索要、下载和候选人记录管理。各平台可用功能以控制台为准。", path: "/features", includeCoreKeywords: false, absoluteTitle: true, keywords: ["招聘自动化功能", "AI简历筛选", "BOSS复打招呼", "招聘简历下载"] });

const features = [
  { icon: TuneRoundedIcon, title: "关键词规则筛选", text: "按岗位设置关键词、排除词和匹配规则。关键词筛选可按免费额度使用，每日打招呼数量以当前套餐为准。" },
  { icon: PsychologyRoundedIcon, title: "AI 匹配与详情分析", text: "根据岗位要求分析候选人信息，给出匹配分和判断理由。详情读取可结合页面文字、截图或 OCR，具体方式以平台和岗位配置为准。" },
  { icon: AutoAwesomeRoundedIcon, title: "自动打招呼", text: "按岗位筛选条件查看候选人，对符合要求的人打招呼。可设置运行节奏、休息和停止条件。" },
  { icon: ChatRoundedIcon, title: "复打招呼", text: "目前仅支持 BOSS直聘。对已打过招呼、当前尚未回复且未收到简历的候选人再次发送消息，可设置时间范围、间隔、次数上限和岗位提示词。" },
  { icon: SmartToyRoundedIcon, title: "AI 自动回复", text: "目前仅支持 BOSS直聘。结合岗位说明、问答配置和聊天上下文生成回复。复打与自动回复需具备对应会员权限，可单独运行，也可组合运行。" },
  { icon: DownloadRoundedIcon, title: "简历索要与下载", text: "在平台允许的情况下向合适的候选人索要附件简历，并记录索要、接收和下载进度。复打阶段只发送消息，不直接索要简历。" },
  { icon: ManageAccountsRoundedIcon, title: "候选人记录管理", text: "通过控制台查看候选人信息、匹配评分、沟通事件和简历进度，支持筛选和备注，方便后续跟进。" },
  { icon: QueryStatsRoundedIcon, title: "任务记录与统计", text: "查看岗位运行状态、处理数量和关键日志。组合任务按打招呼、复打、自动回复的顺序执行，各阶段完成后再进入下一阶段，可随时停止任务。" },
  { icon: SmartToyRoundedIcon, title: "本地执行与平台适配", text: "在当前电脑上操作招聘平台浏览器。目前已有 BOSS直聘、猎聘企业端与猎头端、智联招聘的适配，各平台开放状态和可用功能以控制台为准。" },
];

/** FeaturesPage 展示 HRPlus 的主要产品能力。 */
export default function FeaturesPage() {
  return <MarketingShell eyebrow="功能介绍" title="围绕真实招聘流程，减少每天重复的动作" description="为 HR 和猎头提供候选人筛选、打招呼、消息沟通和简历管理工具。按岗位选择需要的任务，各招聘平台的可用功能有所不同。">
    <Box component="section" sx={{ pb: { xs: 8, md: 12 } }}><Container maxWidth="lg">
      <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(3, 1fr)" }, borderTop: "1px solid", borderColor: "divider" }}>
        {features.map((item, index) => { const Icon = item.icon; return <Box key={item.title} sx={{ py: 4, px: { md: 3 }, borderRight: { md: index % 3 !== 2 ? "1px solid" : "none" }, borderBottom: "1px solid", borderColor: "divider" }}>
          <Icon color="primary" /><Typography component="h2" sx={{ mt: 2, fontSize: 21, fontWeight: 760 }}>{item.title}</Typography><Typography sx={{ mt: 1.25, color: "text.secondary", lineHeight: 1.8 }}>{item.text}</Typography>
        </Box>; })}
      </Box>
    </Container></Box>
  </MarketingShell>;
}
