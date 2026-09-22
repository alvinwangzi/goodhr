/** 本文件负责新版后台任务记录列表：展示每次岗位运行的记录、统计和详情入口。 */
"use client";

import OpenInNewRoundedIcon from "@mui/icons-material/OpenInNewRounded";
import {
  Box,
  Button,
  Chip,
  Pagination,
  Stack,
  Typography,
} from "@mui/material";
import Link from "next/link";
import { useEffect, useState } from "react";
import {
  EmptyState,
  PageHeader,
  RefreshButton,
  SectionPanel,
} from "@/components/admin/AdminUI";
import { useAdmin } from "@/components/admin/AdminApp";
import PlatformLogo from "@/components/admin/PlatformLogo";
import { cloudRequest, formatDateTime } from "@/lib/admin-api";

/** TaskRunItem 表示一条任务记录的接口数据。 */
type TaskRunItem = {
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
function taskRunStatusColor(status: string): "info" | "success" | "warning" | "error" | "default" {
  if (status === "running") return "info";
  if (status === "completed") return "success";
  if (status === "stopped") return "warning";
  if (status === "failed") return "error";
  return "default";
}

/** taskRunTypeText 返回任务类型的中文文案。 */
function taskRunTypeText(taskType: string) {
  return (
    ({ greeting: "岗位运行", auto_reply: "AI对答" } as Record<string, string>)[
      taskType
    ] || taskType
  );
}

/** PositionRunsPage 展示任务记录分页列表，点击单行进入任务详情。 */
export default function PositionRunsPage() {
  const { notify } = useAdmin();
  const [items, setItems] = useState<TaskRunItem[]>([]);
  const [pageSize] = useState(10);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);

  /** load 读取任务记录分页列表。 */
  async function load(nextPage = page) {
    setLoading(true);
    try {
      const query = new URLSearchParams({
        page: String(nextPage),
        page_size: String(pageSize),
      });
      const data = await cloudRequest(`/api/task-runs?${query}`);
      setItems(data.runs || []);
      setTotal(Number(data.total || 0));
      setPage(Number(data.page || nextPage));
    } catch (error) {
      notify(
        error instanceof Error ? error.message : "任务记录读取失败",
        "error",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <>
      <PageHeader
        title='任务记录'
        description='每次点击开始岗位运行都会在这里记录一条任务，可以查看本次打招呼和索要简历的名单。'
        actions={
          <RefreshButton loading={loading} onClick={() => void load()} />
        }
      />
      <SectionPanel sx={{ p: 0, overflow: "hidden" }}>
        {items.length ? (
          <>
            <Box
              sx={{
                display: { xs: "none", md: "grid" },
                gridTemplateColumns:
                  "minmax(0,1.6fr) minmax(0,.9fr) minmax(0,1.05fr) minmax(0,1.05fr) minmax(0,.55fr) minmax(0,.55fr) .5fr",
                px: 2,
                py: 1.5,
                bgcolor: "action.hover",
                borderBottom: "1px solid",
                borderColor: "divider",
                "& p": { fontWeight: 800 },
              }}
            >
              <Typography>岗位</Typography>
              <Typography>状态</Typography>
              <Typography>开始时间</Typography>
              <Typography>结束时间</Typography>
              <Typography sx={{ textAlign: "center" }}>打招呼</Typography>
              <Typography sx={{ textAlign: "center" }}>要简历</Typography>
              <Typography />
            </Box>
            <Stack>
              {items.map((item) => (
                <RunRow key={item.id} item={item} />
              ))}
            </Stack>
          </>
        ) : (
          <EmptyState text={loading ? "正在读取任务记录" : "暂无任务记录，去岗位页点击开始后这里会记录。"} />
        )}
        <Stack
          direction={{ xs: "column", sm: "row" }}
          spacing={2}
          sx={{
            p: 2,
            justifyContent: "space-between",
            alignItems: "center",
            borderTop: "1px solid",
            borderColor: "divider",
          }}
        >
          <Typography color='text.secondary'>共 {total} 条任务</Typography>
          <Pagination
            page={page}
            count={Math.max(1, Math.ceil(total / pageSize))}
            onChange={(_, value) => void load(value)}
            color='primary'
          />
        </Stack>
      </SectionPanel>
    </>
  );
}

/** RunRow 展示一行任务记录。 */
function RunRow({ item }: { item: TaskRunItem }) {
  const href = `/admin/position-runs/detail?run_id=${encodeURIComponent(item.id)}`;
  const positionText = item.position_name || "岗位已删除";
  const running = item.status === "running";
  return (
    <Box
      sx={{
        display: "grid",
        gridTemplateColumns: {
          xs: "1fr",
          md:
            "minmax(0,1.6fr) minmax(0,.9fr) minmax(0,1.05fr) minmax(0,1.05fr) minmax(0,.55fr) minmax(0,.55fr) .5fr",
        },
        gap: { xs: 1, md: 2 },
        alignItems: "center",
        width: "100%",
        px: 2,
        py: 1.8,
        borderBottom: "1px solid",
        borderColor: "divider",
        transition: "background-color .2s",
        "&:hover": { bgcolor: "action.hover" },
      }}
    >
      <Box sx={{ minWidth: 0 }}>
        <Stack direction='row' spacing={1} sx={{ alignItems: "center" }}>
          <PlatformLogo
            platformID={item.platform_id}
            size={22}
          />
          <Typography noWrap title={positionText} sx={{ fontWeight: 800 }}>
            {positionText}
          </Typography>
        </Stack>
        <Typography
          noWrap
          sx={{ mt: 0.4, color: "text.secondary", fontSize: 12 }}
        >
          {taskRunTypeText(item.task_type)} · {item.user_email || "未知成员"}
        </Typography>
      </Box>
      <Box sx={{ minWidth: 0 }}>
        <Chip
          size='small'
          color={taskRunStatusColor(item.status)}
          label={
            <Stack
              direction='row'
              spacing={0.75}
              sx={{ alignItems: "center" }}
            >
              {running ? (
                <Box
                  sx={{
                    width: 7,
                    height: 7,
                    borderRadius: "50%",
                    bgcolor: "info.main",
                    animation: "goodhrRunPulse 1.2s ease-in-out infinite",
                    "@keyframes goodhrRunPulse": {
                      "0%, 100%": { opacity: 0.25 },
                      "50%": { opacity: 1 },
                    },
                  }}
                />
              ) : null}
              <span>{taskRunStatusText(item.status)}</span>
            </Stack>
          }
        />
        {item.status === "failed" && item.error_message ? (
          <Typography
            title={item.error_message}
            noWrap
            sx={{ mt: 0.4, color: "error.main", fontSize: 12 }}
          >
            {item.error_message}
          </Typography>
        ) : null}
      </Box>
      <Typography
        noWrap
        sx={{
          fontSize: 13,
          color: "text.secondary",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {formatDateTime(item.started_at)}
      </Typography>
      <Typography
        noWrap
        sx={{
          fontSize: 13,
          color: "text.secondary",
          fontVariantNumeric: "tabular-nums",
        }}
      >
        {item.finished_at ? formatDateTime(item.finished_at) : "--"}
      </Typography>
      <Typography
        sx={{
          fontWeight: 800,
          textAlign: "center",
          fontVariantNumeric: "tabular-nums",
          color:
            item.greeted_count > 0 ? "text.primary" : "text.disabled",
        }}
      >
        {item.greeted_count}
      </Typography>
      <Typography
        sx={{
          fontWeight: 800,
          textAlign: "center",
          fontVariantNumeric: "tabular-nums",
          color:
            item.resume_requested_count > 0
              ? "text.primary"
              : "text.disabled",
        }}
      >
        {item.resume_requested_count}
      </Typography>
      <Button
        component={Link}
        href={href}
        size='small'
        color='secondary'
        startIcon={<OpenInNewRoundedIcon />}
        sx={{ minWidth: 0, justifyContent: "center", whiteSpace: "nowrap" }}
      >
        详情
      </Button>
    </Box>
  );
}
