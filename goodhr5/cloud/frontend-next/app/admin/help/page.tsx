/** 本文件负责后台帮助指南 FAQ 问答展示。 */
"use client";

import ExpandMoreRoundedIcon from "@mui/icons-material/ExpandMoreRounded";
import { Accordion, AccordionDetails, AccordionSummary, Stack, Typography } from "@mui/material";
import { useEffect, useMemo, useState } from "react";
import { cloudRequest } from "@/lib/admin-api";
import { PageHeader, RefreshButton } from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";

type GuideCard = { id?: string; title?: string; summary?: string; content?: string };

/** HelpPage 以 FAQ 问答形式展示系统帮助指南。 */
export default function HelpPage() {
  const { notify } = useAdmin();
  const [guide, setGuide] = useState<any>({});
  const [guideLoading, setGuideLoading] = useState(false);
  const cards = useMemo<GuideCard[]>(() => Array.isArray(guide?.cards) ? guide.cards : [], [guide]);

  /** loadGuide 读取系统帮助指南。 */
  async function loadGuide() {
    setGuideLoading(true);
    try { const data = await cloudRequest("/api/help/guide", { auth: false }); setGuide(data.guide || data || {}); }
    catch (error) { notify(error instanceof Error ? error.message : "帮助指南读取失败", "error"); }
    finally { setGuideLoading(false); }
  }

  useEffect(() => { void loadGuide(); }, []);

  return <><PageHeader title="常见问题" description="查看系统使用说明。" actions={<RefreshButton loading={guideLoading} onClick={() => void loadGuide()} />} />
    <Stack spacing={1.5}>{cards.length === 0 ? <Typography color="text.secondary">帮助指南正在准备中。</Typography> : cards.map((card, index) =>
      <Accordion key={card.id || `${card.title}-${index}`} disableGutters elevation={0} sx={{ border: "1px solid", borderColor: "divider", borderRadius: "8px !important", "&:not(:last-child)": { borderBottom: 0 }, "&::before": { display: "none" } }}>
        <AccordionSummary expandIcon={<ExpandMoreRoundedIcon />} sx={{ px: 2 }}>
          <Typography sx={{ fontSize: 15, fontWeight: 700 }}>{String(index + 1).padStart(2, "0")}. {card.title || "使用指南"}</Typography>
        </AccordionSummary>
        <AccordionDetails sx={{ px: 2, pb: 2 }}>
          <Typography sx={{ color: "text.secondary", lineHeight: 1.8, whiteSpace: "pre-wrap" }}>{card.content || card.summary || "暂无说明"}</Typography>
        </AccordionDetails>
      </Accordion>
    )}</Stack></>;
}
