// 本文件使用独立本地页面与系统 Edge 验证 Boss 完整候选人 ID，测试页面不使用脚本注入。
import assert from "node:assert/strict";
import http from "node:http";
import { before, after, test } from "node:test";
import { chromium } from "playwright-core";
import { readBossRecommendationID, matchBossRecommendationID, isBossRecommendationGuide } from "../src/boss-candidate-identity.js";

let server, browser, baseURL;

// 启动只包含静态 HTML 的独立页面，避免依赖业务账号或发送消息。
before(async () => {
 server=http.createServer((req,res)=>{
  res.setHeader("Content-Type","text/html; charset=utf-8");
  const cards=req.url.includes("reorder")?["opaque-B-0","opaque-A-0"]:["opaque-A-0","opaque-B-0"];
  res.end(cards.map(id=>`<section class="candidate-card-wrap"><div class="card-inner" data-geekid="${id}"><span>张三 29岁 某公司</span></div></section>`).join("")+`<section id="missing">张三</section><section id="guide" class="anonymous-geek-guide-card">热搜牛人推荐</section><section id="ambiguous"><div class="card-inner" data-geekid="one"></div><div class="card-inner" data-geekid="two"></div></section>`);
 });
 await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
 baseURL=`http://127.0.0.1:${server.address().port}`;
 browser=await chromium.launch({channel:"msedge",headless:true});
});

// 清理测试创建的浏览器与服务。
after(async()=>{await browser?.close();if(server)await new Promise(resolve=>server.close(resolve));});

test("同名同龄同公司仍按完整 ID 区分，重新排序重新定位",async()=>{
 const page=await browser.newPage();try{
  await page.goto(baseURL);
  let cards=await page.locator(".candidate-card-wrap").all();
  assert.equal(await readBossRecommendationID(cards[0]),"opaque-A-0");
  assert.deepEqual(await matchBossRecommendationID(cards,"opaque-B-0"),{index:1,score:1});
  await page.goto(baseURL+"/reorder");cards=await page.locator(".candidate-card-wrap").all();
  assert.deepEqual(await matchBossRecommendationID(cards,"opaque-B-0"),{index:0,score:1});
  assert.equal(await matchBossRecommendationID(cards,"boss_张三_29"),null);
 }finally{await page.close()}
});

test("缺少或多个真实 ID 不猜测，重复 ID 拒绝操作",async()=>{
 const page=await browser.newPage();try{
  await page.goto(baseURL);
  assert.equal(await readBossRecommendationID(page.locator("#missing")),"");
  assert.equal(await isBossRecommendationGuide(page.locator("#guide")),true);
  assert.equal(await isBossRecommendationGuide(page.locator("#missing")),false);
  assert.equal(await readBossRecommendationID(page.locator("#ambiguous")),"");
  const card=page.locator(".candidate-card-wrap").first();
  await assert.rejects(matchBossRecommendationID([card,card],"opaque-A-0"),/多个卡片/);
 }finally{await page.close()}
});
