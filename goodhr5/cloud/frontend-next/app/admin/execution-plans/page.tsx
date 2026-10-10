/** 本文件负责 HRPlus 执行计划管理，允许同岗位多次编排，停止确认后才能编辑。 */
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, Chip, FormControlLabel, MenuItem, Stack, TextField, Typography } from "@mui/material";
import AddRoundedIcon from "@mui/icons-material/AddRounded";
import { useAdmin } from "@/components/admin/AdminApp";
import AdminDialog from "@/components/admin/AdminDialog";
import ExecutionPlanReport from "@/components/admin/ExecutionPlanReport";
import { EXECUTION_STATE_LABELS, reportReasonText, type ExecutionReport } from "@/lib/execution-plan-report";
import { EmptyState, PageHeader, RefreshButton, SectionPanel } from "@/components/admin/AdminUI";
import { cloudRequest, localRequest } from "@/lib/admin-api";
import { canUseAutoReply } from "@/lib/subscription";
import { subscribeExecutionPlanEvents } from "@/lib/execution-plan-events";
import { canEditExecutionPlan, emptyPlanConfig, executionPlanIntent, executionPlanRuns, listExecutionPlans, PLAN_ACTION_LABELS, readExecutionReport, saveExecutionPlan, startExecutionPlan, timeMinute, timeText, validatePlanConfig, type ExecutionPlan, type ExecutionRun, type PlanAction, type PlanConfig, type PlanDevice } from "@/lib/execution-plans";

type PositionOption = { id: string; name: string; platform_id: string; match_limit?: number };
type WindowDraft = { id: string; start: string; end: string };
const RUN_LABELS = EXECUTION_STATE_LABELS;

/** ExecutionPlansPage 展示和保存独立编排，加载页面仅读取事实，不重发开始命令。 */
export default function ExecutionPlansPage() {
  const { agentBase, notify, confirm, subscription, user } = useAdmin();
  const [plans, setPlans] = useState<ExecutionPlan[]>([]);
  const [positions, setPositions] = useState<PositionOption[]>([]);
  const [devices, setDevices] = useState<PlanDevice[]>([]);
  const [deviceError, setDeviceError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [machine, setMachine] = useState("");
  const [connectedMachine, setConnectedMachine] = useState("");
  const [supported, setSupported] = useState(false);
  const [statusConnected, setStatusConnected] = useState(false);
  const [dialog, setDialog] = useState(false);
  const [original, setOriginal] = useState<ExecutionPlan>();
  const [config, setConfig] = useState<PlanConfig>(emptyPlanConfig);
  const [windows, setWindows] = useState<WindowDraft[]>([]);
  const [runs, setRuns] = useState<Record<string, ExecutionRun[]>>({});
  const [report, setReport] = useState<ExecutionReport>();
  const [reportTimezone, setReportTimezone] = useState("UTC");
  const [reportRefreshError, setReportRefreshError] = useState("");
  const reportRequest = useRef(0);
  const reportOpenRun = useRef<ExecutionRun | undefined>(undefined);
  const generation = useRef(0);
  const intents = useRef(new Map<string, string>());

  /** load 并行读取岗位与计划，迟到旧读取不能覆盖后续页面状态。 */
  const load = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    try {
      const [items, jobs, bindings] = await Promise.all([listExecutionPlans(), cloudRequest("/api/positions"), cloudRequest("/api/agents/bindings").catch(() => ({ agents: [], error: "绑定电脑暂时无法读取，请刷新后再选择" }))]);
      if (current !== generation.current) return;
      setPlans(items); setPositions(jobs.positions || []); setDevices(bindings.agents || []); setDeviceError(bindings.error || "");
      const history = await Promise.all(items.map(async plan => [plan.id, await executionPlanRuns(plan.id)] as const));
      if (current === generation.current) {
        setRuns(Object.fromEntries(history));
        const opened = reportOpenRun.current; const request = reportRequest.current;
        if (opened) {
          try { const updated = await readExecutionReport(opened.id); if (current === generation.current && request === reportRequest.current) { setReport(updated); setReportRefreshError(""); } }
          catch { if (current === generation.current && request === reportRequest.current) setReportRefreshError("报告更新暂未确认，当前显示上次读取的记录，可刷新后核对。"); }
        }
      }
    } catch (error) { if (current === generation.current) notify(error instanceof Error ? error.message : "计划暂时无法读取", "error"); }
    finally { if (current === generation.current) setLoading(false); }
  }, [notify]);

  useEffect(() => {
    reportRequest.current++; reportOpenRun.current = undefined;
    setPlans([]); setPositions([]); setDevices([]); setDeviceError(""); setRuns({}); setReport(undefined); setReportRefreshError(""); setDialog(false); setOriginal(undefined); intents.current.clear(); setStatusConnected(false); void load();
    const disconnect = subscribeExecutionPlanEvents(load, setStatusConnected);
    return () => { disconnect(); generation.current++; };
  }, [load, user?.email]);
  useEffect(() => {
    let active = true; setSupported(false); setConnectedMachine("");
    if (agentBase) void localRequest(agentBase, "/health").then(data => { if (active) { setSupported(data.capabilities?.execution_plans === true); setConnectedMachine(String(data.machine_id || "")); } }).catch(() => { if (active) setSupported(false); });
    return () => { active = false; };
  }, [agentBase]);

  /** edit 拷贝配置到编辑草稿，原运行中的计划不能打开编辑入口。 */
  function edit(plan?: ExecutionPlan) {
    if (plan && !canEditExecutionPlan(plan)) { notify("请先停止并等待收尾，再编辑", "warning"); return; }
    const draft = plan ? structuredClone(plan.config) : emptyPlanConfig();
    setOriginal(plan); setConfig(draft); setWindows(draft.schedule.windows.map(window => ({ id: crypto.randomUUID(), start: timeText(window.start_minute), end: timeText(window.end_minute) }))); setMachine(plan?.machine_id || (devices.some(device => device.machine_id === connectedMachine) ? connectedMachine : devices[0]?.machine_id || "")); setDialog(true);
  }
  /** save 只提交本次草稿及原版本，服务端拒绝时保留草稿并刷新事实。 */
  async function save() {
    const submitted = { ...config, schedule: { ...config.schedule, windows: windows.map((window, order) => ({ order, start_minute: timeMinute(window.start), end_minute: timeMinute(window.end) })) } };
    const problem = validatePlanConfig(submitted);
    if (problem || !devices.some(device => device.machine_id === machine)) { notify(problem || "请选择仍与当前账号绑定的执行电脑", "warning"); return; }
    setBusy("save");
    try { await saveExecutionPlan(submitted, machine, original); setDialog(false); notify("计划已保存", "success"); await load(); }
    catch (error) { notify(error instanceof Error ? error.message : "计划保存未确认", "error"); await load(); }
    finally { setBusy(""); }
  }
  /** intent 对启用和停止保留原请求编号，响应不明后重试不能生成新批次。 */
  async function intent(plan: ExecutionPlan, action: "arm" | "stop", immediate = false) {
    if (action === "stop" && !await confirm("停止计划", "停止后等待当前动作收尾，再允许编辑。重新启用会从第一个岗位开始。")) return;
    if (action === "arm" && (!supported || !agentBase)) { notify("请连接支持执行计划的本地程序", "warning"); return; }
    const key = `${plan.id}:${plan.version}:${plan.activation_id}:${action}`;
    const requestID = intents.current.get(key) || crypto.randomUUID(); intents.current.set(key, requestID);
    setBusy(plan.id);
    try {
      const updated = await executionPlanIntent(plan, action, requestID, immediate ? "immediate" : "scheduled"); intents.current.delete(key);
      setPlans(values => values.map(value => value.id === plan.id ? updated : value));
      if (immediate) { const result = await startExecutionPlan(agentBase, updated); notify(result.message || "已登记开始请求", "info"); }
      else notify(action === "stop" ? "正在收尾，确认停止后才能编辑" : "计划已启用，等待定时执行", "success");
      await load();
    } catch (error) { notify(error instanceof Error ? error.message : "计划操作未确认", "error"); await load(); }
    finally { setBusy(""); }
  }
  /** start 仅在原已启用批次请求立即开始，页面成功反馈以实际接口状态为准。 */
  async function start(plan: ExecutionPlan) {
    if (!supported || !agentBase) { notify("请更新并连接支持执行计划的本地程序", "warning"); return; }
    setBusy(plan.id);
    try { const result = await startExecutionPlan(agentBase, plan); notify(result.message || "已登记开始请求", "info"); await load(); }
    catch (error) { notify(error instanceof Error ? error.message : "开始请求未确认", "error"); }
    finally { setBusy(""); }
  }
  /** move 调整独立执行项顺序，同岗位条目仍保留不同编号。 */
  function move(index: number, delta: number) { setConfig(value => { const items = [...value.items]; [items[index], items[index + delta]] = [items[index + delta], items[index]]; return { ...value, items: items.map((item, order) => ({ ...item, order })) }; }); }
  /** openReport 读取原报告，待核对和待补传单独显示。 */
  async function openReport(run: ExecutionRun) {
    const request = ++reportRequest.current; reportOpenRun.current = run;
    try { const value = await readExecutionReport(run.id); if (request === reportRequest.current) { setReportTimezone(run.snapshot?.schedule?.timezone || "UTC"); setReportRefreshError(""); setReport(value); } }
    catch (error) { if (request === reportRequest.current) { reportOpenRun.current = undefined; notify(error instanceof Error ? error.message : "报告暂时未同步", "warning"); } }
  }
  /** closeReport 关闭原报告并取消迟到响应的页面更新，不发送任务操作。 */
  function closeReport() { reportRequest.current++; reportOpenRun.current = undefined; setReport(undefined); setReportRefreshError(""); }

  return <>
    <PageHeader title="执行计划" description="按工作时间安排岗位顺序，同一岗位可添加多次，选择不同动作。" actions={<><RefreshButton loading={loading} onClick={() => void load()} /><Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => edit()}>新建计划</Button></>} />
    {!supported && <Alert severity="info" sx={{ mb: 2 }}>当前本地程序未提供执行计划能力，运行前请更新并连接。计划配置仍可查看。</Alert>}
    {!statusConnected && <Alert severity="warning" sx={{ mb: 2 }}>状态通知正在连接，当前显示可能不是最新状态，可点击刷新核对。</Alert>}
    <Stack spacing={2}>{!plans.length && !loading ? <EmptyState text="暂无执行计划" /> : plans.map(plan => {
      const history = runs[plan.id] || []; const latest = history.find(run => run.activation_id === plan.activation_id);
      return <SectionPanel key={plan.id}><Stack spacing={1.5}>
        <Stack direction="row" spacing={1} sx={{ alignItems: "center" }}><Typography variant="h6">{plan.config.name}</Typography><Chip size="small" label={plan.stop_requested ? "正在收尾" : plan.state === "enabled" ? "已启用" : "已停止"} />{latest && <Chip size="small" variant="outlined" label={RUN_LABELS[latest.state] || "待核对"} />}</Stack>
        <Typography color="text.secondary">{plan.config.schedule.cycle === "once" ? `一次性 · ${plan.config.schedule.once_date}` : plan.config.schedule.cycle === "weekly" ? "每周执行" : "每天执行"} · {plan.config.schedule.windows.map(window => `${timeText(window.start_minute)}–${timeText(window.end_minute)}`).join("，")}</Typography>
        <Typography color="text.secondary">执行电脑：{plan.machine_id === connectedMachine ? "当前电脑" : `电脑 ${plan.machine_id.slice(-8)}`} · 计划启用不代表电脑正在执行</Typography>
        <Typography>岗位顺序：{plan.config.items.map((item, index) => `${index + 1}. ${positions.find(position => position.id === item.position_id)?.name || "原岗位"}（${item.actions.map(action => PLAN_ACTION_LABELS[action]).join("、")}）`).join(" → ")}</Typography>
        {latest && <Typography color="text.secondary">执行日期 {latest.execution_date} · 当前第 {Math.min(latest.current_item + 1, latest.items.length)} 项{latest.end_reason ? ` · ${reportReasonText(latest.end_reason)}` : ""}</Typography>}
        <Stack direction="row" spacing={1} sx={{ flexWrap: "wrap" }}>
          {canEditExecutionPlan(plan) ? <Button disabled={!!busy || !supported} onClick={() => void intent(plan, "arm")}>启用定时</Button> : <Button color="error" disabled={!!busy || plan.stop_requested} onClick={() => void intent(plan, "stop")}>停止</Button>}
          <Button disabled={!!busy || !supported || plan.stop_requested} onClick={() => canEditExecutionPlan(plan) ? void intent(plan, "arm", true) : void start(plan)}>立马开始</Button>
          <Button disabled={!!busy || !canEditExecutionPlan(plan)} onClick={() => edit(plan)}>编辑</Button>
          {history.map(run => <Button key={run.id} onClick={() => void openReport(run)}>报告 {run.execution_date}</Button>)}
        </Stack>
      </Stack></SectionPanel>;
    })}</Stack>
    <AdminDialog open={dialog} title={original ? "编辑执行计划" : "新建执行计划"} maxWidth="md" loading={busy === "save"} onClose={() => setDialog(false)} onConfirm={() => void save()}>
      <Stack spacing={3}>
        <Typography sx={{ fontWeight: 700 }}>名称与周期</Typography>
        <TextField label="计划名称" value={config.name} onChange={event => setConfig(value => ({ ...value, name: event.target.value }))} />
        <TextField select label="执行电脑" helperText={deviceError || (devices.length ? "仅显示当前账号已绑定的电脑。计划只在所选电脑执行，连接记录不代表当前在线。" : "暂无已绑定电脑，请先登录本地程序并连接。") } error={!!deviceError || !devices.length} value={machine} onChange={event => setMachine(event.target.value)}>
          <MenuItem value="" disabled>请选择执行电脑</MenuItem>
          {machine && !devices.some(device => device.machine_id === machine) && <MenuItem value={machine} disabled>原电脑已解绑，请重新选择</MenuItem>}
          {devices.map(device => <MenuItem key={device.machine_id} value={device.machine_id}>{device.machine_id === connectedMachine ? "当前电脑" : `电脑 ${device.machine_id.slice(-8)}`} · 程序 {device.agent_version || "版本未提供"}</MenuItem>)}
        </TextField>
        <TextField select label="任务周期" value={config.schedule.cycle} onChange={event => setConfig(value => ({ ...value, schedule: { ...value.schedule, cycle: event.target.value as PlanConfig["schedule"]["cycle"] } }))}><MenuItem value="once">一次性</MenuItem><MenuItem value="daily">每天</MenuItem><MenuItem value="weekly">每周</MenuItem></TextField>
        {config.schedule.cycle === "once" ? <TextField type="date" label="执行日期" slotProps={{ inputLabel: { shrink: true } }} value={config.schedule.once_date || ""} onChange={event => setConfig(value => ({ ...value, schedule: { ...value.schedule, once_date: event.target.value } }))} /> : <Stack direction={{ xs: "column", sm: "row" }} spacing={2}>{(["start_date", "end_date"] as const).map(key => <TextField key={key} type="date" label={key === "start_date" ? "生效日期（可选）" : "结束日期（可选）"} slotProps={{ inputLabel: { shrink: true } }} value={config.schedule[key] || ""} onChange={event => setConfig(value => ({ ...value, schedule: { ...value.schedule, [key]: event.target.value } }))} />)}</Stack>}
        {config.schedule.cycle === "weekly" && <Stack direction="row" sx={{ flexWrap: "wrap" }}>{[1, 2, 3, 4, 5, 6, 7].map(day => <FormControlLabel key={day} label={`周${"一二三四五六日"[day - 1]}`} control={<Checkbox checked={config.schedule.weekdays.includes(day)} onChange={(_, checked) => setConfig(value => ({ ...value, schedule: { ...value.schedule, weekdays: checked ? [...value.schedule.weekdays, day] : value.schedule.weekdays.filter(value => value !== day) } }))} />} />)}</Stack>}
        <Typography sx={{ fontWeight: 700 }}>执行时间段（时区：{config.schedule.timezone}）</Typography>
        {windows.map(window => <Stack direction={{ xs: "column", sm: "row" }} spacing={1} key={window.id}>{(["start", "end"] as const).map(key => <TextField key={key} label={key === "start" ? "开始时间" : "结束时间"} placeholder="09:00" value={window[key]} onChange={event => setWindows(values => values.map(value => value.id === window.id ? { ...value, [key]: event.target.value } : value))} />)}<Button color="error" onClick={() => setWindows(values => values.filter(value => value.id !== window.id))}>删除时间段</Button></Stack>)}
        <Button onClick={() => setWindows(values => [...values, { id: crypto.randomUUID(), start: "09:00", end: "12:00" }])}>添加时间段</Button>
        <Typography sx={{ fontWeight: 700 }}>岗位执行顺序</Typography>
        {config.items.map((item, index) => { const position = positions.find(value => value.id === item.position_id); return <SectionPanel key={item.id}><Stack spacing={1}>
          <Typography>第 {index + 1} 项</Typography>
          <Typography variant="caption" color="text.secondary" sx={{ wordBreak: "break-all" }}>执行项编号：{item.id}</Typography>
          <TextField select label="招聘岗位" value={item.position_id} onChange={event => setConfig(value => ({ ...value, items: value.items.map(entry => entry.id === item.id ? { ...entry, position_id: event.target.value, actions: ["greeting"], prioritize_reply: false } : entry) }))}>{positions.map(position => <MenuItem value={position.id} key={position.id}>{position.name}</MenuItem>)}</TextField>
          <Stack direction="row" sx={{ flexWrap: "wrap" }}>{(Object.keys(PLAN_ACTION_LABELS) as PlanAction[]).map(action => <FormControlLabel key={action} label={PLAN_ACTION_LABELS[action]} control={<Checkbox checked={item.actions.includes(action)} disabled={action !== "greeting" && !item.actions.includes(action) && (position?.platform_id !== "boss" || !canUseAutoReply(subscription))} onChange={(_, checked) => setConfig(value => ({ ...value, items: value.items.map(entry => entry.id === item.id ? { ...entry, actions: checked ? [...entry.actions, action] : entry.actions.filter(value => value !== action), prioritize_reply: action === "auto_reply" && !checked ? false : entry.prioritize_reply } : entry) }))} />} />)}</Stack>
          {item.actions.includes("auto_reply") && <FormControlLabel label="优先回复新消息" control={<Checkbox checked={item.prioritize_reply} onChange={(_, checked) => setConfig(value => ({ ...value, items: value.items.map(entry => entry.id === item.id ? { ...entry, prioritize_reply: checked } : entry) }))} />} />}
          {item.actions.includes("greeting") && <Typography color="text.secondary">打招呼按原岗位上限执行：{position?.match_limit ?? "原岗位配置"}</Typography>}
          <Typography color="text.secondary">回复和复打目前仅支持 BOSS，且需要对应会员权限。</Typography>
          <Stack direction="row"><Button disabled={!index} onClick={() => move(index, -1)}>上移</Button><Button disabled={index === config.items.length - 1} onClick={() => move(index, 1)}>下移</Button><Button color="error" onClick={() => setConfig(value => ({ ...value, items: value.items.filter(entry => entry.id !== item.id).map((entry, order) => ({ ...entry, order })) }))}>删除执行项</Button></Stack>
        </Stack></SectionPanel>; })}
        <Button onClick={() => setConfig(value => ({ ...value, items: [...value.items, { id: crypto.randomUUID(), position_id: positions[0]?.id || "", order: value.items.length, actions: ["greeting"], prioritize_reply: false }] }))}>添加岗位执行项</Button>
      </Stack>
    </AdminDialog>
    <AdminDialog open={!!report} title="执行报告" maxWidth="md" onClose={closeReport}><Stack spacing={2}>{reportRefreshError && <Alert severity="warning">{reportRefreshError}</Alert>}{report && <ExecutionPlanReport report={report} timezone={reportTimezone} positionNames={Object.fromEntries(positions.map(position => [position.id, position.name]))} />}</Stack></AdminDialog>
  </>;
}
