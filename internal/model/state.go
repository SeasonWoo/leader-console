package model

import "time"

// State 对应 ~/.leader/<project>/state.json（结构化台账），由 leader 写临时文件后 rename 覆盖。
type State struct {
	SchemaVersion int       `json:"schema_version"`
	Project       string    `json:"project"`
	UpdatedAt     time.Time `json:"updated_at"`
	Tasks         []Task    `json:"tasks"`
}

// TaskStatus 是任务状态。前 7 个与现有 ledger.md 一致，queued / ready_to_merge 为协议新增。
type TaskStatus string

const (
	TaskQueued       TaskStatus = "queued"
	TaskWorking      TaskStatus = "working"
	TaskBlocked      TaskStatus = "blocked"
	TaskNeedsUser    TaskStatus = "needs_user"
	TaskReviewing    TaskStatus = "reviewing"
	TaskReadyToMerge TaskStatus = "ready_to_merge"
	TaskDone         TaskStatus = "done"
	TaskFailed       TaskStatus = "failed"
	TaskLost         TaskStatus = "lost"
)

// Active 表示任务理应有一个存活的 worker（用于 lost 对账）。
func (s TaskStatus) Active() bool {
	switch s {
	case TaskWorking, TaskBlocked, TaskNeedsUser, TaskReviewing:
		return true
	}
	return false
}

// Task 是台账中的一行。
type Task struct {
	// ID 在项目内唯一且稳定；当前约定等于 Name。
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind 是 worker 类型：claude / codex。
	Kind string `json:"kind"`
	// Role 可选：impl / review / research / ship / merge。
	Role      string `json:"role,omitempty"`
	ParentID  string `json:"parent_id,omitempty"` // 评审对象或被接管的任务
	AgentName string `json:"agent_name,omitempty"`
	PaneID    string `json:"pane_id,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Goal      string `json:"goal"`

	Status        TaskStatus `json:"status"`
	ResultSummary string     `json:"result_summary,omitempty"`
	// Reports 是报告路径：相对项目目录（reports/<task>/x.md），过渡期允许 /tmp/<name>.md。
	Reports     []string `json:"reports,omitempty"`
	Links       []Link   `json:"links,omitempty"`
	ReworkCount int      `json:"rework_count,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
}

// LinkType 是外部链接类型。
type LinkType string

const (
	LinkMR          LinkType = "mr"
	LinkMeego       LinkType = "meego"
	LinkDevTask     LinkType = "dev_task"
	LinkLarkDoc     LinkType = "lark_doc"
	LinkDevxRelease LinkType = "devx_release"
	LinkCommit      LinkType = "commit"
	LinkOther       LinkType = "other"
)

// Link 是任务关联的外部链接；URL 为空时前端只显示 Title。
type Link struct {
	Type  LinkType `json:"type"`
	Title string   `json:"title"`
	URL   string   `json:"url,omitempty"`
}
