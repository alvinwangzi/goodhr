/** 本文件负责验证岗位启动时仅对使用 AI 的岗位执行余额检查。 */

import assert from "node:assert/strict";
import test from "node:test";
import { evaluatePositionStartGuard, positionUsesAI, isVersionLower } from "./position-start-guard.ts";

test("非 AI 岗位跳过余额检查", () => {
  const failure = evaluatePositionStartGuard(null, "5.3.5", "5.3.5", false);
  assert.equal(failure, null);
});

test("AI 岗位余额不足时禁止启动", () => {
  const failure = evaluatePositionStartGuard({ balance: "0" }, "5.3.5", "5.3.5", true);
  assert.equal(failure?.code, "ai_balance_insufficient");
});

test("本地程序版本过低时提示刷新并触发更新提醒", () => {
  const failure = evaluatePositionStartGuard(null, "5.3.91", "5.3.94", false);
  assert.equal(failure?.code, "agent_version_outdated");
  assert.match(failure?.message || "", /请立即刷新浏览器或重启浏览器/);
  assert.match(failure?.message || "", /自动弹出更新提醒/);
});

test("基础筛选或详情筛选启用 AI 时识别为 AI 岗位", () => {
  assert.equal(positionUsesAI({ common_config: { mode_default: "ai", detail_mode: "ocr" } }), true);
  assert.equal(positionUsesAI({ common_config: { mode_default: "keyword", detail_mode: "ai" } }), true);
  assert.equal(positionUsesAI({ common_config: { mode_default: "keyword", detail_mode: "ocr" } }), false);
});

test("AI 岗位但用户已配置自定义 API 时跳过余额检查", () => {
  const failure = evaluatePositionStartGuard({ balance: "0" }, "5.3.5", "5.3.5", true, true);
  assert.equal(failure, null);
});

// 验证本次开发版升级的启动拦截与固定更新提醒共用相同版本比较结果。
test("开发版 0.1.2：旧版本被拦截，新版本和更高版本通过", () => {
  assert.equal(evaluatePositionStartGuard(null, "0.1.1", "0.1.2", false)?.code, "agent_version_outdated");
  assert.equal(isVersionLower("0.1.1", "0.1.2"), true);
  for (const version of ["0.1.2", "0.1.3"]) {
    assert.equal(evaluatePositionStartGuard(null, version, "0.1.2", false), null);
    assert.equal(isVersionLower(version, "0.1.2"), false);
  }
});
