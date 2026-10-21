// Package web 嵌入前端静态导出产物（frontend/ 下的 Next.js 应用，`make web` 构建后同步到 web/dist）。
//
// 产物入库：干净 clone 后不装 Node 也能直接 go build / go test 得到完整的单二进制。
package web

import (
	"embed"
	"io/fs"
)

// all: 前缀是必须的：Next 产物里有 _next/ 目录和 __next.*.txt 文件，默认规则会跳过以 _ 开头的文件。
//
//go:embed all:dist
var files embed.FS

// Static 返回以 dist/ 为根的文件系统。
func Static() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
