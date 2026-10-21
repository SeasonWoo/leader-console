// 待我决定（首页 #/decisions?project=&focus=）。PRD §7.1，状态推导见 design §7.1。

import { api, newClientRequestID } from "../api.js";
import { h, fill, serverHTML, fmtClock, parseTime, relTime, toast, errorMessage } from "../dom.js";
import { DECISION_STATE, CLOSED_STATES, PROGRESS_STEPS, URGENCY, LEADER_REASON } from "../labels.js";
import { taskHref, decisionsHref, syncChildren } from "../nav.js";

// 跨刷新保留：未提交的草稿（选项、补充说明、client_request_id）。
const drafts = new Map();
// 提交 / 重试成功后、快照尚未反映之前，用服务端返回的 DecisionView 覆盖展示。
// base 记录当时快照里的视图，快照一旦变化就丢弃覆盖。
const optimistic = new Map();

const keyOf = (v) => `${v.project}/${v.decision.id}`;

export function create(root, route, ctx) {
  const project = route.params.get("project") || "";
  let focus = route.params.get("focus") || "";
  const cache = new Map();
  const closedCache = new Map();

  const activeList = h("div", { class: "dlist" });
  const closedCount = h("span");
  const closedList = h("div", { class: "dlist closed-list" });
  const closedBox = h("details", { class: "fold" }, h("summary", {}, "已处理（最近 24h）", closedCount), closedList);
  const legacyBox = h("section", { class: "fold legacy", hidden: true });
  const summary = h("span", { class: "muted" });

  root.append(
    h("div", { class: "toolbar" }, h("h2", { text: "待我决定" }), summary, h("span", { class: "spacer" }), h("span", { class: "muted", text: "排序：待回答优先 · 紧急度 ↓ · 截止时间 ↑" })),
    activeList,
    closedBox,
    legacyBox,
  );

  function views() {
    const out = [];
    for (const v of ctx.overview.decisions || []) {
      if (project && v.project !== project) continue;
      const key = keyOf(v);
      const o = optimistic.get(key);
      if (o && o.base === JSON.stringify(v)) out.push(o.view);
      else {
        optimistic.delete(key);
        out.push(v);
      }
    }
    return out;
  }

  function sigOf(v) {
    const ps = ctx.projectSummary(v.project);
    return JSON.stringify([v, ps && ps.leader.online, ps && ps.leader.reason, deliveredStale(v, ctx.now())]);
  }

  async function refresh() {
    const all = views();
    const active = all.filter((v) => !CLOSED_STATES.has(v.state));
    const closed = all.filter((v) => CLOSED_STATES.has(v.state));
    const open = active.filter((v) => v.state === "open").length;
    summary.textContent = `${open} 条待回答 · ${active.length - open} 条回传中`;

    const build = (v, readonly) => () => card(v, ctx, { readonly, rebuild: refresh });
    syncChildren(activeList, active.map((v) => ({ key: keyOf(v), sig: sigOf(v), build: build(v, false) })), cache);
    if (!active.length) fill(activeList, h("p", { class: "empty", text: project ? `项目 ${project} 没有待你决定的事项` : "没有待你决定的事项" }));

    closedCount.textContent = ` · ${closed.length} 条`;
    syncChildren(
      closedList,
      closed.map((v) => ({
        key: keyOf(v),
        sig: sigOf(v),
        build: () =>
          h("details", { class: "closed-item", dataset: { key: keyOf(v) } },
            h("summary", {}, h("span", { class: `state st-${v.state}`, text: DECISION_STATE[v.state] || v.state }), " ", h("code", { text: v.decision.id }), ` ${v.project} · ${v.decision.question}`),
            card(v, ctx, { readonly: true })),
      })),
      closedCache,
    );
    if (!closed.length) fill(closedList, h("p", { class: "empty", text: "最近 24h 没有已处理的决策" }));

    renderLegacy();

    if (focus) {
      const el = root.querySelector(`[data-key="${CSS.escape(`${project || findProject(focus)}/${focus}`)}"]`);
      if (el) {
        if (el.tagName === "DETAILS") {
          closedBox.open = true;
          el.open = true;
        }
        el.classList.add("focus");
        el.scrollIntoView({ block: "center" });
        focus = "";
      }
    }
  }

  function findProject(id) {
    const v = (ctx.overview.decisions || []).find((d) => d.decision.id === id);
    return v ? v.project : "";
  }

  function renderLegacy() {
    const hints = (ctx.overview.legacy_hints || []).filter((x) => !project || x.project === project);
    legacyBox.hidden = !hints.length;
    fill(legacyBox,
      h("h3", { text: "疑似待确认（来自 ledger.md，不可在此作答）" }),
      h("p", { class: "muted", text: "这些项目尚未启用 leader 协议，请在 leader 对话中回答。" }),
      h("ul", {}, hints.map((x) => h("li", {}, h("a", { href: taskHref(x.project, x.task_id), text: `${x.project} · ${x.task_id}` }), "：", x.text))),
    );
  }

  return { refresh };
}

function deliveredStale(v, now) {
  if (v.state !== "delivered" || !v.answer) return false;
  const t = parseTime(v.answer.updated_at);
  return t !== null && now - t > 60_000;
}

function optionLabel(d, key) {
  const o = (d.options || []).find((x) => x.key === key);
  return o ? `${o.key} ${o.label}` : key;
}

// card 渲染单张决策卡片。readonly：已关闭折叠区里的只读展示。
function card(v, ctx, { readonly, rebuild }) {
  const d = v.decision;
  const key = keyOf(v);
  const ps = ctx.projectSummary(v.project);
  const leader = ps ? ps.leader : { online: false, reason: "" };
  const now = ctx.now();
  const urg = d.urgency || "normal";

  const el = h("article", { class: `dcard st-${v.state} urg-${urg}`, dataset: { key } });

  // 头部：紧急度 · 项目 · 任务 · 状态
  const head = h("div", { class: "dhead" },
    h("span", { class: `badge urg urg-${urg}`, text: URGENCY[urg] || urg }),
    h("a", { class: "proj", href: `#/board?project=${encodeURIComponent(v.project)}`, text: v.project }),
    d.task_id ? [" · ", h("a", { href: taskHref(v.project, d.task_id), text: d.task_id })] : null,
    h("code", { class: "did", text: d.id }),
    h("span", { class: "spacer" }),
    statusLine(v, leader, now),
  );
  el.append(head);
  el.append(h("h3", { class: "question", text: d.question }));

  if (v.context_html || d.context) {
    const body = h("div", { class: "context md clamped" });
    if (v.context_html) serverHTML(body, v.context_html);
    else body.textContent = d.context;
    const toggle = h("button", { class: "link", type: "button", hidden: true, text: "展开全文" });
    toggle.addEventListener("click", () => {
      toggle.textContent = body.classList.toggle("clamped") ? "展开全文" : "收起";
    });
    requestAnimationFrame(() => (toggle.hidden = body.scrollHeight <= body.clientHeight + 2));
    el.append(h("div", { class: "ctx" }, h("span", { class: "label", text: "背景：" }), body, toggle));
  }

  const answerable = !readonly && v.state === "open" && v.can_answer;
  if (answerable) el.append(answerForm(v, ctx, leader, rebuild));
  else if (v.state === "open") {
    el.append(optionList(d, null, true));
    if (!readonly) el.append(h("p", { class: "note warn", text: "当前不可在面板作答（leader 协议不可用），请在 leader 对话中回答。" }));
  } else el.append(answeredBlock(v, ctx, leader, now, readonly));

  el.append(related(v));
  return el;
}

function statusLine(v, leader, now) {
  const d = v.decision;
  const a = v.answer;
  const s = h("span", { class: `dstatus st-${v.state}` });
  switch (v.state) {
    case "open": {
      const dl = parseTime(d.deadline);
      if (dl !== null) s.append("截止 ", fmtClock(dl, now), "（", relTime(dl, "countdown", now), "）");
      if (v.overdue) s.append(h("span", { class: "badge red", text: "已超时，仍可作答" }));
      if (dl === null && !v.overdue) s.append(h("span", { class: "muted", text: "无截止时间" }));
      break;
    }
    case "queued":
      s.append(leader.online ? "⏳ 已记录，等待投递" : `⚠ leader 离线（${LEADER_REASON[leader.reason] || leader.reason}），回答已排队`);
      break;
    case "delivered":
      s.append(`✔ 已回传 ${fmtClock(a && a.updated_at, now, true)}`);
      break;
    case "delivery_failed":
      s.append(`✖ 投递失败（${a ? a.attempts : 0} 次）`);
      break;
    case "superseded":
      s.append("↷ 已被 ", d.superseded_by ? h("a", { href: decisionsHref(v.project, d.superseded_by), text: d.superseded_by }) : "新决策", " 取代");
      break;
    default:
      s.append(DECISION_STATE[v.state] || v.state);
  }
  return s;
}

function optionList(d, draft, disabled, onPick) {
  const opts = d.options || [];
  if (!opts.length) return null;
  const name = `opt-${d.id}`;
  return h("div", { class: "options", role: "radiogroup" },
    opts.map((o) =>
      h("label", { class: `opt${d.recommended === o.key ? " rec" : ""}` },
        h("input", {
          type: "radio", name, value: o.key, disabled,
          checked: !!draft && draft.option === o.key,
          onchange: () => onPick && onPick(o.key),
        }),
        h("span", { class: "okey", text: o.key }),
        h("span", { class: "obody" },
          h("span", { class: "olabel", text: o.label }),
          o.detail ? h("span", { class: "odetail", text: o.detail }) : null,
          o.impact ? h("span", { class: "oimpact", text: `影响：${o.impact}` }) : null),
        d.recommended === o.key ? h("span", { class: "badge rec", text: "★ 推荐" }) : null)),
  );
}

function answerForm(v, ctx, leader, rebuild) {
  const d = v.decision;
  const key = keyOf(v);
  const draft = drafts.get(key) || { option: "", text: "", crid: "" };
  drafts.set(key, draft);

  const submit = h("button", { class: "primary", type: "button", text: "提交决定" });
  const confirmBox = h("div", { class: "confirm", hidden: true });
  const valid = () => !!draft.option || (d.allow_free_text && draft.text.trim() !== "");
  const sync = () => (submit.disabled = !valid());

  const form = h("div", { class: "aform" });
  const opts = optionList(d, draft, false, (k) => ((draft.option = k), sync()));
  if (opts) form.append(opts);
  if (d.recommend_reason) form.append(h("p", { class: "reason" }, h("span", { class: "label", text: "推荐理由：" }), d.recommend_reason));
  if (d.allow_free_text) {
    const ta = h("textarea", {
      rows: 2, maxlength: 2000,
      placeholder: (d.options || []).length ? "补充说明（可选）" : "你的回答",
      oninput: (e) => ((draft.text = e.target.value), sync()),
    });
    ta.value = draft.text;
    form.append(h("label", { class: "freetext" }, h("span", { class: "label", text: (d.options || []).length ? "补充说明：" : "回答：" }), ta));
  }
  if (!leader.online) {
    form.append(h("p", { class: "note warn", text: `⚠ leader 离线（${LEADER_REASON[leader.reason] || leader.reason}），回答将排队，leader 上线后自动投递。` }));
  }

  submit.addEventListener("click", () => {
    if (!valid()) return;
    const parts = [draft.option ? optionLabel(d, draft.option) : "", draft.text.trim() ? `补充：${draft.text.trim()}` : ""].filter(Boolean);
    const ok = h("button", { class: "primary", type: "button", text: "确认提交" });
    const cancel = h("button", { type: "button", text: "取消" });
    fill(confirmBox, h("span", { text: `提交后不可修改。确认提交「${parts.join("；")}」？` }), ok, cancel);
    confirmBox.hidden = false;
    submit.hidden = true;
    cancel.addEventListener("click", () => ((confirmBox.hidden = true), (submit.hidden = false)));
    ok.addEventListener("click", async () => {
      ok.disabled = cancel.disabled = true;
      ok.textContent = "提交中…";
      draft.crid ||= newClientRequestID();
      const base = JSON.stringify(v);
      try {
        const res = await api.answer(v.project, d.id, { option: draft.option || undefined, text: draft.text.trim() || undefined, client_request_id: draft.crid });
        optimistic.set(key, { base, view: res });
        drafts.delete(key);
        toast(leader.online ? "已记录，正在回传 leader" : "已记录，leader 离线，回答已排队", "ok");
      } catch (e) {
        if (e.status === 409) {
          drafts.delete(key);
          if (e.body && e.body.decision) optimistic.set(key, { base, view: e.body.decision });
          toast(e.code === "protocol_unavailable" ? "该项目尚未启用 leader 协议，无法在面板作答" : "该决策已被处理，已刷新为最新状态", "warn");
        } else {
          toast(errorMessage(e), "error");
          ok.disabled = cancel.disabled = false;
          ok.textContent = "确认提交";
          return;
        }
      }
      await rebuild();
      ctx.refresh();
    });
  });
  sync();
  form.append(h("div", { class: "actions" }, submit, confirmBox));
  return form;
}

function answeredBlock(v, ctx, leader, now, readonly) {
  const d = v.decision;
  const a = v.answer;
  const box = h("div", { class: "answered" });
  if (a) {
    box.append(h("p", {},
      h("span", { class: "label", text: "你的选择：" }),
      a.option ? optionLabel(d, a.option) : "（未选选项）",
      a.text ? [h("span", { class: "label", text: "　补充：" }), a.text] : null,
      h("span", { class: "muted", text: `　${fmtClock(a.answered_at, now, true)} 提交` })));
  }
  if (v.state === "answered_in_chat") {
    if (d.answer && d.answer.summary) box.append(h("p", {}, h("span", { class: "label", text: "对话中的回答：" }), d.answer.summary));
    if (a) box.append(h("p", { class: "note warn", text: "对话中已处理，面板回答未被采用。" }));
  }
  if (v.state === "expired") box.append(h("p", { class: "note", text: "leader 已将该决策标记为过期，无需再回答。" }));

  const idx = PROGRESS_STEPS.findIndex(([s]) => s === v.state);
  if (idx >= 0 || v.state === "delivery_failed") {
    const failed = v.state === "delivery_failed";
    const steps = failed ? [PROGRESS_STEPS[0], ["delivery_failed", "投递失败"]] : PROGRESS_STEPS;
    const cur = failed ? 1 : idx;
    box.append(h("ol", { class: "progress" },
      steps.map(([s, label], i) => {
        const fail = failed && i === 1;
        const mark = fail ? "✖" : i <= cur ? "●" : "○";
        return h("li", { class: `${i <= cur ? "on" : ""}${i === cur ? " cur" : ""}${fail ? " fail" : ""}`, dataset: { step: s }, text: `${mark} ${label}` });
      })));
  }
  if (v.state === "queued" && !leader.online) box.append(h("p", { class: "note warn", text: "leader 上线后会自动投递。" }));
  if (deliveredStale(v, now)) box.append(h("p", { class: "note", text: "leader 可能正忙：已送达，等待确认。" }));
  if (v.state === "delivery_failed" && a) {
    if (a.last_error) box.append(h("p", { class: "note error" }, "最后一次错误：", h("code", { text: a.last_error })));
    if (!readonly) {
      const btn = h("button", { class: "primary", type: "button", text: "重试投递" });
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        btn.textContent = "重试中…";
        const base = JSON.stringify(v);
        try {
          const res = await api.retry(v.project, a.answer_id);
          optimistic.set(keyOf(v), { base, view: res });
          toast("已重新加入投递队列（答案不变）", "ok");
        } catch (e) {
          toast(e.status === 409 ? "该回答当前不需要重试，已刷新" : errorMessage(e), e.status === 409 ? "warn" : "error");
        }
        ctx.refresh();
      });
      box.append(h("div", { class: "actions" }, h("span", { class: "muted", text: "只重发同一条回答，不能修改答案。" }), btn));
    }
  }
  return box;
}

function related(v) {
  const d = v.decision;
  const refs = (d.report_refs || []).map((p) => {
    const base = p.split("/").pop();
    return h("a", { href: d.task_id ? taskHref(v.project, d.task_id, base) : "#", text: base });
  });
  if (!refs.length && !d.task_id) return null;
  return h("div", { class: "related" },
    h("span", { class: "label", text: "相关：" }),
    refs.flatMap((a, i) => (i ? [" · ", a] : [a])),
    d.task_id ? [refs.length ? " · " : "", h("a", { href: taskHref(v.project, d.task_id), text: "任务详情 →" })] : null);
}
