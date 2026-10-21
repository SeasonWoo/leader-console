package model

import "time"

// PanelLine 是 ~/.leader/<project>/panel.jsonl 的一行，只由面板追加，leader 不读不写。
// 记录面板自己的动作和观察，用于重启后去重和审计：
//   - Type=report：一次【定期汇报】投递结果（delivered / retry / failed）；
//   - Type=leader：面板观察到的 leader.json 身份（首次看到或发生变化时各记一行）。
type PanelLine struct {
	Type PanelLineType `json:"type"`
	At   time.Time     `json:"at"`

	// Type=report
	Delivery DeliveryStatus `json:"delivery,omitempty"`
	Attempt  int            `json:"attempt,omitempty"`
	Target   string         `json:"target,omitempty"`
	Error    string         `json:"error,omitempty"`

	// Type=leader
	Leader    *LeaderAgent `json:"leader,omitempty"`
	StartedAt *time.Time   `json:"started_at,omitempty"`
}

// PanelLineType 区分 panel.jsonl 的行类型。
type PanelLineType string

const (
	PanelReport PanelLineType = "report"
	PanelLeader PanelLineType = "leader"
)

// PanelState 是 store 折叠 panel.jsonl 后的结果，只保留调度和展示需要的尾部。
type PanelState struct {
	// Reports 是最近的 report 行（时间升序，最多 PanelKeep 条）。
	Reports []PanelLine `json:"reports,omitempty"`
	// Leaders 是最近的 leader 行（时间升序，最多 PanelKeep 条）。
	Leaders []PanelLine `json:"leaders,omitempty"`
}

// PanelKeep 是 PanelState 每类保留的行数。
const PanelKeep = 20

// SameLeader 判断两次观察到的 leader 身份是否相同（agent、名字、pane、session、启动时间）。
func SameLeader(a PanelLine, li *LeaderInfo) bool {
	if a.Leader == nil || li == nil || *a.Leader != li.Leader {
		return false
	}
	if a.StartedAt == nil {
		return li.StartedAt.IsZero()
	}
	return a.StartedAt.Equal(li.StartedAt)
}
