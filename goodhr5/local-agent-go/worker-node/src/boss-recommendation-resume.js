// 本文件通过标准 Locator、截图尺寸和真实滚轮恢复 Boss 推荐进度，不读取脚本变量或注入页面代码。
import { readBossRecommendationID } from "./boss-candidate-identity.js";

/** recommendationSurface 每次操作重新取得推荐 iframe 和卡片定位器，不复用跨菜单 DOM 引用。 */
function recommendationSurface(page) {
  const frame = page.frameLocator('iframe[name="recommendFrame"]');
  return { frame, cards: frame.locator(".candidate-card-wrap") };
}

/** viewportSize 使用标准 viewport 或 CSS 像素截图读取真实视口，不推断窗口缩放。 */
async function viewportSize(page) {
  const viewport = page.viewportSize();
  if (viewport) return viewport;
  const png = await page.screenshot({ type: "png", scale: "css" });
  if (png.length < 24) throw new Error("浏览器截图无法读取视口尺寸");
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20) };
}

/** recommendationSignature 读取实际 iframe 地址中的岗位、沟通过滤和推荐排序，不保存瞬时时间参数。 */
export async function recommendationSignature(page) {
  const src = await page.locator('iframe[name="recommendFrame"]').getAttribute("src");
  if (!src) throw new Error("推荐页面尚未准备完成");
  const url = new URL(src, page.url());
  return JSON.stringify(["jobid", "status", "filterParams", "source"].map(key => [key, url.searchParams.get(key)]));
}

/** cardWithID 返回按完整 ID 限定的唯一卡片，列表变化时 Locator 自身仍绑定该 ID。 */
function cardWithID(page, id) {
  const { frame, cards } = recommendationSurface(page);
  const inner = frame.locator(`.card-inner[data-geekid=${JSON.stringify(id)}]`);
  return cards.filter({ has: inner });
}

/** captureRecommendationAnchors 保存最近处理者对应位置的提示，只用三个完整 ID 判定相邻；提示不作为身份。 */
export async function captureRecommendationAnchors(page, payload) {
  const anchors = payload.anchors || [];
  const signature = await recommendationSignature(page);
  if (!anchors.length || anchors.length > 3) return { valid: false, reason: "no_anchors", signature };
  const last = cardWithID(page, anchors[anchors.length - 1]);
  if (await last.count() !== 1) return { valid: false, reason: "anchor_missing", signature };
  const index = await last.locator('xpath=preceding-sibling::*[contains(concat(" ", normalize-space(@class), " "), " candidate-card-wrap ")]').count();
  const start = index - anchors.length + 1;
  if (start < 0) return { valid: false, reason: "anchor_order_changed", signature };
  const { cards } = recommendationSurface(page);
  for (let i = 0; i < anchors.length; i++) {
    if (await readBossRecommendationID(cards.nth(start + i)) !== anchors[i]) return { valid: false, reason: "anchor_not_adjacent", signature };
  }
  return { valid: true, start_index: start, signature, anchors };
}

/** checkRecommendationAnchors 返回后只读取原位置对应的最多三个卡片，不从头遍历历史列表。 */
export async function checkRecommendationAnchors(page, payload) {
  const saved = payload.cursor || {};
  const anchors = saved.anchors || [];
  if (!saved.valid || !anchors.length || anchors.length > 3) return { matched: false, reason: saved.reason || "no_anchors" };
  if (await recommendationSignature(page) !== saved.signature) return { matched: false, reason: "position_or_filter_changed" };
  const start = Number(saved.start_index);
  if (!Number.isInteger(start) || start < 0) return { matched: false, reason: "invalid_hint" };
  const { cards } = recommendationSurface(page);
  if (await cards.count() < start + anchors.length) return { matched: false, reason: "anchor_missing" };
  for (let i = 0; i < anchors.length; i++) {
    if (await readBossRecommendationID(cards.nth(start + i)) !== anchors[i]) return { matched: false, reason: "anchor_changed" };
  }
  return { matched: true, reason: "resume_anchor_match" };
}

/** wheelAtCards 将鼠标移入当前视口内的真实卡片区域后滚轮，不直接设置滚动位置。 */
async function wheelAtCards(page, distance) {
  const viewport = await viewportSize(page);
  const iframe = await page.locator('iframe[name="recommendFrame"]').boundingBox();
  if (!iframe) throw new Error("无法确认推荐列表滚动区域");
  const top = Math.max(8, iframe.y), bottom = Math.min(viewport.height - 8, iframe.y + iframe.height);
  if (bottom <= top) throw new Error("推荐列表不在当前视口，无法确定真实滚轮区域");
  await page.mouse.move(Math.max(8, Math.min(viewport.width - 8, iframe.x + iframe.width / 2)), (top + bottom) / 2);
  await page.mouse.wheel(0, distance);
  await page.waitForTimeout(180);
}

/** rewindRecommendation 通过真实滚轮回到当前已加载列表的起点；有界失败返回错误，不能把读取失败当作空名单。 */
export async function rewindRecommendation(page) {
  for (let i = 0; i < 120; i++) {
    const { cards } = recommendationSurface(page);
    if (await cards.count() === 0) throw new Error("推荐列表未加载，无法恢复扫描");
    const first = cards.first();
    const box = await first.boundingBox();
    const frameBox = await page.locator('iframe[name="recommendFrame"]').boundingBox();
    if (box && frameBox && box.y >= Math.max(0, frameBox.y) && box.y < frameBox.y + frameBox.height) return { rewound: true };
    await wheelAtCards(page, -2400);
  }
  throw new Error("推荐列表无法回到起点，请核对页面后重新开始");
}

/** ensureRecommendationVisible 按完整 ID 重新定位，用真实滚轮将目标卡片带入视口。 */
export async function ensureRecommendationVisible(page, payload) {
  const id = String(payload.recommendation_candidate_id || "");
  if (!id) throw new Error("缺少候选人真实 ID");
  for (let i = 0; i < 60; i++) {
    const card = cardWithID(page, id);
    if (await card.count() !== 1) throw new Error("候选人真实 ID 未唯一匹配");
    const box = await card.boundingBox();
    const viewport = await viewportSize(page);
    const frameBox = await page.locator('iframe[name="recommendFrame"]').boundingBox();
    if (!box || !frameBox) throw new Error("候选人卡片位置无法确认");
    const top = Math.max(0, frameBox.y), bottom = Math.min(viewport.height, frameBox.y + frameBox.height);
    if (box.y >= top && box.y + Math.min(box.height, bottom - top) <= bottom) return { card, attempts: i + 1, by_identity: true };
    await wheelAtCards(page, box.y < top ? -Math.min(600, top - box.y + 40) : Math.min(600, box.y + box.height - bottom + 40));
  }
  throw new Error("真实滚轮未能显示目标候选人，已停止操作");
}
