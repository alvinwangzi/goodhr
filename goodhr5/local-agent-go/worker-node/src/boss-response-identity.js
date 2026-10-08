// 本文件只观察 Boss 页面正常加载的响应 ID，并结合真实聊天行验证跨入口身份；不请求接口、不读取凭证、不注入脚本。
const observers = new WeakMap();
const recommendationPath = "/wapi/zpjob/rec/geek/list";
const friendPath = "/wapi/zprelation/friend/getBossFriendListV2.json";
const detailPath = "/wapi/zpjob/chat/geek/info";

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
  const state = { facts: new Map(), conflicts: new Set(), pending: new Set(), epoch: 0 };
  observers.set(page, state);
  page.on("framenavigated", frame => {
    if (frame !== page.mainFrame()) return;
    state.epoch++;
    state.facts.clear();
    state.conflicts.clear();
  });
  page.on("response", response => {
    let url;
    try { url = new URL(response.url()); } catch { return; }
    if (url.protocol !== "https:" || url.hostname !== "www.zhipin.com" ||
        ![recommendationPath, friendPath, detailPath].includes(url.pathname)) return;
    const epoch = state.epoch;
    const pending = (async () => {
      try {
        const body = await response.json();
        if (epoch !== state.epoch) return;
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
export async function resolveBossResponseIdentity(page, recommendationID) {
  const state = observeBossResponseIdentities(page);
  await Promise.allSettled([...state.pending]);
  const fact = state.facts.get(recommendationID);
  if (!fact || state.conflicts.has(recommendationID)) return { verified: false };
  const epoch = state.epoch;
  const rows = await page.locator("[data-id]").all();
  const matches = [];
  for (const row of rows) {
    const id = await row.getAttribute("data-id");
    const parsed = /^(\d+)-(\d+)$/.exec(id || "");
    if (parsed?.[1] === fact.numeric_id && await row.isVisible()) matches.push(id);
  }
  if (matches.length !== 1 || epoch !== state.epoch || state.conflicts.has(recommendationID)) return { verified: false };
  return { verified: true, recommendation_id: recommendationID, conversation_id: matches[0],
    source: fact.source + "_and_visible_chat_id" };
}
