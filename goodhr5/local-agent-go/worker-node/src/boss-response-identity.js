// 本文件只观察 Boss 页面正常加载的响应 ID，并结合真实聊天行验证跨入口身份；不请求接口、不读取凭证、不注入脚本。
const observers = new WeakMap();
import { locateBossConversationByNumericID, locateBossConversationByEncryptedID } from "./boss-chat-search.js";
const recommendationPath = "/wapi/zpjob/rec/geek/list";
const friendPath = "/wapi/zprelation/friend/getBossFriendListV2.json";
const detailPath = "/wapi/zpjob/chat/geek/info";
const accountPath = "/wapi/zpuser/wap/getUserInfo.json";
const batchPath = "/wapi/batch/requests";

/** waitBossIdentityResponses 有界等待已观察响应，取消或超时后不使用旧证明，也不继续页面动作。 */
async function waitBossIdentityResponses(state, signal) {
  signal?.throwIfAborted();
  if (state.pending.size === 0) return true;
  let timer, onAbort;
  const interrupted = new Promise((resolve, reject) => {
    timer = setTimeout(() => resolve(false), 5000);
    onAbort = () => reject(signal.reason || new Error("身份核对已取消"));
    signal?.addEventListener("abort", onAbort, { once: true });
  });
  try { return await Promise.race([Promise.allSettled([...state.pending]).then(() => true), interrupted]); }
  finally { clearTimeout(timer); signal?.removeEventListener("abort", onAbort); }
}

/** readBossResponseAccountID 只读取账号信息节点中的数字用户 ID，不访问 token、联系方式或其他批量响应。 */
export function readBossResponseAccountID(path, body) {
  const response = path === accountPath ? body : path === batchPath && body?.code === 0 ? body.zpData?.[accountPath] : null;
  return response?.code === 0 ? numericID(response.zpData?.userId) : "";
}

/** numericID 只接受无精度损失的正整数候选人 ID。 */
function numericID(value) {
  if (typeof value === "number") return Number.isSafeInteger(value) && value > 0 ? String(value) : "";
  return typeof value === "string" && /^[1-9]\d{0,15}$/.test(value) ? value : "";
}

/** readBossResponseIdentityFacts 仅提取已核对响应结构中的成对 ID，不返回姓名、正文、电话或 securityId。 */
export function readBossResponseIdentityFacts(path, body) {
  if (body?.code !== 0) return [];
  const facts = [];
  const add = (encrypted, numeric, source) => {
    const id = numericID(numeric);
    if (typeof encrypted === "string" && encrypted.length > 0 && encrypted.length <= 128 && id) {
      facts.push({ recommendation_id: encrypted, numeric_id: id, source });
    }
  };
  if (path === recommendationPath && Array.isArray(body.zpData?.geekList)) {
    for (const row of body.zpData.geekList) {
      // 同一记录内的加密字段发生矛盾时，整条身份事实不可用。
      const card = row?.geekCard;
      if (!card || [card.encGeekId, card.encryptGeekId].some(id => id && id !== row.encryptGeekId)) continue;
      add(row.encryptGeekId, card.geekId, "boss_recommend_response");
    }
  } else if (path === friendPath && Array.isArray(body.zpData?.friendList)) {
    for (const row of body.zpData.friendList) add(row?.encryptUid, row?.uid, "boss_friend_response");
  } else if (path === detailPath) {
    const row = body.zpData?.data;
    add(row?.encryptUid, row?.uid, "boss_chat_detail_response");
  }
  return facts;
}

/** observeBossResponseIdentities 在标准 Page 上被动观察三类响应，只在内存保存 ID 事实。 */
export function observeBossResponseIdentities(page) {
  if (observers.has(page)) return observers.get(page);
  const state = { facts: new Map(), conflicts: new Set(), pending: new Set(), epoch: 0, accountID: "", accountConflict: false };
  observers.set(page, state);
  page.on("framenavigated", frame => {
    if (frame !== page.mainFrame()) return;
    state.epoch++;
    state.facts.clear();
    state.conflicts.clear();
    state.accountID = "";
    state.accountConflict = false;
  });
  page.on("response", response => {
    let url;
    try { url = new URL(response.url()); } catch { return; }
    if (url.protocol !== "https:" || url.hostname !== "www.zhipin.com" ||
        ![recommendationPath, friendPath, detailPath, accountPath, batchPath].includes(url.pathname)) return;
    const epoch = state.epoch;
    const pending = (async () => {
      try {
        const body = await response.json();
        if (epoch !== state.epoch) return;
        const accountID = readBossResponseAccountID(url.pathname, body);
        if (accountID) {
          if (state.accountID && state.accountID !== accountID) {
            state.accountConflict = true;
            state.facts.clear();
          }
          state.accountID = accountID;
        }
        for (const fact of readBossResponseIdentityFacts(url.pathname, body)) {
          const old = state.facts.get(fact.recommendation_id);
          if (old && old.numeric_id !== fact.numeric_id) {
            state.conflicts.add(fact.recommendation_id);
            state.facts.delete(fact.recommendation_id);
          } else if (!state.conflicts.has(fact.recommendation_id)) {
            if (state.facts.size >= 10000 && !old) continue;
            state.facts.set(fact.recommendation_id, fact);
          }
        }
      } catch { /* 响应失效或结构改变时保留未核对，不猜测身份。 */ }
    })();
    state.pending.add(pending);
    void pending.finally(() => state.pending.delete(pending));
  });
  return state;
}

/** resolveBossResponseIdentity 用成对响应事实和当前唯一真实聊天行建立映射，不合成 data-id 后缀。 */
export async function resolveBossResponseIdentity(page, recommendationID, candidateName = "", signal) {
  signal?.throwIfAborted();
  const state = observeBossResponseIdentities(page);
  if (!await waitBossIdentityResponses(state, signal)) return { verified: false };
  signal?.throwIfAborted();
  const epoch = state.epoch;
  let fact = state.facts.get(recommendationID);
  if (state.accountConflict || state.conflicts.has(recommendationID)) return { verified: false };
  if (!fact && candidateName && recommendationID) {
    const opened = await locateBossConversationByEncryptedID(page, candidateName, recommendationID, signal);
    if (!await waitBossIdentityResponses(state, signal)) return { verified: false };
    fact = state.facts.get(recommendationID);
    if (!opened.found || !fact || epoch !== state.epoch || state.accountConflict || state.conflicts.has(recommendationID) ||
        /^(\d+)-\d+$/.exec(opened.conversation_id || "")?.[1] !== fact.numeric_id) return { verified: false };
    return { verified: true, recommendation_id: recommendationID, conversation_id: opened.conversation_id,
      source: fact.source + "_and_selected_chat_id" };
  }
  if (!fact) return { verified: false };
  const rows = await page.locator("[data-id]").all();
  const matches = [];
  for (const row of rows) {
    const id = await row.getAttribute("data-id");
    const parsed = /^(\d+)-(\d+)$/.exec(id || "");
    if (parsed?.[1] === fact.numeric_id && await row.isVisible()) matches.push(id);
  }
  if (matches.length === 0 && candidateName) {
    const opened = await locateBossConversationByNumericID(page, candidateName, fact.numeric_id, signal);
    if (!await waitBossIdentityResponses(state, signal)) return { verified: false };
    if (!opened.found || epoch !== state.epoch || state.accountConflict || state.conflicts.has(recommendationID) ||
        state.facts.get(recommendationID)?.numeric_id !== fact.numeric_id) return { verified: false };
    return { verified: true, recommendation_id: recommendationID, conversation_id: opened.conversation_id,
      source: fact.source + "_and_selected_chat_id" };
  }
  if (matches.length !== 1 || epoch !== state.epoch || state.accountConflict || state.conflicts.has(recommendationID)) return { verified: false };
  return { verified: true, recommendation_id: recommendationID, conversation_id: matches[0],
    source: fact.source + "_and_visible_chat_id" };
}

/** readBossObservedAccountIdentity 返回当前主文档正常加载的账号 ID 证明，缺失或同页变化时不猜测。 */
export async function readBossObservedAccountIdentity(page, signal, refreshIfMissing = false, waitForAccount = false) {
  signal?.throwIfAborted();
  const state = observeBossResponseIdentities(page);
  if (!await waitBossIdentityResponses(state, signal)) return { verified: false };
  if (!state.accountID && !state.accountConflict && waitForAccount) {
    const deadline = Date.now() + 5000;
    while (!state.accountID && !state.accountConflict && Date.now() < deadline) {
      signal?.throwIfAborted();
      await page.waitForTimeout(100);
    }
  }
  signal?.throwIfAborted();
  if (!state.accountID && !state.accountConflict && refreshIfMissing) {
    const url = new URL(page.url());
    if (url.hostname !== "www.zhipin.com" || !url.pathname.startsWith("/web/chat/")) return { verified: false };
    // 仅在首次任务准备时补取账号证明；保留人工草稿，绝不为刷新账号丢弃输入。
    for (const input of await page.locator('[contenteditable="true"], .boss-chat-editor-input, textarea').all()) {
      if (!await input.isVisible()) continue;
      const draft = await input.getAttribute("contenteditable") === "true" ? await input.innerText() : await input.inputValue().catch(() => input.innerText());
      if (String(draft || "").trim()) throw new Error("页面有尚未发送的输入，请先处理后再开始任务");
    }
    signal?.throwIfAborted();
    await page.reload({ waitUntil: "domcontentloaded", timeout: 15000 });
    const deadline = Date.now() + 5000;
    while (!state.accountID && !state.accountConflict && Date.now() < deadline) {
      signal?.throwIfAborted();
      await page.waitForTimeout(100);
    }
  }
  if (!state.accountID || state.accountConflict) return { verified: false };
  return { verified: true, account_id: state.accountID, source: "boss_user_info_response" };
}
