// 本文件验证响应成对 ID、冲突隔离与真实聊天行核对，全部使用虚构 ID，不连接 Boss。
import { EventEmitter } from "node:events";
import { test } from "node:test";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";
import { readBossResponseIdentityFacts, observeBossResponseIdentities, resolveBossResponseIdentity, readBossResponseAccountID, readBossObservedAccountIdentity } from "../src/boss-response-identity.js";
import { searchBossChatSessionOnPage } from "../src/boss-chat-search.js";
const recPath = "/wapi/zpjob/rec/geek/list";
const friendPath = "/wapi/zprelation/friend/getBossFriendListV2.json";
const accountPath = "/wapi/zpuser/wap/getUserInfo.json";

test("账号证明只读取用户 ID，不访问 token 或批量凭证节点", () => {
  const data = { userId: 901 };
  Object.defineProperty(data, "token", { get() { throw new Error("禁止读取凭证"); } });
  const account = { code: 0, zpData: data };
  const batchData = { [accountPath]: account };
  Object.defineProperty(batchData, "/wapi/zppassport/get/wt", { get() { throw new Error("禁止读取凭证节点"); } });
  assert.equal(readBossResponseAccountID(accountPath, account), "901");
  assert.equal(readBossResponseAccountID("/wapi/batch/requests", { code: 0, zpData: batchData }), "901");
  assert.equal(readBossResponseAccountID(accountPath, { code: 1, zpData: data }), "");
});

test("账号响应尚未结束时停止，立即退出等待且不使用旧证明", async () => {
  const page = fixturePage(); observeBossResponseIdentities(page);
  page.emit("response", { url: () => `https://www.zhipin.com${accountPath}`, json: () => new Promise(() => {}) });
  const controller = new AbortController();
  const reading = readBossObservedAccountIdentity(page, controller.signal);
  controller.abort();
  await assert.rejects(reading, error => error.name === "AbortError");
});

test("补取账号证明保留人工草稿；空草稿首次准备才可刷新", async () => {
  const browser = await chromium.launch({ channel: "msedge", headless: true });
  try {
    const page = await browser.newPage();
    let visits = 0;
    await page.route("https://www.zhipin.com/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === accountPath) await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { userId: 901 } }) });
      else {
        visits++;
        await route.fulfill({ contentType: "text/html; charset=utf-8", body: path.includes("draft") ? "<textarea>人工草稿</textarea>" : `<iframe src="${accountPath}"></iframe>` });
      }
    });
    await page.goto("https://www.zhipin.com/web/chat/draft");
    observeBossResponseIdentities(page);
    await assert.rejects(readBossObservedAccountIdentity(page, undefined, true), /尚未发送/);
    assert.equal(visits, 1);
    assert.equal(await page.locator("textarea").inputValue(), "人工草稿");
    // 新页面在观察器安装前已加载，模拟启动时复用已打开的浏览器。
    const empty = await browser.newPage();
    await empty.route("https://www.zhipin.com/**", async route => {
      const path = new URL(route.request().url()).pathname;
      await route.fulfill(path === accountPath ? { contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { userId: 902 } }) } : { contentType: "text/html", body: `<iframe src="${accountPath}"></iframe>` });
    });
    await empty.goto("https://www.zhipin.com/web/chat/index");
    observeBossResponseIdentities(empty);
    const proof = await readBossObservedAccountIdentity(empty, undefined, true);
    assert.equal(proof.account_id, "902");
  } finally { await browser.close(); }
});

test("缺失账号不猜测，同文档换账号清空候选人证明并保持拒绝状态", async () => {
  const page = fixturePage(); observeBossResponseIdentities(page);
  assert.deepEqual(await readBossObservedAccountIdentity(page), { verified: false });
  const account = uid => page.emit("response", { url: () => `https://www.zhipin.com${accountPath}`, json: async () => ({ code: 0, zpData: { userId: uid } }) });
  account(901); emitFriend(page);
  assert.equal((await readBossObservedAccountIdentity(page)).account_id, "901");
  assert.equal((await resolveBossResponseIdentity(page, "opaque-A")).verified, true);
  account(902); emitFriend(page);
  assert.deepEqual(await readBossObservedAccountIdentity(page), { verified: false });
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
  page.emit("request", { isNavigationRequest: () => true, frame: () => page.mainFrame() }); page.emit("framenavigated", page.mainFrame());
  assert.deepEqual(await readBossObservedAccountIdentity(page), { verified: false });
  account(902);
  assert.equal((await readBossObservedAccountIdentity(page)).account_id, "902");
});

/** fixturePage 构造标准事件与 Locator 形状，不提供脚本或凭证接口。 */
function fixturePage(ids = ["123-0"]) {
  const page = new EventEmitter();
  const frame = {};
  page.mainFrame = () => frame;
  page.locator = () => ({ all: async () => ids.map(id => ({ getAttribute: async () => id, isVisible: async () => true })) });
  return page;
}

/** emitFriend 发送虚构的已观测响应，模拟正常页面加载。 */
function emitFriend(page, encrypted = "opaque-A", uid = 123, host = "www.zhipin.com") {
  page.emit("response", { url: () => `https://${host}${friendPath}`, json: async () => ({ code: 0, zpData: { friendList: [{ uid, encryptUid: encrypted, name: "相同姓名", securityId: "must-not-return" }] } }) });
}

test("仅提取推荐记录内成对 ID，拒绝矛盾字段和精度丢失", () => {
  const body = { code: 0, zpData: { geekList: [
    { encryptGeekId: "opaque-A", geekCard: { geekId: 123, encGeekId: "opaque-A" } },
    { encryptGeekId: "opaque-B", geekCard: { geekId: 124, encGeekId: "different" } },
    { encryptGeekId: "opaque-C", geekCard: { geekId: Number.MAX_SAFE_INTEGER + 1 } },
  ] } };
  assert.deepEqual(readBossResponseIdentityFacts(recPath, body), [{ recommendation_id: "opaque-A", numeric_id: "123", source: "boss_recommend_response" }]);
  assert.deepEqual(readBossResponseIdentityFacts(recPath, { ...body, code: 1 }), []);
});

test("必须从可见行读取完整会话 ID，不猜后缀，也不按姓名合并", async () => {
  const page = fixturePage(["123-7", "124-0"]);
  observeBossResponseIdentities(page);
  emitFriend(page);
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: true, recommendation_id: "opaque-A", conversation_id: "123-7", source: "boss_friend_response_and_visible_chat_id" });
  assert.deepEqual(await resolveBossResponseIdentity(page, "legacy-name-age"), { verified: false });
});

test("冲突记录不覆盖，导航清空旧账号事实，其他来源不读取", async () => {
  const page = fixturePage();
  observeBossResponseIdentities(page);
  emitFriend(page); emitFriend(page, "opaque-A", 124);
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
  page.emit("request", { isNavigationRequest: () => true, frame: () => page.mainFrame() }); page.emit("framenavigated", page.mainFrame());
  emitFriend(page, "opaque-A", 123, "example.com");
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
  emitFriend(page);
  assert.equal((await resolveBossResponseIdentity(page, "opaque-A")).verified, true);
  page.emit("request", { isNavigationRequest: () => true, frame: () => page.mainFrame() }); page.emit("framenavigated", page.mainFrame());
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
});

test("多个行或不同会话后缀对应同一数字 ID 时拒绝映射", async () => {
  for (const ids of [["123-0", "123-0"], ["123-0", "123-1"], ["124-0"]]) {
    const page = fixturePage(ids); observeBossResponseIdentities(page); emitFriend(page);
    assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
  }
});

test("导航之后迟到的旧响应不能重新污染身份", async () => {
  const page = fixturePage(); observeBossResponseIdentities(page);
  let deliver;
  page.emit("response", { url: () => `https://www.zhipin.com${friendPath}`, json: () => new Promise(resolve => { deliver = resolve; }) });
  page.emit("request", { isNavigationRequest: () => true, frame: () => page.mainFrame() }); page.emit("framenavigated", page.mainFrame());
  deliver({ code: 0, zpData: { friendList: [{ uid: 123, encryptUid: "opaque-A" }] } });
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
});

test("系统 Edge 的真实响应事件与标准 Locator 完成联合核对，不连接业务页面", async () => {
  const browser = await chromium.launch({ channel: "msedge", headless: true });
  try {
    const page = await browser.newPage();
    await page.route("https://www.zhipin.com/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === accountPath) {
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { userId: 901, name: "同名招聘者" } }) });
      } else if (path === friendPath) {
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { friendList: [{ uid: 123, encryptUid: "opaque-A" }] } }) });
      } else {
        await route.fulfill({ contentType: "text/html; charset=utf-8", body: `<div data-id="123-9">虚构候选人</div><iframe src="${friendPath}"></iframe><iframe src="${accountPath}"></iframe>` });
      }
    });
    observeBossResponseIdentities(page);
    await page.goto("https://www.zhipin.com/fixture");
    const identity = await resolveBossResponseIdentity(page, "opaque-A");
    assert.equal(identity.verified, true);
    assert.equal(identity.conversation_id, "123-9");
    assert.deepEqual(await readBossObservedAccountIdentity(page), { verified: true, account_id: "901", source: "boss_user_info_response" });
  } finally { await browser.close(); }
});

test("未加载目标可逐个打开同名结果，详情响应和迟到的 selected 均须匹配", async () => {
  const browser = await chromium.launch({ channel: "msedge", headless: true });
  try {
    const page = await browser.newPage();
    const openedUIDs = [];
    let cancelController;
    let includeInitialIdentity = true;
    // 这是独立虚构页面自身的交互代码，不向招聘页面注入代码。
    await page.route("https://www.zhipin.com/**", async route => {
      const url = new URL(route.request().url());
      if (url.pathname === friendPath) {
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { friendList: includeInitialIdentity ? [{ uid: 123, encryptUid: "opaque-A" }] : [] } }) });
      } else if (url.pathname === "/wapi/zpjob/chat/geek/info") {
        const uid = Number(url.searchParams.get("uid"));
        openedUIDs.push(uid);
        cancelController?.abort();
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { data: { uid, encryptUid: uid === 123 ? "opaque-A" : "opaque-B" } } }) });
      } else {
        await route.fulfill({ contentType: "text/html; charset=utf-8", body: `<button class="chat-search-btn" onclick="document.querySelector('.chat-job-search').hidden=false;document.querySelector('.geek-search-list').hidden=false">搜索</button><div class="chat-job-search"><input class="search-input" oninput="document.querySelector('.geek-search-list').hidden=false"></div>
          <div class="geek-search-list"><ul><li onclick="openFixture(124)"><div class="search-right"><span class="content-text">张三_某公司</span></div></li><li onclick="openFixture(123)"><div class="search-right"><span class="content-text">张三_某公司</span></div></li></ul></div>
          <div class="user-list"><div class="geek-item selected" data-id="999-0">张三</div></div><div class="chat-conversation"><span class="base-name">张三</span></div>
          <iframe src="${friendPath}"></iframe><script>async function openFixture(uid){document.querySelector('.geek-search-list').hidden=true;document.querySelector('.chat-job-search').hidden=true;document.querySelector('.geek-item').setAttribute('data-id','123-9'); await fetch('/wapi/zpjob/chat/geek/info?uid='+uid);setTimeout(()=>document.querySelector('.geek-item').setAttribute('data-id',uid+'-9'),150)}</script>` });
      }
    });
    observeBossResponseIdentities(page);
    await page.goto("https://www.zhipin.com/fixture");
    const identity = await resolveBossResponseIdentity(page, "opaque-A", "张三");
    assert.equal(identity.verified, true, JSON.stringify({ identity, selected: await page.locator('.geek-item.selected').getAttribute('data-id'), inputVisible: await page.locator('.search-input').isVisible(), searchTexts: await page.locator('.geek-search-list li').allTextContents() }));
    assert.equal(identity.conversation_id, "123-9");
    assert.equal(await page.locator(".geek-item.selected").getAttribute("data-id"), "123-9");
    assert.deepEqual(openedUIDs, [124, 123]);
    // 已保存完整会话 ID 的搜索也不能采信错误点击期间遗留的目标 selected。
    await page.goto("https://www.zhipin.com/fixture");
    openedUIDs.length = 0;
    const known = await searchBossChatSessionOnPage(page, { candidate_name: "张三", conversation_id: "123-9" });
    assert.equal(known.found, true);
    assert.deepEqual(openedUIDs, [124, 123]);
    // 复用启动前已打开的页面时没有初始响应缓存，也要从本次详情的完整加密 ID 建立映射。
    includeInitialIdentity = false;
    await page.goto("https://www.zhipin.com/fixture");
    openedUIDs.length = 0;
    const discovered = await resolveBossResponseIdentity(page, "opaque-A", "张三");
    assert.equal(discovered.verified, true);
    assert.equal(discovered.conversation_id, "123-9");
    assert.deepEqual(openedUIDs, [124, 123]);
    // 第一个同名入口打开期间停止，之后不得继续点击第二个人。
    await page.goto("https://www.zhipin.com/fixture");
    openedUIDs.length = 0;
    cancelController = new AbortController();
    await assert.rejects(resolveBossResponseIdentity(page, "opaque-A", "张三", cancelController.signal), error => error.name === "AbortError");
    assert.deepEqual(openedUIDs, [124]);
  } finally { await browser.close(); }
});


// 菜单的同文档导航保留账号事实；真正重载仍清空，并且冲突不能被菜单切换洗掉。
test("正常菜单切换保留账号，实际主文档重载必须重新核对", async () => {
 const page=fixturePage(); observeBossResponseIdentities(page);
 const account=uid=>page.emit("response",{url:()=>`https://www.zhipin.com${accountPath}`,json:async()=>({code:0,zpData:{userId:uid}})});
 account(901); assert.equal((await readBossObservedAccountIdentity(page)).account_id,"901");
 page.emit("framenavigated",page.mainFrame());
 assert.equal((await readBossObservedAccountIdentity(page)).account_id,"901");
 account(902); assert.equal((await readBossObservedAccountIdentity(page)).verified,false);
 page.emit("framenavigated",page.mainFrame());
 assert.equal((await readBossObservedAccountIdentity(page)).verified,false);
 page.emit("request",{isNavigationRequest:()=>true,frame:()=>page.mainFrame()});
 assert.equal((await readBossObservedAccountIdentity(page)).verified,false);
 account(902); assert.equal((await readBossObservedAccountIdentity(page)).account_id,"902");
});
