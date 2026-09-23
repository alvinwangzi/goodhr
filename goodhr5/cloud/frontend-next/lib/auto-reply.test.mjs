/** 本文件负责验证 AI 自动回复入口的平台支持、本地程序能力和回复统计判断。 */

import assert from "node:assert/strict";
import test from "node:test";
import {
  agentSupportsAutoReply,
  autoReplyEnabledForPlatform,
  mergeReplyPrompt,
  normalizeReplyStats,
  replyStatsText,
} from "./auto-reply.ts";

test("本地程序明确声明能力时才允许自动回复入口", () => {
  assert.equal(agentSupportsAutoReply({ capabilities: { auto_reply: true } }), true);
  assert.equal(agentSupportsAutoReply({ capabilities: { auto_reply: false } }), false);
  assert.equal(agentSupportsAutoReply({ version: "0.1.1" }), false);
  assert.equal(agentSupportsAutoReply(null), false);
});

test("首批只有 Boss 平台开放自动回复入口", () => {
  assert.equal(autoReplyEnabledForPlatform("boss"), true);
  assert.equal(autoReplyEnabledForPlatform("Boss"), true);
  assert.equal(autoReplyEnabledForPlatform("zhaopin"), false);
  assert.equal(autoReplyEnabledForPlatform(""), false);
});

test("回复统计转换为安全非负整数", () => {
  assert.deepEqual(
    normalizeReplyStats({ checked: 3, replied: 2, skipped: 1, failed: -5, unknown: "4" }),
    { checked: 3, replied: 2, skipped: 1, failed: 0, unknown: 4 },
  );
  assert.deepEqual(normalizeReplyStats(undefined), { checked: 0, replied: 0, skipped: 0, failed: 0, unknown: 0 });
  assert.deepEqual(normalizeReplyStats({ checked: 1.9 }), { checked: 1, replied: 0, skipped: 0, failed: 0, unknown: 0 });
});

test("回复统计展示文本包含全部分类", () => {
  const text = replyStatsText({ checked: 3, replied: 2, skipped: 1, failed: 0, unknown: 1 });
  assert.match(text, /检查 3/);
  assert.match(text, /回复 2/);
  assert.match(text, /未知 1/);
});

test("合并回复提示词时保留其他配置键", () => {
  const merged = mergeReplyPrompt(
    { position_requirement: "三年经验", review_prompt: "复核" },
    "  回复要简短  ",
  );
  assert.equal(merged.reply_prompt, "回复要简短");
  assert.equal(merged.position_requirement, "三年经验");
  assert.equal(merged.review_prompt, "复核");
  assert.deepEqual(mergeReplyPrompt(null, "规则"), { reply_prompt: "规则" });
  assert.deepEqual(mergeReplyPrompt({ reply_prompt: "旧" }, ""), { reply_prompt: "" });
});
