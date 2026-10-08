// 本文件用独立静态 iframe 与系统 Edge 验证 HRPlus 三锚点恢复和真实滚轮，禁止页面脚本注入。
import assert from "node:assert/strict";
import http from "node:http";
import { before, after, test } from "node:test";
import { chromium } from "playwright-core";
import { captureRecommendationAnchors, checkRecommendationAnchors, rewindRecommendation, ensureRecommendationVisible } from "../src/boss-recommendation-resume.js";

let server, browser, base;

// 启动纯静态页面，列表内有足够多的候选人，以验证快速路径不会逐人遍历历史。
before(async () => {
 server=http.createServer((req,res)=>{
  res.setHeader("Content-Type","text/html; charset=utf-8");
  const url=new URL(req.url,"http://fixture.invalid");
  if(url.pathname==="/recommend"){
   let ids=Array.from({length:80},(_,i)=>`candidate-${i}`);
   if(url.searchParams.get("variant")==="reorder") [ids[16],ids[17]]=[ids[17],ids[16]];
   if(url.searchParams.get("variant")==="new-first") ids[0]="new-candidate";
   if(url.searchParams.get("variant")==="missing") ids=ids.slice(0,10);
   res.end(`<style>body{margin:0}.list{height:420px;overflow:auto}.candidate-card-wrap{height:100px;box-sizing:border-box;border:1px solid #ccc}</style><div class="list">${ids.map(id=>`<section class="candidate-card-wrap"><div class="card-inner" data-geekid="${id}">同名 同龄 同公司</div></section>`).join("")}</div>`);
  }else{
   res.end(`<a href="/?menu=chat">沟通</a><a href="/">推荐牛人</a>${url.searchParams.get("menu")==="chat"?"沟通页":`<iframe name="recommendFrame" style="width:780px;height:450px" src="/recommend?jobid=job&status=0&source=${url.searchParams.get("source")||"0"}&variant=${url.searchParams.get("variant")||""}"></iframe>`}`);
  }
 });await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));base=`http://127.0.0.1:${server.address().port}`;
 browser=await chromium.launch({channel:"msedge",headless:true});
});

// 关闭本次测试创建的浏览器和服务。
after(async()=>{await browser?.close();if(server)await new Promise(resolve=>server.close(resolve));});

test("三 ID 相邻匹配时快速继续，菜单来回不复用旧 DOM",async()=>{
 const page=await browser.newPage({viewport:{width:900,height:650}});try{
  await page.goto(base);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  const cursor=await captureRecommendationAnchors(page,{anchors:["candidate-15","candidate-16","candidate-17"]});
  assert.equal(cursor.valid,true);assert.equal(cursor.start_index,15);
  await page.getByText("沟通",{exact:true}).click();await page.getByText("推荐牛人",{exact:true}).click();
  await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  assert.equal((await checkRecommendationAnchors(page,{cursor})).matched,true);
 }finally{await page.close()}
});

test("顺序、缺失、筛选和零锚点均回退，一两人也核对全部已有锚点",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  const cursor=await captureRecommendationAnchors(page,{anchors:["candidate-15","candidate-16","candidate-17"]});
  for(const suffix of ["?variant=reorder","?variant=missing","?source=2"]){
   await page.goto(base+suffix);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
   assert.equal((await checkRecommendationAnchors(page,{cursor})).matched,false);
  }
  await page.goto(base);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  for(const anchors of [["candidate-0"],["candidate-0","candidate-1"]]){
   const short=await captureRecommendationAnchors(page,{anchors});assert.equal((await checkRecommendationAnchors(page,{cursor:short})).matched,true);
  }
  assert.equal((await captureRecommendationAnchors(page,{anchors:[]})).valid,false);
  assert.equal((await captureRecommendationAnchors(page,{anchors:["candidate-0","candidate-2"]})).valid,false);
 }finally{await page.close()}
});

test("局部锚点不变仍走快速路径，不承诺锚点前新增者已处理",async()=>{
 const page=await browser.newPage();try{
  await page.goto(base);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  const cursor=await captureRecommendationAnchors(page,{anchors:["candidate-15","candidate-16","candidate-17"]});
  await page.goto(base+"?variant=new-first");await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  const result=await checkRecommendationAnchors(page,{cursor});assert.deepEqual(result,{matched:true,reason:"resume_anchor_match"});
 }finally{await page.close()}
});

test("真实滚轮定位第十八人并返回起点，不调用 evaluate",async()=>{
 const page=await browser.newPage({viewport:{width:900,height:650}});try{
  await page.goto(base);await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().waitFor();
  const located=await ensureRecommendationVisible(page,{recommendation_candidate_id:"candidate-17"});
  assert.equal(located.by_identity,true);assert.ok(located.attempts>1);
  assert.equal(await located.card.locator(".card-inner").getAttribute("data-geekid"),"candidate-17");
  assert.equal((await rewindRecommendation(page)).rewound,true);
  const first=await page.frameLocator('iframe[name="recommendFrame"]').locator(".candidate-card-wrap").first().boundingBox();
  assert.ok(first.y>=0);
 }finally{await page.close()}
});
