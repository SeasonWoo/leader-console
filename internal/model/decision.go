package model

import "time"

// Decision 是 ~/.leader/<project>/decisions.jsonl 的一行，只由 leader 追加。
// 同一 ID 可出现多行，按文件顺序“后写覆盖前写”折叠成最新版本（整条覆盖，不做字段合并）。
type Decision struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id,omitempty"`
	Question string `json:"question"`
	// Context 是背景摘要（Markdown，渲染前脱敏）。
	Context         string     `json:"context,omitempty"`
	Options         []Option   `json:"options,omitempty"`
	Recommended     string     `json:"recommended,omitempty"` // Option.Key
	RecommendReason string     `json:"recommend_reason,omitempty"`
	AllowFreeText   bool       `json:"allow_free_text"`
	Urgency         Urgency    `json:"urgency,omitempty"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	// ReportRefs 是相关报告路径，规则同 Task.Reports。
	ReportRefs []string `json:"report_refs,omitempty"`

	Status       DecisionStatus  `json:"status"`
	Answer       *DecisionAnswer `json:"answer,omitempty"`
	SupersededBy string          `json:"superseded_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Option 是决策选项。
type Option struct {
	Key    string `json:"key"` // 短标识，如 "A"
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Impact string `json:"impact,omitempty"`
}

// Urgency 决定首页排序。
type Urgency string

const (
	UrgencyHigh   Urgency = "high"
	UrgencyNormal Urgency = "normal"
	UrgencyLow    Urgency = "low"
)

// DecisionStatus 是 leader 视角的决策状态。
type DecisionStatus string

const (
	DecisionOpen       DecisionStatus = "open"
	DecisionAnswered   DecisionStatus = "answered"
	DecisionExpired    DecisionStatus = "expired"
	DecisionSuperseded DecisionStatus = "superseded"
)

// DecisionAnswer 是 leader 确认收到的回答。Via=chat 表示用户在对话框里直接回答了。
type DecisionAnswer struct {
	Via        AnswerVia `json:"via"`
	AnswerID   string    `json:"answer_id,omitempty"` // Via=panel 时必填
	Summary    string    `json:"summary,omitempty"`
	AnsweredAt time.Time `json:"answered_at"`
}

// AnswerVia 是回答渠道。
type AnswerVia string

const (
	ViaPanel AnswerVia = "panel"
	ViaChat  AnswerVia = "chat"
)
