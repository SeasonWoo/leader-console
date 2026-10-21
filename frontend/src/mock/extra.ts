// mock 追加的项目：参照真实 ~/.leader/trae-tob-dev 的数据形态（长目标、长进展、worker 复用、Markdown 背景），
// 用来检验长文本截断与信息层级。内容为虚构示例。

import type { DecisionView, Overview, Report, TaskDetail, TaskView, TasksResponse } from "@/lib/types";

interface DB {
  overview: Overview;
  tasks: TasksResponse;
  details: Record<string, TaskDetail>;
  reports: Record<string, Report>;
}

const P = "trae-tob-dev";
const at = (hm: string) => `2026-10-21T${hm}:00+08:00`;

function task(
  id: string,
  column: TaskView["column"],
  t: Partial<TaskView["task"]>,
  live: TaskView["live"],
  running: number,
): TaskView {
  return {
    project: P,
    column,
    live,
    running_for_sec: running,
    task: {
      id,
      name: id,
      kind: "claude",
      goal: "",
      status: "working",
      created_at: at("13:20"),
      updated_at: at("15:58"),
      ...t,
    },
  };
}

export function extraProject(db: DB, herdrDown: boolean) {
  const tasks: TaskView[] = [
    task("remote-pre-recheck", "in_progress", {
      kind: "claude",
      role: "research",
      goal: "独立重新梳理 Remote 支持 ToB 预发的链路、差距与方案，最后与旧报告和飞书文档逐条对照，标出结论分歧",
      status: "working",
      agent_name: "w3:pB",
      started_at: at("14:41"),
    }, { agent_found: true, agent_status: "working", activity: "◐ 对照旧报告 §3 链路图", lost: false }, 4860),
    task("leader-portability", "in_progress", {
      kind: "codex",
      role: "research",
      goal: "评估 leader 机制哪些绑定 Claude Code，Codex 当 leader 是否可行及最小改造方案；只读",
      status: "working",
      started_at: at("15:10"),
    }, { agent_found: true, agent_status: "working", activity: "✳ remote-pre-implementation-plan", lost: false }, 3120),
    task("uih-fe-phase2", "blocked", {
      kind: "claude",
      role: "impl",
      goal: "前端阶段二：接入 BAM 新生成的客户端，补齐 UserIdentityHeaderScopes 的保存与回显",
      status: "needs_user",
      result_summary: "BAM version list / idl schema 均返回 no permission（缺 bam.service.view），等用户决定如何推进",
      started_at: at("11:05"),
      rework_count: 1,
    }, { agent_found: true, agent_status: "blocked", activity: "⏸ 等待决策 d-20261021-012", lost: false }, 17820),
    task("uih-integration-preflight", "done", {
      kind: "codex",
      role: "research",
      goal: "只读核对 GTM MR、下一班集成发布单、已有开发任务与需求分支，产出准确的集成前置和命令，不 merge",
      status: "done",
      result_summary: "只读核查完成：MR 仍为 open，需 1 名有写权限的人工审批；合入设置与流水线卡片已核对，命令清单见报告",
      updated_at: at("15:12"),
    }, { agent_found: true, agent_status: "idle", activity: "✳ leader-portability", lost: false }, 0),
    task("lc-roadmap", "done", {
      kind: "claude",
      role: "research",
      goal: "梳理 leader-console 规划的实现现状与偏差，结合真实使用痛点提出下一步演进方向；只读",
      status: "done",
      result_summary: "MVP A–E 已全部实现，test/e2e 通过；发现 2 个面板 bug（任务 reports 字段被忽略、decision_* 事件缺 answer_id）",
      updated_at: at("15:40"),
    }, { agent_found: false, lost: false }, 0),
  ];
  for (const t of tasks) if (herdrDown) t.live = { agent_found: false, lost: false };
  db.tasks.tasks.push(...tasks);

  const counts: Record<string, number> = { in_progress: 0, blocked: 0, reviewing: 0, to_merge: 0, done: 0 };
  for (const t of tasks) counts[t.column]++;

  const decision: DecisionView = {
    project: P,
    state: "open",
    overdue: false,
    can_answer: true,
    decision: {
      id: "d-20261021-012",
      task_id: "uih-fe-phase2",
      question: "前端联调卡在平台权限：当前账号缺 bam.service.view，如何推进？",
      context: "",
      options: [
        { key: "A", label: "申请权限后继续", detail: "提权限工单，拿到 bam.service.view 后查重再建版本", impact: "预计等待 0.5–1 天" },
        { key: "B", label: "请 GTM 同学代建版本", detail: "把 IDL 变更交给有权限的同学执行 bam idl update", impact: "依赖对方排期" },
        { key: "C", label: "先用本地生成的客户端联调", detail: "离线生成客户端，只做前端自测，不发布", impact: "阶段二上线前仍需重新生成" },
      ],
      recommended: "A",
      recommend_reason: "权限是长期需要的，且查重后再建版本可以避免重复版本。",
      allow_free_text: true,
      urgency: "high",
      deadline: at("17:30"),
      report_refs: ["reports/uih-fe-phase2/bam-permission.md"],
      status: "open",
      created_at: at("15:46"),
      updated_at: at("15:46"),
    },
    context_html:
      "<ul><li>你已授权测试阶段用 GTM <code>boe/release</code> 生成 BAM 联调版本（d-011）。</li>" +
      "<li>worker 已核实 <code>boe/release</code> 最新提交包含三个新字段，编号与类型与需求一致。</li>" +
      "<li>BAM <code>version list</code> / <code>idl schema</code> 均返回 <strong>no permission</strong>（status_code 105）；授权后复查仍相同。</li>" +
      "<li>因无法查重，未发起 <code>bam idl update</code>，避免重复建版本。</li>" +
      "<li>前端阶段一改动完整保留，相关 153 个用例通过；无提交、无部署。</li></ul>",
  };
  // 新决策排在最前（高紧急度）。
  db.overview.decisions.unshift(decision);

  db.overview.projects.unshift({
    name: P,
    title: "Trae ToB 开发",
    source: "state.json",
    leader: herdrDown
      ? { online: false, reason: "herdr_unavailable", target: "w1:pA", agent_status: "idle", heartbeat_at: at("15:59") }
      : { online: true, reason: "ok", target: "w1:pA", agent_status: "idle", heartbeat_at: at("15:59") },
    open_decisions: 1,
    task_counts: counts,
    warnings: null,
  });

  const key = `${P}/uih-fe-phase2`;
  db.details[key] = {
    ...tasks[2],
    decisions: [],
    events: [
      { ts: at("11:02"), type: "task_created", actor: "leader", task_id: "uih-fe-phase2", summary: "创建前端阶段二任务" },
      { ts: at("11:05"), type: "dispatched", actor: "leader", task_id: "uih-fe-phase2", summary: "派发给 w3:pB（claude）" },
      { ts: at("12:40"), type: "progress", actor: "uih-fe-phase2", task_id: "uih-fe-phase2", summary: "阶段一改动保留，153 个用例通过" },
      { ts: at("14:02"), type: "review_failed", actor: "rv-fe", task_id: "uih-fe-phase2", summary: "保存期间未禁用复选框，返工" },
      { ts: at("14:03"), type: "rework", actor: "leader", task_id: "uih-fe-phase2", summary: "返工 1：补保存中禁用态" },
      { ts: at("15:44"), type: "blocked", actor: "uih-fe-phase2", task_id: "uih-fe-phase2", summary: "BAM 查询返回 no permission" },
      { ts: at("15:46"), type: "decision_opened", actor: "leader", task_id: "uih-fe-phase2", decision_id: "d-20261021-012", summary: "如何推进 BAM 权限" },
    ],
    reports: [{ id: "bam-permission.md", title: "bam-permission.md", size: 2210 }],
  };
  db.reports[`${key}/bam-permission.md`] = {
    id: "bam-permission.md",
    title: "bam-permission.md",
    truncated: false,
    html:
      "<h1>BAM 权限核查</h1><p>结论：当前账号缺少 <code>bam.service.view</code>，无法查询已有版本，因此没有执行建版本操作。</p>" +
      "<h2>复现</h2><pre><code>bam version list --psm example.gtm.api\n# status_code=105 no permission</code></pre>" +
      "<h2>影响</h2><table><thead><tr><th>环节</th><th>状态</th><th>说明</th></tr></thead><tbody>" +
      "<tr><td>阶段一前端</td><td>已完成</td><td>153 个用例通过</td></tr>" +
      "<tr><td>BAM 版本</td><td>阻塞</td><td>缺查看权限，无法查重</td></tr>" +
      "<tr><td>阶段二联调</td><td>未开始</td><td>依赖新客户端</td></tr></tbody></table>" +
      "<blockquote><p>注意：直接建版本可能产生重复版本，需要先查重。</p></blockquote>",
  };
}
