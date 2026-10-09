/** 本文件将强类型选择器协议适配为标准 Locator 和真实输入动作，不包含平台或业务逻辑。 */
import { humanTypeText } from "./human-type.js";

/** createActionGate 保证断连后的底层动作未退出时，其他请求不能抢占浏览器。 */
export function createActionGate() {
  let active = 0;
  let exclusiveActive = false;
  return async (action, exclusive = true) => {
    if (exclusiveActive || (exclusive && active > 0)) throw new Error("浏览器仍被当前动作占用，请稍后重试");
    active++;
    if (exclusive) exclusiveActive = true;
    try { return await action(); } finally { active--; if (exclusive) exclusiveActive = false; }
  };
}

/** locatorActionHandler 保持原协议可用；新协议页面关闭时不创建新页面或切到其他标签。 */
export function locatorActionHandler(currentPage, legacy, action, downloadTracker) {
  return async (payload, signal) => {
    if (!Object.hasOwn(payload, "selector_spec")) return legacy(payload);
    const page = currentPage();
    if (!page || page.isClosed()) throw new Error("浏览器页面已关闭");
    if (action === "click" && payload.download) {
      if (!downloadTracker) throw new Error("当前 Worker 不支持等待下载，请更新本地程序");
      return downloadTracker.clickAndWait(page, payload, signal);
    }
    return executeLocatorAction(page, action, payload, signal);
  };
}

/** createDownloadTracker 关联一次点击与文件保存结果，复用页面监听，避免重复保存同一个下载事件。 */
export function createDownloadTracker(saveDownload) {
  const pending = new WeakMap();
  const saves = new WeakMap();

  /** capture 接管页面下载事件；业务关联键作为不透明元数据透传，不解析平台含义。 */
  function capture(page, download) {
    if (saves.has(download)) return saves.get(download);
    const request = pending.get(page);
    const claimed = request?.armed && !request.captured;
    if (claimed) request.captured = true;
    const metadata = claimed ? request.metadata : {};
    const saved = Promise.resolve().then(() => saveDownload(download, page, metadata));
    saves.set(download, saved);
    if (claimed) {
      request.resolve(saved);
      // 超时后仍保存原关联，迟到的下载事件不能被记到下一个候选人的请求上。
      const settled = () => { request.settled = true; if (request.finished && pending.get(page) === request) pending.delete(page); };
      saved.then(settled, settled);
    }
    return saved;
  }

  /** clickAndWait 在真实点击前监听下载，等待文件保存，取消或超时后清理监听但不重复点击。 */
  async function clickAndWait(page, payload, signal) {
    signal?.throwIfAborted();
    const id = payload.download?.id;
    if (typeof id !== "string" || !id.trim() || id.length > 200) throw new Error("缺少有效下载记录标识");
    if (pending.has(page)) return { clicked: false, download: { id, status: "failed", error: "上次下载仍待确认，请等待完成或重启浏览器后再试" } };
    const timeout = Number(payload.download.timeout_ms ?? 20000);
    if (!Number.isFinite(timeout) || timeout < 100 || timeout > 60000) throw new Error("下载等待时间不正确");
    let resolve, reject;
    const completed = new Promise(res => { resolve = res; });
    const interrupted = new Promise((_, fail) => { reject = fail; });
    // 保存仍在进行时，取消和截止时间也必须能中断等待。
    completed.catch(() => {});
    interrupted.catch(() => {});
    const request = { metadata: { id, position_id: String(payload.download.position_id || ""), source_key: String(payload.download.source_key || "") }, resolve, armed: false, captured: false, settled: false, finished: false };
    const controller = new AbortController();
    const interrupt = error => { controller.abort(error); reject(error); };
    const onDownload = download => { void capture(page, download).catch(() => {}); };
    const onAbort = () => interrupt(signal.reason || new Error("下载等待已取消"));
    const onClose = () => interrupt(new Error("下载期间页面已关闭"));
    pending.set(page, request);
    page.on("download", onDownload);
    page.on("close", onClose);
    signal?.addEventListener("abort", onAbort, { once: true });
    const timer = setTimeout(() => interrupt(new Error("等待下载完成超时，未确认成功，请勿重复点击")), timeout);
    if (signal?.aborted) onAbort();
    try {
      const clicked = await executeLocatorAction(page, "click", payload, controller.signal, () => { request.armed = true; });
      controller.signal.throwIfAborted();
      const download = await Promise.race([completed, interrupted]);
      return { ...clicked, download };
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      page.off("download", onDownload);
      page.off("close", onClose);
      request.finished = true;
      if (!request.armed || request.settled) pending.delete(page);
    }
  }
  return { capture, clickAndWait };
}

/** cssString 转义属性值，防止动态标识改变选择器含义。 */
function cssString(value) {
  return `"${String(value).replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\a ").replace(/\r/g, "\\d ").replace(/\0/g, "\\fffd ")}"`;
}

/** resolveSelector 保留延迟定位语义，属性过滤不依赖扫描时的数组下标。 */
export function resolveSelector(scope, spec) {
  if (!spec || !Array.isArray(spec.selectors) || !spec.selectors.length || spec.selectors.some(value => typeof value !== "string" || !value.trim())) {
    throw new Error("缺少有效选择器");
  }
  if (spec.frame) scope = scope.frameLocator(spec.frame);
  if (spec.parent) scope = resolveSelector(scope, spec.parent);
  const attributes = Object.entries(spec.attributes || {}).map(([name, value]) => {
    if (!/^[a-zA-Z_][a-zA-Z0-9_:-]*$/.test(name)) throw new Error("属性名称不正确");
    return `[${name}=${cssString(value)}]`;
  }).join("");
  let locator = scope.locator(`:is(${spec.selectors.join(",")})${attributes}`);
  if (spec.text) locator = locator.filter({ hasText: new RegExp(`^${String(spec.text).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) });
  if (spec.nth !== undefined) {
    if (!Number.isInteger(spec.nth) || spec.nth < 0) throw new Error("元素序号不正确");
    locator = locator.nth(spec.nth);
  }
  return locator;
}

/** uniqueVisible 拒绝零目标或多目标动作，不以第一个命中代替唯一性。 */
async function uniqueVisible(locator) {
  if (await locator.count() !== 1 || !await locator.isVisible()) throw new Error("目标不唯一或不可见");
  return locator;
}

/** editableValue 通过标准读取能力兼容普通输入框与 contenteditable。 */
async function editableValue(locator) {
  const editable = await locator.getAttribute("contenteditable");
  return editable === "true" || editable === "" || editable === "plaintext-only"
    ? await locator.innerText({ timeout: 1000 })
    : await locator.inputValue({ timeout: 1000 });
}

/** resolveVisibleTextSelector 用标准可见文字核对唯一目标，隐藏提示不参与菜单匹配。 */
async function resolveVisibleTextSelector(locator, expectedText, signal) {
  const expected = String(expectedText).replace(/\s+/g, " ").trim();
  const matches = [];
  const count = await locator.count();
  if (count > 100) throw new Error("可见文字定位范围过大");
  for (let index = 0; index < count; index++) {
    signal?.throwIfAborted();
    const item = locator.nth(index);
    if (await item.isVisible() && (await item.innerText({ timeout: 1000 })).replace(/\s+/g, " ").trim() === expected) matches.push(index);
  }
  if (matches.length !== 1) throw new Error("可见文字目标不唯一或不可见");
  return locator.nth(matches[0]);
}

/** moveToVisible 只移动到可见区域，不使用脚本或隐式滚动寻找目标。 */
async function moveToVisible(page, locator) {
  const box = await locator.boundingBox();
  const viewport = page.viewportSize();
  if (!box || box.width <= 0 || box.height <= 0 || box.x < 0 || box.y < 0 || (viewport && (box.x + box.width > viewport.width || box.y + box.height > viewport.height))) {
    throw new Error("目标不在可操作区域，请先滚动到目标");
  }
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 8 });
}

/** executeLocatorAction 在现有通用路由上处理新选择器协议，旧格式仍由原适配器处理。 */
export async function executeLocatorAction(page, action, payload, signal, beforeClick) {
  signal?.throwIfAborted();
  if (payload.include_html) throw new Error("不支持 HTML 读取");
  let locator = resolveSelector(page, payload.selector_spec);
  if (payload.selector_spec.visible_text) {
    locator = await resolveVisibleTextSelector(locator, payload.selector_spec.visible_text, signal);
  }
  if (action === "find-elements") {
    const count = await locator.count();
    const limit = Math.max(1, Math.min(1000, Number(payload.max_items || 100)));
    const items = [];
    for (let index = 0; index < count && items.length < limit; index++) {
      const item = locator.nth(index);
      if (payload.visible_only !== false && !await item.isVisible()) continue;
      const fields = {};
      for (const [name, field] of Object.entries(payload.fields || {})) {
        const target = field.selector ? resolveSelector(item, field.selector) : item;
        if (await target.count() !== 1) { fields[name] = ""; continue; }
        fields[name] = field.attribute ? (await target.getAttribute(field.attribute) || "") : field.editable ? await editableValue(target) : await target.innerText({ timeout: 1000 });
      }
      items.push({ text: await item.innerText({ timeout: 1000 }), fields });
    }
    return { items, count: items.length };
  }
  await uniqueVisible(locator);
  if (action === "extract-text") {
    return { text: payload.editable ? await editableValue(locator) : await locator.innerText({ timeout: 1000 }) };
  }
  if (action === "type") {
    if (!await locator.isEditable()) throw new Error("目标不可输入");
    if ((await editableValue(locator)).trim()) throw new Error("发现人工草稿，已跳过输入");
    await moveToVisible(page, locator);
    await uniqueVisible(locator);
    signal?.throwIfAborted();
    await locator.click({ timeout: 1000 });
    if ((await editableValue(locator)).trim()) throw new Error("发现人工草稿，已跳过输入");
    const focused = resolveSelector(page, { ...payload.selector_spec, selectors: payload.selector_spec.selectors.map(selector => `:is(${selector}):focus`) });
    let typed = "";
    await humanTypeText({ type: async text => {
      signal?.throwIfAborted();
      await uniqueVisible(locator);
      if (await focused.count() !== 1) throw new Error("输入焦点已变化，已停止输入");
      if ((await editableValue(locator)).trim() !== typed.trim()) throw new Error("输入草稿已变化，已停止输入");
      signal?.throwIfAborted();
      await page.keyboard.insertText(text);
      typed += text;
    } }, String(payload.text || ""), payload);
    signal?.throwIfAborted();
    const value = await editableValue(locator);
    if (value.trim() !== String(payload.text || "").trim()) throw new Error("输入内容校验失败");
    return { typed: true, verified: true, value };
  }
  if (action === "click") {
    await moveToVisible(page, locator);
    if (payload.selector_spec.visible_text) {
      locator = await resolveVisibleTextSelector(resolveSelector(page, payload.selector_spec), payload.selector_spec.visible_text, signal);
      await moveToVisible(page, locator);
    }
    await uniqueVisible(locator);
    signal?.throwIfAborted();
    beforeClick?.();
    await locator.click({ timeout: 1000 });
    return { clicked: true };
  }
  if (action === "scroll") {
    await moveToVisible(page, locator);
    await page.mouse.wheel(0, Math.max(-2000, Math.min(2000, Number(payload.distance || 500))));
    return { scrolled: true };
  }
  throw new Error("不支持的通用浏览器动作");
}
