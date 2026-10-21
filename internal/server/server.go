// Package server serves the local API, authenticated writes, static UI and SSE.
package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"leader-console/internal/herdr"
	"leader-console/internal/service"
	"leader-console/internal/store"
)

var Version = "dev"

// Options.Addr supplies the configured port for the strict loopback Host list.
// The listen address is separately validated by config before starting HTTP.
type Options struct{ Addr string }

type handler struct {
	svc       *service.Service
	static    fs.FS
	token     string
	port      string
	started   time.Time
	streams   atomic.Int32
	heartbeat time.Duration
}

// New keeps the frozen three-argument signature. A nil service provides an empty,
// read-only placeholder for the pre-integration command (leader-approved adapter).
func New(svc *service.Service, static fs.FS, opts Options) http.Handler {
	if svc == nil {
		svc = service.New(service.Deps{}, service.Options{})
	}
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:7788"
	}
	_, port, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		panic("server: invalid Addr")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		panic("server: invalid port")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic("server: token generation failed")
	}
	h := &handler{svc: svc, static: static, token: hex.EncodeToString(token[:]), port: port, started: time.Now(), heartbeat: 15 * time.Second}
	return h.routes()
}

func (h *handler) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.only("GET", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "version": Version, "uptime_sec": int64(time.Since(h.started).Seconds())})
	}))
	mux.HandleFunc("/api/v1/overview", h.only("GET", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, h.svc.Overview()) }))
	mux.HandleFunc("/api/v1/tasks", h.only("GET", func(w http.ResponseWriter, r *http.Request) {
		out, err := h.svc.Tasks(r.URL.Query().Get("project"), r.URL.Query().Get("column"))
		respond(w, out, err)
	}))
	mux.HandleFunc("/api/v1/tasks/{project}/{task}", h.only("GET", func(w http.ResponseWriter, r *http.Request) {
		out, err := h.svc.TaskDetail(r.Context(), r.PathValue("project"), r.PathValue("task"))
		respond(w, out, err)
	}))
	mux.HandleFunc("/api/v1/tasks/{project}/{task}/reports/{report}", h.only("GET", func(w http.ResponseWriter, r *http.Request) {
		out, err := h.svc.Report(r.Context(), r.PathValue("project"), r.PathValue("task"), r.PathValue("report"))
		respond(w, out, err)
	}))
	mux.HandleFunc("/api/v1/tasks/{project}/{task}/terminal", h.only("GET", h.terminal))
	mux.HandleFunc("/api/v1/decisions", h.only("GET", func(w http.ResponseWriter, r *http.Request) {
		out, err := h.svc.Decisions(r.URL.Query().Get("project"), r.URL.Query().Get("state"))
		respond(w, out, err)
	}))
	mux.HandleFunc("/api/v1/decisions/{project}/{decision}/answer", h.only("POST", func(w http.ResponseWriter, r *http.Request) {
		var in service.AnswerInput
		if !readJSON(w, r, &in) {
			return
		}
		out, err := h.svc.Answer(r.Context(), r.PathValue("project"), r.PathValue("decision"), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, out)
	}))
	mux.HandleFunc("/api/v1/answers/{project}/{answer}/retry", h.only("POST", func(w http.ResponseWriter, r *http.Request) {
		// No payload is required; if present it must be an empty JSON object.
		if r.ContentLength != 0 {
			var in struct{}
			if !readJSON(w, r, &in) {
				return
			}
		}
		out, err := h.svc.Retry(r.Context(), r.PathValue("project"), r.PathValue("answer"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, out)
	}))
	mux.HandleFunc("/api/v1/stream", h.only("GET", h.stream))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "not_found", "Resource not found") })
	mux.HandleFunc("/", h.only("GET", h.assets))
	return h.security(mux)
}

func (h *handler) only(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			apiError(w, 405, "method_not_allowed", "Method not allowed")
			return
		}
		next(w, r)
	}
}
func (h *handler) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		allowed := r.Host == net.JoinHostPort("127.0.0.1", h.port) || r.Host == net.JoinHostPort("localhost", h.port) || r.Host == net.JoinHostPort("::1", h.port)
		if !allowed {
			apiError(w, 403, "forbidden", "Host is not allowed")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-LC-Token")), []byte(h.token)) != 1 {
				apiError(w, 403, "forbidden", "Invalid token")
				return
			}
			origins := r.Header.Values("Origin")
			if len(origins) > 1 || (len(origins) == 1 && origins[0] != "http://"+r.Host) {
				apiError(w, 403, "forbidden", "Invalid Origin")
				return
			}
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				apiError(w, 415, "invalid_content_type", "Content-Type must be application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		}
		next.ServeHTTP(w, r)
	})
}
func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	data, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		apiError(w, 413, "body_too_large", "Body must not exceed 16 KiB")
		return false
	}
	if err != nil || !utf8.Valid(data) {
		apiError(w, 400, "invalid_answer", "Invalid JSON body")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if len(data) == 0 || strings.TrimSpace(string(data)) == "null" {
		apiError(w, 400, "invalid_answer", "Expected a JSON object")
		return false
	}
	if err := dec.Decode(out); err != nil {
		apiError(w, 400, "invalid_answer", "Invalid JSON body")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		apiError(w, 400, "invalid_answer", "Expected one JSON object")
		return false
	}
	return true
}
func (h *handler) terminal(w http.ResponseWriter, r *http.Request) {
	lines := 0
	if v, ok := r.URL.Query()["lines"]; ok {
		n, err := strconv.Atoi(v[0])
		if err != nil || n < 0 {
			apiError(w, 400, "invalid_terminal", "Invalid line count")
			return
		}
		lines = n
	}
	out, err := h.svc.Terminal(r.Context(), r.PathValue("project"), r.PathValue("task"), herdr.ReadOptions{Source: r.URL.Query().Get("source"), Lines: lines})
	respond(w, out, err)
}
func (h *handler) assets(w http.ResponseWriter, r *http.Request) {
	if h.static == nil {
		apiError(w, 404, "not_found", "Page not found")
		return
	}
	// Serve index directly, including /index.html, to avoid FileServer's redirect
	// bypassing token injection. All other assets must be actual embedded files.
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		data, err := fs.ReadFile(h.static, "index.html")
		if err != nil {
			apiError(w, 404, "not_found", "Page not found")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, strings.ReplaceAll(string(data), "__LC_TOKEN__", html.EscapeString(h.token)))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !fs.ValidPath(path) {
		apiError(w, 404, "not_found", "Asset not found")
		return
	}
	info, err := fs.Stat(h.static, path)
	if err != nil || info.IsDir() {
		apiError(w, 404, "not_found", "Asset not found")
		return
	}
	// FileServer handles MIME types, ranges and conditional reads without opening
	// directories to browsing. Missing-file errors have already been normalized.
	http.FileServerFS(h.static).ServeHTTP(w, r)
}
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	if h.streams.Add(1) > 16 {
		h.streams.Add(-1)
		apiError(w, 503, "stream_limit", "Too many event streams")
		return
	}
	defer h.streams.Add(-1)
	if _, ok := w.(http.Flusher); !ok {
		apiError(w, 500, "stream_unavailable", "Streaming is unavailable")
		return
	}
	versions, unsubscribe := h.svc.Subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	send := func(data string) bool {
		rc := http.NewResponseController(w)
		// A slow local client must not retain a stream slot forever. A Recorder does
		// not support deadlines; its fast in-memory writes remain testable without TCP.
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(w, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case v := <-versions:
			data, _ := json.Marshal(map[string]string{"version": v})
			if !send(fmt.Sprintf("event: version\ndata: %s\n\n", data)) {
				return
			}
		case <-ticker.C:
			if !send(": keepalive\n\n") {
				return
			}
		}
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, out any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeError(w http.ResponseWriter, err error) {
	var app *service.Error
	if errors.As(err, &app) {
		body := map[string]any{"error": map[string]string{"code": app.Code, "message": app.Message}}
		if app.Current != nil {
			body["current"] = app.Current
		}
		writeJSON(w, app.Status, body)
		return
	}
	if errors.Is(err, store.ErrProjectNotFound) || errors.Is(err, store.ErrReportNotFound) {
		apiError(w, 404, "not_found", "Resource not found")
		return
	}
	apiError(w, 500, "internal_error", "Request could not be completed")
}
