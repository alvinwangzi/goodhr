/** 本文件展示 HRPlus 当前只读运行事实和名义安排，执行详情按原项和原任务保留归属。 */
"use client";
import { Alert, Stack, Typography } from "@mui/material";
import { PLAN_ACTION_LABELS, PLAN_WAIT_LABELS, type PlanAction, type PlanRuntime } from "@/lib/execution-plans";
import { EXECUTION_STATE_LABELS, reportTimeText } from "@/lib/execution-plan-report";
import ExecutionPlanItemLogs from "./ExecutionPlanItemLogs";

/** ExecutionPlanRuntime 不以名义时间或电脑连接判断已开始，展示原云端确认时间和独立动作数量。 */
export default function ExecutionPlanRuntime({ value, names, details = false }: { value: PlanRuntime; names: Record<string, string>; details?: boolean }) {
  const timezone = value.plan.config.schedule.timezone;
  const run = value.current_run;
  return <Stack spacing={1}>
    <Typography>当前安排：{PLAN_WAIT_LABELS[value.wait_reason] || "状态需要核对"}</Typography>
    {value.nominal_at && <Typography>名义开始时间：{reportTimeText(value.nominal_at, timezone)} · {timezone}</Typography>}
    {value.waiting && <>
      <Typography>原定触发时间：{reportTimeText(value.waiting.triggered_at, timezone)}</Typography>
      <Typography>实际入队时间：{value.waiting.queued_at ? reportTimeText(value.waiting.queued_at, timezone) : "旧记录未保存"} · {value.wait_seconds === undefined ? "等待时长未记录" : `读取时已等待 ${Math.floor(value.wait_seconds / 60)} 分 ${value.wait_seconds % 60} 秒`}</Typography>
    </>}
    {["account_busy", "queued", "queue_waiting_time"].includes(value.wait_reason) && value.account_owner && <Typography color="text.secondary">占用电脑：{value.account_owner.machine_id.slice(-8)} · {EXECUTION_STATE_LABELS[value.account_owner.state] || "等待核对"}。当前计划尚未确认开始。</Typography>}
    <Typography variant="caption" color="text.secondary">云端事实读取于 {reportTimeText(value.observed_at, timezone)}；名义时间不代表实际开始时间。</Typography>
    {details && <>
      {!run ? <Alert severity="info">当前执行日尚无已确认的运行记录。</Alert> : <>
        <Typography>原执行日期：{run.execution_date} · {EXECUTION_STATE_LABELS[run.state] || "待核对"}</Typography>
        {run.items.map((item, index) => { const config = run.snapshot.items.find(entry => entry.id === item.item_id); return <Stack key={item.id} spacing={0.5}>
          <Typography sx={{ fontWeight: 700 }}>第 {index + 1} 项 · {names[config?.position_id || ""] || "原岗位"} · {EXECUTION_STATE_LABELS[item.state] || "待核对"}{index === run.current_item && ["starting", "running", "draining"].includes(run.state) ? " · 当前执行项" : ""}</Typography>
          <Typography variant="caption" sx={{ wordBreak: "break-all" }}>原执行项：{item.id} · 原任务：{item.task_run_id || "尚未准备"}</Typography>
          {Object.entries(item.actions).map(([action, progress]) => <Typography key={action}>{PLAN_ACTION_LABELS[action as PlanAction] || action}：{EXECUTION_STATE_LABELS[progress.state] || "待核对"} · 已确认 {progress.count} · 结果待核对 {progress.unknown_count}</Typography>)}
          {item.task_run_id && <ExecutionPlanItemLogs runID={run.id} itemID={item.id} taskID={item.task_run_id} timezone={timezone} />}
        </Stack>; })}
      </>}
    </>}
  </Stack>;
}
