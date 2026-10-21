// Package render 把（已脱敏的）Markdown 渲染成安全 HTML：先整体转义，再只生成白名单标签，
// 链接只允许 http/https。只用标准库。
//
// 负责任务：D。骨架阶段为空包。
package render
