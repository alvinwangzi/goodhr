/** 本文件验证 HRPlus 状态订阅分片、保活、断线重连、撤销和卸载，不发起真实任务。 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";

// eventFunctions 编译原订阅模块，以受控请求替换云端及登录依赖。
function eventFunctions(fetcher) {
  const source = readFileSync(new URL("./execution-plan-events.ts", import.meta.url), "utf8");
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  const exports = {};
  vm.runInNewContext(output, { exports, TextDecoder, AbortController, setTimeout, clearTimeout, fetch: fetcher, require(name) {
    assert.equal(name, "./admin-api");
    return { CLOUD_API_BASE: "http://fixture", getToken: () => "test-only-token" };
  } });
  return exports;
}

// streamResponse 构造按真实网络分片提供的 SSE 响应。
function streamResponse(parts) {
  return new Response(new ReadableStream({ start(controller) { for (const part of parts) controller.enqueue(new TextEncoder().encode(part)); controller.close(); } }), { headers: { "content-type": "text/event-stream" } });
}

test("CRLF 和拆开的事件重读事实，保活及未知事件不重读", async () => {
  const { readPlanEventStream } = eventFunctions();
  let reads = 0;
  await readPlanEventStream(streamResponse(["event: re", "ady\r\ndata: {}\r", "\n\r\n", ": heartbeat\n\n", "event: ignored\ndata: {}\n\n", "event: changed\ndata:", " {}\n\n"]), async () => { reads++; }, new AbortController().signal);
  assert.equal(reads, 2);
});

test("流中多次变更合并，变化只调用读取，不解释数据中的启动命令", async () => {
  const { readPlanEventStream } = eventFunctions();
  let reads = 0;
  await readPlanEventStream(streamResponse(["event: changed\ndata: {\"start\":true}\n\nevent: changed\ndata: {}\n\n"]), async () => { reads++; }, new AbortController().signal);
  assert.equal(reads, 1);
});

test("卸载取消正在读取的流，迟到事件不覆盖已离开的页面", async () => {
  const { readPlanEventStream } = eventFunctions();
  const controller = new AbortController();
  let reads = 0, canceled = false;
  const response = new Response(new ReadableStream({ cancel() { canceled = true; } }));
  const reading = readPlanEventStream(response, async () => { reads++; }, controller.signal);
  controller.abort(); await reading;
  assert.equal(reads, 0); assert.equal(canceled, true);
});

test("认证放请求头，401 停止重连，不用查询串或发开始请求", async () => {
  let calls = 0;
  const { subscribeExecutionPlanEvents } = eventFunctions(async (url, options) => {
    calls++; assert.equal(url, "http://fixture/api/execution-plan-events");
    assert.equal(options.headers.Authorization, "Bearer test-only-token");
    assert.equal(options.method, undefined);
    return new Response("", { status: 401 });
  });
  let finish;
  const ended = new Promise(resolve => { finish = resolve; });
  const close = subscribeExecutionPlanEvents(async () => { assert.fail("认证失败不重读"); }, connected => { assert.equal(connected, false); finish(); });
  await ended; close(); assert.equal(calls, 1);
});

test("断线重连 ready 必须重新读取，离开后取消原连接", async () => {
  let calls = 0, reads = 0, finish;
  const loaded = new Promise(resolve => { finish = resolve; });
  const { subscribeExecutionPlanEvents } = eventFunctions(async () => { calls++; return streamResponse(["event: ready\ndata: {}\n\n"]); });
  const close = subscribeExecutionPlanEvents(async () => { reads++; if (reads === 2) finish(); }, () => {});
  try { await loaded; assert.equal(calls, 2); assert.equal(reads, 2); } finally { close(); }
});
