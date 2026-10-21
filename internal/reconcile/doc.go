// Package reconcile 是纯函数层：把 store 读出的 ProjectData 与 herdr agent 列表对账，
// 产出 LeaderPresence（leader 是否在线、可投递 target）和每个任务的 TaskLive（含 lost 判定）。
//
// 负责任务：D。骨架阶段为空包。
package reconcile
