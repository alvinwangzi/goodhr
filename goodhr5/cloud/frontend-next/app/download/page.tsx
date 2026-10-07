/** 本文件负责官网本地程序下载页面。 */

import AppleIcon from "@mui/icons-material/Apple";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import DownloadRoundedIcon from "@mui/icons-material/DownloadRounded";
import WindowRoundedIcon from "@mui/icons-material/WindowRounded";
import { Box, Button, Container, Paper, Stack, Typography } from "@mui/material";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import MarketingShell from "@/components/MarketingShell";
import { getLocalAgentUpdates, type LocalAgentUpdate } from "@/lib/public-data";
import { createPageMetadata } from "@/lib/seo";

export const metadata: Metadata = createPageMetadata({ title: "下载HRPlus - HR与猎头招聘自动化工具", description: "下载已发布的 HRPlus 本地安装程序，用于候选人筛选、自动打招呼、BOSS 复打与 AI 自动回复、简历索要和下载管理。可下载系统与版本以本页安装包为准。", path: "/download", includeCoreKeywords: false, absoluteTitle: true, keywords: ["招聘软件免费下载", "BOSS自动打招呼软件下载", "猎聘自动化工具下载", "HR招聘助手下载"] });

/** DownloadPage 提供 Windows 和 macOS 本地程序下载入口。 */
export default async function DownloadPage() {
	const updates = await getLocalAgentUpdates();
	const latest = updates[0];

	return <MarketingShell eyebrow="本地程序" title="下载并安装 HRPlus" description="先安装本地程序，再登录控制台、连接本地程序并登录招聘平台。任务在当前电脑上执行；只打开官网页面无法运行招聘任务。">
    <Box component="section" sx={{ pb: { xs: 8, md: 12 } }}><Container maxWidth="lg">
      <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(2, 1fr)" }, gap: 2 }}>
        <DownloadCard icon={<WindowRoundedIcon />} system="Windows" note="当前提供 Windows 64 位安装程序。下载后按安装向导完成安装。" href={latest?.urlWin || ""} available={Boolean(latest?.urlWin)} />
        <DownloadCard icon={<AppleIcon />} system="macOS" note="是否可下载以已发布的安装包为准，安装前确认包适用于你的电脑。" href={latest?.urlMac || ""} available={Boolean(latest?.urlMac)} />
      </Box>
      <UpdateRecords updates={updates} />
      <Box sx={{ mt: 8, borderTop: "1px solid", borderColor: "divider", pt: 4 }}><Typography component="h2" sx={{ fontSize: 28, fontWeight: 760 }}>安装与首次使用</Typography><Stack spacing={1.5} sx={{ mt: 2.5 }}>{["安装后启动 HRPlus，登录控制台并确认本地程序已连接。", "按控制台提示检查并安装所需运行组件，再登录招聘平台账号。", "创建岗位并配置筛选规则，先确认平台可用功能，再选择打招呼、复打或自动回复。", "浏览器登录状态和页面截图保存在当前电脑；岗位配置、运行记录和候选人处理结果通过控制台管理。", "本地程序低于后台要求版本时，需要更新后才能启动岗位。免费额度、会员权限和 AI 余额分别以控制台显示为准。"].map((text) => <Stack key={text} direction="row" spacing={1} sx={{ alignItems: "center" }}><CheckCircleRoundedIcon color="primary" fontSize="small" /><Typography color="text.secondary">{text}</Typography></Stack>)}</Stack></Box>
      <Box sx={{ mt: 7, maxWidth: 860 }}><Typography component="h2" sx={{ fontSize: 28, fontWeight: 760 }}>适合哪些招聘工作</Typography><Typography sx={{ mt: 2, color: "text.secondary", lineHeight: 1.9 }}>适合需要重复筛选候选人、打招呼、处理招聘消息和整理简历的 HR 与猎头。目前已有 BOSS直聘、猎聘企业端与猎头端、智联招聘的适配；复打招呼和 AI 自动回复目前仅支持 BOSS直聘，其他能力以控制台开放状态为准。</Typography></Box>
    </Container></Box>
  </MarketingShell>;
}

/** DownloadCard 展示一个操作系统的下载入口。 */
function DownloadCard({ icon, system, note, href, available }: { icon: ReactNode; system: string; note: string; href: string; available: boolean }) {
	return <Paper variant="outlined" sx={{ p: { xs: 3, md: 4 }, borderRadius: "8px", borderColor: "divider" }}><Box sx={{ color: "primary.main", "& svg": { fontSize: 34 } }}>{icon}</Box><Typography component="h2" sx={{ mt: 2, fontSize: 28, fontWeight: 760 }}>{system}</Typography><Typography sx={{ mt: 1, minHeight: 48, color: "text.secondary" }}>{note}</Typography>{available ? <Button component="a" href={href} variant="contained" startIcon={<DownloadRoundedIcon />} sx={{ mt: 3 }}>下载安装程序</Button> : <Button disabled variant="outlined" sx={{ mt: 3 }}>暂未提供安装包</Button>}</Paper>;
}

/** UpdateRecords 展示本地程序历史更新记录。 */
function UpdateRecords({ updates }: { updates: LocalAgentUpdate[] }) {
	return <Box sx={{ mt: 8, borderTop: "1px solid", borderColor: "divider", pt: 4 }}><Typography component="h2" sx={{ fontSize: 28, fontWeight: 760 }}>更新记录</Typography>{updates.length > 0 ? <Stack spacing={2} sx={{ mt: 2.5 }}>{updates.map((item, index) => <Box key={`${item.version}-${index}`} sx={{ pb: 2, borderBottom: "1px solid", borderColor: "divider" }}><Stack direction={{ xs: "column", sm: "row" }} spacing={1} sx={{ alignItems: { xs: "flex-start", sm: "center" } }}><Typography sx={{ fontWeight: 760 }}>{item.version || "未标版本"}</Typography>{index === 0 ? <Typography sx={{ color: "primary.main", fontWeight: 700 }}>最新安装包</Typography> : null}</Stack><Typography sx={{ mt: 1, color: "text.secondary", lineHeight: 1.8 }}>{item.note || "暂无更新说明。"}</Typography></Box>)}</Stack> : <Typography sx={{ mt: 2, color: "text.secondary" }}>暂无更新记录。</Typography>}</Box>;
}
