// 本文件通过真实 Edge 验证 HRPlus 标准坐标观察、固定标题滚动和遮挡检查，不注入页面代码。
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import { before, after, test } from "node:test";
import { chromium } from "playwright-core";
import { nativeViewport, observeContainer, observeScrollAtPointer } from "../src/native-page-observation.js";
import { pointHitsTarget } from "../src/hliepin-stable-click.js";
import { BrowserOverlayActions } from "../src/browser-actions.js";

let server,browser,base;

// 创建有足够长内容的独立页面，所有文案和 ID 均为测试数据。
before(async()=>{
 server=http.createServer((req,res)=>{res.setHeader("Content-Type","text/html; charset=utf-8");
  if(req.url==="/frame")res.end(`<iframe name="detail" src="/sticky" style="width:700px;height:500px"></iframe>`);
  else res.end(`<style>body{margin:8px}.content{width:600px;height:400px;overflow:auto}.row{height:120px}header{height:30px;${req.url==="/sticky"?"position:sticky;top:0;background:white":""}}</style><section class="content"><header>固定标题</header>${Array.from({length:12},(_,i)=>`<div class="row">测试简历第${i+1}段</div>`).join("")}</section><button id="covered" style="position:fixed;left:700px;top:0;width:80px;height:50px">操作</button><div style="position:fixed;left:700px;top:0;width:80px;height:50px;background:white">遮挡</div>`);
 });await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));base=`http://127.0.0.1:${server.address().port}`;
 browser=await chromium.launch({channel:"msedge",headless:true});
});

// 关闭本次测试创建的浏览器和服务。
after(async()=>{await browser?.close();if(server)await new Promise(resolve=>server.close(resolve));});

test("普通和固定标题的内容都能观察真实滚轮变化，滚到实际末尾再结束",async()=>{
 for(const route of ["/normal","/sticky"]){const page=await browser.newPage({viewport:{width:900,height:650}});try{
  await page.goto(base+route);const content=page.locator(".content");await page.mouse.move(300,200);
  let previous=await observeContainer(content);assert.equal(previous.scrollable,true);assert.equal(previous.measured_scroll_top,false);
  await page.mouse.wheel(0,240);await page.waitForTimeout(200);
  let current=await observeContainer(content);assert.ok(current.scrollTop>previous.scrollTop);assert.equal(current.can_scroll_down,true);assert.equal(current.scrollHeight,previous.scrollHeight);
  for(let i=0;i<10&&current.can_scroll_down;i++){await page.mouse.wheel(0,400);await page.waitForTimeout(120);current=await observeContainer(content)}
  assert.equal(current.can_scroll_down,false);assert.ok(current.scrollTop>=900);
  const last=await content.locator(".row").last().boundingBox();const box=await content.boundingBox();assert.ok(last.y+last.height<=box.y+box.height+2);
 }finally{await page.close()}}
});

test("悬停信息缺失时也能按标准坐标找到 iframe 内部滚动区域",async()=>{
 const page=await browser.newPage({viewport:{width:900,height:650}});try{
  await page.goto(base+"/frame");await page.frameLocator('iframe[name="detail"]').locator(".content").waitFor();
  await page.mouse.move(300,200);const before=await observeScrollAtPointer(page,{x:300,y:200});
  await page.mouse.wheel(0,240);await page.waitForTimeout(180);const after=await observeScrollAtPointer(page,{x:300,y:200});
  assert.equal(before.scrollable,true,JSON.stringify({before,after,hoverCounts:await Promise.all(page.frames().map(async frame=>({url:frame.url(),count:await frame.locator(":hover").count()})))}));assert.equal(after.source,"locator-geometry");assert.ok(after.scrollTop>before.scrollTop);
 }finally{await page.close()}
});

test("没有固定 viewport 也只用 CSS 截图读取尺寸，遮挡的 trial 不真实点击",async()=>{
 const page=await browser.newPage({viewport:null});try{
  await page.goto(base);const viewport=await nativeViewport(page);assert.equal(viewport.source,"css-screenshot");assert.ok(viewport.width>0&&viewport.height>0);
  assert.equal(await pointHitsTarget(page.locator("#covered"),{x:740,y:25}),false);
 }finally{await page.close()}
});

test("旧浮层入口明确不显示且不访问招聘页面",async()=>{
 const overlay=new BrowserOverlayActions({ensurePage:()=>{throw new Error("不应读取招聘页面")}});
 assert.equal((await overlay.showCard({title:"HRPlus"})).visible,false);assert.equal((await overlay.hideCard()).unsupported,true);
});

test("Worker 生产源码和截图诊断都没有页面脚本调用",async()=>{
 const files=await fs.readdir(new URL("../src/",import.meta.url));
 for(const file of files.filter(name=>name.endsWith(".js")&&!name.endsWith(".test.js"))){const source=await fs.readFile(new URL("../src/"+file,import.meta.url),"utf8");assert.doesNotMatch(source,/\.\s*(?:evaluate(?:All|Handle)?|\$eval|\$\$eval|addScriptTag|addInitScript|dispatchEvent)\s*\(/,"禁止脚本调用："+file)}
});
