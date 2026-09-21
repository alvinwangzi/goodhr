/** 本文件验证全站统一蓝色主题的关键色值。 */

import assert from "node:assert/strict";
import test from "node:test";
import { createGoodHRTheme } from "./theme.ts";

test("全站使用统一浅色蓝色主题", () => {
  const theme = createGoodHRTheme();

  assert.equal(theme.palette.mode, "light");
  assert.equal(theme.palette.primary.main, "#0052CC");
  assert.equal(theme.palette.primary.light, "#EBF0FF");
  assert.equal(theme.palette.background.paper, "#ffffff");
});

test("业务状态色不受主题调整影响", () => {
  const theme = createGoodHRTheme();

  assert.equal(theme.palette.success.main, "#238653");
  assert.equal(theme.palette.success.light, "#eaf5ee");
  assert.equal(theme.palette.warning.main, "#c47a1a");
  assert.equal(theme.palette.error.main, "#c83f49");
});
