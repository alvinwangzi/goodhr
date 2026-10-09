/** 本文件验证 HRPlus 旧程序能力隔离、动作状态与待核对计数，不用页面启动定时任务。 */
import assert from "node:assert/strict";
import test from "node:test";
import { agentSupportsCooperativeActions, normalizeReGreetStats, normalizeActionDispatch, currentActionLabel, checkTimeLabel, terminalLocalTaskStatus, canControlPositionRun, localTaskMatchesRun, mergeCloudRunStatus } from "./single-position-actions.ts";

test("优先回复只接受明确能力标志，旧程序和字符串 true 均不假装支持",()=>{
 assert.equal(agentSupportsCooperativeActions({capabilities:{cooperative_actions:true}}),true);
 for(const health of [null,{version:"6"},{capabilities:{auto_reply:true}},{capabilities:{cooperative_actions:"true"}}])assert.equal(agentSupportsCooperativeActions(health),false);
});
test("复打未知结果单独显示，不增加成功次数",()=>{
 assert.deepEqual(normalizeReGreetStats({total:5,sent:1,unknown:2,failed:-1,skipped:Infinity}),{total:5,sent:1,unknown:2,failed:0,skipped:0});
});

test("本地结束事实保留成功、失败和用户停止，兼容嵌套岗位状态",()=>{
 for(const status of ["completed","failed","stopped"]){
  assert.equal(terminalLocalTaskStatus({status,running:false}),status);
  assert.equal(terminalLocalTaskStatus({position:{status}}),status);
 }
 assert.equal(terminalLocalTaskStatus({status:"failed",position:{status:"running"}}),"failed");
});

test("缺失状态、连接失败和仍在运行不能把停止按钮误改为开始",()=>{
 for(const task of [null,{}, {logs:[]}, {status:"running"}, {status:"unknown"}, {status:"failed",running:true}]){
  assert.equal(terminalLocalTaskStatus(task),null);
 }
});

test("A 执行 B 查看：设备和运行编号必须同时匹配，旧记录不覆盖当前任务",()=>{
 const position={active_run:{id:"run-current",machine_id:"A"}};
 assert.equal(canControlPositionRun(position,"A"),true);
 assert.equal(canControlPositionRun(position,"B"),false);
 assert.equal(canControlPositionRun(position,""),false);
 assert.equal(localTaskMatchesRun(position,"B",{status:"failed",cloud_run_id:"run-current"}),false);
 assert.equal(localTaskMatchesRun(position,"A",{cloud_run_id:"run-old"}),false);
 assert.equal(localTaskMatchesRun(position,"A",{cloud_run_id:"run-current"}),true);
 assert.equal(canControlPositionRun({},"A"),false);
});

test("云端迟到的 running 不覆盖同一运行已失败，但新运行和异机仍服从云端",()=>{
 const fresh={status:"running",active_run:{id:"current",machine_id:"A"}};
 const previous={status:"failed",active_run:{id:"current",machine_id:"A"}};
 assert.equal(mergeCloudRunStatus(fresh,previous,"A").status,"failed");
 assert.equal(mergeCloudRunStatus(fresh,previous,"B").status,"running");
 assert.equal(mergeCloudRunStatus({...fresh,active_run:{id:"new",machine_id:"A"}},previous,"A").status,"running");
});
test("动作状态缺少真实运行 ID 或检查时间时不编造运行事实",()=>{
 assert.equal(normalizeActionDispatch({current_action:"auto_reply"}),null);
 const result=normalizeActionDispatch({local_run_id:"run",current_action:"re_greet",last_message_check:"0001-01-01T00:00:00Z",next_message_check:"bad",prioritize_reply:true});
 assert.equal(result.lastMessageCheck,null);assert.equal(result.nextMessageCheck,null);assert.equal(result.prioritizeReply,true);
 assert.equal(currentActionLabel(result.currentAction),"自动复打招呼");assert.equal(checkTimeLabel(null),"尚未检查");
});
