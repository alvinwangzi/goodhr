/** 本文件负责官网联系我们页面及联系方式展示。 */
import EmailRoundedIcon from "@mui/icons-material/EmailRounded";
import ChatRoundedIcon from "@mui/icons-material/ChatRounded";
import GroupsRoundedIcon from "@mui/icons-material/GroupsRounded";
import LanguageRoundedIcon from "@mui/icons-material/LanguageRounded";
import PhoneRoundedIcon from "@mui/icons-material/PhoneRounded";
import { Box, Container, Link, Stack, Typography } from "@mui/material";
import type { Metadata } from "next";
import Image from "next/image";
import MarketingShell from "@/components/MarketingShell";
import { createPageMetadata } from "@/lib/seo";

export const metadata: Metadata = createPageMetadata({
  title: "联系HRPlus - 招聘自动化与AI招聘工具咨询",
  description:
    "联系 HRPlus，咨询本地程序安装与连接、岗位筛选、BOSS 复打招呼和 AI 自动回复、简历下载、AI 配置及订阅问题。",
  path: "/contact",
  includeCoreKeywords: false,
  absoluteTitle: true,
  keywords: ["招聘自动化咨询", "AI招聘工具客服", "BOSS自动打招呼技术支持"],
});

const contacts = [
  {
    icon: PhoneRoundedIcon,
    label: "手机与微信",
    value: "15365142034",
    href: "tel:15365142034",
    note: "可留言说明使用的平台、遇到的问题和联系方式。",
  },
  {
    icon: EmailRoundedIcon,
    label: "电子邮箱",
    value: "425942228@qq.com",
    href: "mailto:425942228@qq.com",
    note: "可发送问题描述、程序版本和已去除敏感信息的截图。",
  },
  {
    icon: LanguageRoundedIcon,
    label: "官方网站",
    value: "HRPlus 官方网站",
    href: process.env.NEXT_PUBLIC_SITE_URL || "#",
    note: "查看产品更新、教程和下载入口。",
  },
];

const qrcodes = [
  {
    icon: ChatRoundedIcon,
    title: "微信联系作者",
    description: "咨询安装、订阅和使用问题，或反馈功能建议。",
    image: "/assets/contact/wechat-developer.jpg",
    alt: "HRPlus 作者微信二维码",
  },
  {
    icon: GroupsRoundedIcon,
    title: "加入 HRPlus 解答群",
    description: "常见问题、使用交流、更新通知，群里会同步。",
    image: "/assets/contact/qq-group.jpg",
    alt: "HRPlus 解答 QQ 群二维码",
  },
];

/** ContactPage 展示 HRPlus 联系方式。 */
export default function ContactPage() {
  return (
    <MarketingShell
      eyebrow="联系我们"
      title="遇到问题，直接找到我们"
      description="本地程序安装与连接、岗位筛选、复打招呼、AI 自动回复、简历下载、AI 配置或订阅问题，都可以通过下面的方式联系。"
    >
      <Box component="section" sx={{ pb: { xs: 8, md: 12 } }}>
        <Container maxWidth="lg">
          <Box sx={{ borderTop: "1px solid", borderColor: "divider" }}>
            {contacts.map((item) => {
              const Icon = item.icon;
              return (
                <Box
                  key={item.label}
                  sx={{
                    display: "grid",
                    gridTemplateColumns: { xs: "1fr", md: "220px 1fr" },
                    gap: 2,
                    py: 4,
                    borderBottom: "1px solid",
                    borderColor: "divider",
                  }}
                >
                  <Stack direction="row" spacing={1.25} sx={{ alignItems: "center" }}>
                    <Icon color="primary" />
                    <Typography sx={{ fontWeight: 750 }}>{item.label}</Typography>
                  </Stack>
                  <Box>
                    <Link
                      href={item.href}
                      underline="hover"
                      color="text.primary"
                      sx={{ fontSize: { xs: 24, md: 30 }, fontWeight: 760 }}
                    >
                      {item.value}
                    </Link>
                    <Typography sx={{ mt: 1, color: "text.secondary" }}>
                      {item.note}
                    </Typography>
                  </Box>
                </Box>
              );
            })}
          </Box>
          {/* <Box
            sx={{
              mt: 5,
              display: "grid",
              gridTemplateColumns: { xs: "1fr", md: "repeat(2, minmax(0, 1fr))" },
              gap: 2.5,
            }}
          >
            {qrcodes.map((item) => {
              const Icon = item.icon;
              return (
                <Box
                  key={item.title}
                  sx={{
                    p: { xs: 2, md: 2.5 },
                    border: "1px solid",
                    borderColor: "divider",
                    borderRadius: "8px",
                    bgcolor: "background.paper",
                    display: "grid",
                    gridTemplateColumns: { xs: "1fr", sm: "160px 1fr" },
                    gap: 2.5,
                    alignItems: "center",
                  }}
                >
                  <Box
                    sx={{
                      width: 160,
                      height: 160,
                      mx: { xs: "auto", sm: 0 },
                      borderRadius: "8px",
                      overflow: "hidden",
                      border: "1px solid",
                      borderColor: "divider",
                      position: "relative",
                      bgcolor: "#fff",
                    }}
                  >
                    <Image src={item.image} alt={item.alt} fill sizes="160px" style={{ objectFit: "cover" }} />
                  </Box>
                  <Box>
                    <Stack direction="row" spacing={1} sx={{ alignItems: "center" }}>
                      <Icon color="primary" />
                      <Typography sx={{ fontSize: 20, fontWeight: 780 }}>
                        {item.title}
                      </Typography>
                    </Stack>
                    <Typography sx={{ mt: 1, color: "text.secondary", lineHeight: 1.75 }}>
                      {item.description}
                    </Typography>
                  </Box>
                </Box>
              );
            })}
          </Box> */}
        </Container>
      </Box>
    </MarketingShell>
  );
}
