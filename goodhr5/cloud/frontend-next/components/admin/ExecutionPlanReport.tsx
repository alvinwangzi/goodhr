/** 本文件展示 HRPlus 原报告及重复岗位独立结果，任务详情始终链接原 TaskRun。 */
"use client";
import { Alert, Button, Stack, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Typography } from "@mui/material";
import Link from "next/link";
import { SectionPanel } from "./AdminUI";
import ExecutionPlanItemLogs from "./ExecutionPlanItemLogs";
import { PLAN_ACTION_LABELS, type PlanAction } from "@/lib/execution-plans";
import { EXECUTION_STATE_LABELS, INFORMATION_LABELS, reportCountText, reportItemStatusText, reportNotificationText, reportReasonText, reportSyncText, reportTimeText, reportUnfinishedText, type ExecutionReport, type ReportCount } from "@/lib/execution-plan-report";

/** ResultTable 分列确认、待核对、跳过与失败，不把各列相加当成功。 */
function ResultTable({ counts, labels }: { counts: Record<string, ReportCount>; labels: Record<string, string> }) {
  return <TableContainer><Table size="small"><TableHead><TableRow>{["动作", "状态", "已确认", "结果待核对", "跳过", "失败"].map(label => <TableCell key={label}>{label}</TableCell>)}</TableRow></TableHead><TableBody>{Object.entries(counts).map(([action, value]) => <TableRow key={action}><TableCell>{labels[action] || "动作待核对"}</TableCell><TableCell>{EXECUTION_STATE_LABELS[value.state] || "状态待核对"}</TableCell><TableCell>{reportCountText(value.confirmed)}</TableCell><TableCell>{reportCountText(value.unknown)}</TableCell><TableCell>{reportCountText(value.skipped)}</TableCell><TableCell>{reportCountText(value.failed)}</TableCell></TableRow>)}</TableBody></Table></TableContainer>;
}

/** ExecutionPlanReport 展示首次生成摘要及当前同步/邮件事实，不把历史下次时间当当前启动许可。 */
export default function ExecutionPlanReport({ report, timezone, positionNames }: { report: ExecutionReport; timezone: string; positionNames: Record<string, string> }) {
  const summary = report.summary;
  return <Stack spacing={2}>
    <Typography variant="h6">{summary.plan_name} · {summary.execution_date}</Typography>
    <Typography>结果：{EXECUTION_STATE_LABELS[summary.run_state] || "需要核对"} · {reportReasonText(summary.end_reason)}</Typography>
    <Typography>同步：{reportSyncText(report.sync_state)} · 邮件：{reportNotificationText(report.notification_state)}</Typography>
    {report.sync_state === "pending" && <Alert severity="info">报告已保存，部分执行事实仍待补传。下方已确认数量保持原记录，不能据此认为尚未执行。</Alert>}
    {report.notification_error && <Alert severity="warning">通知原因：{report.notification_error}</Alert>}
    <Typography>未完成项：{reportUnfinishedText(report)}</Typography>
    <Typography color="text.secondary">生成时间：{reportTimeText(summary.generated_at, timezone)} · 结束时间：{reportTimeText(summary.finished_at, timezone)} · 时区：{timezone}</Typography>
    <Typography color="text.secondary">报告生成时的下次执行时间：{summary.next_nominal_at ? reportTimeText(summary.next_nominal_at, timezone) : "无"}。当前安排以计划最新状态为准。</Typography>
    <Typography variant="caption" sx={{ wordBreak: "break-all" }}>运行编号：{summary.run_id} · 配置版本：{summary.config_version} · 报告更新：{reportTimeText(report.updated_at, timezone)}</Typography>
    {summary.items.map(item => <SectionPanel key={item.id}><Stack spacing={1}>
      <Typography sx={{ fontWeight: 700 }}>第 {item.order + 1} 项 · {positionNames[item.position_id] || "原岗位"} · {reportItemStatusText(item)}</Typography>
      <Typography variant="caption" sx={{ wordBreak: "break-all" }}>执行项运行编号：{item.id} · 编排编号：{item.item_id}</Typography>
      <Typography>扫描人数：{item.details_available ? reportCountText(item.scanned) : "明细未齐，暂不显示"}</Typography>
      {!item.details_available && <Alert severity="info">本项详细记录尚未齐备，以下显示原运行已保存的动作摘要。</Alert>}
      <ResultTable counts={item.actions} labels={PLAN_ACTION_LABELS as Record<PlanAction, string>} />
      {Object.keys(item.information).length ? <><Typography>索要记录</Typography><ResultTable counts={item.information} labels={INFORMATION_LABELS} /></> : <Typography color="text.secondary">{item.details_available ? "本项没有索要记录" : "索要明细尚未齐备"}</Typography>}
      {item.task_run_id ? <Button component={Link} href={`/admin/position-runs/detail?run_id=${encodeURIComponent(item.task_run_id)}`}>查看本项原任务详情</Button> : <Typography color="text.secondary">本项尚未生成任务记录</Typography>}
      {item.task_run_id && <ExecutionPlanItemLogs runID={summary.run_id} itemID={item.id} taskID={item.task_run_id} timezone={timezone} />}
    </Stack></SectionPanel>)}
  </Stack>;
}
