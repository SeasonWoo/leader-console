// Package service 组装 store + herdr + reconcile：维护内存快照与变更版本号、推导看板列和
// 决策展示状态、执行决策回传（追加 answers.jsonl → herdr prompt → 重试队列）。
//
// 能力通过 Deps 注入；不依赖 A/D 的具体实现。Run 启动前即可读取空视图，
// 生产环境应先运行 Run 取得单实例锁，再接受作答请求。Run 停止时调用方应停止 HTTP。
// Store.LoadProject 在写入前重新检查磁盘指纹，Store.AppendAnswer 负责文件锁和持久化。
package service
