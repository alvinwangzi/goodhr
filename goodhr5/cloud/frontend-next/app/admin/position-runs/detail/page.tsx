/** 本文件负责新版后台任务记录详情：任务统计和本次运行的打招呼、索要简历名单。 */
"use client";

import ArrowBackRoundedIcon from "@mui/icons-material/ArrowBackRounded";
import {
  Box,
  Button,
  Chip,
  Pagination,
  Stack,
  Tab,
  Tabs,
  Typography,
} from "@mui/material";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import {
  EmptyState,
  PageHeader,
  RefreshButton,
  SectionPanel,
} from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";
import { cloudRequest, formatDate, formatDateTime } from "@/lib/admin-api";
import { scoreText } from "@/lib/candidate-normalize";

/** TaskRunDetail 表示任务记录详情的接口数据。 */
type TaskRunDetail = {
  id: string;
  user_email: string;
  position_id: string;
  position_name: string;
  platform_id: string;
  task_type: string;
  status: string;
  scanned_count: number;
  greeted_count: number;
  resume_requested_count: number;
  skipped_count: number;
  failed_count: number;
  error_message: string;
  started_at: string;
  finished_at: string | null;
};

/** TaskRunCandidateItem 表示任务记录名单里的一条候选人记录。 */
type TaskRunCandidateItem = {
  candidate_id: string;
  candidate_name: string;
  phone: string;
  ai_detail_score: number | null;
  ai_detail_reason: string;
  ai_greet_score: number | null;
  ai_greet_reason: string;
  action_at: string | null;
  message_text: string;
};

/** taskRunStatusText 返回任务记录状态的中文文案。 */
function taskRunStatusText(status: string) {
  return (
    ({
      running: "运行中",
      completed: "已完成",
      stopped: "已停止",
      failed: "失败",
    } as Record<string, string>)[status] || status || "未知"
  );
}

/** taskRunStatusColor 返回任务记录状态对应的展示颜色。 */
function taskRunStatusColor(
  status: string,
): "info" | "success" | "warning" | "error" | "default" {
  if (status === "running") return "info";
  if (status === "completed") return "success";
  if (status === "stopped") return "warning";
  if (status === "failed") return "error";
  return "default";
}

/** PositionRunDetailPage 展示一次任务记录的统计和候选人名单。 */
export default function PositionRunDetailPage() {
  const params = useSearchParams();
  const { notify } = useAdmin();
  const runID = params.get("run_id") || "";
  const [run, setRun] = useState<TaskRunDetail | null>(null);
  const [tab, setTab] = useState(0);
  const [candidates, setCandidates] = useState<TaskRunCandidateItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [candidatesLoading, setCandidatesLoading] = useState(false);

  /** loadRun 读取任务详情。 */
  async function loadRun() {
    if (!runID) return;
    setLoading(true);
    try {
      const data = await cloudRequest(
        `/api/task-runs/${encodeURIComponent(runID)}`,
      );
      setRun(data.run || null);
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "任务记录读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  /** loadCandidates 读取当前名单 Tab 的候选人数据。 */
  async function loadCandidates(filter: string) {
    if (!runID) return;
    setCandidatesLoading(true);
    try {
      const data = await cloudRequest(
        `/api/task-runs/${encodeURIComponent(runID)}/candidates?filter=${filter}`,
      );
      setCandidates(data.candidates || []);
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "名单读取失败",
        "error",
      );
    } finally {
      setCandidatesLoading(false);
    }
  }

  useEffect(() => {
    void loadRun();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runID]);

  useEffect(() => {
    void loadCandidates(tab === 0 ? "greeted" : "resume");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runID, tab]);

  if (!runID)
    return (
      <SectionPanel>
        <Typography color='error'>缺少任务记录 ID</Typography>
      </SectionPanel>
    );

  return (
    <>
      <PageHeader
        title={
          run
            ? `任务记录 · ${run.position_name || "岗位已删除"}`
            : "任务记录详情"
        }
        description={
          run
            ? `${taskRunStatusText(run.status)} · 开始时间 ${formatDateTime(run.started_at)}`
            : undefined
        }
        actions={
          <>
            <RefreshButton
              loading={loading || candidatesLoading}
              onClick={() => {
                void loadRun();
                void loadCandidates(tab === 0 ? "greeted" : "resume");
              }}
            />
            <Button
              component={Link}
              href='/admin/position-runs'
              startIcon={<ArrowBackRoundedIcon />}
            >
              返回列表
            </Button>
          </>
        }
      />
      {run ? <RunSummary run={run} /> : null}
      <SectionPanel sx={{ p: 0, overflow: "hidden" }}>
        <Box sx={{ px: 2, pt: 1.5, borderBottom: "1px solid", borderColor: "divider" }}>
          <Tabs
            value={tab}
            onChange={(_, value: number) => setTab(value)}
            sx={{ minHeight: 42 }}
          >
            <Tab label={`打招呼名单（${run?.greeted_count ?? 0}）`} />
            <Tab label={`索要简历名单（${run?.resume_requested_count ?? 0}）`} />
          </Tabs>
        </Box>
        {candidates.length ? (
          <CandidateList
            items={candidates}
            showGreetMessage={tab === 0}
          />
        ) : (
          <EmptyState
            text={
              candidatesLoading
                ? "正在读取名单"
                : tab === 0
                  ? "本次任务暂无打招呼记录"
                  : "本次任务暂无索要简历记录"
            }
          />
        )}
      </SectionPanel>
    </>
  );
}

/** RunSummary 展示任务统计信息。 */
function RunSummary({ run }: { run: TaskRunDetail }) {
  const stats = [
    { label: "扫描候选人", value: run.scanned_count },
    { label: "打招呼", value: run.greeted_count },
    { label: "索要简历", value: run.resume_requested_count },
    { label: "跳过", value: run.skipped_count },
    { label: "失败", value: run.failed_count },
  ];
  return (
    <SectionPanel sx={{ mb: 2 }}>
      <Stack
        direction={{ xs: "column", sm: "row" }}
        spacing={2}
        sx={{
          justifyContent: "space-between",
          alignItems: { sm: "center" },
        }}
      >
        <Stack direction='row' spacing={1} sx={{ alignItems: "center", flexWrap: "wrap" }}>
          <Chip
            size='small'
            label={taskRunStatusText(run.status)}
            color={taskRunStatusColor(run.status)}
          />
          <Typography sx={{ color: "text.secondary", fontSize: 13 }}>
            结束时间：{run.finished_at ? formatDateTime(run.finished_at) : "--"}
          </Typography>
          <Typography sx={{ color: "text.secondary", fontSize: 13 }}>
            创建人：{run.user_email || "未知成员"}
          </Typography>
        </Stack>
        <Stack direction='row' spacing={{ xs: 1.5, sm: 3 }}>
          {stats.map((item) => (
            <Box key={item.label} sx={{ textAlign: "center" }}>
              <Typography sx={{ fontWeight: 820, fontSize: 20 }}>
                {item.value}
              </Typography>
              <Typography sx={{ color: "text.secondary", fontSize: 12 }}>
                {item.label}
              </Typography>
            </Box>
          ))}
        </Stack>
      </Stack>
      {run.error_message ? (
        <Typography
          sx={{
            mt: 1.5,
            color: "error.main",
            fontSize: 13,
            whiteSpace: "pre-wrap",
          }}
        >
          停止原因：{run.error_message}
        </Typography>
      ) : null}
    </SectionPanel>
  );
}

/** CandidateList 展示名单表头和候选人行。 */
function CandidateList({
  items,
  showGreetMessage,
}: {
  items: TaskRunCandidateItem[];
  showGreetMessage: boolean;
}) {
  return (
    <>
      <Box
        sx={{
          display: { xs: "none", md: "grid" },
          gridTemplateColumns: "1.2fr .6fr .6fr 1.6fr 1fr",
          px: 2,
          py: 1.5,
          bgcolor: "action.hover",
          borderBottom: "1px solid",
          borderColor: "divider",
          "& p": { fontWeight: 800 },
        }}
      >
        <Typography>候选人</Typography>
        <Typography>详情分</Typography>
        <Typography>招呼分</Typography>
        <Typography>{showGreetMessage ? "AI 判断原因与招呼语" : "AI 判断原因"}</Typography>
        <Typography>动作时间</Typography>
      </Box>
      <Stack>
        {items.map((item) => (
          <CandidateRow
            key={`${item.candidate_id}-${item.action_at || ""}`}
            item={item}
            showGreetMessage={showGreetMessage}
          />
        ))}
      </Stack>
    </>
  );
}

/** CandidateRow 展示一行名单候选人。 */
function CandidateRow({
  item,
  showGreetMessage,
}: {
  item: TaskRunCandidateItem;
  showGreetMessage: boolean;
}) {
  const detailHref = `/admin/resumes/detail?candidate_id=${encodeURIComponent(item.candidate_id)}`;
  const reason = item.ai_greet_reason || item.ai_detail_reason || "";
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: {
          xs: "1fr",
          md: "1.2fr .6fr .6fr 1.6fr 1fr",
        },
        gap: { xs: 1, md: 2 },
        alignItems: "center",
        width: "100%",
        px: 2,
        py: 1.8,
        borderBottom: "1px solid",
        borderColor: "divider",
      }}
    >
      <Box sx={{ minWidth: 0 }}>
        <Button
          component={Link}
          href={detailHref}
          color='secondary'
          sx={{
            justifyContent: "flex-start",
            p: 0,
            minWidth: 0,
            textAlign: "left",
          }}
        >
          <Typography noWrap sx={{ fontWeight: 800 }}>
            {item.candidate_name || "未命名候选人"}
          </Typography>
        </Button>
        <Typography noWrap sx={{ color: "text.secondary", fontSize: 12 }}>
          {item.phone || "暂无手机号"}
        </Typography>
      </Box>
      <Typography>{scoreText(item.ai_detail_score)}</Typography>
      <Typography>{scoreText(item.ai_greet_score)}</Typography>
      <Box sx={{ minWidth: 0 }}>
        {reason ? (
          <Typography
            title={reason}
            sx={{
              color: "text.secondary",
              fontSize: 13,
              display: "-webkit-box",
              overflow: "hidden",
              WebkitBoxOrient: "vertical",
              WebkitLineClamp: 2,
              overflowWrap: "anywhere",
            }}
          >
            {reason}
          </Typography>
        ) : (
          <Typography sx={{ color: "text.secondary", fontSize: 13 }}>
            暂无判断原因
          </Typography>
        )}
        {showGreetMessage && item.message_text ? (
          <Typography
            title={item.message_text}
            sx={{
              mt: 0.6,
              fontSize: 12,
              display: "-webkit-box",
              overflow: "hidden",
              WebkitBoxOrient: "vertical",
              WebkitLineClamp: 2,
              overflowWrap: "anywhere",
            }}
          >
            招呼语:{item.message_text}
          </Typography>
        ) : null}
      </Box>
      <Typography sx={{ fontSize: 13, color: "text.secondary" }}>
        {item.action_at ? formatDate(item.action_at) : "--"}
      </Typography>
    </Box>
  );
}
