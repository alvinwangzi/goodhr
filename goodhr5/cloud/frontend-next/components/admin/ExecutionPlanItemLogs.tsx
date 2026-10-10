/** 本文件只读展示 HRPlus 执行电脑的原执行项进度日志，明确本地保存及候选人明细尚未接入。 */
"use client";
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Stack, Typography } from "@mui/material";
import AdminDialog from "./AdminDialog";
import { useAdmin } from "./AdminApp";
import { localRequest } from "@/lib/admin-api";
import { readPlanProgressLogs, type PlanProgressLog } from "@/lib/execution-plans";
import { reportTimeText } from "@/lib/execution-plan-report";

/** ExecutionPlanItemLogs 核对明确日志能力后打开原任务读取，旧程序不显示假日志入口。 */
export default function ExecutionPlanItemLogs({ runID, itemID, taskID, timezone }: { runID: string; itemID: string; taskID: string; timezone: string }) {
  const { agentBase } = useAdmin();
  const [supported, setSupported] = useState(false);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [logs, setLogs] = useState<PlanProgressLog[]>([]);
  const [before, setBefore] = useState(0);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const generation = useRef(0);
  useEffect(() => { let active = true; setSupported(false); setOpen(false); setLogs([]); setBefore(0); if (agentBase) void localRequest(agentBase,"/health").then(value => { if (active) setSupported(value.capabilities?.execution_plan_item_logs === true); }).catch(() => {}); return () => { active=false; generation.current++; }; },[agentBase]);
  /** load 只读指定原任务，翻页失败保留原记录，关闭后迟到读取不能写回。 */
  async function load(cursor = 0) {
    const current = ++generation.current; setLoading(true); setError("");
    try { const result = await readPlanProgressLogs(agentBase,runID,itemID,taskID,cursor); if (current === generation.current) { setLogs(previous => cursor ? [...previous,...result.logs] : result.logs); setBefore(result.next_before); setMessage(result.message); } }
    catch (cause) { if (current === generation.current) setError(cause instanceof Error ? cause.message : "原日志暂时无法读取"); }
    finally { if (current === generation.current) setLoading(false); }
  }
  /** close 取消本弹框旧读取，不改变运行状态或原日志。 */
  function close() { generation.current++; setOpen(false); setLoading(false); }
  return <>
    <Button disabled={!supported} onClick={() => { setOpen(true); void load(); }}>查看本项进度日志</Button>
    {!supported && <Typography variant="caption" color="text.secondary">需连接实际执行电脑及支持进度日志的本地程序</Typography>}
    <AdminDialog open={open} title="原执行项进度日志" maxWidth="md" onClose={close}><Stack spacing={1.5}>
      <Alert severity="info">这些记录只保存在执行电脑，尚未上传云端。当前为安全步骤进度摘要，候选人级详细日志还未接入。</Alert>
      {error && <Alert severity="warning">{error}</Alert>}
      <Typography color="text.secondary">{message}</Typography>
      <Button disabled={loading} onClick={() => void load()}>刷新日志</Button>
      {logs.map(log => <Stack key={log.id} spacing={0.5}><Typography variant="caption">{reportTimeText(log.created_at,timezone)} · {log.level === "error" ? "错误" : log.level === "warning" ? "警告" : "信息"}</Typography><Typography sx={{ whiteSpace:"pre-wrap",wordBreak:"break-word" }}>{log.message}</Typography></Stack>)}
      {before > 0 && <Button disabled={loading} onClick={() => void load(before)}>读取更早记录</Button>}
    </Stack></AdminDialog>
  </>;
}
