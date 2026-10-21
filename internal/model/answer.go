package model

import "time"

// AnswerLine 是 ~/.leader/<project>/answers.jsonl 的一行，只由面板追加。
// 两种行共用一个扁平结构：
//   - Type=answer：用户的回答（每个 AnswerID 只出现一次）；
//   - Type=delivery：该回答的一次投递结果（可多行，取最后一行）。
type AnswerLine struct {
	Type     AnswerLineType `json:"type"`
	AnswerID string         `json:"answer_id"`
	At       time.Time      `json:"at"`

	// Type=answer
	DecisionID      string `json:"decision_id,omitempty"`
	Option          string `json:"option,omitempty"` // Option.Key；与 Text 至少有一个
	Text            string `json:"text,omitempty"`
	By              string `json:"by,omitempty"` // 固定 "panel"
	ClientRequestID string `json:"client_request_id,omitempty"`

	// Type=delivery
	Delivery DeliveryStatus `json:"delivery,omitempty"`
	Attempt  int            `json:"attempt,omitempty"`
	Target   string         `json:"target,omitempty"` // herdr target
	Error    string         `json:"error,omitempty"`
}

// AnswerLineType 区分 answers.jsonl 的行类型。
type AnswerLineType string

const (
	LineAnswer   AnswerLineType = "answer"
	LineDelivery AnswerLineType = "delivery"
)

// DeliveryStatus 是写入磁盘的投递状态。
type DeliveryStatus string

const (
	DeliveryQueued    DeliveryStatus = "queued"    // 已记录，等待 leader 在线
	DeliveryRetry     DeliveryStatus = "retry"     // 本次投递失败，稍后重试
	DeliveryDelivered DeliveryStatus = "delivered" // herdr prompt 成功
	DeliveryFailed    DeliveryStatus = "failed"    // 放弃自动重试，等用户手动重试
)

// Answer 是按 AnswerID 折叠后的回答：answer 行的内容 + 最后一条 delivery 行的状态。
type Answer struct {
	AnswerID        string    `json:"answer_id"`
	DecisionID      string    `json:"decision_id"`
	Option          string    `json:"option,omitempty"`
	Text            string    `json:"text,omitempty"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	AnsweredAt      time.Time `json:"answered_at"`

	Delivery  DeliveryStatus `json:"delivery"` // 无 delivery 行时为 queued
	Attempts  int            `json:"attempts"`
	Target    string         `json:"target,omitempty"`
	LastError string         `json:"last_error,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"` // 最后一行的 At
}
