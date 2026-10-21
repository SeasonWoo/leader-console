package model

import "time"

// 本文件是 HTTP API 返回给前端的视图契约（docs/design.md §6）。

// LeaderPresence 是 leader 在线判定结果（reconcile 产出，Change / Report 由 service 补充）。
type LeaderPresence struct {
	Online bool `json:"online"`
	// Reason：ok / no_leader_json / heartbeat_stale / agent_not_found / identity_mismatch /
	// agent_kind_mismatch（原 pane 上换成了另一种 agent，通常是 leader 切换 CLI 中）/ herdr_unavailable
	Reason      string     `json:"reason"`
	Target      string     `json:"target,omitempty"` // 可用于 herdr prompt 的 target（Identified 时给出）
	AgentStatus string     `json:"agent_status,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeat_at,omitempty"`
	// Identified 表示 herdr 中找到了 leader.json 描述的 agent 且身份核验通过，不看心跳。
	// Online = Identified 且心跳未过期。定期汇报只要求 Identified（它本身就是让心跳恢复的手段）。
	Identified bool `json:"identified"`
	// Via：name（按 herdr_name 找到）/ pane（名字丢失或未命名，按 pane_id + cwd + agent (+session) 兜底）。
	Via string `json:"via,omitempty"`
	// Agent 是 leader.json 声明的 agent kind；ObservedAgent 是现场找到的 agent kind。
	Agent         string `json:"agent,omitempty"`
	ObservedAgent string `json:"observed_agent,omitempty"`
	// Change 是面板记录的最近一次 leader 身份变化（panel.jsonl），从未变化时为 nil。
	Change *LeaderChange `json:"change,omitempty"`
	// Report 是面板定时投递【定期汇报】的调度状态；调度关闭或项目不适用时为 nil。
	Report *ReportSchedule `json:"report,omitempty"`
}

// LeaderChange 是面板观察到的一次 leader.json 身份变化。
type LeaderChange struct {
	At   time.Time   `json:"at"`
	From LeaderAgent `json:"from"`
	To   LeaderAgent `json:"to"`
	// Switched 表示 agent kind 变了（如 claude→codex）；否则是同类 leader 重启、改名或换 pane。
	Switched bool `json:"switched"`
}

// ReportSchedule 是单个项目的【定期汇报】调度状态。
type ReportSchedule struct {
	IntervalMin int        `json:"interval_min"`
	NextDueAt   time.Time  `json:"next_due_at"`
	LastSentAt  *time.Time `json:"last_sent_at,omitempty"`
	// Unacked 是最近连续投递后 leader 仍未刷新 heartbeat 的次数（>1 时间隔按倍数拉长）。
	Unacked int `json:"unacked"`
	// Waiting 说明已到期但暂缓的原因：not_identified / leader_busy / leader_blocked / backoff；未到期为空。
	Waiting   string `json:"waiting,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// ReportSchedulerInfo 是面板级的定时汇报配置（overview / healthz 返回，leader 可据此决定是否自建 cron）。
type ReportSchedulerInfo struct {
	Enabled bool `json:"enabled"`
	// Projects 为空表示所有 lc-1 项目。
	Projects []string `json:"projects,omitempty"`
	GraceSec int64    `json:"grace_sec"`
}

// TaskLive 是任务与 herdr 对账后的实时信息（reconcile 产出）。
type TaskLive struct {
	AgentFound  bool   `json:"agent_found"`
	AgentStatus string `json:"agent_status,omitempty"` // idle / working / blocked / done / unknown
	Activity    string `json:"activity,omitempty"`     // terminal_title_stripped
	// Lost 表示台账认为任务在进行，但 herdr 中找不到对应 worker。
	Lost bool `json:"lost"`
}

// ProjectSummary 用于项目筛选与概览。
type ProjectSummary struct {
	Name          string         `json:"name"`
	Title         string         `json:"title,omitempty"`
	Source        DataSource     `json:"source"`
	Leader        LeaderPresence `json:"leader"`
	OpenDecisions int            `json:"open_decisions"`
	TaskCounts    map[string]int `json:"task_counts"` // 按 BoardColumn 计数
	Warnings      []string       `json:"warnings,omitempty"`
}

// BoardColumn 是看板列。
type BoardColumn string

const (
	ColumnInProgress BoardColumn = "in_progress" // queued / working
	ColumnBlocked    BoardColumn = "blocked"     // blocked / needs_user / failed / lost
	ColumnReviewing  BoardColumn = "reviewing"
	ColumnToMerge    BoardColumn = "to_merge" // ready_to_merge
	ColumnDone       BoardColumn = "done"
)

// TaskView 是看板卡片。
type TaskView struct {
	Project string      `json:"project"`
	Task    Task        `json:"task"`
	Column  BoardColumn `json:"column"`
	Live    TaskLive    `json:"live"`
	// RunningFor 是已运行秒数（StartedAt 或 CreatedAt 起算，done/failed 时为 0）。
	RunningFor int64 `json:"running_for_sec"`
}

// DecisionState 是面板综合 decision + answer + events 推导出的展示状态（docs/design.md §7）。
type DecisionState string

const (
	DStateOpen           DecisionState = "open"
	DStateQueued         DecisionState = "queued"
	DStateDelivered      DecisionState = "delivered"
	DStateAcked          DecisionState = "acked"
	DStateExecuting      DecisionState = "executing"
	DStateDone           DecisionState = "done"
	DStateDeliveryFailed DecisionState = "delivery_failed"
	DStateInChat         DecisionState = "answered_in_chat"
	DStateExpired        DecisionState = "expired"
	DStateSuperseded     DecisionState = "superseded"
)

// DecisionView 是“待我决定”卡片。
type DecisionView struct {
	Project  string        `json:"project"`
	Decision Decision      `json:"decision"`
	State    DecisionState `json:"state"`
	Answer   *Answer       `json:"answer,omitempty"`
	// Overdue 表示已过截止时间但 leader 还没改成 expired。
	Overdue bool `json:"overdue"`
	// CanAnswer = State==open && leader 协议可用；leader 离线时仍可作答（进入排队）。
	CanAnswer bool `json:"can_answer"`
	// ContextHTML 是 Context 脱敏后渲染的安全 HTML。
	ContextHTML string `json:"context_html,omitempty"`
}

// ReportRef 是报告列表项；ID 用于 GET .../reports/{id}，不暴露真实路径。
type ReportRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Size  int64  `json:"size"`
}

// TaskDetail 是任务详情页。
type TaskDetail struct {
	TaskView
	Events    []Event        `json:"events"`
	Reports   []ReportRef    `json:"reports"`
	Decisions []DecisionView `json:"decisions"`
}

// Overview 是 GET /api/v1/overview 的返回。
type Overview struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Version     string           `json:"version"`
	Projects    []ProjectSummary `json:"projects"`
	Decisions   []DecisionView   `json:"decisions"` // 未关闭的，已排序
	LegacyHints []LegacyHintView `json:"legacy_hints"`
	Herdr       HerdrHealth      `json:"herdr"`
	// ReportScheduler 是面板定时汇报的配置。
	ReportScheduler ReportSchedulerInfo `json:"report_scheduler"`
}

// LegacyHintView 带上项目名。
type LegacyHintView struct {
	Project string `json:"project"`
	LegacyHint
}

// HerdrHealth 是 herdr CLI 可用性。
type HerdrHealth struct {
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Agents    int       `json:"agents"`
}
