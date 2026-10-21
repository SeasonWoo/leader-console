// 枚举值 → 中文展示文案。枚举定义见 internal/model。

export const DECISION_STATE = {
  open: "待回答",
  queued: "已排队",
  delivered: "已回传",
  acked: "leader 已确认",
  executing: "执行中",
  done: "已完成",
  delivery_failed: "投递失败",
  answered_in_chat: "已在对话中处理",
  expired: "已过期",
  superseded: "已被取代",
};

// 已关闭的决策进入“已处理”折叠区。
export const CLOSED_STATES = new Set(["done", "answered_in_chat", "expired", "superseded"]);

// 回传进度条的步骤（design §7.1）。
export const PROGRESS_STEPS = [
  ["queued", "已记录"],
  ["delivered", "已回传"],
  ["acked", "leader 已确认"],
  ["executing", "执行中"],
  ["done", "完成"],
];

export const URGENCY = { high: "高", normal: "中", low: "低" };

export const LEADER_REASON = {
  ok: "在线",
  no_leader_json: "无 leader.json",
  heartbeat_stale: "心跳超时",
  agent_not_found: "herdr 中找不到 leader",
  identity_mismatch: "身份核验不通过",
  herdr_unavailable: "herdr 不可用",
};

export const COLUMNS = [
  ["in_progress", "进行中"],
  ["blocked", "阻塞/异常"],
  ["reviewing", "评审中"],
  ["to_merge", "待合入"],
  ["done", "已完成"],
];
export const COLUMN_LABEL = Object.fromEntries(COLUMNS);

export const TASK_STATUS = {
  queued: "排队中",
  working: "进行中",
  blocked: "阻塞",
  needs_user: "等用户",
  reviewing: "评审中",
  ready_to_merge: "待合入",
  done: "已完成",
  failed: "失败",
  lost: "丢失",
};

export const EVENT_TYPE = {
  task_created: "创建",
  dispatched: "派发",
  started: "开工",
  progress: "进展",
  blocked: "阻塞",
  unblocked: "解除阻塞",
  review_requested: "请求评审",
  review_passed: "评审通过",
  review_failed: "评审不通过",
  rework: "返工",
  merged: "合入",
  done: "完成",
  failed: "失败",
  lost: "丢失",
  report_written: "报告",
  notified: "通知",
  decision_opened: "新决策",
  decision_ack: "决策已确认",
  decision_executing: "决策执行中",
  decision_done: "决策完成",
  other: "其他",
};

export const AGENT_STATUS = { idle: "空闲", working: "工作中", blocked: "阻塞", done: "完成", unknown: "未知" };
