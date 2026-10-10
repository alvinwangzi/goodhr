/** 本文件定义 HRPlus 原执行报告的显示契约，结果未知、同步等待与通知等待分别展示。 */
export type ReportCount = { state: string; confirmed: number; unknown: number; skipped: number; failed: number };
export type ReportItem = { id: string; item_id: string; task_run_id?: string; position_id: string; order: number; state: string; scanned: number; details_available: boolean; actions: Record<string, ReportCount>; information: Record<string, ReportCount> };
export type ExecutionReport = { run_id: string; body_hash: string; sync_state: string; notification_state: string; notification_error?: string; notification_recipient?: string; created_at: string; updated_at: string; summary: { schema_version: number; run_id: string; plan_id: string; activation_id: string; config_version: number; execution_date: string; plan_name: string; kind: string; run_state: string; end_reason: string; generated_at: string; finished_at?: string; next_nominal_at?: string; items: ReportItem[]; unfinished_item_ids: string[] } };

export const EXECUTION_STATE_LABELS: Record<string, string> = { pending: "等待执行", pending_check: "结果待核对", waiting_resource: "等待当前任务结束", starting: "正在准备", running: "执行中", draining: "正在收尾", waiting_window: "等待下一时段", completed: "完成", incomplete: "当天未完成", stopped: "已停止", blocked: "需要核对", failed: "失败", active: "已激活" };
export const INFORMATION_LABELS: Record<string, string> = { resume: "索要简历", phone: "索要电话", wechat: "索要微信" };

/** reportSyncText 同步等待不等同于动作结果未知。 */
export function reportSyncText(state: string) { return ({ confirmed: "已同步", pending: "待补传" } as Record<string, string>)[state] || "同步状态待核对"; }

/** reportNotificationText 不把发送中或回执不明称为已经通知。 */
export function reportNotificationText(state: string) { return ({ pending: "等待发送", sending: "发送中，结果尚未确认", sent: "已发送", unknown: "发送结果待核对", not_configured: "邮件未配置" } as Record<string, string>)[state] || "通知状态待核对"; }

/** reportReasonText 显示明确中文结论，未知工程代码不冒充已完成。 */
export function reportReasonText(reason: string) {
  return ({ plan_work_finished: "所选动作当前工作已完成", plan_window_ended: "执行时间段已结束", plan_execution_failed: "执行遇到异常，请查看原任务详情", plan_authority_or_stop: "计划已停止或登录状态发生变化", plan_window_closed_during_start: "准备过程中执行时段已结束" } as Record<string, string>)[reason] || (/[\u3400-\u9fff]/.test(reason) ? reason : "结束原因待核对");
}

/** reportTimeText 使用原运行时区，缺失或不支持的时间不能生成假的下次安排。 */
export function reportTimeText(value: string | undefined, timezone: string) {
  if (!value) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间待核对";
  try { return new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(date); }
  catch { return "时间待核对"; }
}

/** reportUnfinishedText 根据原执行项运行编号显示顺序，不按重复岗位合并。 */
export function reportUnfinishedText(report: ExecutionReport) {
  if (!report.summary.unfinished_item_ids.length) return "无";
  return report.summary.unfinished_item_ids.map(id => { const item = report.summary.items.find(item => item.id === id); return item ? `第 ${item.order + 1} 项` : "执行项待核对"; }).join("、");
}

/** reportCountText 不把缺失、负值或未知计数转换成零或成功。 */
export function reportCountText(value: number) { return Number.isSafeInteger(value) && value >= 0 ? String(value) : "未提供"; }

/** reportItemStatusText 工作结束与结果全部确认是不同事实。 */
export function reportItemStatusText(item: ReportItem) {
  const unknown = [...Object.values(item.actions), ...Object.values(item.information)].some(value => value.unknown > 0);
  return item.state === "completed" && unknown ? "工作已结束，仍有待核对结果" : EXECUTION_STATE_LABELS[item.state] || "状态待核对";
}
