/** 本文件在自建页面验证通用 Locator 协议、草稿保护和 contenteditable 输入，不访问招聘网站。 */
import assert from "node:assert/strict";
import http from "node:http";
import { before, after, test } from "node:test";
import { chromium } from "playwright-core";
import { executeLocatorAction, locatorActionHandler, createActionGate } from "../src/locator-actions.js";
import { readBrowserDisplayMetrics } from "../src/browser-display.js";

let browser, server, baseURL;
const fixture = `<!doctype html><html><body>
<section data-id="a"><b class="name">同名</b><span class="job">Go 工程师</span><textarea></textarea><button>发送</button></section>
<section data-id="b"><b class="name">同名</b><span class="job">Go 工程师</span><div contenteditable="true" role="textbox" style="width:200px;height:50px;border:1px solid"> </div><button>发送</button></section>
<iframe src="/frame"></iframe></body></html>`;

// 创建独立本地测试服务器和无账号的浏览器，不使用持久化 Profile。
before(async () => {
 server = http.createServer((req, res) => { res.setHeader("Content-Type", "text/html; charset=utf-8"); res.end(req.url === "/frame" ? '<span class="inside">框架内容</span>' : req.url === "/reordered" ? fixture.replace(/(<section data-id="a">[\s\S]*?<\/section>)\s*(<section data-id="b">[\s\S]*?<\/section>)/, "$2$1") : fixture); });
 await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
 baseURL = `http://127.0.0.1:${server.address().port}`;
 browser = await chromium.launch({ channel: "msedge", headless: true });
});
after(async () => { await browser?.close(); if (server) await new Promise(resolve => server.close(resolve)); });

// fixturePage 打开自建页面，测试结束时自动关闭。
async function fixturePage(t) {
 const page = await browser.newPage();
 t.after(() => page.close());
 await page.goto(baseURL);
 return page;
}

// 读取属性和字段必须基于稳定身份，而不是同名列表下标。
test("稳定属性、父级和数组选择器定位，列表重排后身份不变", async t => {
 const page = await fixturePage(t);
 const spec = { selectors: ["section"], attributes: { "data-id": "b" } };
 const fields = { id: { attribute: "data-id" }, name: { selector: { selectors: [".missing", ".name"] } } };
 const result = await executeLocatorAction(page, "find-elements", { selector_spec: spec, fields });
 assert.deepEqual(result.items.map(item => item.fields), [{ id: "b", name: "同名" }]);
 const nested = await executeLocatorAction(page, "extract-text", { selector_spec: { selectors: [".job"], parent: spec, text: "Go 工程师" } });
 assert.equal(nested.text, "Go 工程师");
 await page.goto(`${baseURL}/reordered`);
 const again = await executeLocatorAction(page, "find-elements", { selector_spec: spec, fields });
 assert.equal(again.items[0].fields.id, "b");
});

test("frame 和序号约束只读取指定目标", async t => {
 const page = await fixturePage(t);
 const frame = await executeLocatorAction(page, "extract-text", { selector_spec: { frame: "iframe", selectors: [".inside"] } });
 assert.equal(frame.text, "框架内容");
 const result = await executeLocatorAction(page, "find-elements", { selector_spec: { selectors: ["section"], nth: 1 }, fields: { id: { attribute: "data-id" } } });
 assert.equal(result.items[0].fields.id, "b");
});

for (const [name, id, selector] of [["textarea", "a", "textarea"], ["contenteditable", "b", "[contenteditable]"]]) {
 test(`${name} 使用标准键盘输入并验证正文`, async t => {
  const page = await fixturePage(t);
  const result = await executeLocatorAction(page, "type", { selector_spec: { selectors: [selector], parent: { selectors: ["section"], attributes: { "data-id": id } } }, text: "你好。", delay_min_ms: 0, delay_max_ms: 0, typing_delay_ms: 0 });
  assert.equal(result.verified, true);
  assert.equal(result.value.trim(), "你好。");
 });
}

test("人工草稿不会被覆盖，多义按钮不会点击", async t => {
 const page = await fixturePage(t);
 await page.locator("textarea").fill("人工草稿");
 await assert.rejects(executeLocatorAction(page, "type", { selector_spec: { selectors: ["textarea"] }, text: "AI 内容" }), /草稿/);
 assert.equal(await page.locator("textarea").inputValue(), "人工草稿");
 await assert.rejects(executeLocatorAction(page, "click", { selector_spec: { selectors: ["button"] } }), /唯一/);
});

test("同一通用入口兼容旧协议，新协议不经过旧查找器", async t => {
 const page = await fixturePage(t);
 const handler = locatorActionHandler(() => page, async () => ({ legacy: true }), "extract-text");
 assert.deepEqual(await handler({ element: { selector: "textarea" } }), { legacy: true });
 const result = await handler({ selector_spec: { selectors: [".inside"], frame: "iframe" } });
 assert.equal(result.text, "框架内容");
 await page.close();
 await assert.rejects(handler({ selector_spec: { selectors: ["textarea"] } }), /关闭/);
});

// 严格模式读取启动显示参数时不得访问页面脚本能力；原生窗口尺寸未知时不编造值。
test("严格模式的启动显示读取不注入脚本", async () => {
 const page = { viewportSize: () => ({ width: 900, height: 600 }), evaluate: () => { throw new Error("脚本注入"); } };
 assert.deepEqual(await readBrowserDisplayMetrics(page, { no_script: true }), { inner_width: 900, inner_height: 600, source: "playwright-viewport" });
 page.viewportSize = () => null;
 assert.deepEqual(await readBrowserDisplayMetrics(page, { no_script: true }), { inner_width: 0, inner_height: 0, source: "unknown" });
});

// 停止必须在鼠标移动之后、实际点击之前再次检查。
test("移动期间取消后不点击、不输入", async t => {
 const page = await fixturePage(t);
 const controller = new AbortController();
 const move = page.mouse.move.bind(page.mouse);
 page.mouse.move = async (...args) => { await move(...args); controller.abort(); };
 await assert.rejects(executeLocatorAction(page, "type", { selector_spec: { selectors: ["textarea"] }, text: "不应输入" }, controller.signal), /abort/i);
 assert.equal(await page.locator("textarea").inputValue(), "");
});

// 断开连接不等于底层动作已结束，后续请求必须等当前动作真正退出。
test("取消中的动作退出之前仍拒绝新动作", async () => {
 const gate = createActionGate();
 let finish;
 const active = gate(() => new Promise(resolve => { finish = resolve; }));
 await assert.rejects(gate(async () => "不应执行"), /占用/);
 finish();
 await active;
 assert.equal(await gate(async () => "已释放"), "已释放");
});

// 每次分段输入前检查取消，换行通过文本插入而不是 Enter 按键。
test("分段输入收到取消立即停止后续文字", async t => {
 const page = await fixturePage(t);
 const controller = new AbortController();
 const insert = page.keyboard.insertText.bind(page.keyboard);
 page.keyboard.insertText = async text => { await insert(text); controller.abort(); };
 await assert.rejects(executeLocatorAction(page, "type", { selector_spec: { selectors: ["textarea"] }, text: "甲乙丙", chunk_min: 1, chunk_max: 1, delay_min_ms: 0, delay_max_ms: 0 }, controller.signal), /abort/i);
 assert.equal(await page.locator("textarea").inputValue(), "甲");
});

// 保留旧协议内部允许的并发，严格协议仍与全部页面动作互斥。
test("旧协议并发不被改变，严格动作与旧动作互斥", async () => {
 const gate = createActionGate();
 let finish;
 const active = gate(() => new Promise(resolve => { finish = resolve; }), false);
 assert.equal(await gate(async () => "旧动作", false), "旧动作");
 await assert.rejects(gate(async () => "严格动作", true), /占用/);
 finish(); await active;
});

// 人工换到另一个输入框时，后续字符不能落到新的目标里。
test("输入过程中人工切换焦点后停止", async t => {
 const page = await fixturePage(t);
 const insert = page.keyboard.insertText.bind(page.keyboard);
 page.keyboard.insertText = async text => { await insert(text); await page.locator("[contenteditable]").focus(); };
 await assert.rejects(executeLocatorAction(page, "type", { selector_spec: { selectors: ["textarea"] }, text: "甲乙", chunk_min: 1, chunk_max: 1, delay_min_ms: 0, delay_max_ms: 0 }), /焦点/);
 assert.equal((await page.locator("[contenteditable]").innerText()).trim(), "");
});

test("空选择器和 HTML 读取请求被拒绝", async t => {
 const page = await fixturePage(t);
 await assert.rejects(executeLocatorAction(page, "find-elements", { selector_spec: {} }), /选择器/);
 await assert.rejects(executeLocatorAction(page, "find-elements", { selector_spec: { selectors: ["section"] }, include_html: true }), /HTML/);
});
