/** 本文件在自建页面验证通用 Locator 协议、草稿保护和 contenteditable 输入，不访问招聘网站。 */
import assert from "node:assert/strict";
import http from "node:http";
import { readFileSync } from "node:fs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import * as locatorActions from "../src/locator-actions.js";
import { BrowserDownloadActions } from "../src/browser-actions.js";
import { before, after, test } from "node:test";
import { chromium } from "playwright-core";
import { executeLocatorAction, locatorActionHandler, createActionGate } from "../src/locator-actions.js";
import { readBrowserDisplayMetrics } from "../src/browser-display.js";

let browser, server, baseURL;
const fixture = `<!doctype html><html><body>
<section data-id="a"><b class="name">同名</b><span class="job">Go 工程师</span><textarea></textarea><button>发送</button></section>
<section data-id="b"><b class="name">同名</b><span class="job">Go 工程师</span><div contenteditable="true" role="textbox" style="width:200px;height:50px;border:1px solid"> </div><button>发送</button></section>
<iframe src="/frame"></iframe></body></html>`;

// resumeOfferFixture 复现用户提供的通知 DOM，链接仅用锚点记录点击结果，不注入脚本。
function resumeOfferFixture(mode) {
 const notice = `<div class="notice-list notice-blue-list"><div class="text">对方想发送附件简历给您，您是否同意</div> <div class="op"><a href="#refused">拒绝</a> <a href="#accepted" class="btn">同意</a></div></div>`;
 let activeNotice = notice;
 if (mode === "absent") activeNotice = "";
 if (mode === "hidden") activeNotice = `<div hidden>${notice}</div>`;
 if (mode === "hidden-button") activeNotice = notice.replace('class="btn"', 'class="btn" hidden');
 if (mode === "other-notice") activeNotice = notice.replace("对方想发送附件简历给您", "对方想交换联系方式");
 if (mode === "other-button") activeNotice = notice.replace('class="btn">同意', 'class="btn">不同意');
 if (mode === "duplicate") activeNotice = notice + notice;
 if (mode === "hidden-duplicate") activeNotice = `<div hidden>${notice}</div>${notice}`;
 return `<!doctype html><html><body>
 <aside>${notice.replace("#accepted", "#wrong-panel")}</aside>
 <section class="chat-conversation">
 <a class="btn resume-btn-online" href="#wrong-online">在线简历</a>
 <a class="btn resume-btn-file disabled" href="#wrong-file">附件简历</a>
 <div class="chat-message-list"><div class="text">对方想发送附件简历给您，您是否同意</div><a class="btn" href="#wrong-card">同意</a></div>
 ${activeNotice}</section></body></html>`;
}

// attachmentFixture 使用原生链接和 :target 复现全屏浮层；不向页面注入脚本。
function attachmentFixture(mode) {
 const icon = name => `<svg width="22" height="22"><use xlink:href="#icon-attacthment-${name}"></use></svg>`;
 const toolbar = `<div class="attachment-resume-btns">
 <a href="#wrong-fullscreen"><div class="popover icon-content">${icon("fullscreen")}</div></a>
 <a href="#wrong-print"><div class="popover icon-content">${icon("print")}</div></a>
 <a href="${mode === "no-download" ? "#attachment" : "/resume-file"}"><div class="popover icon-content">${icon("download")}</div></a></div>`;
 return `<!doctype html><html><head><style>
 #attachment{display:none;position:fixed;inset:0;background:white;z-index:10}
 #attachment:target{display:block}.attachment-resume-btns{display:flex;gap:20px}
 .popover,.close-btn{width:30px;height:30px}.close-btn{background:black}
 </style></head><body><section class="chat-conversation"><a href="#attachment" class="resume-btn-file">附件简历</a><div class="close-btn">其他关闭</div></section>
 <div hidden>${toolbar}</div><div id="attachment">${toolbar}
 <a href="#closed"><div data-v-12b9a7dc="" class="close-btn"><svg width="14" height="14"><path fill="rgb(255, 255, 255)"></path></svg></div></a></div></body></html>`;
}

// 创建独立本地测试服务器和无账号的浏览器，不使用持久化 Profile。
before(async () => {
 server = http.createServer((req, res) => {
  res.setHeader("Content-Type", "text/html; charset=utf-8");
  const url = new URL(req.url, "http://fixture.invalid");
  if (url.pathname === "/resume-offer") { res.end(resumeOfferFixture(url.searchParams.get("mode"))); return; }
  if (url.pathname === "/attachment") { res.end(attachmentFixture(url.searchParams.get("mode"))); return; }
  if (url.pathname === "/resume-file") {
   res.setHeader("Content-Type", "application/pdf");
   res.setHeader("Content-Disposition", 'attachment; filename="resume.pdf"');
   res.end("%PDF-test-resume"); return;
  }
  res.end(req.url === "/frame" ? '<span class="inside">框架内容</span>' : req.url === "/reordered" ? fixture.replace(/(<section data-id="a">[\s\S]*?<\/section>)\s*(<section data-id="b">[\s\S]*?<\/section>)/, "$2$1") : fixture);
 });
 await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
 baseURL = `http://127.0.0.1:${server.address().port}`;
 browser = await chromium.launch({ channel: "msedge", headless: true });
});
after(async () => { await browser?.close(); if (server) await new Promise(resolve => server.close(resolve)); });

// fixturePage 打开自建页面，测试结束时自动关闭。
async function fixturePage(t, path = "") {
 const page = await browser.newPage();
 t.after(() => page.close());
 await page.goto(`${baseURL}${path}`);
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

// pendingResumeSpec 使用实际内嵌配置验证定位结果，避免测试写死一份不随生产变化的选择器。
function pendingResumeSpec() {
 const config = JSON.parse(readFileSync(new URL("../../internal/platforms/boss/config.json", import.meta.url), "utf8"));
 assert.ok(config.pending_resume_accept?.selectors?.length, "缺少待接受简历按钮配置");
 return { ...config.pending_resume_accept, parent: config.active };
}

// 下载后必须得到保存结果而非仅点击成功，重复监听不能生成两份文件。
test("附件下载：全屏浮层定位、文件落地、关闭与单次保存", async t => {
 const page = await fixturePage(t, "/attachment");
 const config = JSON.parse(readFileSync(new URL("../../internal/platforms/boss/config.json", import.meta.url), "utf8"));
 assert.ok(config.attachment_download, "缺少附件下载配置");
 assert.ok(config.attachment_close, "缺少附件关闭配置");
 assert.equal(typeof locatorActions.createDownloadTracker, "function", "缺少点击后同步等待下载能力");
 const directory = await fs.mkdtemp(path.join(os.tmpdir(), "goodhr-download-test-"));
 t.after(() => fs.rm(directory, { recursive: true, force: true }));
 const saver = new BrowserDownloadActions({ log() {} }, { downloadsPath: directory });
 let saves = 0;
 const tracker = locatorActions.createDownloadTracker(async (download, targetPage, metadata) => {
  saves++;
  return { ...await saver.handleDownload(download, targetPage), ...metadata, status: "saved" };
 });
 page.on("download", d => { void tracker.capture(page, d).catch(() => {}); });
 const click = locatorActionHandler(() => page, () => { throw Error("不能回退旧协议"); }, "click", tracker);
 await click({ selector_spec: { ...config.resume_received, parent: config.active } });
 const result = await click({ selector_spec: config.attachment_download, download: { id: "test-receipt", source_key: "source-1", position_id: "p1", timeout_ms: 3000 } });
 assert.equal(result.download.status, "saved");
 assert.equal(result.download.id, "test-receipt");
 assert.equal(result.download.position_id, "p1");
 assert.equal(result.download.source_key, "source-1");
 assert.equal(await fs.readFile(result.download.file_path, "utf8"), "%PDF-test-resume");
 assert.equal(saves, 1);
 assert.equal((await fs.readdir(directory)).length, 1);
 await click({ selector_spec: config.attachment_close });
 assert.equal(new URL(page.url()).hash, "#closed");
 assert.equal(await page.locator(".attachment-resume-btns:visible").count(), 0);
});

// 超时或取消不能返回已下载，也不能留下监听把下次操作误关联到旧请求。
test("附件下载：未触发下载超时，不误报成功且清理监听", async t => {
 const page = await fixturePage(t, "/attachment?mode=no-download");
 assert.equal(typeof locatorActions.createDownloadTracker, "function");
 const tracker = locatorActions.createDownloadTracker(async () => { throw Error("不应保存"); });
 const click = locatorActionHandler(() => page, null, "click", tracker);
 const config = JSON.parse(readFileSync(new URL("../../internal/platforms/boss/config.json", import.meta.url), "utf8"));
 await click({ selector_spec: { ...config.resume_received, parent: config.active } });
 await assert.rejects(click({ selector_spec: config.attachment_download, download: { id: "timeout", timeout_ms: 300 } }), /超时/);
 assert.equal(page.listenerCount("download"), 0);
 const controller = new AbortController(); controller.abort();
 await assert.rejects(click({ selector_spec: config.attachment_download, download: { id: "cancel" } }, controller.signal), /abort/i);
 assert.equal(page.listenerCount("download"), 0);
});

// 捕获事件不等于保存完成，保存期间的停止信号必须及时结束等待。
test("附件下载：文件保存期间取消，仍退出等待并清理监听", async t => {
 const page = await fixturePage(t, "/attachment");
 const config = JSON.parse(readFileSync(new URL("../../internal/platforms/boss/config.json", import.meta.url), "utf8"));
 let started, finish;
 const saving = new Promise(resolve => { started = resolve; });
 const tracker = locatorActions.createDownloadTracker(() => { started(); return new Promise(resolve => { finish = resolve; }); });
 const click = locatorActionHandler(() => page, null, "click", tracker);
 await click({ selector_spec: { ...config.resume_received, parent: config.active } });
 const controller = new AbortController();
 const action = click({ selector_spec: config.attachment_download, download: { id: "abort-saving", timeout_ms: 3000 } }, controller.signal);
 const rejected = assert.rejects(action, /abort/i);
 await saving;
 controller.abort();
 await rejected;
 assert.equal(page.listenerCount("download"), 0);
 const blocked = await click({ selector_spec: config.attachment_download, download: { id: "second-request", timeout_ms: 300 } });
 assert.equal(blocked.clicked, false);
 assert.equal(blocked.download.status, "failed");
 finish({ status: "saved" });
});

// 等待额度在移动时耗尽也不能继续点击；否则调用方以为超时后仍可能产生文件。
test("附件下载：移动期间超时不会继续点击", async t => {
 const page = await fixturePage(t, "/attachment?mode=no-download");
 const tracker = locatorActions.createDownloadTracker(async () => { throw Error("不应下载"); });
 const click = locatorActionHandler(() => page, null, "click", tracker);
 const move = page.mouse.move.bind(page.mouse);
 page.mouse.move = async (...args) => { await move(...args); await new Promise(resolve => setTimeout(resolve, 200)); };
 await assert.rejects(click({ selector_spec: { selectors: [".resume-btn-file"] }, download: { id: "timeout-before-click", timeout_ms: 100 } }), /超时/);
 assert.equal(new URL(page.url()).hash, "");
});

// 原故障的请求把过滤文本放在外层，无法排除在线简历和聊天卡片中的按钮。
test("待接受简历：旧的宽泛按钮请求触发唯一性保护", async t => {
 const page = await fixturePage(t, "/resume-offer");
 await assert.rejects(executeLocatorAction(page, "click", {
  selector_spec: { selectors: [".btn"], parent: { selectors: [".chat-conversation"] } }, text: "同意",
 }), /唯一/);
 assert.equal(new URL(page.url()).hash, "");
});

// 即使历史消息、其他区域和隐藏提示都含同名按钮，也只接受当前会话底部的简历请求。
for (const mode of ["pending", "hidden-duplicate"]) {
 test(`待接受简历：${mode} 只点击当前提示条的同意`, async t => {
  const page = await fixturePage(t, `/resume-offer?mode=${mode}`);
  const selector_spec = pendingResumeSpec();
  const found = await executeLocatorAction(page, "find-elements", { selector_spec });
  assert.equal(found.count, 1);
  assert.equal(found.items[0].text, "同意");
  const result = await executeLocatorAction(page, "click", { selector_spec });
  assert.equal(result.clicked, true);
  assert.equal(new URL(page.url()).hash, "#accepted");
 });
}

// 不把历史消息、隐藏提示、其他类型通知或不同意按钮当作当前可接受的简历。
for (const mode of ["absent", "hidden", "hidden-button", "other-notice", "other-button"]) {
 test(`待接受简历：${mode} 不命中也不误点`, async t => {
  const page = await fixturePage(t, `/resume-offer?mode=${mode}`);
  const selector_spec = pendingResumeSpec();
  const found = await executeLocatorAction(page, "find-elements", { selector_spec });
  assert.equal(found.count, 0);
  await assert.rejects(executeLocatorAction(page, "click", { selector_spec }), /唯一|不可见/);
  assert.equal(new URL(page.url()).hash, "");
 });
}

// 多个可见有效目标仍须拒绝点击，不能通过取第一个元素掩盖歧义。
test("待接受简历：重复可见通知保留唯一性保护", async t => {
 const page = await fixturePage(t, "/resume-offer?mode=duplicate");
 const selector_spec = pendingResumeSpec();
 const found = await executeLocatorAction(page, "find-elements", { selector_spec });
 assert.equal(found.count, 2);
 await assert.rejects(executeLocatorAction(page, "click", { selector_spec }), /唯一/);
 assert.equal(new URL(page.url()).hash, "");
});
