// 本文件验证响应成对 ID、冲突隔离与真实聊天行核对，全部使用虚构 ID，不连接 Boss。
import { EventEmitter } from "node:events";
import { test } from "node:test";
import assert from "node:assert/strict";
import { chromium } from "playwright-core";
import { readBossResponseIdentityFacts, observeBossResponseIdentities, resolveBossResponseIdentity } from "../src/boss-response-identity.js";
const recPath = "/wapi/zpjob/rec/geek/list";
const friendPath = "/wapi/zprelation/friend/getBossFriendListV2.json";

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
  page.emit("framenavigated", page.mainFrame());
  emitFriend(page, "opaque-A", 123, "example.com");
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
  emitFriend(page);
  assert.equal((await resolveBossResponseIdentity(page, "opaque-A")).verified, true);
  page.emit("framenavigated", page.mainFrame());
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
  page.emit("framenavigated", page.mainFrame());
  deliver({ code: 0, zpData: { friendList: [{ uid: 123, encryptUid: "opaque-A" }] } });
  assert.deepEqual(await resolveBossResponseIdentity(page, "opaque-A"), { verified: false });
});

test("系统 Edge 的真实响应事件与标准 Locator 完成联合核对，不连接业务页面", async () => {
  const browser = await chromium.launch({ channel: "msedge", headless: true });
  try {
    const page = await browser.newPage();
    await page.route("https://www.zhipin.com/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === friendPath) {
        await route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { friendList: [{ uid: 123, encryptUid: "opaque-A" }] } }) });
      } else {
        await route.fulfill({ contentType: "text/html", body: `<div data-id="123-9">虚构候选人</div><iframe src="${friendPath}"></iframe>` });
      }
    });
    observeBossResponseIdentities(page);
    await page.goto("https://www.zhipin.com/fixture");
    const identity = await resolveBossResponseIdentity(page, "opaque-A");
    assert.equal(identity.verified, true);
    assert.equal(identity.conversation_id, "123-9");
  } finally { await browser.close(); }
});
