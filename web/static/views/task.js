// 任务详情（#/task/<project>/<id>?report=）。PRD §7.3。

import { api } from "../api.js";
import { h, fill, serverHTML, extLink, fmtClock, fmtDuration, parseTime, errorMessage } from "../dom.js";
import { COLUMN_LABEL, TASK_STATUS, AGENT_STATUS, EVENT_TYPE, DECISION_STATE } from "../labels.js";
import { taskHref, boardHref, decisionsHref } from "../nav.js";

const TERMINAL_THROTTLE_MS = 5000;

export function create(root, route, ctx) {
  const { project, id } = route;
  let selectedReport = route.params.get("report") || "";
  let reportSig = "";

  const header = h("div", { class: "dheader" });
  const info = h("section", { class: "panel info" });
  const timeline = h("section", { class: "panel timeline" });
  const tabs = h("div", { class: "tabs", role: "tablist" });
  const reportBody = h("div", { class: "report md" });
  const reportNote = h("p", { class: "note", hidden: true });
  const reports = h("section", { class: "panel reports" }, h("h3", { text: "报告" }), tabs, reportNote, reportBody);
  const terminal = terminalPanel(project, id);

  root.append(header, h("div", { class: "split" }, info, timeline), reports, terminal.el);

  async function refresh() {
    let d;
    try {
      d = await api.task(project, id);
    } catch (e) {
      if (e.status !== 404) throw e;
      fill(header, h("a", { href: boardHref(project), text: "← 看板" }));
      fill(info, h("p", { class: "empty", text: `任务 ${project}/${id} 不存在` }));
      return;
    }
    renderHeader(d);
    renderInfo(d);
    renderTimeline(d);
    await renderReports(d);
    if (!terminal.loaded) terminal.load(d);
  }

  function renderHeader(d) {
    const t = d.task;
    const live = d.live || {};
    fill(header,
      h("a", { href: boardHref(project), text: "← 看板" }),
      h("h2", {}, h("a", { href: boardHref(project), text: project }), " / ", t.name || t.id),
      h("span", { class: `badge col col-${d.column}`, text: COLUMN_LABEL[d.column] || d.column }),
      live.lost || t.status === "lost" ? h("span", { class: "badge red", text: "LOST" }) : null,
      t.status === "failed" ? h("span", { class: "badge red", text: "FAILED" }) : null,
      h("span", { class: "muted" },
        [t.kind, t.role, t.pane_id ? `pane ${t.pane_id}` : ""].filter(Boolean).join(" · "),
        live.agent_found ? h("span", { class: `astatus as-${live.agent_status}`, text: ` · ● ${AGENT_STATUS[live.agent_status] || live.agent_status || "未知"}` }) : h("span", { text: " · ○ herdr 中无对应 worker" })),
    );
  }

  function renderInfo(d) {
    const t = d.task;
    const now = ctx.now();
    const rows = [
      ["目标", t.goal],
      ["状态", `${TASK_STATUS[t.status] || t.status}${d.running_for_sec > 0 ? ` · 已运行 ${fmtDuration(d.running_for_sec)}` : ""}`],
      ["进展/结果", t.result_summary],
      ["worker 状态", d.live && d.live.lost ? "herdr 中找不到 worker（只展示，不回写台账）" : ""],
      ["终端标题", d.live && d.live.activity ? `${d.live.activity}（worker 会话标题，可能沿用旧任务）` : ""],
      ["仓库/分支", [t.repo, t.branch].filter(Boolean).join(" @ ")],
      ["worktree", t.worktree],
      ["agent", t.agent_name],
      ["父任务", t.parent_id ? h("a", { href: taskHref(project, t.parent_id), text: t.parent_id }) : ""],
      ["返工", t.rework_count ? `${t.rework_count} 次` : ""],
      ["时间", [
        parseTime(t.created_at) !== null ? `创建 ${fmtClock(t.created_at, now)}` : "",
        parseTime(t.started_at) !== null ? `开始 ${fmtClock(t.started_at, now)}` : "",
        parseTime(t.updated_at) !== null ? `更新 ${fmtClock(t.updated_at, now)}` : "",
        parseTime(t.notified_at) !== null ? `通知 ${fmtClock(t.notified_at, now)}` : "",
      ].filter(Boolean).join(" · ")],
      ["链接", (t.links || []).length ? (t.links || []).flatMap((l, i) => [i ? " · " : "", extLink(l.url, l.title)]) : ""],
    ];
    const dl = h("dl", {});
    for (const [k, v] of rows) {
      if (!v || (Array.isArray(v) && !v.length)) continue;
      dl.append(h("dt", { text: k }), h("dd", {}, v));
    }
    const decs = d.decisions || [];
    fill(info, dl, h("h3", { text: "相关决策" }),
      decs.length
        ? h("ul", { class: "rel-decisions" }, decs.map((v) => h("li", {},
            h("a", { href: decisionsHref(project, v.decision.id), text: v.decision.id }), " ",
            h("span", { class: `state st-${v.state}`, text: DECISION_STATE[v.state] || v.state }), " ",
            v.decision.question)))
        : h("p", { class: "muted", text: "无" }));
  }

  function renderTimeline(d) {
    const now = ctx.now();
    const evs = d.events || [];
    fill(timeline, h("h3", { text: "事件时间线" }),
      evs.length
        ? h("ol", { class: "events" }, evs.map((e) => h("li", { class: `ev ev-${e.type}` },
            h("time", { datetime: e.ts, text: fmtClock(e.ts, now) }),
            h("span", { class: "etype", title: e.type, text: EVENT_TYPE[e.type] || e.type }),
            e.actor ? h("span", { class: "actor", text: e.actor }) : null,
            h("span", { class: "esum", text: e.summary }),
            e.decision_id ? h("a", { href: decisionsHref(project, e.decision_id), text: e.decision_id }) : null)))
        : h("p", { class: "muted", text: "暂无事件（过渡期 ledger.md 项目没有事件流）" }));
  }

  async function renderReports(d) {
    const list = d.reports || [];
    if (!list.length) {
      fill(tabs);
      reportNote.hidden = true;
      fill(reportBody, h("p", { class: "muted", text: "暂无报告" }));
      return;
    }
    const match = list.find((r) => r.id === selectedReport || r.title === selectedReport);
    const cur = match || list[0];
    selectedReport = cur.id;
    fill(tabs, ...list.map((r) => h("button", {
      type: "button", role: "tab", class: r.id === cur.id ? "tab on" : "tab",
      "aria-selected": r.id === cur.id ? "true" : "false",
      title: `${r.size} 字节`, text: r.title,
      onclick: () => {
        selectedReport = r.id;
        history.replaceState(null, "", taskHref(project, id, r.id));
        renderReports(d);
      },
    })));
    try {
      const rep = await api.report(project, id, cur.id);
      const sig = `${cur.id}\n${rep.html}`;
      if (sig !== reportSig) {
        reportSig = sig;
        serverHTML(reportBody, rep.html);
      }
      reportNote.hidden = !rep.truncated;
      reportNote.textContent = rep.truncated ? "报告过大，已截断显示。" : "";
    } catch (e) {
      reportSig = "";
      fill(reportBody, h("p", { class: "note error", text: `报告加载失败：${errorMessage(e)}` }));
    }
  }

  return { refresh };
}

// terminalPanel：worker 终端最近输出（只读，已脱敏），按需拉取，手动刷新最多 5s 一次。
function terminalPanel(project, id) {
  let source = "recent";
  let last = 0;
  const pre = h("pre", { class: "term" });
  const meta = h("span", { class: "muted" });
  const refreshBtn = h("button", { type: "button", text: "刷新" });
  const srcBtns = ["visible", "recent"].map((s) => h("button", { type: "button", class: s === source ? "tab on" : "tab", text: s, dataset: { src: s } }));
  const self = {
    loaded: false,
    el: h("section", { class: "panel terminal" },
      h("div", { class: "phead" }, h("h3", { text: "worker 终端最近输出（只读，脱敏）" }), h("span", { class: "seg" }, srcBtns), refreshBtn, meta),
      pre),
    load,
  };

  async function load(detail) {
    self.loaded = true;
    if (detail && !detail.live.agent_found) {
      pre.textContent = "herdr 中没有找到该任务的 worker，无法读取终端。";
      pre.classList.add("dim");
    }
    await fetchNow();
  }

  let deferred = 0;
  async function fetchNow() {
    const wait = last + TERMINAL_THROTTLE_MS - Date.now();
    if (wait > 0) {
      // 节流：5s 内的再次请求推迟到窗口结束后执行一次
      meta.textContent = `${Math.ceil(wait / 1000)}s 后刷新…`;
      clearTimeout(deferred);
      deferred = setTimeout(fetchNow, wait);
      return;
    }
    last = Date.now();
    refreshBtn.disabled = true;
    setTimeout(() => (refreshBtn.disabled = false), TERMINAL_THROTTLE_MS);
    meta.textContent = "读取中…";
    try {
      const r = await api.terminal(project, id, source, 80);
      pre.classList.remove("dim");
      pre.textContent = r.text || "（无输出）";
      meta.textContent = `${r.agent_status ? `● ${AGENT_STATUS[r.agent_status] || r.agent_status} · ` : ""}${fmtClock(r.fetched_at, Date.now(), true)} 读取`;
    } catch (e) {
      pre.classList.add("dim");
      if (e.status === 404) pre.textContent = "该任务没有可读取的 worker 终端。";
      else pre.textContent = `读取失败：${errorMessage(e)}`;
      meta.textContent = "";
    }
  }

  refreshBtn.addEventListener("click", fetchNow);
  for (const b of srcBtns) {
    b.addEventListener("click", () => {
      if (source === b.dataset.src) return;
      source = b.dataset.src;
      for (const x of srcBtns) x.className = x.dataset.src === source ? "tab on" : "tab";
      fetchNow();
    });
  }
  return self;
}
