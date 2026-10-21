// Package server 是 HTTP 层：路由、鉴权（token + Host/Origin 校验）、安全响应头、SSE、静态资源。
//
// 负责任务：B。骨架阶段只提供 /healthz 和嵌入的静态页面。
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"
)

// Version 由构建时 -ldflags 注入。
var Version = "dev"

// New 返回根 handler。static 是嵌入的前端文件系统（web.Static）。
func New(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	started := time.Now()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"version":    Version,
			"uptime_sec": int64(time.Since(started).Seconds()),
		})
	})
	mux.Handle("GET /", http.FileServerFS(static))
	return mux
}
