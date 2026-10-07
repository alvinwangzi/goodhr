// 本文件在无账号的本地页面验证 Boss 搜索的精确匹配、同名保护和面板切换，不访问招聘网站。
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { chromium } from "playwright-core";
import { searchBossChatSessionOnPage } from "../src/boss-chat-search.js";

let server, browser, baseURL;

// searchFixture 使用原生链接和静态文本模拟搜索条目，不执行页面脚本。
function searchFixture(duplicate) {
  const item = (name, id) => `<li><a style="display:block;padding:15px" href="/panel?id=${id}"><span>${name}</span><span>工程师</span></a></li>`;
  return `<html><body><div class="chat-job-search"><input class="search-input"></div><div class="geek-search-list"><ul>${item("张三丰", "wrong")}${item("张三", "correct")}${duplicate ? item("张三", "duplicate") : ""}</ul></div><div class="chat-conversation"><span class="base-name">旧会话</span></div></body></html>`;
}

// before 启动独立临时服务与浏览器，使用系统已有 Edge，不下载依赖或浏览器。
before(async () => {
  server = http.createServer((req, res) => {
    res.setHeader("Content-Type", "text/html; charset=utf-8");
    if (req.url.startsWith("/panel")) res.end('<div class="chat-conversation"><span class="base-name">张三</span></div>');
    else res.end(searchFixture(req.url.includes("duplicate")));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  baseURL = `http://127.0.0.1:${server.address().port}`;
  browser = await chromium.launch({ channel: "msedge", headless: true });
});

// after 清理本次测试创建的浏览器与临时服务。
after(async () => { await browser?.close(); if (server) await new Promise(resolve => server.close(resolve)); });

test("搜索必须精确匹配，并等到目标面板", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(baseURL);
    const result = await searchBossChatSessionOnPage(page, { candidate_name: "张三" });
    assert.equal(result.found, true);
    assert.equal(result.panel_name, "张三");
    assert.ok(page.url().includes("id=correct"));
  } finally { await page.close(); }
});

test("多个同名结果不允许点击", async () => {
  const page = await browser.newPage();
  try {
    await page.goto(baseURL + "/duplicate");
    const result = await searchBossChatSessionOnPage(page, { candidate_name: "张三" });
    assert.equal(result.found, false);
    assert.ok(result.error.includes("多个"));
    assert.equal(page.url(), baseURL + "/duplicate");
  } finally { await page.close(); }
});
