// 本文件通过标准 Locator 搜索 Boss 聊天会话，拒绝同名歧义并等待正确面板；不注入页面脚本。
/** 按姓名搜索唯一可见结果并打开聊天面板，返回页面读取的身份信息。 */
export async function searchBossChatSessionOnPage(currentPage, payload, signal) {
  signal?.throwIfAborted();
  const startedAt = Date.now();
  const name = String(payload.candidate_name || payload.name || "").trim();
  if (!name) throw new Error("候选人姓名不能为空");
  if (payload.conversation_id) return locateBossConversationByID(currentPage, name, String(payload.conversation_id), false, signal);

  // 已在搜索态则复用输入框，否则点击搜索按钮打开。
  let searchInput = currentPage
    .locator(".chat-job-search .search-input")
    .first();
  if (!(await searchInput.isVisible({ timeout: 1000 }).catch(() => false))) {
    const searchBtn = currentPage.locator(".chat-search-btn").first();
    await searchBtn.click({ timeout: 5000 });
    searchInput = currentPage
      .locator(".chat-job-search .search-input")
      .first();
    await searchInput.waitFor({ state: "visible", timeout: 5000 });
  }

  // 清空并输入候选人姓名，等待过滤弹层渲染。
  await searchInput.click();
  await searchInput.fill("");
  await searchInput.type(name, { delay: 60 });

  // 姓名必须精确匹配且可见结果唯一，同名或部分匹配时不点击。
  const listItems = currentPage.locator(".geek-search-list ul li");
  const deadline = Date.now() + 6000;
  let matchedText = "";
  let resumeStatus = "unknown";
  while (Date.now() < deadline) {
    const matches = [];
    const count = await listItems.count();
    for (let i = 0; i < count; i++) {
      const item = listItems.nth(i);
      if (!(await item.isVisible())) continue;
      const nameField = item.locator(".search-right .content-text");
      if (await nameField.count() === 1) {
        if (!bossSearchNameMatches(await nameField.innerText(), name)) continue;
      } else {
        // 兼容旧页面的独立姓名元素，不把公司名或说明中的姓名当作候选人身份。
        if (!(await item.getByText(name, { exact: true }).count())) continue;
      }
      matches.push(item);
    }
    if (matches.length > 1) {
      return { found: false, panel_name: "", error: "候选人姓名对应多个搜索结果，已跳过，避免发错人" };
    }
    if (matches.length === 1) {
      const item = matches[0];
      matchedText = await item.innerText();
      const exchange = item.locator(".search-right .content-exchange");
      if (await exchange.count() === 1 && (await exchange.innerText()).trim() === "已获取简历") resumeStatus = "received";
      await item.click({ timeout: 3000 });
      break;
    }
    await currentPage.waitForTimeout(300);
  }
  if (!matchedText) {
    return { found: false, panel_name: "", error: "未找到精确匹配的候选人", elapsed_ms: Date.now() - startedAt };
  }

  // 等待聊天面板打开并读取面板姓名，供 Go 侧身份核对。
  const panelName = currentPage
    .locator(".chat-conversation .base-name")
    .first();
  const panelDeadline = Date.now() + 6000;
  let panelText = "";
  while (Date.now() < panelDeadline) {
    panelText = String(
      await panelName.innerText({ timeout: 1000 }).catch(() => ""),
    );
    if (panelText.trim() === name) break;
    await currentPage.waitForTimeout(300);
  }
  return {
    found: panelText.trim() === name,
    text: matchedText,
    panel_name: panelText.trim(),
    resume_status: resumeStatus,
    elapsed_ms: Date.now() - startedAt,
  };
}

/** bossSearchNameMatches 核对真实姓名字段；只接受完整姓名或已验证的“姓名_公司”格式。 */
export function bossSearchNameMatches(displayName, expectedName) {
  const display = String(displayName || "").trim();
  const name = String(expectedName || "").trim();
  return Boolean(name) && (display === name || display.startsWith(name + "_"));
}

/** waitBossSelectedID 只在唯一选中 ID 和面板姓名均匹配时返回，不采信旧 selected 或同名面板。 */
async function waitBossSelectedID(page, name, expectedID, timeout = 6000, numeric = false, signal) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    signal?.throwIfAborted();
    const selected = page.locator(".user-list .geek-item.selected");
    const panel = page.locator(".chat-conversation .base-name");
    const actualID = await selected.count() === 1 ? await selected.getAttribute("data-id") : "";
    const matches = numeric ? /^(\d+)-\d+$/.exec(actualID || "")?.[1] === expectedID : actualID === expectedID;
    if (matches && await panel.count() === 1 && (await panel.innerText()).trim() === name) {
      return { found: true, panel_name: name, conversation_id: actualID };
    }
    await page.waitForTimeout(200);
  }
  return { found: false, panel_name: "", conversation_id: "", error: "会话真实 ID 未匹配，已停止定位" };
}

/** locateBossConversationByID 按预期 ID 打开已加载会话；搜索同名结果只能逐个核对，不能输入或发送消息。 */
export async function locateBossConversationByID(page, name, expectedID, numeric = false, signal, encrypted = false) {
  signal?.throwIfAborted();
  const rows = page.locator(".user-list .geek-item");
  const direct = [];
  for (let i = 0; i < await rows.count(); i++) {
    const id = await rows.nth(i).getAttribute("data-id");
    if (!encrypted && (numeric ? /^(\d+)-\d+$/.exec(id || "")?.[1] === expectedID : id === expectedID)) direct.push(rows.nth(i));
  }
  if (direct.length > 1) throw new Error("会话真实 ID 对应多个入口，已停止定位");
  if (direct.length === 1) {
    signal?.throwIfAborted();
    await direct[0].click({ timeout: 3000 });
    return waitBossSelectedID(page, name, expectedID, 6000, numeric, signal);
  }
  // 每次重新打开搜索并读取结果，跳转后不复用旧 Locator 和旧 selected。
  for (let index = 0; index < 20; index++) {
    signal?.throwIfAborted();
    let input = page.locator(".chat-job-search .search-input").first();
    if (!(await input.isVisible().catch(() => false))) {
      signal?.throwIfAborted();
      await page.locator(".chat-search-btn").first().click({ timeout: 5000 });
      await input.waitFor({ state: "visible", timeout: 5000 });
    }
    signal?.throwIfAborted();
    await input.fill(name);
    const items = page.locator(".geek-search-list ul li");
    const deadline = Date.now() + 6000;
    let matches = [];
    while (Date.now() < deadline) {
      signal?.throwIfAborted();
      matches = [];
      for (let i = 0; i < await items.count(); i++) {
        const item = items.nth(i);
        if (!(await item.isVisible())) continue;
        const field = item.locator(".search-right .content-text");
        const match = await field.count() === 1 ? bossSearchNameMatches(await field.innerText(), name) : await item.getByText(name, { exact: true }).count() === 1;
        if (match) matches.push(item);
      }
      if (matches.length) break;
      await page.waitForTimeout(200);
    }
    if (index >= matches.length) break;
    // 搜索项没有 ID。数字目标必须等本次点击产生的详情响应，旧 selected 即使同名也不能放行。
    const expectedNumericID = numeric ? expectedID : /^(\d+)-\d+$/.exec(expectedID)?.[1];
    const freshDetail = expectedNumericID || encrypted ? page.waitForResponse(response => {
      try {
        const url = new URL(response.url());
        return url.protocol === "https:" && url.hostname === "www.zhipin.com" && url.pathname === "/wapi/zpjob/chat/geek/info";
      } catch { return false; }
    }, { timeout: 6000 }).then(response => response.json()).catch(() => null) : null;
    signal?.throwIfAborted();
    await matches[index].click({ timeout: 3000 });
    let selectedExpectedID = expectedID;
    if (freshDetail) {
      const detail = await freshDetail;
      signal?.throwIfAborted();
      const uid = detail?.zpData?.data?.uid;
      if (detail?.code !== 0) continue;
      if (encrypted) {
        if (detail.zpData?.data?.encryptUid !== expectedID || !Number.isSafeInteger(uid) || uid <= 0) continue;
        selectedExpectedID = String(uid);
      } else if (String(uid || "") !== expectedNumericID) continue;
    }
    const selected = await waitBossSelectedID(page, name, selectedExpectedID, 6000, numeric || encrypted, signal);
    if (selected.found) return selected;
  }
  return { found: false, panel_name: "", conversation_id: "", error: "未找到预期会话 ID，不能按姓名发送" };
}

/** locateBossConversationByNumericID 用响应给出的数字身份搜索并读取真实完整会话 ID，不合成后缀。 */
export async function locateBossConversationByNumericID(page, name, numericID, signal) {
  if (!/^[1-9]\d{0,15}$/.test(numericID) || !name) return { found: false };
  return locateBossConversationByID(page, name, numericID, true, signal);
}

/** locateBossConversationByEncryptedID 从本次点击的详情响应核对完整加密 ID，补齐复用已打开页面时缺失的观察事实。 */
export async function locateBossConversationByEncryptedID(page, name, encryptedID, signal) {
  if (!encryptedID || !name) return { found: false };
  return locateBossConversationByID(page, name, encryptedID, false, signal, true);
}
