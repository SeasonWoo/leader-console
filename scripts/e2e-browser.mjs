// make e2e 的浏览器阶段：用无头 Chrome 打开真实二进制提供的页面，在真实 CSP 下走查三个页面并在页面上作答一次。
// 用法：node scripts/e2e-browser.mjs <base-url> <project> <ui-decision-id> <leader-root 副本目录>
//   leader-root 用于追加事件制造真实的版本变化（SSE 推送 → 页面刷新），测试“先成功再失败”的刷新路径。
// 退出码：0 通过；1 失败；2 本机没有可用的 Chrome（由 e2e.sh 决定跳过还是失败）。

import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { findChrome, launch, sleep } from "./cdp.mjs";

const [, , BASE, PROJECT, UI_DECISION, ROOT] = process.argv;
const chrome = findChrome();
if (!chrome) {
  console.error("e2e-browser: 找不到 Chrome / Chromium（可用环境变量 CHROME 指定）");
  process.exit(2);
}

const problems = [];
const b = await launch(chrome);
b.on("Runtime.consoleAPICalled", (p) => {
  if (p.type === "error" || p.type === "assert") problems.push(`console.${p.type}: ${p.args.map((a) => a.value ?? a.description).join(" ")}`);
});
b.on("Runtime.exceptionThrown", (p) => problems.push(`exception: ${p.exceptionDetails.exception?.description || p.exceptionDetails.text}`));
// CSP 违规、资源加载失败等浏览器自身的报错走 Log 域。
// 测试主动注入失败的请求（见 failRequests）不算页面错误。
const injected = new Set();
b.on("Log.entryAdded", ({ entry }) => {
  if (entry.level === "error" && !injected.has(entry.url)) problems.push(`log(${entry.source}): ${entry.text}${entry.url ? ` ${entry.url}` : ""}`);
});
b.on("Network.responseReceived", ({ response }) => {
  if (response.status >= 400 && !injected.has(response.url)) problems.push(`HTTP ${response.status} ${response.url}`);
});

// failRequests(re)：之后匹配 re 的 /api/v1/tasks… 请求由浏览器直接返回 500（Fetch 域拦截），null 恢复正常。
let failPattern = null;
const failRequests = (re) => (failPattern = re);
b.on("Fetch.requestPaused", ({ requestId, request }) => {
  let reply;
  if (failPattern?.test(request.url)) {
    injected.add(request.url);
    const body = Buffer.from(JSON.stringify({ error: { code: "internal_error", message: "e2e 注入的失败" } })).toString("base64");
    reply = b.send("Fetch.fulfillRequest", { requestId, responseCode: 500, responseHeaders: [{ name: "Content-Type", value: "application/json" }], body });
  } else {
    reply = b.send("Fetch.continueRequest", { requestId });
  }
  reply.catch(() => {}); // 页面跳转后请求可能已不存在
});

// bump：leader 追加一条事件 → 服务端版本变化 → SSE 推送 → 页面按正常流程刷新各资源。
let bumps = 0;
function bump() {
  const ev = { ts: new Date().toISOString(), type: "progress", actor: "e2e", task_id: "uih-copilot", summary: `e2e 刷新 ${++bumps}` };
  appendFileSync(join(ROOT, PROJECT, "events.jsonl"), JSON.stringify(ev) + "\n");
}
b.on("Network.loadingFailed", (p) => {
  // 页面跳转时取消的请求（含 SSE 长连接）是正常的。
  if (!p.canceled && p.errorText !== "net::ERR_ABORTED") problems.push(`loading failed: ${p.errorText}`);
});

const text = (sel = "body") => `(document.querySelector(${JSON.stringify(sel)})?.innerText || "")`;
const step = (s) => console.log(`  -> ${s}`);
const card = `document.querySelector('[data-key="${PROJECT}/${UI_DECISION}"]')`;
const stale = (scope, needle = "刷新失败") => `[...document.querySelectorAll("${scope} [data-stale]")].some((x) => x.innerText.includes(${JSON.stringify(needle)}))`;
const clickStaleRetry = (scope) => `[...document.querySelector("${scope} [data-stale]").querySelectorAll("button")].find((x) => x.textContent.includes("重试")).click()`;
const noReload = async (what) => {
  if ((await b.evaluate(`window.__lcNoReload`)) !== 1) throw new Error(`${what}触发了整页刷新`);
};

async function open(path) {
  await b.send("Page.navigate", { url: BASE + path });
  await b.waitFor(`页面加载 ${path}`, `document.readyState === "complete"`);
}

try {
  await Promise.all([b.send("Runtime.enable"), b.send("Page.enable"), b.send("Log.enable"), b.send("Network.enable")]);
  await b.send("Fetch.enable", { patterns: [{ urlPattern: "*/api/v1/tasks*", requestStage: "Request" }] });
  await b.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false });

  step("待我决定：真实数据渲染、标题显示待回答数、SSE 已连接");
  await open("/");
  await b.waitFor("决策卡片", `${text("main")}.includes("uih-copilot 评审通过")`);
  await b.waitFor("curl 作答后的回传进度", `${text("main")}.includes("leader 已确认")`);
  await b.waitFor("浏览器标题 (n) leader-console", `/^\\(\\d+\\) leader-console$/.test(document.title)`);
  await b.waitFor("实时状态 SSE", `${text("aside")}.includes("SSE")`);

  step(`页面作答 ${UI_DECISION}：选项 → 提交决定 → 确认提交 → 已回传（token + Origin 由浏览器真实发送）`);
  await b.waitFor("UI 决策卡片", `!!${card}`);
  await b.evaluate(`${card}.querySelector('input[type=radio][value="A"]').click()`);
  await b.evaluate(`[...${card}.querySelectorAll("button")].find((x) => x.textContent.trim() === "提交决定").click()`);
  await b.waitFor("二次确认", `[...${card}.querySelectorAll("button")].some((x) => x.textContent.trim() === "确认提交")`);
  await b.evaluate(`[...${card}.querySelectorAll("button")].find((x) => x.textContent.trim() === "确认提交").click()`);
  await b.waitFor("作答后进入回传进度", `/已回传|leader 已确认/.test(${card}?.innerText || "")`, 20_000);

  step("客户端导航：侧栏进入看板、点卡片进入详情（不整页刷新，RSC 载荷由 Go 静态服务提供）");
  await b.evaluate(`window.__lcNoReload = 1`);
  await b.evaluate(`document.querySelector('aside a[href^="/board/"]').click()`);
  await b.waitFor("跳到 /board/", `location.pathname === "/board/"`);
  await b.waitFor("看板五列与卡片", `["进行中", "阻塞 / 异常", "评审中", "待合入", "已完成"].every((c) => ${text("main")}.includes(c)) && ${text("main")}.includes("uih-copilot")`);
  if ((await b.evaluate(`window.__lcNoReload`)) !== 1) throw new Error("客户端导航触发了整页刷新");
  await b.evaluate(`document.querySelector('main a[href*="id=uih-copilot"]').click()`);
  await b.waitFor("点卡片进入详情", `location.pathname === "/task/" && ${text("main")}.includes("概况")`);
  if ((await b.evaluate(`window.__lcNoReload`)) !== 1) throw new Error("看板 → 详情触发了整页刷新");

  step("旧 hash 链接 /#/task/<project>/<id> 跳到新路由；详情含报告、时间线、终端（已脱敏）");
  await open(`/#/task/${PROJECT}/uih-copilot`);
  await b.waitFor("跳到 /task/", `location.pathname === "/task/" && new URLSearchParams(location.search).get("id") === "uih-copilot"`);
  await b.waitFor("报告渲染为 HTML", `!!document.querySelector(".prose-lc h1, .prose-lc table, .prose-lc ul")`);
  await b.waitFor("时间线", `${text("main")}.includes("时间线") && document.querySelectorAll("main ol li").length > 0`);
  await b.waitFor("终端输出", `${text("pre")}.includes("正在跑单测")`);
  if (await b.evaluate(`${text("main")}.includes("sk-FAKEFAKE")`)) throw new Error("终端输出未脱敏");

  step("先成功再失败：看板 / 详情 / 报告刷新失败时保留旧数据，同时提示“可能是旧数据”并可重试");
  await open(`/board/?project=${PROJECT}`);
  await b.waitFor("看板卡片", `${text("main")}.includes("uih-copilot")`);
  failRequests(/\/api\/v1\/tasks\?/);
  bump();
  await b.waitFor("看板刷新失败提示", stale("main"), 15_000);
  if (!(await b.evaluate(`${text("main")}.includes("uih-copilot")`))) throw new Error("看板刷新失败后旧卡片消失");
  failRequests(null);
  await b.evaluate(clickStaleRetry("main"));
  await b.waitFor("看板重试后提示消失", `!document.querySelector("main [data-stale]") && ${text("main")}.includes("uih-copilot")`);

  await open(`/task/?project=${PROJECT}&id=uih-copilot`);
  await b.waitFor("详情与报告", `${text("main")}.includes("概况") && !!document.querySelector(".prose-lc h1, .prose-lc ul")`);
  failRequests(new RegExp(`/api/v1/tasks/${PROJECT}/uih-copilot($|\\?)`));
  bump();
  await b.waitFor("详情刷新失败提示", stale("main"), 15_000);
  if (!(await b.evaluate(`${text("main")}.includes("概况")`))) throw new Error("详情刷新失败后旧内容消失");
  failRequests(null);
  await b.evaluate(clickStaleRetry("main"));
  await b.waitFor("详情重试后提示消失", `!document.querySelector("main [data-stale]")`);

  failRequests(/\/reports\//);
  bump();
  await b.waitFor("报告刷新失败提示", stale("main", "报告刷新失败"), 15_000);
  if (!(await b.evaluate(`!!document.querySelector(".prose-lc h1, .prose-lc ul")`))) throw new Error("报告刷新失败后旧报告消失");
  failRequests(null);
  await b.evaluate(clickStaleRetry("main"));
  await b.waitFor("报告重试后提示消失", `!document.querySelector("main [data-stale]")`);

  step("页面已打开时切换旧 hash 链接（看板 / 任务 / 决策 focus）、前进后退；非 #/ 的 hash 保持默认行为");
  await open("/");
  await b.waitFor("决策列表", `${text("main")}.includes("uih-copilot 评审通过")`);
  await b.evaluate(`window.__lcNoReload = 1`);
  await b.evaluate(`location.hash = "#/board?project=${PROJECT}"`);
  await b.waitFor("旧看板链接 → /board/", `location.pathname === "/board/" && location.search === "?project=${PROJECT}" && location.hash === "" && ${text("main")}.includes("任务看板")`);
  await b.evaluate(`history.back()`);
  await b.waitFor("后退回到待我决定", `location.pathname === "/" && location.hash === "" && ${text("main")}.includes("uih-copilot 评审通过")`);
  await b.evaluate(`history.forward()`);
  await b.waitFor("前进回到看板", `location.pathname === "/board/" && location.search === "?project=${PROJECT}" && ${text("main")}.includes("任务看板")`);
  await b.evaluate(`location.hash = "#/task/${PROJECT}/uih-copilot"`);
  await b.waitFor("旧任务链接 → /task/", `location.pathname === "/task/" && new URLSearchParams(location.search).get("id") === "uih-copilot" && ${text("main")}.includes("概况")`);
  await b.evaluate(`location.hash = "#/decisions?project=${PROJECT}&focus=d-20261021-001"`);
  await b.waitFor("旧决策 focus 链接 → /?focus=，并高亮该决策", `location.pathname === "/" && new URLSearchParams(location.search).get("focus") === "d-20261021-001" && !!document.querySelector('[data-key="${PROJECT}/d-20261021-001"]')?.classList.contains("ring-4")`);
  await b.evaluate(`history.back()`);
  await b.waitFor("后退回到任务详情", `location.pathname === "/task/" && ${text("main")}.includes("概况")`);
  await b.evaluate(`history.forward()`);
  await b.waitFor("前进回到待我决定", `location.pathname === "/" && ${text("main")}.includes("uih-copilot 评审通过")`);
  const before = await b.evaluate(`location.pathname + location.search`);
  await b.evaluate(`location.hash = "#main"`);
  await sleep(500);
  if ((await b.evaluate(`location.pathname + location.search + location.hash`)) !== `${before}#main`) throw new Error("非 #/ hash（#main）被改写");
  await noReload("同文档 hash 导航");

  step("过渡期 ledger.md 项目只读标记");
  await open("/board/?project=legacy-demo");
  await b.waitFor("ledger.md 只读", `${text("main")}.includes("ledger.md 只读")`);

  step("深色跟随系统；手动切换（外链 theme-init.js）在 CSP 下生效");
  await b.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "dark" }] });
  await open("/board/");
  await b.waitFor("深色背景", `getComputedStyle(document.body).backgroundColor === "rgb(0, 0, 0)"`);
  await b.evaluate(`localStorage.setItem("lc-theme", "light")`);
  await open("/board/");
  await b.waitFor("手动浅色", `document.documentElement.dataset.theme === "light" && getComputedStyle(document.body).backgroundColor === "rgb(245, 245, 247)"`);
  await b.evaluate(`localStorage.removeItem("lc-theme")`);

  await sleep(300);
  if (problems.length) throw new Error(`页面出现错误：\n    ${problems.join("\n    ")}`);
  console.log("  浏览器走查通过（无控制台错误、CSP 违规或失败请求）");
} catch (e) {
  console.error(`e2e-browser FAIL: ${e.message}`);
  if (problems.length) console.error(`  页面错误：\n    ${problems.join("\n    ")}`);
  await b.close();
  process.exit(1);
}
await b.close();
