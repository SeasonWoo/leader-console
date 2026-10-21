package model

import (
	"encoding/json"
	"time"
)

// Event 是 ~/.leader/<project>/events.jsonl 的一行，只由 leader 追加。
type Event struct {
	TS   time.Time `json:"ts"`
	Type EventType `json:"type"`
	// Actor 是动作发起方：leader / <worker name> / user。
	Actor      string          `json:"actor,omitempty"`
	TaskID     string          `json:"task_id,omitempty"`
	DecisionID string          `json:"decision_id,omitempty"`
	AnswerID   string          `json:"answer_id,omitempty"`
	Summary    string          `json:"summary"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// EventType 枚举。读取时遇到未知类型按 EventOther 展示，不报错。
type EventType string

const (
	EventTaskCreated     EventType = "task_created"
	EventDispatched      EventType = "dispatched"
	EventStarted         EventType = "started"
	EventProgress        EventType = "progress"
	EventBlocked         EventType = "blocked"
	EventUnblocked       EventType = "unblocked"
	EventReviewRequested EventType = "review_requested"
	EventReviewPassed    EventType = "review_passed"
	EventReviewFailed    EventType = "review_failed"
	EventRework          EventType = "rework"
	EventMerged          EventType = "merged"
	EventDone            EventType = "done"
	EventFailed          EventType = "failed"
	EventLost            EventType = "lost"
	EventReportWritten   EventType = "report_written"
	EventNotified        EventType = "notified"

	EventDecisionOpened    EventType = "decision_opened"
	EventDecisionAck       EventType = "decision_ack"       // leader 已读到回答
	EventDecisionExecuting EventType = "decision_executing" // leader 开始执行后续动作
	EventDecisionDone      EventType = "decision_done"      // 后续动作完成

	EventOther EventType = "other"
)
