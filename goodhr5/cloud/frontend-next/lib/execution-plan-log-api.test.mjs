/** 本文件验证 HRPlus 日志回执原归属与只读请求，其他任务日志不能显示为当前执行项。 */
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";

// logClient 编译原接口模块，使用受控回执替代本地请求，不连接真实服务。
function logClient(result, inspect = () => {},cloud=false) {
  const source = readFileSync(new URL("./execution-plans.ts", import.meta.url), "utf8");
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  const exports = {};
  vm.runInNewContext(output, { exports, require(name) { assert.equal(name,"./admin-api"); return { getToken: () => "fixture-token", localRequest: async (...args) => { inspect(...args); return result; },cloudRequest:async (...args)=>{inspect(...args);return result;} }; } });
  return cloud?exports.readCloudPlanLogs:exports.readPlanProgressLogs;
}
test("原日志读取使用请求头，不发开始或停止，不接受另一任务回执", async () => {
  const response = { run_id:"run",item_run_id:"item",task_run_id:"task",logs:[{plan_run_id:"run",item_run_id:"item",task_run_id:"task"}],next_before:0,message:"原进度" };
  const read = logClient(response, (base,path,options) => { assert.equal(base,"http://fixture"); assert.match(path,/execution-plan-runs\/run\/items\/item\/logs\?/); assert.equal(options.headers.Authorization,"Bearer fixture-token"); assert.equal(options.method,undefined); });
  assert.equal(await read("http://fixture","run","item","task"),response);
  for (const invalid of [{...response,task_run_id:"other"},{...response,logs:[{plan_run_id:"run",item_run_id:"other",task_run_id:"task"}]},{...response,logs:undefined}]) await assert.rejects(logClient(invalid)("http://fixture","run","item","task"),/原执行项不一致/);
});

test("云端原日志可在未连接本地程序时只读，不接受其他任务回执",async()=>{
  const result={run_id:"run",item_run_id:"item",task_run_id:"task",logs:[],next_before:0,message:"已同步部分"};
  const read=logClient(result,(path,options)=>{assert.match(path,/^\/api\/execution-plan-runs\/run\/items\/item\/logs\?/);assert.equal(options,undefined);},true);
  assert.equal(await read("run","item","task"),result);
  await assert.rejects(logClient({...result,task_run_id:"other"},()=>{},true)("run","item","task"),/原执行项不一致/);
});
