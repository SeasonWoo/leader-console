// Package service 组装 store + herdr + reconcile：维护内存快照与变更版本号、推导看板列和
// 决策展示状态、执行决策回传（追加 answers.jsonl → herdr prompt → 重试队列）。
//
// 负责任务：B。骨架阶段为空包。
package service
