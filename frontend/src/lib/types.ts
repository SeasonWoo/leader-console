// 与 internal/model 对齐的视图契约（docs/design.md §6）。时间字段均为 RFC3339 字符串。

export type DataSource = "state.json" | "ledger.md" | "none";

export type LeaderReason =
  | "ok"
  | "no_leader_json"
  | "heartbeat_stale"
  | "agent_not_found"
  | "identity_mismatch"
  | "herdr_unavailable";

export interface LeaderPresence {
  online: boolean;
  reason: LeaderReason | string;
  target?: string;
  agent_status?: string;
  heartbeat_at?: string;
}

export type AgentStatus = "idle" | "working" | "blocked" | "done" | "unknown";

export interface TaskLive {
  agent_found: boolean;
  agent_status?: AgentStatus | string;
  /** worker 终端标题（可能沿用旧任务），只在详情页展示 */
  activity?: string;
  lost: boolean;
}

export interface ProjectSummary {
  name: string;
  title?: string | null;
  source: DataSource;
  leader: LeaderPresence;
  open_decisions: number;
  task_counts: Record<string, number>;
  warnings?: string[] | null;
}

export type BoardColumn = "in_progress" | "blocked" | "reviewing" | "to_merge" | "done";

export type TaskStatus =
  | "queued"
  | "working"
  | "blocked"
  | "needs_user"
  | "reviewing"
  | "ready_to_merge"
  | "done"
  | "failed"
  | "lost";

export interface Link {
  type: string;
  title: string;
  url?: string;
}

export interface Task {
  id: string;
  name: string;
  kind: string;
  role?: string;
  parent_id?: string;
  agent_name?: string;
  pane_id?: string;
  repo?: string;
  branch?: string;
  worktree?: string;
  goal: string;
  status: TaskStatus | string;
  result_summary?: string;
  reports?: string[];
  links?: Link[];
  rework_count?: number;
  created_at: string;
  updated_at: string;
  started_at?: string;
  notified_at?: string;
}

export interface TaskView {
  project: string;
  task: Task;
  column: BoardColumn;
  live: TaskLive;
  running_for_sec: number;
}

export interface Option {
  key: string;
  label: string;
  detail?: string;
  impact?: string;
}

export interface DecisionAnswer {
  via: "panel" | "chat";
  answer_id?: string;
  summary?: string;
  answered_at: string;
}

export interface Decision {
  id: string;
  task_id?: string;
  question: string;
  context?: string;
  options?: Option[];
  recommended?: string;
  recommend_reason?: string;
  allow_free_text: boolean;
  urgency?: "high" | "normal" | "low";
  deadline?: string;
  report_refs?: string[];
  status: string;
  answer?: DecisionAnswer;
  superseded_by?: string;
  created_at: string;
  updated_at: string;
}

export type DecisionState =
  | "open"
  | "queued"
  | "delivered"
  | "acked"
  | "executing"
  | "done"
  | "delivery_failed"
  | "answered_in_chat"
  | "expired"
  | "superseded";

export interface Answer {
  answer_id: string;
  decision_id: string;
  option?: string;
  text?: string;
  client_request_id?: string;
  answered_at: string;
  delivery: "queued" | "retry" | "delivered" | "failed";
  attempts: number;
  target?: string;
  last_error?: string;
  updated_at: string;
}

export interface DecisionView {
  project: string;
  decision: Decision;
  state: DecisionState;
  answer?: Answer;
  overdue: boolean;
  can_answer: boolean;
  /** 服务端脱敏 + 白名单渲染后的 HTML */
  context_html?: string;
}

export interface Event {
  ts: string;
  type: string;
  actor?: string;
  task_id?: string;
  decision_id?: string;
  answer_id?: string;
  summary: string;
}

export interface ReportRef {
  id: string;
  title: string;
  size: number;
}

export interface TaskDetail extends TaskView {
  events: Event[];
  reports: ReportRef[];
  decisions: DecisionView[];
}

export interface LegacyHintView {
  project: string;
  task_id: string;
  text: string;
}

export interface HerdrHealth {
  ok: boolean;
  error?: string;
  checked_at: string;
  agents: number;
}

export interface Overview {
  generated_at: string;
  version: string;
  projects: ProjectSummary[];
  decisions: DecisionView[];
  legacy_hints: LegacyHintView[];
  herdr: HerdrHealth;
}

export interface TasksResponse {
  version: string;
  tasks: TaskView[];
}

export interface Report {
  id: string;
  title: string;
  /** 服务端脱敏 + 白名单渲染后的 HTML */
  html: string;
  truncated: boolean;
}

export interface Terminal {
  text: string;
  agent_status?: string;
  fetched_at: string;
}

export interface AnswerInput {
  option?: string;
  text?: string;
  client_request_id: string;
}
