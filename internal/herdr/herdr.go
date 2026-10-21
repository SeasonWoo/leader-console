// Package herdr 封装 herdr CLI（命令可配置），只暴露面板需要的四个只读/受限操作。
//
// 负责任务：D（herdr 适配层）。骨架阶段只定义接口和类型。
package herdr

import (
	"context"
	"fmt"
)

// Agent 是 `herdr agent list|get` 结果中面板关心的字段子集。
type Agent struct {
	Name             string `json:"name,omitempty"` // 未命名的 agent 为空
	Agent            string `json:"agent"`          // claude / codex
	AgentStatus      string `json:"agent_status"`   // idle / working / blocked / done / unknown
	PaneID           string `json:"pane_id"`
	TabID            string `json:"tab_id"`
	WorkspaceID      string `json:"workspace_id"`
	Cwd              string `json:"cwd"`
	ForegroundCwd    string `json:"foreground_cwd,omitempty"`
	TerminalTitle    string `json:"terminal_title,omitempty"` // 已去掉状态前缀（terminal_title_stripped）
	SessionID        string `json:"session_id,omitempty"`     // agent_session.value，codex 通常为空
	InteractiveReady bool   `json:"interactive_ready,omitempty"`
}

// ReadOptions 对应 `herdr agent read` 的参数。
type ReadOptions struct {
	Source string // visible / recent（默认）
	Lines  int    // 默认 80，上限 400
}

// Client 是 service / reconcile 依赖的 herdr 端口。实现必须并发安全，且每次调用都有超时。
type Client interface {
	List(ctx context.Context) ([]Agent, error)
	Get(ctx context.Context, target string) (Agent, error)
	// Read 返回已去除 ANSI / 控制字符的纯文本（未脱敏）。
	Read(ctx context.Context, target string, opts ReadOptions) (string, error)
	// Prompt 向 target 提交一条单行文本。text 由调用方构造并已清洗。
	Prompt(ctx context.Context, target, text string) error
}

// 已知错误码：前四个来自 herdr JSON 错误，后两个由适配层产生。
const (
	CodeAgentNotFound      = "agent_not_found"
	CodeAgentBlocked       = "agent_blocked"
	CodeAgentPromptStalled = "agent_prompt_stalled"
	CodeTimeout            = "timeout"
	CodeExec               = "exec_failed" // 命令不存在、非 0 退出且无 JSON 错误等
	CodeDecode             = "decode_failed"
)

// Error 是 herdr 调用失败时返回的错误。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("herdr: %s: %s", e.Code, e.Message) }
