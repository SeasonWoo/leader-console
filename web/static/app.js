// 入口：布局、hash 路由、实时刷新（SSE，断开回退 5s 轮询）、页面标题。

import { api, mockScenario } from "./api.js";
import { h, fill, fmtClock, updateRelTimes, toast, errorMessage } from "./dom.js";
import { parseRoute, decisionsHref, boardHref } from "./nav.js";
import * as decisionsView from "./views/decisions.js";
import * as boardView from "./views/board.js";
import * as taskView from "./views/task.js";

const VIEWS = { decisions: decisionsView, board: boardView, task: taskView };
const POLL_MS = 5000;
const SSE_RETRY_MS = 30000;

// mock 数据的时间固定在 overview.generated_at 附近；mock 模式下把“现在”平移到那里（只校准一次），倒计时才有意义。
let clockOffset = 0;
let clockCalibrated = false;

const ctx = {
  overview: { projects: [], decisions: [], legacy_hints: [], herdr: { ok: true } },
  now: () => Date.now() + clockOffset,
  projectSummary: (name) => (ctx.overview.projects || []).find((p) => p.name === name),
  refresh: () => refresh(),
};

// ---- 布局 ----

const projectSelect = h("select", { "aria-label": "项目筛选" });
const herdrStatus = h("span", { class: "herdr" });
const liveStatus = h("span", { class: "live" });
const clock = h("span", { class: "clock" });
const navDecisions = h("a", { href: "#/decisions" }, "待我决定 ", h("span", { class: "count" }));
const navBoard = h("a", { href: "#/board", text: "任务看板" });
const projectNav = h("ul", { class: "pnav" });
const banner = h("div", { class: "banner", hidden: true });
const main = document.getElementById("app");

fill(document.getElementById("topbar"),
  h("a", { class: "brand", href: "#/decisions", text: "leader-console" }),
  h("label", { class: "psel" }, "项目：", projectSelect),
  h("span", { class: "spacer" }),
  mockScenario ? h("span", { class: "badge amber", text: `mock：${mockScenario}` }) : null,
  herdrStatus,
  liveStatus,
  clock,
);
fill(document.getElementById("sidenav"), h("nav", {}, navDecisions, navBoard), h("h4", { text: "项目" }), projectNav);
main.before(banner);

projectSelect.addEventListener("change", () => {
  const p = projectSelect.value;
  const r = parseRoute(location.hash);
  location.hash = r.name === "decisions" ? decisionsHref(p) : boardHref(p);
});

function currentProject(route) {
  return route.name === "task" ? route.project : route.params.get("project") || "";
}

function renderChrome(route) {
  const ov = ctx.overview;
  const open = (ov.decisions || []).filter((d) => d.state === "open").length;
  document.title = `(${open}) leader-console`;
  navDecisions.querySelector(".count").textContent = String(open);
  navDecisions.classList.toggle("on", route.name === "decisions");
  navBoard.classList.toggle("on", route.name === "board" || route.name === "task");

  const cur = currentProject(route);
  const names = (ov.projects || []).map((p) => `${p.name}\t${p.title || ""}`).join("\n");
  if (projectSelect.dataset.sig !== names) {
    // 项目列表不变时不重建，避免打开中的下拉框被刷新关掉
    projectSelect.dataset.sig = names;
    fill(projectSelect, h("option", { value: "", text: "全部" }),
      ...(ov.projects || []).map((p) => h("option", { value: p.name, text: p.title ? `${p.name}（${p.title}）` : p.name })));
  }
  projectSelect.value = cur;

  fill(projectNav, ...(ov.projects || []).map((p) =>
    h("li", { class: p.name === cur ? "on" : "" },
      h("a", { href: route.name === "decisions" ? decisionsHref(p.name) : boardHref(p.name), title: p.title || p.name },
        h("span", { class: `dot ${p.leader.online ? "on" : "off"}`, text: p.leader.online ? "●" : "○" }), " ", p.name,
        p.open_decisions ? h("span", { class: "count", text: String(p.open_decisions) }) : null))));

  const hd = ov.herdr || {};
  herdrStatus.className = `herdr ${hd.ok ? "ok" : "bad"}`;
  herdrStatus.textContent = hd.ok ? `herdr ● 正常（${hd.agents} agents）` : "herdr ○ 不可用";
  herdrStatus.title = hd.ok ? "" : hd.error || "";
}

function tick() {
  const now = ctx.now();
  clock.textContent = fmtClock(now, now);
  updateRelTimes(document.body, now);
}

// ---- 路由与刷新 ----

let route = null;
let view = null;
let lastVersion = "";
let inflight = null;
let pending = false;

function navigate() {
  route = parseRoute(location.hash);
  if (!location.hash) history.replaceState(null, "", "#/decisions");
  fill(main);
  view = VIEWS[route.name].create(main, route, ctx);
  renderChrome(route);
  refresh();
}

function setOverview(ov) {
  ctx.overview = ov;
  lastVersion = ov.version;
  if (mockScenario && !clockCalibrated) {
    clockOffset = Date.parse(ov.generated_at) - Date.now();
    clockCalibrated = true;
  }
}

// refresh 拉取 overview（顶栏、标题、决策都依赖它）后刷新当前页面；并发调用合并为一次补刷。
async function refresh(prefetched) {
  if (inflight) {
    pending = true;
    return inflight;
  }
  const v = view;
  inflight = (async () => {
    try {
      setOverview(prefetched || (await api.overview()));
      renderChrome(route);
      if (v === view) await view.refresh();
      banner.hidden = true;
    } catch (e) {
      banner.hidden = false;
      banner.textContent = `数据加载失败：${errorMessage(e)}`;
      if (e.code !== "network") console.warn("refresh:", e);
    }
    tick();
  })();
  await inflight;
  inflight = null;
  if (pending) {
    pending = false;
    refresh();
  }
}

// ---- 实时 ----

let es = null;
let pollTimer = 0;

function setLive(mode) {
  const text = { sse: "⟳ 实时（SSE）", polling: "⟳ 轮询（5s）", mock: "⟳ mock 模拟推送", offline: "✕ 连接断开" }[mode];
  liveStatus.textContent = text;
  liveStatus.className = `live ${mode}`;
}

function onVersion(v) {
  if (v && v !== lastVersion) refresh();
}

function connectSSE() {
  if (typeof EventSource === "undefined") return startPolling();
  es = new EventSource("/api/v1/stream");
  es.addEventListener("open", () => {
    stopPolling();
    setLive("sse");
  });
  es.addEventListener("version", (e) => {
    try {
      onVersion(JSON.parse(e.data).version);
    } catch {
      /* 忽略坏帧 */
    }
  });
  es.addEventListener("error", () => {
    es.close();
    es = null;
    startPolling();
    setTimeout(connectSSE, SSE_RETRY_MS);
  });
}

function startPolling() {
  setLive("polling");
  if (pollTimer) return;
  pollTimer = setInterval(async () => {
    try {
      const ov = await api.overview();
      setLive(es ? "sse" : "polling");
      if (ov.version !== lastVersion) refresh(ov);
    } catch {
      setLive("offline");
    }
  }, POLL_MS);
}

function stopPolling() {
  clearInterval(pollTimer);
  pollTimer = 0;
}

window.addEventListener("hashchange", navigate);
window.addEventListener("unhandledrejection", (e) => toast(errorMessage(e.reason), "error"));
setInterval(tick, 15000);

if (mockScenario) {
  setLive("mock");
  api.subscribe(onVersion);
} else {
  connectSSE();
}
navigate();
