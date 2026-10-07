// 本文件读取 Boss 卡片中已经沟通和已收简历的确定事实，仅使用标准 Locator，不点击或注入脚本。

/** readBossCandidateState 读取可见状态控件，返回页面事实，不推断实际沟通时间。 */
export async function readBossCandidateState(card, continueSelectors = []) {
  let contacted = false;
  for (const selector of continueSelectors) {
    const buttons = card.locator(selector);
    for (let i = 0; i < await buttons.count(); i++) {
      if (await buttons.nth(i).isVisible()) contacted = true;
    }
  }
  for (const text of ["继续沟通", "已沟通"]) {
    const labels = card.getByText(text, { exact: true });
    for (let i = 0; i < await labels.count(); i++) {
      if (await labels.nth(i).isVisible()) contacted = true;
    }
  }
  const received = card.getByText("已获取简历", { exact: true });
  let resumeReceived = false;
  for (let i = 0; i < await received.count(); i++) {
    if (await received.nth(i).isVisible()) resumeReceived = true;
  }
  return { contact_observed: contacted || resumeReceived, resume_status: resumeReceived ? "received" : "unknown" };
}
