/** 本文件验证浏览器显示校准失败时返回可供用户自行调整的错误说明。 */
import assert from "node:assert/strict";
import test from "node:test";

import {
  browserDisplayAdjustmentMessage,
  readBrowserViewportSize,
} from "./browser-display.js";

/** 验证 90% 页面缩放会生成包含当前尺寸、缩放比例和处理方法的错误说明。 */
test("browserDisplayAdjustmentMessage explains how to fix 90 percent zoom", () => {
  const message = browserDisplayAdjustmentMessage({
    target_width: 1440,
    target_height: 900,
    inner_width: 1600,
    inner_height: 1000,
  });

  assert.match(message, /期望视口 1440x900/);
  assert.match(message, /实际 1600x1000/);
  assert.match(message, /缩放约 90%/);
  assert.match(message, /任务已停止，浏览器会保持打开/);
  assert.match(message, /Ctrl\+0/);
});

/** 验证固定 viewport 存在时优先使用 Playwright 返回的尺寸。 */
test("readBrowserViewportSize prefers configured Playwright viewport", async () => {
  const viewport = await readBrowserViewportSize({
    viewportSize: () => ({ width: 1440, height: 900 }),
  });

  assert.deepEqual(viewport, {
    width: 1440,
    height: 900,
    source: "playwright-viewport",
  });
});

/** 验证原生窗口通过 CSS 像素截图读取尺寸，脚本能力不被调用。 */
test("readBrowserViewportSize reads CSS screenshot for native viewport", async () => {
  const png=Buffer.alloc(24);png.write("PNG",1,"ascii");png.writeUInt32BE(1180,16);png.writeUInt32BE(650,20);
  const viewport = await readBrowserViewportSize({
    viewportSize: () => null,
    screenshot:async options=>{assert.equal(options.scale,"css");return png},
    evaluate:()=>{throw new Error("不允许脚本读取")},
  });

  assert.equal(viewport.width, 1180);
  assert.equal(viewport.height, 650);
  assert.equal(viewport.source, "css-screenshot");
  assert.equal(viewport.device_pixel_ratio, undefined);
});

/** 验证页面暂时无法读取尺寸时仍返回安全兜底视口。 */
test("readBrowserViewportSize keeps a non-zero fallback", async () => {
  const viewport = await readBrowserViewportSize(null);

  assert.equal(viewport.width, 1280);
  assert.equal(viewport.height, 900);
  assert.equal(viewport.source, "fallback");
});
