// 本文件在 Windows 独立进程中执行完整 Worker HTTP 路由和已安装真实 CloakBrowser，受控页面不会联系招聘网站。
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

/** freePort 分配独立测试端口，不清理或复用其他进程。 */
async function freePort() { const server = net.createServer(); await new Promise(resolve => server.listen(0, "127.0.0.1", resolve)); const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port; }

test("完整 Worker 以零脚本模式启动真实 CloakBrowser，核对账号、推荐锚点和聊天 ID", { timeout: 60000 }, async () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const binary = process.env.HRPLUS_M1_BROWSER_PATH || path.join(process.env.APPDATA || "", "HRPlus", "runtime", "cloakbrowser", "chrome.exe");
  await fs.access(binary);
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), "hrplus-m1-worker-native-"));
  const ledgerPath = path.join(directory, "ledger.json");
  const port = await freePort();
  const worker = spawn(process.execPath, ["--import", pathToFileURL(path.join(root, "test", "fixtures", "worker-native-preload.mjs")).href, path.join(root, "src", "index.js")], { cwd: root, windowsHide: true, env: { ...process.env, GOODHR_WORKER_ADDR: `127.0.0.1:${port}`, GOODHR_WORKER_PORT_END: String(port), CLOAKBROWSER_BINARY_PATH: binary, HRPLUS_M1_FIXTURE_LEDGER: ledgerPath }, stdio: ["pipe", "pipe", "pipe"] });
  let output = "";
  worker.stdout.on("data", bytes => { output += bytes.toString(); }); worker.stderr.on("data", bytes => { output += bytes.toString(); });
  const base = `http://127.0.0.1:${port}`;
  /** request 使用真实 HTTP 契约，不替换 Worker 页面动作。 */
  async function request(route, body) { const response = await fetch(base + route, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ no_script: true, ...body }) }); const result = await response.json(); return { response, result }; }
  try {
    const deadline = Date.now() + 10000;
    while (true) { try { await fetch(base + "/health"); break; } catch { if (Date.now() > deadline || worker.exitCode !== null) throw new Error("Worker 未启动：" + output.slice(-1200)); await new Promise(resolve => setTimeout(resolve, 100)); } }
    const launch = await request("/api/v1/browser/start", { humanize: true, headless: true, user_data_dir: path.join(directory, "profile"), downloads_path: path.join(directory, "downloads") });
    assert.equal(launch.result.ok, true, JSON.stringify(launch.result));
    const opened = await request("/api/v1/page/open", { url: "https://www.zhipin.com/web/chat/recommend" });
    assert.equal(opened.result.ok, true, JSON.stringify(opened.result));
    const account = await request("/api/v1/boss/account/identity", {});
    assert.equal(account.result.data.account_id, "901");
    const extracted = await request("/api/v1/boss/candidates/extract", { platform_config: { id: "boss", card: { item: ".candidate-card-wrap", fields: { name: ".card-inner" } } } });
    assert.equal(extracted.result.ok, true, JSON.stringify(extracted.result));
    assert.deepEqual(extracted.result.data.candidates.map(row => row.recommendation_candidate_id), ["opaque-A", "opaque-B", "opaque-C", "opaque-D"]);
    const anchors = await request("/api/v1/boss/candidates/capture-anchors", { anchors: ["opaque-A", "opaque-B", "opaque-C"] });
    assert.equal(anchors.result.ok, true, JSON.stringify(anchors.result));
    assert.equal(anchors.result.data.valid, true);
    const chat = await request("/api/v1/page/open", { url: "https://www.zhipin.com/web/chat/index", expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(chat.result.ok, true, JSON.stringify(chat.result));
    const identity = await request("/api/v1/boss/candidates/identity", { recommendation_id: "opaque-A", candidate_name: "同名候选人 A", expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(identity.result.data.conversation_id, "123-0");
    const typed = await request("/api/v1/page/type", { selector_spec: { selectors: ["#draft"] }, text: "仅虚构测试", expected_account_platform: "boss", expected_platform_account_id: "901", delay_min_ms: 0, delay_max_ms: 0, typing_delay_ms: 0 });
    assert.equal(typed.result.ok, true, JSON.stringify(typed.result));
    const draft = await request("/api/v1/page/extract-text", { selector_spec: { selectors: ["#draft"] }, editable: true, expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(draft.result.data.text, "仅虚构测试");
    const sent = await request("/api/v1/page/click", { selector_spec: { selectors: ["#send"] }, expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(sent.result.ok, true, JSON.stringify(sent.result));
    const returned = await request("/api/v1/page/click", { selector_spec: { selectors: ['dl a[href="/web/chat/recommend"]'] }, expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(returned.result.ok, true, JSON.stringify(returned.result));
    const checked = await request("/api/v1/boss/candidates/check-anchors", { cursor: anchors.result.data, expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(checked.result.data.matched, true);
    const changedAccount = await request("/api/v1/page/open", { url: "https://www.zhipin.com/web/chat/index?fixtureAccount=902", expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(changedAccount.result.ok, true, JSON.stringify(changedAccount.result));
    const blocked = await request("/api/v1/page/click", { selector_spec: { selectors: ["#send"] }, expected_account_platform: "boss", expected_platform_account_id: "901" });
    assert.equal(blocked.result.ok, false);
    assert.match(blocked.result.msg, /登录账号已变化/);
    const ledger = JSON.parse(await fs.readFile(ledgerPath, "utf8"));
    assert.equal(ledger.launches[0].humanize, false);
    assert.ok(ledger.requests.includes("/wapi/zpjob/rec/geek/list"));
    assert.equal(ledger.clicks, 1);
    const resume = await request("/api/v1/page/open", {url:"https://www.zhipin.com/web/chat/resume-fixture"});
    assert.equal(resume.result.ok,true);
    const captured = await request("/api/v1/page/screenshot", {selector:".resume-detail-wrap",scroll_full:true,directory,filename:"resume-test.png"});
    assert.equal(captured.result.ok,true,JSON.stringify(captured.result)+output.slice(-9000));
    assert.ok(captured.result.data.parts_count >= 4,JSON.stringify(captured.result.data));
    const captureDebug=JSON.parse(captured.result.data._scroll_debug);
    assert.equal(captureDebug.rounds.at(-1).maxed,true);
    assert.equal(captureDebug.rounds.at(-1).beforeShot.maxed,true);
    await request("/api/v1/page/open", {url:"https://www.zhipin.com/web/chat/spa-fixture"});
    const currentAccount=await request("/api/v1/boss/account/identity", {});
    assert.equal(currentAccount.result.data.account_id,"902");
    const sameDocument=await request("/api/v1/page/click", {selector_spec:{selectors:['a[href="#recommend"]']},expected_account_platform:"boss",expected_platform_account_id:"902"});
    assert.equal(sameDocument.result.ok,true);
    const afterMenu=await request("/api/v1/page/extract-text", {selector_spec:{selectors:["#same-document"]},expected_account_platform:"boss",expected_platform_account_id:"902"});
    assert.equal(afterMenu.result.ok,true,JSON.stringify(afterMenu.result));
  } finally {
    await request("/api/v1/browser/stop", {}).catch(() => {});
    worker.kill();
    await new Promise(resolve => { if (worker.exitCode !== null) resolve(); else worker.once("exit", resolve); });
  }
});
