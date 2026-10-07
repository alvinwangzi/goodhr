// 本文件用用户提供的确认框结构验证三项控件隔离，不访问招聘网站，不交换真实联系方式。
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { readFileSync } from "node:fs";
import { chromium } from "playwright-core";
import { executeLocatorAction } from "../src/locator-actions.js";

const config = JSON.parse(readFileSync(new URL("../../internal/platforms/boss/config.json", import.meta.url), "utf8"));
let browser, server, baseURL;

// fixture 用原生锚点展示确认框，用原生导航记录确认结果，不向页面注入脚本。
function fixture(mode = "") {
  const entry = (action, label, question) => `<div class="operate-icon-item${mode === "disabled" && action === "phone" ? " disabled" : ""}">
  <span class="operate-btn"><a href="#${action}">${label}</a></span><span class="chat-tooltip-custom">${label}</span>
  <div id="${action}" class="exchange-tooltip"><span class="text">${action === "phone" ? `<span class="phone-tip"> ${question}</span>` : question}</span>
  <div class="btn-box"><span class="boss-btn-outline boss-btn"><a href="#cancelled">取消</a></span><span class="boss-btn-primary boss-btn"><a href="/completed?kind=${action}">确定</a></span></div></div></div>`;
  const outside = `<div class="exchange-tooltip" style="display:block"><span class="text">确定与对方交换手机吗？</span><div class="btn-box"><span class="boss-btn-primary boss-btn"><a href="/wrong">确定</a></span></div></div>`;
  return `<html><head><style>.exchange-tooltip{display:none}.exchange-tooltip:target{display:block}a{display:inline-block;padding:10px}</style></head><body>${outside}
  <section class="chat-conversation"><span class="base-name">测试候选人</span><div class="toolbar-box-right"><div class="operate-exchange-left">
  ${entry("phone", "换电话", "确定与对方交换手机吗？")}${entry("wechat", "换微信", "确定与对方交换微信吗？")}
  </div></div></section></body></html>`;
}

// before 使用系统已有 Edge 和临时页面服务，不下载浏览器。
before(async () => {
  server = http.createServer((req, res) => { res.setHeader("Content-Type", "text/html; charset=utf-8"); res.end(fixture(req.url.includes("disabled") ? "disabled" : "")); });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  baseURL = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch({ channel: "msedge", headless: true });
});

// after 只关闭本测试创建的浏览器和服务。
after(async () => { await browser?.close(); if (server) await new Promise(resolve => server.close(resolve)); });

for (const action of ["phone", "wechat"]) {
  test(`${action} 只操作当前面板的对应入口和对应确认框`, async () => {
    const page = await browser.newPage();
    try {
      await page.goto(baseURL);
      const settings = config.contact_requests[action];
      const scoped = selector => ({ ...selector, parent: config.active });
      await executeLocatorAction(page, "click", { selector_spec: scoped(settings.button) });
      assert.ok(page.url().endsWith("#" + action));
      await executeLocatorAction(page, "click", { selector_spec: scoped(settings.confirm) });
      assert.equal(page.url(), baseURL + "/completed?kind=" + action);
    } finally { await page.close(); }
  });
}

test("取消当前动作不点击其他确认，禁用入口不能点击", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(baseURL);
    const settings = config.contact_requests.phone;
    const scoped = selector => ({ ...selector, parent: config.active });
    await executeLocatorAction(page, "click", { selector_spec: scoped(settings.button) });
    await executeLocatorAction(page, "click", { selector_spec: scoped(settings.cancel) });
    assert.ok(page.url().endsWith("#cancelled"));
    await page.goto(baseURL + "/disabled");
    await assert.rejects(() => executeLocatorAction(page, "click", { selector_spec: scoped(settings.button) }));
    assert.equal(page.url(), baseURL + "/disabled");
  } finally { await page.close(); }
});
