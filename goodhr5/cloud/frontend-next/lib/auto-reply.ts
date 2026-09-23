/** 本文件负责 AI 自动回复入口的平台支持、本地程序能力和回复统计判断。 */

export type ReplyStats = {
  checked: number;
  replied: number;
  skipped: number;
  failed: number;
  unknown: number;
};

/** agentSupportsAutoReply 判断本地程序是否声明了自动回复能力；老程序缺字段时视为不支持。 */
export function agentSupportsAutoReply(health: unknown) {
  const capabilities = (health as any)?.capabilities;
  return capabilities?.auto_reply === true;
}

/** autoReplyEnabledForPlatform 判断平台是否开放 AI 自动回复入口，首批仅 Boss。 */
export function autoReplyEnabledForPlatform(platformID: unknown) {
  return String(platformID || "").trim().toLowerCase() === "boss";
}

/** normalizeReplyStats 把本地任务返回的自动回复统计转换为安全的非负整数。 */
export function normalizeReplyStats(value: unknown): ReplyStats {
  const source =
    value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : {};
  const read = (key: string) => {
    const parsed = Number(source[key] || 0);
    return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
  };
  return {
    checked: read("checked"),
    replied: read("replied"),
    skipped: read("skipped"),
    failed: read("failed"),
    unknown: read("unknown"),
  };
}

/** replyStatsText 把回复统计转换为状态区展示文本。 */
export function replyStatsText(stats: ReplyStats) {
  return `检查 ${stats.checked} · 回复 ${stats.replied} · 跳过 ${stats.skipped} · 失败 ${stats.failed} · 未知 ${stats.unknown}`;
}

/** mergeReplyPrompt 把岗位回复提示词并入 ai_config，保留其他现有配置键。 */
export function mergeReplyPrompt(aiConfig: unknown, replyPrompt: string) {
  const source =
    aiConfig && typeof aiConfig === "object" && !Array.isArray(aiConfig)
      ? { ...(aiConfig as Record<string, unknown>) }
      : {};
  source.reply_prompt = String(replyPrompt || "").trim();
  return source;
}
