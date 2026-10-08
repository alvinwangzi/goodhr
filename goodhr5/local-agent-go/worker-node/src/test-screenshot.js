// 本文件通过标准截图与真实滚轮诊断受控页面；显式传入测试 URL，不自动访问招聘账号或点击候选人。
import { chromium } from "playwright-core";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { nativeViewport, observeScrollAtPointer } from "./native-page-observation.js";

/** main 保存滚动分段截图，不注入脚本、不修改 DOM 或滚动属性。 */
async function main(){
  const url=process.argv[2];if(!url)throw new Error("请传入受控页面 URL：node src/test-screenshot.js http://127.0.0.1:端口/页面");
  const browser=await chromium.launch({channel:"msedge",headless:true});
  try{
    const page=await browser.newPage({viewport:{width:1280,height:900}});await page.goto(url);
    const viewport=await nativeViewport(page);const point={x:viewport.width/2,y:viewport.height/2};
    await page.mouse.move(point.x,point.y);
    const directory=await fs.mkdtemp(path.join(os.tmpdir(),"hrplus-detail-observation-"));
    let previous;
    for(let i=0;i<12;i++){
      const png=await page.screenshot({type:"png",scale:"css"});if(previous?.equals(png))break;
      await fs.writeFile(path.join(directory,`part-${i+1}.png`),png);previous=png;
      const info=await observeScrollAtPointer(page,point);console.log(JSON.stringify({part:i+1,source:info.source,observed_offset:info.scrollTop,can_scroll_down:info.can_scroll_down}));
      if(!info.can_scroll_down)break;
      await page.mouse.wheel(0,Math.round(viewport.height*.7));await page.waitForTimeout(300);
    }
    console.log("截图目录："+directory);
  }finally{await browser.close()}
}
main().catch(error=>{console.error(error.message);process.exitCode=1});