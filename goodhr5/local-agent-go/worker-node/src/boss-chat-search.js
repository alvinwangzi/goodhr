// 本文件通过标准 Locator 搜索 Boss 聊天会话，拒绝同名歧义并等待正确面板；不注入页面脚本。
/** 按姓名搜索唯一可见结果并打开聊天面板，返回页面读取的身份信息。 */
export async function searchBossChatSessionOnPage(currentPage, payload) {
  const startedAt = Date.now();
  const name = String(payload.candidate_name || payload.name || "").trim();
  if (!name) throw new Error("候选人姓名不能为空");

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
