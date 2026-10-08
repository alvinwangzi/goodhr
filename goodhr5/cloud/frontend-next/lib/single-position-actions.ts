/** 本文件规范 HRPlus 单岗位协同动作能力与状态，前端只显示事实，不启动定时任务。 */

export type ReGreetStats = { total: number; sent: number; skipped: number; failed: number; unknown: number };
export type ActionDispatchStatus = { localRunID: string; messagesEnabled: boolean; currentAction: string; prioritizeReply: boolean; lastMessageCheck: string | null; nextMessageCheck: string | null; waitingForCheck: boolean };

/** agentSupportsCooperativeActions 只接受本地程序明确声明的能力，缺字段和旧版本均不推断支持。 */
export function agentSupportsCooperativeActions(health: unknown): boolean {
  return Boolean(health && typeof health === "object" && (health as { capabilities?: { cooperative_actions?: unknown } }).capabilities?.cooperative_actions === true);
}

/** nonNegativeCount 将未知数据转换成安全的非负整数。 */
function nonNegativeCount(value: unknown): number { const number = Number(value); return Number.isFinite(number) && number > 0 ? Math.floor(number) : 0; }

/** normalizeReGreetStats 保留成功、跳过、失败与待核对的区别，不把发送未知算成功。 */
export function normalizeReGreetStats(value: unknown): ReGreetStats {
  const source = value && typeof value === "object" ? value as Record<string, unknown> : {};
  return { total: nonNegativeCount(source.total), sent: nonNegativeCount(source.sent), skipped: nonNegativeCount(source.skipped), failed: nonNegativeCount(source.failed), unknown: nonNegativeCount(source.unknown) };
}

/** validCheckTime 拒绝空值、旧接口零时间和无法解析的时间，不编造检查记录。 */
function validCheckTime(value: unknown): string | null { return typeof value === "string" && !value.startsWith("0001-") && Number.isFinite(Date.parse(value)) ? value : null; }

/** normalizeActionDispatch 兼容旧状态接口，无真实运行编号时不伪造协同状态。 */
export function normalizeActionDispatch(value: unknown): ActionDispatchStatus | null {
  if (!value || typeof value !== "object") return null;
  const source = value as Record<string, unknown>;
  if (typeof source.local_run_id !== "string" || !source.local_run_id) return null;
  return { localRunID: source.local_run_id, messagesEnabled:source.message_actions_enabled===true, currentAction: String(source.current_action || ""), prioritizeReply: source.prioritize_reply === true, lastMessageCheck: validCheckTime(source.last_message_check), nextMessageCheck: validCheckTime(source.next_message_check), waitingForCheck: source.waiting_for_check === true };
}

/** currentActionLabel 将动作标识转换为用户可理解的文案。 */
export function currentActionLabel(value: string): string {
  return ({ greeting: "找简历与打招呼", auto_reply: "自动回复", re_greet: "自动复打招呼", candidate_info: "检查回复并索要信息", check_messages: "检查消息", done: "本次工作已结束" } as Record<string, string>)[value] || "准备执行";
}

/** checkTimeLabel 用招聘工作时区显示真实检查时间。 */
export function checkTimeLabel(value: string | null): string { return value ? new Intl.DateTimeFormat("zh-CN", { timeZone: "Asia/Shanghai", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(new Date(value)) : "尚未检查"; }
