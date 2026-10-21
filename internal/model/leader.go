package model

import "time"

// LeaderInfo 对应 ~/.leader/<project>/leader.json，由 leader 整文件原子覆盖写入。
type LeaderInfo struct {
	SchemaVersion   int    `json:"schema_version"`
	ProtocolVersion string `json:"protocol_version"`
	// Project 等于 ~/.leader 下的目录名，也是面板里的项目 id。
	Project string `json:"project"`
	// Title 是给人看的项目名，可选。
	Title string `json:"title,omitempty"`
	// Cwd 是 leader 会话的工作目录，用于和 herdr agent 的 cwd 对账。
	Cwd    string      `json:"cwd"`
	Leader LeaderAgent `json:"leader"`
	// StartedAt 是本次 leader 会话的启动时间。
	StartedAt time.Time `json:"started_at"`
	// HeartbeatAt 是最后一次心跳；超过 HeartbeatTTL 视为离线。
	HeartbeatAt time.Time `json:"heartbeat_at"`
	// ReportIntervalMin 是定期汇报间隔（分钟），面板据此推算心跳 TTL。
	ReportIntervalMin int `json:"report_interval_min,omitempty"`
}

// LeaderAgent 描述 leader 在 herdr 中的身份。回传时优先用 HerdrName 作为 target，
// 否则用 PaneID；投递前必须用 Cwd / SessionID 校验目标确实是这个 leader。
type LeaderAgent struct {
	Agent     string `json:"agent"` // 目前固定为 "claude"
	HerdrName string `json:"herdr_name,omitempty"`
	PaneID    string `json:"pane_id,omitempty"`
	SessionID string `json:"session_id,omitempty"` // herdr agent_session.value
}
