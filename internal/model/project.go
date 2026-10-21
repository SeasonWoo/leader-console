package model

import "time"

// DataSource 说明任务列表来自哪里。
type DataSource string

const (
	SourceState  DataSource = "state.json"
	SourceLedger DataSource = "ledger.md" // 过渡期兼容解析，只读、尽力而为
	SourceNone   DataSource = "none"
)

// ProjectData 是 store 层读出的单个项目原始数据（已折叠、未与 herdr 对账）。
type ProjectData struct {
	Name   string      `json:"name"`
	Dir    string      `json:"dir"`
	Leader *LeaderInfo `json:"leader,omitempty"` // leader.json 缺失时为 nil
	Source DataSource  `json:"source"`
	Tasks  []Task      `json:"tasks"`
	// Events 按文件顺序（时间升序）。
	Events []Event `json:"events"`
	// Decisions 已按 ID 折叠为最新版本，按 CreatedAt 升序。
	Decisions []Decision `json:"decisions"`
	// Answers 已按 AnswerID 折叠，按 At 升序。
	Answers []Answer `json:"answers"`
	// Panel 是面板自己写的 panel.jsonl 折叠结果（定期汇报投递记录、leader 身份变化）。
	Panel PanelState `json:"panel"`
	// LegacyHints 是从 ledger.md 推断出的“疑似待确认”事项，不可在面板作答。
	LegacyHints []LegacyHint `json:"legacy_hints,omitempty"`
	// Warnings 记录解析时跳过的坏行等问题，前端以小字提示。
	Warnings []string `json:"warnings,omitempty"`
	// Version 是该项目所有源文件 (size, mtime) 的指纹，用于变更检测。
	Version  string    `json:"version"`
	LoadedAt time.Time `json:"loaded_at"`
}

// LegacyHint 是过渡期从 ledger.md 结果列里推断出的待确认事项。
type LegacyHint struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
}
