// Package store 读取 ~/.leader/*/ 下的项目数据，并负责面板唯一的写操作：向 answers.jsonl 追加。
//
// 负责任务：A（数据读取与解析层）。骨架阶段只定义接口，实现见 docs/tasks.md 任务 A。
package store

import (
	"context"
	"errors"

	"leader-console/internal/model"
)

// Store 是 service 层依赖的数据端口。实现必须并发安全。
type Store interface {
	// ListProjects 返回 root 下所有项目目录名（含 leader.json / state.json / ledger.md 任一即算），已排序。
	ListProjects(ctx context.Context) ([]string, error)
	// LoadProject 读取并折叠单个项目。优先 state.json，缺失时回退 ledger.md 兼容解析。
	// 源文件未变化（Version 相同）时应直接返回缓存。
	LoadProject(ctx context.Context, project string) (*model.ProjectData, error)
	// ListReports 返回某任务的可读报告（reports/<task>/*.md + 台账中引用的过渡期 /tmp 报告）。
	ListReports(ctx context.Context, project, taskID string) ([]model.ReportRef, error)
	// ReadReport 按 ListReports 给出的 ID 读取报告原文（未脱敏），大小上限 1 MiB。
	ReadReport(ctx context.Context, project, taskID, reportID string) ([]byte, error)
	// AppendAnswer 以“文件锁 + O_APPEND + 单次 write”向 answers.jsonl 追加一行。
	AppendAnswer(ctx context.Context, project string, line model.AnswerLine) error
}

var (
	// ErrProjectNotFound：项目名非法或目录不存在。
	ErrProjectNotFound = errors.New("store: project not found")
	// ErrReportNotFound：报告 ID 未知或路径不在允许范围内。
	ErrReportNotFound = errors.New("store: report not found")
)
