// 本文件只在独立 Worker 测试进程中包装真实 CloakBrowser 启动，全部请求由虚构页面接管，不连接 Boss 或读取用户登录数据。
import { registerHooks } from "node:module";
import { writeFileSync } from "node:fs";
import { combinedChatFixture } from "./combined-chat-fixture.mjs";

const ledger = { launches: [], requests: [], clicks: 0, sendOrder: [], greetOrder: [], timeline: [] };
let account = 901;
const accountPath = "/wapi/zpuser/wap/getUserInfo.json";

/** saveLedger 保存虚构运行证据，不包含 Cookie、令牌或真实候选人内容。 */
function saveLedger() { writeFileSync(process.env.HRPLUS_M1_FIXTURE_LEDGER, JSON.stringify(ledger)); }

/** fixtureRoute 接管隔离浏览器的全部请求，已知路径返回受控页面，其他请求全部中止。 */
async function fixtureRoute(route) {
  const url = new URL(route.request().url());
  if (url.hostname !== "www.zhipin.com") return route.abort();
  ledger.requests.push(url.pathname); saveLedger();
  if (url.pathname === accountPath) return route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { userId: account } }) });
  if (url.pathname === "/wapi/zpjob/rec/geek/list") return route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { geekList: ["A", "B", "C", "D"].map((suffix, i) => ({ encryptGeekId: "opaque-" + suffix, geekCard: { geekId: 123 + i, encGeekId: "opaque-" + suffix } })) } }) });
  if (url.pathname === "/wapi/zprelation/friend/getBossFriendListV2.json") return route.fulfill({ contentType: "application/json", body: JSON.stringify({ code: 0, zpData: { friendList: [{ uid: 123, encryptUid: "opaque-A" }, { uid: 124, encryptUid: "opaque-B" }] } }) });
  if (url.pathname === "/wapi/zpjob/chat/geek/info") { const uid=Number(url.searchParams.get('uid')); return route.fulfill({contentType:'application/json',body:JSON.stringify({code:0,zpData:{data:{uid,encryptUid:uid===123?'opaque-A':'opaque-B'}}})}); }
  if (url.pathname === "/fixture/click") { ledger.clicks++; if(url.searchParams.has('uid')){ledger.sendOrder.push(Number(url.searchParams.get('uid')));ledger.timeline.push('message:'+url.searchParams.get('uid'));} saveLedger(); return route.fulfill({ contentType: "application/json", body: "{}" }); }
  if (url.pathname === '/fixture/greet') {ledger.greetOrder.push(url.searchParams.get('id'));ledger.timeline.push('greet:'+url.searchParams.get('id'));saveLedger();return route.fulfill({contentType:'application/json',body:'{}'});}
  if (url.pathname === '/web/frame/recommend/' && process.env.HRPLUS_M1_FIXTURE_MODE?.startsWith('triple')) return route.fulfill({contentType:'text/html; charset=utf-8',body:`<style>body{margin:0}.candidate-card-wrap{height:100px;border:1px solid #ddd}</style>${(process.env.HRPLUS_M1_FIXTURE_MODE==='triple-rescan-job'&&ledger.clicks>0?['E','D','C']:['C','D','E']).map(id=>`<section class="candidate-card-wrap"><div class="card-inner" data-geekid="opaque-${id}"><span class="candidate-name">虚构候选人 ${id}</span><button class="greet-btn" onclick="this.textContent='继续沟通';this.className='continue-btn';fetch('/fixture/greet?id=opaque-${id}')">打招呼</button></div></section>`).join('')}`});
  if (url.pathname === "/web/frame/recommend/") return route.fulfill({ contentType: "text/html; charset=utf-8", body: `<style>.candidate-card-wrap{height:120px;border:1px solid #ddd}</style>${["A", "B", "C", "D"].map(suffix => `<section class="candidate-card-wrap"><div class="card-inner" data-geekid="opaque-${suffix}">同名候选人 ${suffix}</div></section>`).join("")}<iframe src="/wapi/zpjob/rec/geek/list"></iframe>` });
  if (!url.pathname.startsWith("/web/chat/")) return route.abort();
  if (url.pathname === "/web/chat/spa-fixture") return route.fulfill({contentType:"text/html; charset=utf-8",body:`<a href="#recommend">推荐牛人</a><p id="same-document">菜单切换后的页面</p><iframe src="${accountPath}"></iframe>`});
  if (url.pathname === "/web/chat/resume-fixture") return route.fulfill({contentType:"text/html; charset=utf-8",body:`<style>body{margin:0;height:12000px}.resume-detail-wrap{position:fixed;left:80px;top:20px;width:700px;height:380px;background:white}.resume-content{height:360px;overflow:auto}.row{height:150px;border-bottom:1px solid #ddd}</style><div class="resume-detail-wrap"><div class="resume-content"><div class="resume-body">${Array.from({length:9},(_,i)=>`<div class="row">虚构在线简历第 ${i+1} 段</div>`).join("")}<div class="row">简历末尾标记</div></div></div></div><iframe hidden src="${accountPath}"></iframe>`});
  if (url.searchParams.has("fixtureAccount")) account = Number(url.searchParams.get("fixtureAccount"));
  if ((process.env.HRPLUS_M1_FIXTURE_MODE === 'combined-job' || process.env.HRPLUS_M1_FIXTURE_MODE?.startsWith('triple')) && !url.pathname.includes('recommend')) return route.fulfill({contentType:'text/html; charset=utf-8',body:combinedChatFixture({ready:!['triple-timed-job','triple-rescan-job'].includes(process.env.HRPLUS_M1_FIXTURE_MODE) || ledger.greetOrder.length>0})});
  if (process.env.HRPLUS_M1_FIXTURE_MODE?.startsWith('triple') && url.pathname.includes('recommend')) return route.fulfill({contentType:'text/html; charset=utf-8',body:`<style>dl{position:fixed;top:0;left:0;z-index:10;background:white}body{padding-top:45px}</style><dl><a href="/web/chat/recommend">推荐牛人</a><a href="/web/chat/index">沟通</a></dl><div class="current-position">Go</div><button class="switch-position">岗位</button><div class="position-list"><div class="position-item"><span class="position-name">Go</span></div></div><iframe name="recommendFrame" style="width:700px;height:380px" src="/web/frame/recommend/?jobid=job1&status=0&filterParams=&source=0"></iframe><iframe src="${accountPath}"></iframe>`});
  if (process.env.HRPLUS_M1_FIXTURE_MODE === "empty-job" && !url.pathname.includes("recommend")) {
    return route.fulfill({ contentType: "text/html; charset=utf-8", body: `<dl><a href="/web/chat/recommend">推荐牛人</a><a href="/web/chat/index">沟通</a></dl><div class="job-select"><ul class="ui-dropmenu-list"><li>Go</li></ul></div><div class="chat-message-filter-left"><span>未读</span></div><div class="user-list"></div><iframe src="${accountPath}"></iframe>` });
  }
  if (["reply-job", "regreet-job"].includes(process.env.HRPLUS_M1_FIXTURE_MODE) && !url.pathname.includes("recommend")) {
    const initialDirection = process.env.HRPLUS_M1_FIXTURE_MODE === "regreet-job" ? "item-myself" : "item-friend";
    return route.fulfill({ contentType: "text/html; charset=utf-8", body: `<dl><a href="/web/chat/recommend">推荐牛人</a><a href="/web/chat/index">沟通</a></dl><div class="job-select"><ul class="ui-dropmenu-list"><li>Go</li></ul></div><div class="chat-message-filter-left"><span onclick="if(window.fixtureReplied)document.querySelector('.geek-item').hidden=true">未读</span></div><div class="user-list"><div class="geek-item selected" data-id="123-0"><span class="geek-name">同名候选人 A</span><span class="source-job">Go</span></div></div><div class="chat-conversation"><span class="base-name">同名候选人 A</span><span class="source-job">Go</span><div class="chat-message-list"><div class="message-item"><span class="message-time"><span class="time">2026-10-08T09:00:00+08:00</span></span><div class="${initialDirection}">请问岗位还招吗？</div></div></div><div class="conversation-editor"><input class="boss-chat-editor-input"><button class="submit" onclick="fixtureSend()">发送</button></div></div><iframe src="${accountPath}"></iframe><iframe src="/wapi/zprelation/friend/getBossFriendListV2.json"></iframe><script>function fixtureSend(){const input=document.querySelector('.boss-chat-editor-input');const item=document.createElement('div');item.className='message-item';item.innerHTML='<span class="message-time"><span class="time">2026-10-08T09:00:01+08:00</span></span>';const text=document.createElement('div');text.className='item-myself';text.textContent=input.value;item.append(text);document.querySelector('.chat-message-list').append(item);input.value='';window.fixtureReplied=true;fetch('/fixture/click')}</script>` });
  }
  const content = url.pathname.includes("recommend") ? '<iframe name="recommendFrame" style="width:900px;height:640px" src="/web/frame/recommend/?jobid=job1&status=0&filterParams=&source=0"></iframe>' : '<div class="user-list"><div class="geek-item selected" data-id="123-0">同名候选人 A</div></div><div class="chat-conversation"><span class="base-name">同名候选人 A</span></div><input id="draft"><button id="send" onclick="fetch(\'/fixture/click\')">虚构发送</button><iframe src="/wapi/zprelation/friend/getBossFriendListV2.json"></iframe>';
  return route.fulfill({ contentType: "text/html; charset=utf-8", body: `<dl><a href="/web/chat/recommend">推荐牛人</a><a href="/web/chat/index">沟通</a></dl>${content}<iframe src="${accountPath}"></iframe>` });
}

/** prepareContext 在真实浏览器的标准路由层安装受控响应，不执行页面脚本。 */
export async function prepareContext(context, options) {
  ledger.launches.push({ humanize: options.humanize, headless: options.headless }); saveLedger();
  await context.route("**/*", fixtureRoute);
  return context;
}

registerHooks({
  /** load 仅包装 SDK 的启动边界，实际浏览器与 Worker HTTP 路由保持生产实现。 */
  load(url, context, nextLoad) {
    if (!url.endsWith("/cloakbrowser/dist/index.js")) return nextLoad(url, context);
    const original = new URL("./playwright.js", url).href;
    const harness = import.meta.url;
    return { format: "module", shortCircuit: true, source: `import * as actual from ${JSON.stringify(original)}; import {prepareContext} from ${JSON.stringify(harness)};
      export async function launchPersistentContext(options){if(options.humanize!==false)throw new Error('禁止安装 SDK 页面脚本人性化层');const fixtureOptions={...options,headless:true};const context=await actual.launchPersistentContext(fixtureOptions);return prepareContext(context,fixtureOptions)}
      export async function launch(options){if(options.humanize!==false)throw new Error('禁止安装 SDK 页面脚本人性化层');const fixtureOptions={...options,headless:true};const browser=await actual.launch(fixtureOptions);const create=browser.newContext.bind(browser);browser.newContext=async settings=>prepareContext(await create(settings),fixtureOptions);return browser}` };
  },
});
