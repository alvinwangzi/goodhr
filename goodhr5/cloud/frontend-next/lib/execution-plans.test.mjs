/** 本文件验证 HRPlus 计划表单的时间边界、重复岗位与停止编辑保护，不连接招聘页面。 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";

// loadPlanFunctions 使用仓库 TypeScript 编译器加载原模块，接口依赖被替换为禁止请求的测试边界。
function loadPlanFunctions() {
  const source = readFileSync(new URL("./execution-plans.ts", import.meta.url), "utf8");
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  const exports = {};
  vm.runInNewContext(compiled, { exports, require(name) {
    assert.equal(name, "./admin-api");
    return { cloudRequest() { throw new Error("配置校验不能发起请求"); }, localRequest() { throw new Error("配置校验不能启动任务"); } };
  } });
  return exports;
}
const { emptyPlanConfig, validatePlanConfig, timeMinute, timeText, canEditExecutionPlan } = loadPlanFunctions();

// validConfig 构造用户明确允许的重复岗位、不同动作和相邻时间段。
function validConfig() {
  const config = emptyPlanConfig();
  config.name = "测试计划";
  config.items = [{ id: "first", position_id: "same-job", order: 0, actions: ["auto_reply"], prioritize_reply: true }, { id: "second", position_id: "same-job", order: 1, actions: ["greeting"], prioritize_reply: false }];
  return config;
}

test("同岗位可以先回复再找简历，相邻时间段允许，重叠拒绝", () => {
  const config = validConfig();
  config.schedule.windows = [{ order: 0, start_minute: 540, end_minute: 720 }, { order: 1, start_minute: 720, end_minute: 1080 }];
  assert.equal(validatePlanConfig(config), "");
  config.schedule.windows[1].start_minute = 719;
  assert.match(validatePlanConfig(config), /不能重叠/);
});

test("半输入及超过当天范围不能保存，24:00 只允许作结束", () => {
  for (const value of ["09:", "24:01", "25:00", "09:60", "", "-1:30"]) assert.ok(Number.isNaN(timeMinute(value)));
  assert.equal(timeMinute("13:30"), 810);
  assert.equal(timeText(1440), "24:00");
  const config = validConfig();
  config.schedule.windows = [{ order: 0, start_minute: 1200, end_minute: 1440 }];
  assert.equal(validatePlanConfig(config), "");
  config.schedule.windows[0].start_minute = 1440;
  assert.notEqual(validatePlanConfig(config), "");
  config.schedule.windows[0].start_minute = NaN;
  assert.notEqual(validatePlanConfig(config), "");
});

test("停止请求和时段间歇不能开放编辑，必须云端确认停止", () => {
  for (const plan of [{ state: "enabled", stop_requested: false }, { state: "stopped", stop_requested: true }, { state: "waiting_window", stop_requested: false }]) assert.equal(canEditExecutionPlan(plan), false);
  assert.equal(canEditExecutionPlan({ state: "stopped", stop_requested: false }), true);
});
