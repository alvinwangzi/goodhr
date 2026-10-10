/** 本文件提供 HRPlus M2 浏览器表单验收的隔离接口，只使用虚构账号、岗位和设备，不连接真实服务或招聘页面。 */
import http from "node:http";
import { randomUUID } from "node:crypto";

const email = "m2-qa@example.com";
const machine = "m2-test-computer-A";
const positions = [{ id: "20000000-0000-0000-0000-000000000001", name: "M2 Java测试岗位", platform_id: "boss", match_limit: 2, common_config: { position_name: "Java开发工程师" } }];
const plans = [];
const runs = new Map();
const reports = new Map();
const mutations = [];
const subscribers = new Set();
let starts = 0;
let supportsPlans = true;
let runtimeMode = "waiting_time";

/** changed 仅向隔离网页提示重读夹具状态，不会调度本地任务。 */
function changed() { for (const response of subscribers) response.write("event: changed\ndata: {}\n\n"); }

/** seedReport 创建已结束的独立项报告，数量和状态为虚构固定事实，供界面验收。 */
function seedReport() {
  const id=randomUUID(), activation_id=randomUUID(), runID=randomUUID();
  const config={name:"M2 报告验收",schedule:{cycle:"daily",timezone:"Asia/Shanghai",weekdays:[1,2,3,4,5],windows:[{order:0,start_minute:540,end_minute:720}]},items:[{id:randomUUID(),position_id:positions[0].id,order:0,actions:["greeting","auto_reply","re_greet"],prioritize_reply:true},{id:randomUUID(),position_id:positions[0].id,order:1,actions:["greeting"],prioritize_reply:false}]};
  const plan={id,machine_id:machine,version:1,state_sequence:1,activation_id,state:"enabled",stop_requested:false,config};
  plans.push(plan);
  const items=config.items.map((item,index)=>({id:randomUUID(),item_id:item.id,position_id:item.position_id,order:index,task_run_id:index===0?randomUUID():undefined,state:index===0?"completed":"pending",scanned:index===0?10:0,details_available:index===0,actions:Object.fromEntries(item.actions.map(action=>[action,{state:index===0?"completed":"pending",confirmed:index===0?3:0,unknown:action==="auto_reply"?1:0,skipped:index===0?2:0,failed:0}])),information:index===0?{resume:{state:"pending_check",confirmed:1,unknown:1,skipped:0,failed:0},phone:{state:"completed",confirmed:1,unknown:0,skipped:0,failed:0},wechat:{state:"pending_check",confirmed:0,unknown:1,skipped:0,failed:0}}:{}}));
  runs.set(id,[{id:runID,activation_id,execution_date:"2026-10-10",state:"incomplete",current_item:1,end_reason:"plan_window_ended",snapshot:config,items:items.map(item=>({...item,actions:Object.fromEntries(Object.entries(item.actions).map(([action,count])=>[action,{state:count.state,count:count.confirmed,unknown_count:count.unknown}]))}))}]);
  reports.set(runID,{run_id:runID,body_hash:"fixture-only-report",sync_state:"pending",notification_state:"unknown",notification_error:"夹具发送回执未确认",created_at:"2026-10-10T12:05:00Z",updated_at:"2026-10-10T12:05:00Z",summary:{schema_version:1,run_id:runID,plan_id:id,activation_id,config_version:1,execution_date:"2026-10-10",plan_name:config.name,kind:"day_incomplete",run_state:"incomplete",end_reason:"plan_window_ended",generated_at:"2026-10-10T12:05:00Z",finished_at:"2026-10-10T12:04:00Z",next_nominal_at:"2026-10-11T01:00:00Z",items,unfinished_item_ids:items.map(item=>item.id)}});
  changed(); return {plan_id:id,run_id:runID};
}

/** fixtureResult 提供表单与状态演示，所有开始只登记计数，没有执行器。 */
function fixtureResult(path, body, method) {
  if (path === "/__fixture/report-seed") return seedReport();
  if (path === "/__fixture/runtime-mode") { runtimeMode = body.mode; changed(); return {ok:true}; }
  if (path === "/__fixture/report-sync") { for (const report of reports.values()) { report.sync_state="confirmed"; report.notification_state="not_configured"; report.notification_error="夹具邮件未配置"; report.updated_at="2026-10-10T12:06:00Z"; } changed(); return {ok:true}; }
  const reportMatch=path.match(/^\/api\/execution-plan-runs\/([^/]+)\/report$/);
  if (reportMatch) return {report:reports.get(reportMatch[1])};
  if (path === "/__fixture/old-agent") { supportsPlans = false; return { ok: true }; }
  if (path === "/__fixture/new-agent") { supportsPlans = true; return { ok: true }; }
  if (path === "/__fixture/state") return { plans, runs: Object.fromEntries(runs), mutations, starts };
  if (path === "/__fixture/confirm-stop") { for (const plan of plans) if (plan.stop_requested) { plan.stop_requested = false; plan.state = "stopped"; plan.state_sequence++; } changed(); return { ok: true }; }
  if (path === "/health") return { ok: true, data: { version: "999.0.0", machine_id: machine, capabilities: { ...(supportsPlans ? { execution_plans: true, execution_plan_item_logs:true } : {}), cooperative_actions: true, auto_reply: true, re_greet: true } } };
  if (path.includes("login-status")) return { status: { has_password: true, is_locked: false } };
  if (path.includes("agreement-status")) return { agreement_accepted: true };
  if (path.includes("login-password")) return { access_token: "m2-fixture-only-token", user: { email } };
  if (path === "/api/auth/me") return { user: { email, name: "M2 隔离验收", is_admin: true }, show_trial_welcome: false };
  if (path === "/api/subscription/status") return { subscription: { active: true, member_type: "pro", member_name: "Pro会员", allow_ai: true, allow_auto_reply: true, remaining_days: 30, remaining_seconds: 2592000, features: [] } };
  if (path === "/api/positions") return { positions };
  if (path === "/api/agents/bindings") return { agents: [{ machine_id: machine, agent_version: "999.0.0", last_seen_at: "2026-10-10T09:00:00+08:00" }, { machine_id: "m2-test-computer-B", agent_version: "999.0.0", last_seen_at: "2026-10-09T09:00:00+08:00" }] };
  if (path === "/api/runtime/config") return { config: { local_agent: [{ version: "1.0.0", url_win: "http://127.0.0.1:26284/test-only.exe" }] } };
  if (path === "/api/ai-wallet") return { wallet: { balance_yuan: 100 } };
  if (path === "/api/v1/runtime/status") return { node_installed: true, cloakbrowser_installed: true, components: { node_runtime: { installed: true }, cloakbrowser: { installed: true } } };
  if (path === "/api/execution-plans" && method === "GET") return { plans };
  if (path === "/api/execution-plans" && method === "POST") {
    const old = plans.find(plan => plan.id === body.id);
    if (old && (old.state !== "stopped" || old.stop_requested)) return { ok: false, error: "计划尚未停止并完成收尾，暂不能编辑" };
    const plan = { id: old?.id || randomUUID(), machine_id: body.machine_id, version: (old?.version || 0) + 1, state_sequence: (old?.state_sequence || 0) + 1, activation_id: "", state: "stopped", stop_requested: false, config: body.config };
    if (old) plans.splice(plans.indexOf(old), 1, plan); else plans.push(plan);
    mutations.push({ action: "save", plan }); changed(); return { plan };
  }
  const match = path.match(/^\/api\/execution-plans\/([^/]+)\/(arm|stop|runs|runtime)$/);
  if (match) {
    const plan = plans.find(plan => plan.id === match[1]);
    if (!plan) return { ok: false, error: "夹具计划不存在" };
    if (match[2] === "runs") return { runs: runs.get(plan.id) || [] };
    if (match[2] === "runtime") {
      const history = runtimeMode === "queued" ? [] : runs.get(plan.id) || [];
      const current = history.find(run => run.activation_id === plan.activation_id);
      if (runtimeMode === "executing" && current) { current.state = "running"; current.current_item = 0; current.items[0].state = "running"; }
      const reason = plan.stop_requested ? "stopping" : plan.state === "stopped" ? "stopped" : runtimeMode;
      return {runtime:{plan,runs:history,observed_at:"2026-10-10T02:00:00Z",wait_reason:reason,...(current?{current_run:current}:{}),...(reason==="waiting_time"?{nominal_at:"2026-10-11T01:00:00Z"}:{}),...(["account_busy","queued"].includes(reason)?{nominal_at:"2026-10-10T01:00:00Z",account_owner:{owner_id:randomUUID(),owner_type:"manual",machine_id:"m2-test-computer-B",state:"draining"}}:{}),...(reason==="queued"?{waiting:{plan_id:plan.id,request_id:randomUUID(),activation_id:plan.activation_id,config_version:plan.version,machine_id:plan.machine_id,triggered_at:"2026-10-10T01:00:00Z",queued_at:"2026-10-10T01:55:00Z"},wait_seconds:300}:{})}};
    }
    if (match[2] === "arm") { plan.state = "enabled"; plan.activation_id = randomUUID(); }
    else plan.stop_requested = true;
    plan.state_sequence++; mutations.push({ action: match[2], request: body }); changed(); return { plan };
  }
  if (/^\/api\/v1\/local\/execution-plans\/[^/]+\/start$/.test(path)) { starts++; return { status: "waiting_time", message: "等待下个执行时间" }; }
  const logsMatch=path.match(/^\/api\/v1\/local\/execution-plan-runs\/([^/]+)\/items\/([^/]+)\/logs$/);
  if (logsMatch) { const report=reports.get(logsMatch[1]); const item=report?.summary.items.find(item=>item.id===logsMatch[2]); return {run_id:logsMatch[1],item_run_id:logsMatch[2],task_run_id:item?.task_run_id,logs:item?[{id:1,plan_run_id:logsMatch[1],item_run_id:item.id,task_run_id:item.task_run_id,level:"info",message:"夹具原执行项安全步骤返回，未知结果保留核对",created_at:"2026-10-10T12:03:00Z"}]:[],next_before:0,message:"夹具原执行项进度日志",local_only:true}; }
  return { ok: true, config: {}, invitations: [], data: {} };
}

/** handle 只允许回环监听，未知接口不转发，订阅取消时释放原响应。 */
async function handle(request, response) {
  response.setHeader("Access-Control-Allow-Origin", "http://127.0.0.1:26373");
  response.setHeader("Access-Control-Allow-Headers", "Content-Type,Authorization");
  response.setHeader("Access-Control-Allow-Methods", "GET,POST,OPTIONS");
  if (request.method === "OPTIONS") { response.end(); return; }
  const path = new URL(request.url, "http://fixture.invalid").pathname;
  if (path === "/api/execution-plan-events") {
    response.setHeader("Content-Type", "text/event-stream"); response.setHeader("Cache-Control", "no-cache");
    subscribers.add(response); response.write("event: ready\ndata: {}\n\n");
    response.on("close", () => subscribers.delete(response)); return;
  }
  let raw = "";
  for await (const chunk of request) { raw += chunk; if (raw.length > 1048576) { response.writeHead(413); response.end(); return; } }
  let body; try { body = JSON.parse(raw || "{}"); } catch { response.writeHead(400); response.end(); return; }
  response.setHeader("Content-Type", "application/json");
  response.end(JSON.stringify(fixtureResult(path, body, request.method)));
}
for (const port of [26284, 26329]) http.createServer(handle).listen(port, "127.0.0.1", () => process.stdout.write(`M2 UI fixture listening ${port}\n`));
