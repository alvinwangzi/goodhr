/** 本文件订阅 HRPlus 云端计划变更，使用请求头认证、断线重读和退出取消，不发送任务命令。 */
"use client";

import { CLOUD_API_BASE, getToken } from "./admin-api";

/** readPlanEventStream 处理跨分片及 CRLF 的 SSE，保活不刷新，变更后只调用事实读取。 */
export async function readPlanEventStream(response: Response, changed: () => Promise<void>, signal: AbortSignal) {
  if (!response.body) throw new Error("状态订阅没有响应流");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let event = "";
  const cancel = () => { void reader.cancel().catch(() => {}); };
  signal.addEventListener("abort", cancel, { once: true });
  try {
    while (!signal.aborted) {
      const chunk = await reader.read();
      if (chunk.done) break;
      buffer += decoder.decode(chunk.value, { stream: true });
      if (buffer.length > 65536) throw new Error("状态订阅分片过大");
      let invalidate = false;
      let end: number;
      while ((end = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, end).replace(/\r$/, "");
        buffer = buffer.slice(end + 1);
        if (!line) { if (event === "ready" || event === "changed") invalidate = true; event = ""; }
        else if (line.startsWith("event:")) event = line.slice(6).trim();
      }
      if (invalidate && !signal.aborted) await changed();
    }
  } finally {
    signal.removeEventListener("abort", cancel);
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** eventReconnectDelay 等待网络重连，离开页面时立即结束等待并移除监听。 */
function eventReconnectDelay(delay: number, signal: AbortSignal) {
  return new Promise<void>(resolve => {
    const finish = () => { clearTimeout(timer); signal.removeEventListener("abort", finish); resolve(); };
    const timer = setTimeout(finish, delay);
    signal.addEventListener("abort", finish, { once: true });
    if (signal.aborted) finish();
  });
}

/** subscribeExecutionPlanEvents 首次订阅及重连后重读事实，401/403 停止重连，卸载取消旧会话。 */
export function subscribeExecutionPlanEvents(changed: () => Promise<void>, connection: (connected: boolean) => void) {
  const controller = new AbortController();
  const { signal } = controller;
  void (async () => {
    let delay = 1000;
    while (!signal.aborted) {
      try {
        const response = await fetch(`${CLOUD_API_BASE}/api/execution-plan-events`, { cache: "no-store", headers: { Authorization: `Bearer ${getToken()}` }, signal });
        if (signal.aborted) break;
        if (response.status === 401 || response.status === 403) { connection(false); break; }
        if (!response.ok || !response.headers.get("content-type")?.includes("text/event-stream")) throw new Error("状态订阅暂时不可用");
        connection(true);
        await readPlanEventStream(response, async () => { delay = 1000; if (!signal.aborted) await changed(); }, signal);
      } catch { /* 网络失败只触发重连，不把缓存改为已停止。 */ }
      if (signal.aborted) break;
      connection(false);
      await eventReconnectDelay(delay, signal);
      delay = Math.min(delay * 2, 30000);
    }
  })();
  return () => controller.abort();
}
