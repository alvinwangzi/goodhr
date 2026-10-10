/** 本文件验证 HRPlus 当前运行只读请求及迟到、错误归属回执的拒绝，不连接真实服务。 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";

/** runtimeReader 加载实际生产读取函数，接口替身只允许原只读路由。 */
function runtimeReader(payload) {
  const code = ts.transpileModule(readFileSync(new URL("./execution-plans.ts", import.meta.url), "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  const exports = {};
  vm.runInNewContext(code, { exports, require(name) { assert.equal(name,"./admin-api"); return { async cloudRequest(path, options) { assert.equal(path,"/api/execution-plans/original-plan/runtime"); assert.equal(options,undefined); return {runtime:payload}; }, localRequest() { throw new Error("读取运行不能发本地命令"); } }; } });
  return exports.readPlanRuntime;
}

/** validRuntime 保留原启用批次的当前运行及云端读取时刻。 */
function validRuntime() { return {plan:{id:"original-plan",activation_id:"original-activation"},runs:[{id:"original-run",activation_id:"original-activation"}],current_run:{id:"original-run",activation_id:"original-activation"},observed_at:"2026-10-10T02:00:00Z",wait_reason:"executing"}; }

test("运行详情只读原云端事实，不请求本地开始或停止",async()=>{
  const value=validRuntime(); assert.equal((await runtimeReader(value)("original-plan")).current_run.id,"original-run");
});

test("其他计划、其他批次、无原运行和损坏时间不能更新当前运行详情",async()=>{
  for (const change of [value=>{value.plan.id="other-plan";},value=>{value.current_run.activation_id="old-activation";},value=>{value.runs=[];},value=>{value.observed_at="broken-time";},value=>{value.nominal_at="broken-time";}]) {
    const value=validRuntime(); change(value); await assert.rejects(runtimeReader(value)("original-plan"),/原配置不一致/);
  }
});
