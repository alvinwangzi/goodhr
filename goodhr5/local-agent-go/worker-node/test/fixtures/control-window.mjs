// 本文件在独立浏览器中打开 HRPlus 受控控制页，验证关闭启动页后后台运行继续，重开只读取状态。
import { launch } from "cloakbrowser";
import { writeFileSync } from "node:fs";

const [base, mode, resultPath] = process.argv.slice(2);
const browser = await launch({ headless: true, humanize: false });
try {
  const context = await browser.newContext();
  await context.route("**/*", route => route.request().url().startsWith(base + "/") ? route.continue() : route.abort());
  const page = await context.newPage();
  await page.goto(base + "/");
  if (mode === "start") {
    await page.getByRole("button", { name: "开始任务", exact: true }).click();
    await page.getByText("启动已确认", { exact: true }).waitFor();
  } else {
    await page.getByRole("button", { name: "读取状态", exact: true }).click();
    await page.getByText("状态已读取", { exact: true }).waitFor();
  }
  await page.close();
  writeFileSync(resultPath, JSON.stringify({ mode, closed: true }));
} finally { await browser.close(); }
