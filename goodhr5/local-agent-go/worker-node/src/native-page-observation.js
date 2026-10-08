// 本文件通过标准 Locator、截图和元素坐标观察页面，不调用脚本或伪造浏览器滚动属性。
import crypto from "node:crypto";

const observations = new WeakMap();

/** nativeViewport 通过标准视口或 CSS 像素 PNG 得到可操作区域，失败时明确返回未知。 */
export async function nativeViewport(page) {
  const configured = page?.viewportSize?.();
  if (configured?.width > 0 && configured?.height > 0) return { ...configured, source: "playwright-viewport" };
  if (typeof page?.screenshot !== "function") return { width: 0, height: 0, source: "unknown" };
  const png = await page.screenshot({ type: "png", scale: "css" });
  if (png.length < 24 || png.toString("ascii", 1, 4) !== "PNG") throw new Error("截图无法读取浏览器尺寸");
  return { width: png.readUInt32BE(16), height: png.readUInt32BE(20), source: "css-screenshot" };
}

/** observeContainer 使用首尾内容位置判断可见边界，兼容固定标题；偏移只作为观察量，不是 DOM scrollTop。 */
export async function observeContainer(locator, viewportHeight = 0, frameTop = 0) {
  const box = await locator.boundingBox();
  if (!box || box.height <= 0) throw new Error("滚动区域位置无法读取");
  const children = locator.locator(":scope > :not(script):not(style):not(link):not(template):visible");
  const count = await children.count();
  const first = count ? children.first() : locator;
  const last = count ? children.last() : locator;
  const [firstBox, lastBox, text] = await Promise.all([first.boundingBox(), last.boundingBox(), locator.innerText()]);
  if (!firstBox || !lastBox) throw new Error("滚动内容边界无法读取");
  const documentRoot = await locator.locator("xpath=self::body | self::html").count() > 0;
  const actualViewport = documentRoot && viewportHeight <= 0 ? await nativeViewport(locator.page()) : null;
  const limit = viewportHeight || actualViewport?.height || box.height;
  const clientHeight = documentRoot || viewportHeight > 0 ? Math.min(box.height, limit) : box.height;
  const signature = crypto.createHash("sha256").update(text.slice(0, 160) + "|" + text.slice(-160)).digest("hex");
  const page = locator.page();
  let pageState = observations.get(page); if (!pageState) { pageState = new Map(); observations.set(page, pageState); }
  const key = locator.toString();
  const old = pageState.get(key);
  let contentTop=firstBox.y,contentBottom=lastBox.y+lastBox.height;
  // 固定首尾工具栏不代表内容末尾，读取有界直接子元素的实际内容范围。
  for(let i=0;i<Math.min(count,128);i++){const childBox=await children.nth(i).boundingBox();if(childBox){contentTop=Math.min(contentTop,childBox.y);contentBottom=Math.max(contentBottom,childBox.y+childBox.height)}}
  let offset = Math.max(0, box.y - contentTop, documentRoot ? frameTop - box.y : 0), movement = 0;
  if (old?.signature === signature) {
    const deltas = [old.first - contentTop, old.last - contentBottom];
    movement = deltas.sort((a, b) => Math.abs(b) - Math.abs(a))[0];
    offset = Math.max(offset, old.offset + movement, 0);
  }
  const currentExtent = Math.max(clientHeight, contentBottom - contentTop);
  const extent = old?.signature === signature ? Math.max(old.extent, currentExtent) : currentExtent;
  pageState.set(key, { signature, first: contentTop, last: contentBottom, offset, extent });
  return { source: "locator-geometry", measured_scroll_top: false, scrollable: extent > clientHeight + 8, scrollTop: Math.round(offset), scrollHeight: Math.round(extent), clientHeight: Math.round(clientHeight), movement, can_scroll_up: offset > 2, can_scroll_down: contentBottom > (documentRoot ? frameTop : box.y) + clientHeight + 2, box };
}

/** observeScrollAtPointer 读取真实鼠标悬停链内的滚动区域，不使用 elementFromPoint 或样式注入。 */
export async function observeScrollAtPointer(page, point) {
  const viewport = await nativeViewport(page);
  if (viewport.height <= 0) throw new Error("浏览器视口无法确认");
  const frames = page.frames().slice().reverse();
  for (const frame of frames) {
    let visibleHeight=viewport.height, frameTop=0;
    if(frame!==page.mainFrame()){
      const parent=frame.parentFrame();const iframes=parent?.locator("iframe");
      if(iframes){for(let i=0;i<await iframes.count();i++){const iframe=iframes.nth(i);const src=await iframe.getAttribute("src");const name=await iframe.getAttribute("name");if((frame.name()&&name===frame.name())||(src&&new URL(src,parent.url()).href===frame.url())){const box=await iframe.boundingBox();if(box){visibleHeight=Math.min(visibleHeight,box.height);frameTop=box.y}break}}}
    }
    const hovered = frame.locator(":hover");
    const count = await hovered.count();
    for (let i = count - 1; i >= 0; i--) {
      const locator = hovered.nth(i);
      const box = await locator.boundingBox();
      if (!box || (point && (point.x < box.x || point.x > box.x + box.width || point.y < box.y || point.y > box.y + box.height))) continue;
      const info = await observeContainer(locator, visibleHeight,frameTop).catch(() => null);
      if (info?.scrollable) return info;
    }
    // 部分原生窗口/iframe 不暴露 :hover，用标准元素坐标核对已知布局容器。
    const containers=frame.locator('body,main,section,article,[style*="overflow"],[class*="scroll"],[class*="resume"],[class*="list"],[class*="content"]');
    const containerCount=Math.min(await containers.count(),256);
    for(let i=containerCount-1;i>=0;i--){
      const locator=containers.nth(i);const box=await locator.boundingBox();
      if(!box||box.height<=0||box.width<=0||(point&&(point.x<box.x||point.x>box.x+box.width||point.y<box.y||point.y>box.y+box.height)))continue;
      const info=await observeContainer(locator,visibleHeight,frameTop).catch(()=>null);if(info?.scrollable)return info;
    }
  }
  return observeContainer(page.locator("body"), viewport.height);
}
