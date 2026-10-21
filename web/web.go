// Package web 嵌入前端静态文件（无构建链：原生 HTML/CSS/ES module）。
//
// 负责任务：C（前端页面）。
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static 返回以 static/ 为根的文件系统。
func Static() fs.FS {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
