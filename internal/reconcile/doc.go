// Package reconcile 是纯函数层：把 store 读出的 ProjectData 与 herdr agent 列表对账，
// 产出 LeaderPresence（leader 是否在线、可投递 target）和每个任务的 TaskLive（含 lost 判定）。
//
// 不进行 I/O，不修改输入快照。
package reconcile
