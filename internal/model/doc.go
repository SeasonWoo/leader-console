// Package model 定义 leader-console 的数据契约，是各并行任务之间唯一共享的类型层。
//
// 分两部分：
//   - 磁盘契约（leader.go / state.go / event.go / decision.go / answer.go）：
//     对应 ~/.leader/<project>/ 下由 leader 或面板写入的文件，字段与 docs/design.md §4 一一对应。
//   - 视图契约（project.go / view.go）：后端各层之间、以及 HTTP API 返回给前端的结构。
//
// 规则：
//   - 只放类型、常量和不含 I/O 的小工具方法，不放业务逻辑。
//   - 读取时必须容忍未知字段（向前兼容）；新增字段一律 omitempty。
//   - 修改已有字段属于契约变更，必须先改 docs/design.md 并通知所有并行任务（见 docs/tasks.md §契约变更）。
package model

// SchemaVersion 是当前磁盘契约版本，写入 leader.json / state.json 的 schema_version。
const SchemaVersion = 1

// ProtocolVersion 是 leader 协议版本，写入 leader.json 的 protocol_version。
const ProtocolVersion = "lc-1"
