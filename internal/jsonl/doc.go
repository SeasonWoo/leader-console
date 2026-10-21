// Package jsonl 提供 JSON Lines 的加锁追加和完整行读取。
// 读取保留坏行及其位置，由调用方解码、警告并跳过；忽略末尾残行。
package jsonl
