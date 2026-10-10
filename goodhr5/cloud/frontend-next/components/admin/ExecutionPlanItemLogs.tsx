/** 本文件只读展示 HRPlus 执行电脑的原执行项日志，明确本地保存及旧无来源记录的范围。 */
"use client";
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Stack, Typography } from "@mui/material";
import AdminDialog from "./AdminDialog";
import { useAdmin } from "./AdminApp";
import { localRequest } from "@/lib/admin-api";
import { readPlanProgressLogs,readCloudPlanLogs, type PlanProgressLog } from "@/lib/execution-plans";
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
  const [source,setSource]=useState<"cloud"|"local">("cloud");
  const generation = useRef(0);
  useEffect(() => { let active = true; setSupported(false); setOpen(false); setLogs([]); setBefore(0); if (agentBase) void localRequest(agentBase,"/health").then(value => { if (active) setSupported(value.capabilities?.execution_plan_item_logs === true); }).catch(() => {}); return () => { active=false; generation.current++; }; },[agentBase]);
  /** load 只读指定原任务，翻页失败保留原记录，关闭后迟到读取不能写回。 */
  async function load(cursor = 0,selected=source) {
    const current = ++generation.current; setLoading(true); setError("");
    try { const result = selected==="cloud" ? await readCloudPlanLogs(runID,itemID,taskID,cursor) : await readPlanProgressLogs(agentBase,runID,itemID,taskID,cursor); if (current === generation.current) { setLogs(previous => cursor ? [...previous,...result.logs] : result.logs); setBefore(result.next_before); setMessage(result.message); } }
    catch (cause) { if (current === generation.current) setError(cause instanceof Error ? cause.message : "原日志暂时无法读取"); }
    finally { if (current === generation.current) setLoading(false); }
  }
  /** close 取消本弹框旧读取，不改变运行状态或原日志。 */
  function close() { generation.current++; setOpen(false); setLoading(false); }
  return <>
    <Button onClick={() => { setSource("cloud"); setOpen(true); void load(0,"cloud"); }}>查看本项进度日志</Button>
    <AdminDialog open={open} title="原执行项进度日志" maxWidth="md" onClose={close}><Stack spacing={1.5}>
      <Alert severity="info">云端显示已补传的原任务日志；执行电脑本地可能还有待补传记录。旧版没有明确来源的日志仍在原岗位日志中。</Alert>
      <Stack direction="row" spacing={1}>{(["cloud","local"] as const).map(value=><Button key={value} variant={source===value?"contained":"outlined"} disabled={value==="local"&&!supported} onClick={()=>{generation.current++;setSource(value);setLogs([]);setBefore(0);void load(0,value);}}>{value==="cloud"?"云端已同步":"当前电脑本地"}</Button>)}</Stack>
      {!supported && <Typography variant="caption" color="text.secondary">本地记录需连接支持日志读取的实际执行电脑，云端读取不受此影响。</Typography>}
      {error && <Alert severity="warning">{error}</Alert>}
      <Typography color="text.secondary">{message}</Typography>
      <Button disabled={loading} onClick={() => void load()}>刷新日志</Button>
      {logs.map(log => <Stack key={log.id} spacing={0.5}><Typography variant="caption">{reportTimeText(log.created_at,timezone)} · {log.level === "error" ? "错误" : log.level === "warning" ? "警告" : "信息"}</Typography><Typography sx={{ whiteSpace:"pre-wrap",wordBreak:"break-word" }}>{log.message}</Typography></Stack>)}
      {before > 0 && <Button disabled={loading} onClick={() => void load(before)}>读取更早记录</Button>}
    </Stack></AdminDialog>
  </>;
}
