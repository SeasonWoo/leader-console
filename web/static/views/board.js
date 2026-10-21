// 任务看板（#/board?project=）。PRD §7.2、§8。

import { api } from "../api.js";
import { h, fill, extLink, fmtDuration, parseTime, relTime } from "../dom.js";
import { COLUMNS, LEADER_REASON, TASK_STATUS, AGENT_STATUS } from "../labels.js";
import { taskHref, boardHref } from "../nav.js";

const DAY = 24 * 3600 * 1000;
let showAllDone = false; // 跨刷新保留“已完成”列的展开状态

export function create(root, route, ctx) {
  const project = route.params.get("project") || "";
  const leaders = h("div", { class: "leaders" });
  const board = h("div", { class: "board" });
  const warnings = h("div", { class: "warnings" });
  root.append(h("div", { class: "toolbar" }, h("h2", { text: "任务看板" }), h("span", { class: "muted", text: project ? `项目：${project}` : "全部项目" })), leaders, board, warnings);

  async function refresh() {
    const res = await api.tasks(project);
    const projects = (ctx.overview.projects || []).filter((p) => !project || p.name === project);
    renderLeaders(projects);
    renderBoard(res.tasks || []);
    const warn = projects.flatMap((p) => (p.warnings || []).map((w) => `${p.name}: ${w}`));
    fill(warnings, ...(warn.length ? [h("h3", { text: "解析警告" }), h("ul", {}, warn.map((w) => h("li", { text: w })))] : []));
  }

  function renderLeaders(projects) {
    const now = ctx.now();
    fill(leaders,
      h("span", { class: "label", text: "leader：" }),
      ...projects.map((p) => {
        const l = p.leader;
        const hb = parseTime(l.heartbeat_at);
        return h("span", { class: `leader ${l.online ? "on" : "off"}` },
          h("a", { href: boardHref(p.name), text: p.name }),
          " ",
          h("span", { class: "dot", text: l.online ? "●" : "○" }),
          l.online ? " 在线" : ` 离线：${LEADER_REASON[l.reason] || l.reason}`,
          hb !== null ? ["（心跳 ", relTime(hb, "ago", now), "）"] : null,
          p.source === "ledger.md" ? h("span", { class: "badge gray", title: "过渡期只读解析 ledger.md，不能在面板作答", text: "ledger.md 只读" }) : null);
      }),
    );
    if (!projects.length) leaders.append(h("span", { class: "muted", text: "没有项目" }));
  }

  function renderBoard(tasks) {
    const now = ctx.now();
    const byCol = Object.fromEntries(COLUMNS.map(([c]) => [c, []]));
    for (const t of tasks) (byCol[t.column] || byCol.in_progress).push(t);

    fill(board,
      ...COLUMNS.map(([col, label]) => {
        let list = byCol[col];
        const head = h("div", { class: "colhead" }, h("span", { text: `${label} (${list.length})` }));
        if (col === "done") {
          const recent = list.filter((t) => (parseTime(t.task.updated_at) || 0) >= now - DAY);
          const hidden = list.length - recent.length;
          if (!showAllDone) list = recent;
          if (hidden > 0 || showAllDone) {
            head.append(h("button", {
              class: "link", type: "button",
              text: showAllDone ? "只看最近 24h" : `显示全部（另有 ${hidden} 个）`,
              onclick: () => ((showAllDone = !showAllDone), renderBoard(tasks)),
            }));
          }
        }
        return h("section", { class: `col col-${col}` }, head,
          h("div", { class: "cards" }, list.length ? list.map((t) => taskCard(t, !project)) : h("p", { class: "empty", text: "—" })));
      }),
    );
  }

  return { refresh };
}

function taskCard(tv, showProject) {
  const t = tv.task;
  const live = tv.live || {};
  const href = taskHref(tv.project, t.id);
  const failed = t.status === "failed";
  const lost = live.lost || t.status === "lost";
  let activity = live.activity || "";
  if (live.lost) activity = "herdr 中找不到 worker";
  else if (!activity && t.result_summary) activity = t.result_summary;

  const el = h("article", { class: `tcard${lost || failed ? " bad" : ""}`, tabindex: 0, role: "link", dataset: { href } },
    h("div", { class: "trow" },
      h("a", { class: "tname", href, text: t.name || t.id }),
      lost ? h("span", { class: "badge red", text: "LOST" }) : null,
      failed ? h("span", { class: "badge red", text: "FAILED" }) : null,
      t.status === "needs_user" ? h("span", { class: "badge amber", text: "等你决定" }) : null,
      t.status === "blocked" ? h("span", { class: "badge amber", text: "阻塞" }) : null),
    h("div", { class: "tmeta" },
      [t.kind, t.role].filter(Boolean).join(" · "),
      showProject ? h("span", { class: "muted", text: ` · ${tv.project}` }) : null,
      live.agent_found && live.agent_status ? h("span", { class: `astatus as-${live.agent_status}`, text: ` · ● ${AGENT_STATUS[live.agent_status] || live.agent_status}` }) : null),
    activity ? h("div", { class: `tact${live.lost ? " red" : ""}`, title: activity, text: activity }) : null,
    h("div", { class: "tfoot" },
      h("span", { title: "已运行", text: `⏱ ${tv.running_for_sec > 0 ? fmtDuration(tv.running_for_sec) : "—"}` }),
      t.rework_count ? h("span", { text: `返工 ${t.rework_count}` }) : null,
      h("span", { class: "muted", text: TASK_STATUS[t.status] || t.status }),
      (t.links || []).map((l) => extLink(l.url, l.title, "tlink"))),
  );
  const go = () => (location.hash = href);
  el.addEventListener("click", (e) => {
    if (!e.target.closest("a")) go();
  });
  el.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && e.target === el) go();
  });
  return el;
}
