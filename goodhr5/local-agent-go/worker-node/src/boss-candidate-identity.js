// 本文件通过标准 Locator 读取 Boss 推荐卡片的完整身份，禁止以姓名、年龄或数组下标代替。

/** readBossRecommendationID 读取卡片自身或唯一内部节点的 data-geekid，缺失或歧义返回空值。 */
export async function readBossRecommendationID(card) {
  const direct = await card.getAttribute("data-geekid");
  if (direct) return direct;
  const inner = card.locator(".card-inner[data-geekid]");
  if (await inner.count() !== 1) return "";
  return String(await inner.getAttribute("data-geekid") || "");
}

/** matchBossRecommendationID 按完整 ID 找唯一卡片，旧引用和列表重排不能改变操作对象。 */
export async function matchBossRecommendationID(cards, expectedID) {
  if (!expectedID) return null;
  let match = null;
  for (let i = 0; i < cards.length; i++) {
    const card = typeof cards[i].getAttribute === "function" ? cards[i] : cards[i].locator;
    if (await readBossRecommendationID(card) !== expectedID) continue;
    if (match) throw new Error("候选人真实 ID 对应多个卡片，已停止操作");
    match = { index: i, score: 1 };
  }
  return match;
}
