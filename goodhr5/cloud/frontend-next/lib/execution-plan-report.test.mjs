/** 本文件验证 HRPlus 报告中的未知结果、同步等待、时区和重复岗位归属，不请求真实服务。 */
import assert from "node:assert/strict";
import test from "node:test";
import { reportCountText, reportItemStatusText, reportNotificationText, reportReasonText, reportSyncText, reportTimeText, reportUnfinishedText } from "./execution-plan-report.ts";

test("待补传、通知回执不明和动作未知是不同结论", () => {
  assert.equal(reportSyncText("pending"), "待补传");
  assert.equal(reportNotificationText("unknown"), "发送结果待核对");
  assert.equal(reportNotificationText("sending"), "发送中，结果尚未确认");
  assert.equal(reportNotificationText("not_configured"), "邮件未配置");
  assert.equal(reportNotificationText("future-state"), "通知状态待核对");
});
test("无明细或非法计数不显示零成功", () => {
  for (const value of [undefined, NaN, -1, Infinity, 1.5]) assert.equal(reportCountText(value), "未提供");
  assert.equal(reportCountText(0), "0"); assert.equal(reportCountText(3), "3");
});
test("重复岗位未完成项按独立运行编号定位，不按岗位合并", () => {
  const report = { summary: { items: [{ id: "first", position_id: "same", order: 0 }, { id: "second", position_id: "same", order: 1 }], unfinished_item_ids: ["second"] } };
  assert.equal(reportUnfinishedText(report), "第 2 项");
  report.summary.unfinished_item_ids = ["missing"];
  assert.equal(reportUnfinishedText(report), "执行项待核对");
});
test("原运行时区决定展示时间，缺失和坏时间不虚构安排", () => {
  assert.match(reportTimeText("2026-10-11T01:00:00Z", "Asia/Shanghai"), /09:00:00/);
  assert.match(reportTimeText("2026-10-11T01:00:00Z", "UTC"), /01:00:00/);
  assert.equal(reportTimeText(undefined, "Asia/Shanghai"), "未记录");
  assert.equal(reportTimeText("bad", "Asia/Shanghai"), "时间待核对");
});
test("未知结束代码不冒充完成，明确原因显示中文", () => {
  assert.equal(reportReasonText("plan_window_ended"), "执行时间段已结束");
  assert.equal(reportReasonText("future-code"), "结束原因待核对");
});
test("工作已结束仍有未知结果时不显示全部完成", () => {
  assert.equal(reportItemStatusText({ state: "completed", actions: { auto_reply: { unknown: 1 } }, information: {} }), "工作已结束，仍有待核对结果");
  assert.equal(reportItemStatusText({ state: "completed", actions: { auto_reply: { unknown: 0 } }, information: {} }), "完成");
});
