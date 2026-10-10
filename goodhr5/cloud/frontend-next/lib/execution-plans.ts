/** 本文件定义 HRPlus 执行计划类型与统一接口，时间段和岗位顺序不依赖网页常开。 */
"use client";

import { cloudRequest, getToken, localRequest } from "./admin-api";

export type PlanAction = "greeting" | "auto_reply" | "re_greet";
export type PlanWindow = { order: number; start_minute: number; end_minute: number };
export type PlanItem = { id: string; position_id: string; order: number; actions: PlanAction[]; prioritize_reply: boolean };
export type PlanConfig = { name: string; schedule: { cycle: "once" | "daily" | "weekly"; timezone: string; once_date?: string; start_date?: string; end_date?: string; weekdays: number[]; windows: PlanWindow[] }; items: PlanItem[] };
export type ExecutionPlan = { id: string; machine_id: string; version: number; state_sequence: number; activation_id: string; state: string; stop_requested: boolean; config: PlanConfig };
export type PlanDevice = { machine_id: string; agent_version: string; last_seen_at: string };
export type ExecutionRun = { id: string; activation_id: string; execution_date: string; state: string; current_item: number; end_reason: string; items: { id: string; item_id: string; task_run_id?: string; state: string; actions: Record<string, { state: string; count: number; unknown_count: number }> }[] };
export const PLAN_ACTION_LABELS: Record<PlanAction, string> = { greeting: "打招呼", auto_reply: "自动回复", re_greet: "自动复打招呼" };

/** emptyPlanConfig 创建默认编排，同岗位可重复添加独立执行项。 */
export function emptyPlanConfig(): PlanConfig {
  return { name: "", schedule: { cycle: "daily", timezone: "Asia/Shanghai", weekdays: [1, 2, 3, 4, 5], windows: [{ order: 0, start_minute: 540, end_minute: 720 }, { order: 1, start_minute: 810, end_minute: 1200 }] }, items: [] };
}

/** timeText 将当天分钟转换为表单时间，不把 24:00 改成次日开始。 */
export function timeText(value: number) { return `${String(Math.floor(value / 60)).padStart(2, "0")}:${String(value % 60).padStart(2, "0")}`; }

/** timeMinute 读取当天时间，允许结束点为 24:00。 */
export function timeMinute(value: string) { if (!/^\d{2}:\d{2}$/.test(value)) return NaN; const [hour, minute] = value.split(":").map(Number); return hour <= 24 && minute < 60 && (hour < 24 || minute === 0) ? hour * 60 + minute : NaN; }

/** validatePlanConfig 对保存前的名义时间和动作做明确校验，服务端仍执行完整权限核对。 */
export function validatePlanConfig(config: PlanConfig): string {
  if (!config.name.trim()) return "请输入计划名称";
  if (config.schedule.cycle === "once" && !config.schedule.once_date) return "请选择一次性执行日期";
  if (config.schedule.cycle === "weekly" && !config.schedule.weekdays.length) return "至少选择一个执行星期";
  if (config.schedule.start_date && config.schedule.end_date && config.schedule.start_date > config.schedule.end_date) return "结束日期不能早于开始日期";
  if (!config.schedule.windows.length) return "至少添加一个时间段";
  const windows = [...config.schedule.windows].sort((a, b) => a.start_minute - b.start_minute);
  for (let index = 0; index < windows.length; index++) {
    const value = windows[index];
    if (!Number.isInteger(value.start_minute) || !Number.isInteger(value.end_minute) || value.start_minute < 0 || value.end_minute > 1440 || value.start_minute >= value.end_minute) return "时间段必须在一天内，开始早于结束";
    if (index && value.start_minute < windows[index - 1].end_minute) return "同一计划的时间段不能重叠";
  }
  if (!config.items.length) return "至少添加一个岗位执行项";
  if (config.items.some(item => !item.position_id || !item.actions.length)) return "每个执行项都需要岗位和动作";
  if (config.items.some(item => item.prioritize_reply && !item.actions.includes("auto_reply"))) return "优先回复需要勾选自动回复";
  return "";
}

/** listExecutionPlans 读取真实账号计划，不使用本地缓存作为启动许可。 */
export async function listExecutionPlans(): Promise<ExecutionPlan[]> { const result = await cloudRequest("/api/execution-plans"); return result.plans || []; }

/** saveExecutionPlan 提交完整独立项及原配置版本，停止收尾未确认时服务端拒绝。 */
export async function saveExecutionPlan(config: PlanConfig, machine: string, original?: ExecutionPlan) {
  const result = await cloudRequest("/api/execution-plans", { method: "POST", body: { ...(original ? { id: original.id } : {}), machine_id: machine, expected_version: original?.version || 0, config } }); return result.plan as ExecutionPlan;
}

/** executionPlanIntent 用新请求登记启用或停止，重复重试应复用调用方传入的原编号。 */
export async function executionPlanIntent(plan: ExecutionPlan, action: "arm" | "stop", requestID: string, mode = "scheduled") {
  const result = await cloudRequest(`/api/execution-plans/${plan.id}/${action}`, { method: "POST", body: { request_id: requestID, expected_version: plan.version, ...(action === "stop" ? { activation_id: plan.activation_id } : { start_mode: mode }) } }); return result.plan as ExecutionPlan;
}

/** startExecutionPlan 在已启用原批次中请求本地立即开始，HTTP 成功不等于已实际执行。 */
export async function startExecutionPlan(base: string, plan: ExecutionPlan) { return localRequest(base, `/api/v1/local/execution-plans/${plan.id}/start`, { method: "POST", body: { token: getToken(), expected_version: plan.version, activation_id: plan.activation_id } }); }

/** executionPlanRuns 读取原执行记录，刷新页面不发起新的开始命令。 */
export async function executionPlanRuns(id: string): Promise<ExecutionRun[]> { const result = await cloudRequest(`/api/execution-plans/${id}/runs`); return result.runs || []; }

/** readExecutionReport 读取云端已保存的原报告及通知状态。 */
export async function readExecutionReport(runID: string) { const result = await cloudRequest(`/api/execution-plan-runs/${runID}/report`); return result.report; }

/** canEditExecutionPlan 停止收尾确认后才开放编辑，不能只看按钮或本地连接状态。 */
export function canEditExecutionPlan(plan: ExecutionPlan) { return plan.state === "stopped" && !plan.stop_requested; }
