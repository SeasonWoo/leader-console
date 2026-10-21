// Package redact 对报告、终端输出、结果摘要中疑似凭证的内容（AK/SK、token、私钥、JWT、
// password=... 等）做脱敏。
//
// 只用标准库；不读取环境或外部数据。
package redact
