/** 本文件负责后台帮助指南卡片的展示。 */
"use client";

import { Box, Stack, Typography } from "@mui/material";
import { useEffect, useMemo, useState } from "react";
import { cloudRequest } from "@/lib/admin-api";
import { PageHeader, RefreshButton, SectionPanel } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

type GuideCard = { id?: string; title?: string; summary?: string; content?: string };

/** HelpPage 展示系统帮助指南卡片和详情说明。 */
export default function HelpPage() {
  const { notify } = useAdmin();
  const [guide, setGuide] = useState<any>({});
  const [activeIndex, setActiveIndex] = useState(0);
  const [guideLoading, setGuideLoading] = useState(false);
  const cards = useMemo<GuideCard[]>(() => Array.isArray(guide?.cards) ? guide.cards : [], [guide]);
  const activeCard = cards[activeIndex] || null;

  /** loadGuide 读取系统帮助指南。 */
  async function loadGuide() {
    setGuideLoading(true);
    try { const data = await cloudRequest("/api/help/guide", { auth: false }); setGuide(data.guide || data || {}); }
    catch (error) { notify(error instanceof Error ? error.message : "帮助指南读取失败", "error"); }
    finally { setGuideLoading(false); }
  }

  useEffect(() => { void loadGuide(); }, []);

  return <><PageHeader title="常见问题" description="查看系统使用说明。" actions={<RefreshButton loading={guideLoading} onClick={() => void loadGuide()} />} />
    <Stack spacing={2}><Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", sm: "repeat(2, 1fr)" }, gap: 1.25 }}>{cards.map((card, index) => <Box component="button" type="button" key={card.id || `${card.title}-${index}`} onClick={() => setActiveIndex(index)} sx={{ minHeight: 128, p: 2, textAlign: "left", font: "inherit", color: "inherit", bgcolor: activeIndex === index ? "action.selected" : "action.hover", border: "1px solid", borderColor: activeIndex === index ? "primary.main" : "divider", borderRadius: "8px", cursor: "pointer", transition: "160ms ease", "&:hover": { borderColor: "primary.main", transform: "translateY(-1px)" } }}><Typography color="primary.main" sx={{ fontSize: 12, fontWeight: 800 }}>{String(index + 1).padStart(2, "0")}</Typography><Typography component="h2" sx={{ mt: 1, fontSize: 17, fontWeight: 780 }}>{card.title || "使用指南"}</Typography><Typography sx={{ mt: 0.75, color: "text.secondary", fontSize: 13, lineHeight: 1.65 }}>{card.summary || "点击查看详细说明"}</Typography></Box>)}</Box>
        <SectionPanel>{activeCard ? <><Typography component="h2" sx={{ fontSize: 19, fontWeight: 780 }}>{activeCard.title}</Typography><Typography sx={{ mt: 1.25, color: "text.secondary", whiteSpace: "pre-wrap", lineHeight: 1.8 }}>{activeCard.content || activeCard.summary}</Typography></> : <Typography color="text.secondary">帮助指南正在准备中。</Typography>}</SectionPanel>
    </Stack></>;
}
