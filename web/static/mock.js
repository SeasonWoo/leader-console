// 内存 mock 后端：URL 带 ?mock=1（或 ?mock=herdr-down）时由 api.js 加载，读取 mock/*.json 并模拟写接口。
// 只用于前端开发与手工走查；数据形状与 internal/model/view.go、docs/design.md §6 一致（web_test.go 严格解码校验）。

const CLOSED = new Set(["done", "answered_in_chat", "expired", "superseded"]);
// 提交这些决策会返回 409 already_answered，用于走查冲突提示。
const CONFLICT = new Set(["tob-x/d-20261021-104"]);

async function load(name) {
  const res = await fetch(`/mock/${name}.json`, { cache: "no-store" });
  if (!res.ok) throw new Error(`mock/${name}.json: HTTP ${res.status}`);
  return res.json();
}

const ok = (body, status = 200) => ({ status, body });
const fail = (status, code, message, extra) => ({ status, body: { error: { code, message }, ...extra } });
const clone = (v) => JSON.parse(JSON.stringify(v));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export function createMock(scenario) {
  const herdrDown = scenario === "herdr-down";
  const listeners = new Set();
  let seq = 0;
  let db = null;

  const ready = (async () => {
    const [overview, tasks, details, reports, terminal] = await Promise.all([
      load(herdrDown ? "overview-herdr-down" : "overview"),
      load("tasks"),
      load("details"),
      load("reports"),
      load("terminal"),
    ]);
    if (herdrDown) {
      // herdr 不可用：live 为空，不判 lost（design §10）。
      for (const t of [...tasks.tasks, ...Object.values(details)]) {
        if (t.live.lost) t.column = "in_progress";
        t.live = { agent_found: false, lost: false };
      }
    }
    db = { overview, tasks, details, reports, terminal };
  })();

  // 模拟后端快照变化：版本号 +1 并通知订阅者（相当于 SSE 推送 version）。
  function bump() {
    seq++;
    db.overview.version = `${db.overview.version.replace(/\+\d+$/, "")}+${seq}`;
    db.tasks.version = db.overview.version;
    for (const fn of listeners) fn(db.overview.version);
  }

  const project = (name) => db.overview.projects.find((p) => p.name === name);
  const findDecision = (p, id) => db.overview.decisions.find((d) => d.project === p && d.decision.id === id);
  const iso = () => new Date(Date.parse(db.overview.generated_at) + seq * 1000).toISOString();

  function recount(p) {
    const s = project(p);
    if (s) s.open_decisions = db.overview.decisions.filter((d) => d.project === p && d.state === "open").length;
  }

  // 投递模拟：leader 在线时依次推进 delivered → acked → executing；离线时保持 queued。
  function simulateDelivery(view) {
    const online = project(view.project)?.leader.online;
    if (!online || herdrDown) return;
    const steps = [
      [1500, () => {
        Object.assign(view.answer, { delivery: "delivered", attempts: view.answer.attempts + 1, target: project(view.project).leader.target, updated_at: iso() });
        view.state = "delivered";
      }],
      [3500, () => {
        view.state = "acked";
        view.decision.status = "answered";
        view.decision.answer = { via: "panel", answer_id: view.answer.answer_id, summary: summaryOf(view), answered_at: view.answer.answered_at };
      }],
      [6000, () => (view.state = "executing")],
    ];
    for (const [ms, fn] of steps) setTimeout(() => (fn(), bump()), ms);
  }

  function summaryOf(view) {
    const opt = (view.decision.options || []).find((o) => o.key === view.answer.option);
    return [opt ? `${opt.key} ${opt.label}` : "", view.answer.text || ""].filter(Boolean).join(" · ");
  }

  function answer(p, id, input) {
    const view = findDecision(p, id);
    if (!view) return fail(404, "not_found", "决策不存在");
    if (project(p)?.source !== "state.json") return fail(409, "protocol_unavailable", "该项目尚未启用 leader 协议");
    if (view.answer && view.answer.client_request_id && view.answer.client_request_id === input.client_request_id) return ok(clone(view), 202);
    if (CONFLICT.has(`${p}/${id}`)) {
      view.state = "answered_in_chat";
      view.can_answer = false;
      view.decision.status = "answered";
      view.decision.answer = { via: "chat", summary: "（mock）已在对话中处理", answered_at: iso() };
      recount(p);
      bump();
      return fail(409, "already_answered", "该决策已被处理", { decision: clone(view) });
    }
    if (view.state !== "open" || view.answer) return fail(409, "already_answered", "该决策已有回答", { decision: clone(view) });
    const option = (input.option || "").trim();
    const text = (input.text || "").trim();
    if (!option && !text) return fail(400, "invalid_answer", "请选择一个选项或填写说明");
    if (option && !(view.decision.options || []).some((o) => o.key === option)) return fail(400, "invalid_answer", `选项 ${option} 不存在`);
    if (text && !view.decision.allow_free_text) return fail(400, "invalid_answer", "该决策不接受补充说明");
    if (text.length > 2000) return fail(400, "invalid_answer", "补充说明过长");
    const at = iso();
    view.answer = {
      answer_id: `a-mock-${Date.now().toString(36)}`,
      decision_id: id,
      option: option || undefined,
      text: text || undefined,
      client_request_id: input.client_request_id,
      answered_at: at,
      delivery: "queued",
      attempts: 0,
      updated_at: at,
    };
    view.state = "queued";
    view.can_answer = false;
    recount(p);
    bump();
    simulateDelivery(view);
    return ok(clone(view), 202);
  }

  function retry(p, answerID) {
    const view = db.overview.decisions.find((d) => d.project === p && d.answer && d.answer.answer_id === answerID);
    if (!view) return fail(404, "not_found", "回答不存在");
    if (view.answer.delivery !== "failed") return fail(409, "not_failed", "只有投递失败的回答可以重试");
    Object.assign(view.answer, { delivery: "queued", last_error: undefined, updated_at: iso() });
    view.state = "queued";
    bump();
    simulateDelivery(view);
    return ok(clone(view), 202);
  }

  function taskDetail(p, id) {
    const key = `${p}/${id}`;
    let d = db.details[key];
    if (!d) {
      const tv = db.tasks.tasks.find((t) => t.project === p && t.task.id === id);
      if (!tv) return null;
      d = { ...tv, events: [], reports: [] };
    }
    d = clone(d);
    d.decisions = clone(db.overview.decisions.filter((v) => v.project === p && v.decision.task_id === id));
    return d;
  }

  async function handle(method, path, body) {
    await ready;
    await sleep(80); // 模拟网络延迟，便于看到加载状态
    const url = new URL(path, location.origin);
    const seg = url.pathname.split("/").filter(Boolean).slice(2).map(decodeURIComponent); // 去掉 api/v1
    const q = url.searchParams;

    if (method === "GET" && seg[0] === "overview") return ok(clone(db.overview));
    if (method === "GET" && seg[0] === "tasks" && seg.length === 1) {
      const tasks = db.tasks.tasks.filter((t) => (!q.get("project") || t.project === q.get("project")) && (!q.get("column") || t.column === q.get("column")));
      return ok({ version: db.overview.version, tasks: clone(tasks) });
    }
    if (method === "GET" && seg[0] === "tasks" && seg.length === 3) {
      const d = taskDetail(seg[1], seg[2]);
      return d ? ok(d) : fail(404, "not_found", "任务不存在");
    }
    if (method === "GET" && seg[0] === "tasks" && seg[3] === "reports") {
      const r = db.reports[`${seg[1]}/${seg[2]}/${seg[4]}`];
      return r ? ok(clone(r)) : fail(404, "not_found", "报告不存在");
    }
    if (method === "GET" && seg[0] === "tasks" && seg[3] === "terminal") {
      if (herdrDown) return fail(503, "herdr_unavailable", "herdr 不可用");
      const t = db.terminal[`${seg[1]}/${seg[2]}`];
      if (!t) return fail(404, "not_found", "该任务没有可读取的 worker");
      const r = clone(t[q.get("source") === "visible" ? "visible" : "recent"]);
      const lines = Number(q.get("lines")) || 80;
      r.text = r.text.split("\n").slice(-lines).join("\n");
      r.fetched_at = new Date().toISOString();
      return ok(r);
    }
    if (method === "GET" && seg[0] === "decisions") {
      const closed = q.get("state") === "closed";
      const list = db.overview.decisions.filter((d) => (!q.get("project") || d.project === q.get("project")) && CLOSED.has(d.state) === closed);
      return ok({ version: db.overview.version, decisions: clone(list) });
    }
    if (method === "POST" && seg[0] === "decisions" && seg[3] === "answer") return answer(seg[1], seg[2], body || {});
    if (method === "POST" && seg[0] === "answers" && seg[3] === "retry") return retry(seg[1], seg[2]);
    return fail(404, "not_found", `mock 未实现 ${method} ${url.pathname}`);
  }

  return {
    handle,
    subscribe(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
