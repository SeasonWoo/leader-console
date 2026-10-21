package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/service"
	"leader-console/internal/store"
)

type apiStore struct {
	mu        sync.Mutex
	pd        model.ProjectData
	lines     []model.AnswerLine
	reportErr error
}

func (f *apiStore) ListProjects(context.Context) ([]string, error) { return []string{"demo"}, nil }
func (f *apiStore) LoadProject(_ context.Context, p string) (*model.ProjectData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p != "demo" {
		return nil, store.ErrProjectNotFound
	}
	data, _ := json.Marshal(f.pd)
	var pd model.ProjectData
	_ = json.Unmarshal(data, &pd)
	return &pd, nil
}
func (f *apiStore) ListReports(context.Context, string, string) ([]model.ReportRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []model.ReportRef{{ID: "r-report", Title: "report", Size: 100}}, f.reportErr
}
func (f *apiStore) ReadReport(context.Context, string, string, string) ([]byte, error) {
	return []byte("report secret-value"), nil
}
func (f *apiStore) AppendPanel(context.Context, string, model.PanelLine) error { return nil }
func (f *apiStore) AppendAnswer(_ context.Context, p string, l model.AnswerLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lines = append(f.lines, l)
	if l.Type == model.LineAnswer {
		f.pd.Answers = append(f.pd.Answers, model.Answer{AnswerID: l.AnswerID, DecisionID: l.DecisionID, Option: l.Option, Text: l.Text, ClientRequestID: l.ClientRequestID, AnsweredAt: l.At, UpdatedAt: l.At, Delivery: model.DeliveryQueued})
	} else {
		for i := range f.pd.Answers {
			a := &f.pd.Answers[i]
			if a.AnswerID == l.AnswerID {
				a.Delivery = l.Delivery
				a.Attempts = l.Attempt
				a.UpdatedAt = l.At
			}
		}
	}
	f.pd.Version = fmt.Sprint(len(f.lines))
	return nil
}

type apiHerdr struct {
	mu      sync.Mutex
	prompts []string
}

func (f *apiHerdr) List(context.Context) ([]herdr.Agent, error) {
	return []herdr.Agent{{Name: "leader-demo", Agent: "claude", Cwd: "/demo"}}, nil
}
func (f *apiHerdr) Get(context.Context, string) (herdr.Agent, error) {
	return herdr.Agent{Name: "leader-demo", Agent: "claude", Cwd: "/demo"}, nil
}
func (f *apiHerdr) Read(context.Context, string, herdr.ReadOptions) (string, error) {
	return "terminal secret-value\x00", nil
}
func (f *apiHerdr) Prompt(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, text)
	return nil
}

func eventually(t *testing.T, p func() bool) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if p() {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("timed out")
		case <-ticker.C:
		}
	}
}
func apiFixture(t *testing.T) (*service.Service, *apiStore, *apiHerdr, http.Handler, string) {
	t.Helper()
	now := time.Now()
	f := &apiStore{pd: model.ProjectData{Name: "demo", Source: model.SourceState, Version: "1", Leader: &model.LeaderInfo{ProtocolVersion: "lc-1", Cwd: "/demo", Leader: model.LeaderAgent{Agent: "claude", HerdrName: "leader-demo"}, HeartbeatAt: now}, Tasks: []model.Task{{ID: "task", Name: "task", Status: model.TaskWorking, AgentName: "worker", Goal: "goal secret-value", CreatedAt: now}}, Decisions: []model.Decision{{ID: "d1", TaskID: "task", Question: "question", Status: model.DecisionOpen, Options: []model.Option{{Key: "A", Label: "Go"}}, AllowFreeText: true, CreatedAt: now, UpdatedAt: now}}}}
	h := &apiHerdr{}
	svc := service.New(service.Deps{Store: f, Herdr: h, Reconcile: func(*model.ProjectData, []herdr.Agent, bool, time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
		return model.LeaderPresence{Online: true, Reason: "ok", Target: "leader-demo"}, map[string]model.TaskLive{"task": {AgentFound: true, AgentStatus: "working"}}
	}, Redact: func(s string) string { return strings.ReplaceAll(s, "secret-value", "****") }, Render: func(s string) string { return "<p>" + s + "</p>" }}, service.Options{ScanInterval: time.Millisecond, HerdrInterval: time.Hour, QueueInterval: time.Millisecond, LockPath: filepath.Join(t.TempDir(), "instance.lock")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	eventually(t, func() bool { return len(svc.Overview().Projects) == 1 })
	handler := New(svc, fstest.MapFS{"index.html": {Data: []byte(`<meta name="lc-token" content="__LC_TOKEN__">`)}, "app.js": {Data: []byte("export const app = true;")}, "dir/file.txt": {Data: []byte("file")}}, Options{})
	index := perform(handler, "GET", "/", "", nil)
	token := regexp.MustCompile(`content="([a-f0-9]{64})"`).FindStringSubmatch(index.Body.String())
	if len(token) != 2 {
		t.Fatal("token missing", index.Body.String())
	}
	return svc, f, h, handler, token[1]
}
func perform(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:7788"+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func writeHeaders(token string) map[string]string {
	return map[string]string{"X-LC-Token": token, "Content-Type": "application/json", "Origin": "http://127.0.0.1:7788"}
}
func checkStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}
func checkAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	checkStatus(t, rec, status)
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("bad error response: %s %v", rec.Body.String(), err)
	}
}

func TestRoutesAndSafeReadViews(t *testing.T) {
	_, _, _, h, token := apiFixture(t)
	for _, path := range []string{"/healthz", "/", "/index.html", "/app.js", "/dir/file.txt", "/api/v1/overview", "/api/v1/tasks", "/api/v1/tasks?project=demo&column=in_progress", "/api/v1/tasks/demo/task", "/api/v1/tasks/demo/task/reports/r-report", "/api/v1/tasks/demo/task/terminal", "/api/v1/decisions", "/api/v1/decisions?project=demo&state=open"} {
		t.Run(path, func(t *testing.T) {
			r := perform(h, "GET", path, "", nil)
			checkStatus(t, r, 200)
			if strings.Contains(r.Body.String(), "secret-value") {
				t.Fatal("secret leaked")
			}
			if path == "/" || path == "/index.html" {
				if !strings.Contains(r.Body.String(), token) || strings.Contains(r.Body.String(), "__LC_TOKEN__") {
					t.Fatal("token not injected")
				}
			}
		})
	}
	r := perform(h, "GET", "/healthz", "", nil)
	var health map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &health)
	if sched, _ := health["report_scheduler"].(map[string]any); health["status"] != "ok" || health["version"] != Version || sched == nil || sched["enabled"] != false {
		t.Fatal(health)
	}
	for _, host := range []string{"localhost:7788", "[::1]:7788"} {
		req := httptest.NewRequest("GET", "http://"+host+"/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		checkStatus(t, rec, 200)
	}
}
func TestSecurityMiddleware(t *testing.T) {
	_, _, _, h, token := apiFixture(t)
	path := "/api/v1/decisions/demo/d1/answer"
	body := `{"option":"A","client_request_id":"c"}`
	for _, tc := range []struct {
		name, header, value string
		missing             bool
		status              int
		code                string
	}{
		{name: "missing token", header: "X-LC-Token", missing: true, status: 403, code: "forbidden"},
		{name: "wrong token", header: "X-LC-Token", value: strings.Repeat("0", 64), status: 403, code: "forbidden"},
		{name: "short token", header: "X-LC-Token", value: "x", status: 403, code: "forbidden"},
		{name: "wrong Origin", header: "Origin", value: "http://evil.example", status: 403, code: "forbidden"},
		{name: "null Origin", header: "Origin", value: "null", status: 403, code: "forbidden"},
		{name: "https Origin", header: "Origin", value: "https://127.0.0.1:7788", status: 403, code: "forbidden"},
		{name: "content type", header: "Content-Type", value: "text/plain", status: 415, code: "invalid_content_type"},
		{name: "missing content type", header: "Content-Type", missing: true, status: 415, code: "invalid_content_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hdr := writeHeaders(token)
			if tc.missing {
				delete(hdr, tc.header)
			} else {
				hdr[tc.header] = tc.value
			}
			r := perform(h, "POST", path, body, hdr)
			checkAPIError(t, r, tc.status, tc.code)
		})
	}
	for _, host := range []string{"evil.example:7788", "127.0.0.1:9999", "127.0.0.1", "127.0.0.1:7788.evil", "localhost.:7788", "[::ffff:127.0.0.1]:7788"} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:7788/healthz", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		checkAPIError(t, rec, 403, "forbidden")
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:7788"+path, strings.NewReader(body))
	for k, v := range writeHeaders(token) {
		req.Header.Set(k, v)
	}
	req.Header.Add("Origin", "http://127.0.0.1:7788")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	checkAPIError(t, rec, 403, "forbidden")
	for _, path := range []string{"/", "/healthz", "/api/v1/overview", "/api/v1/missing"} {
		r := perform(h, "GET", path, "", nil)
		if r.Header().Get("Content-Security-Policy") != "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'" || r.Header().Get("X-Content-Type-Options") != "nosniff" || r.Header().Get("Referrer-Policy") != "no-referrer" || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("security headers missing", r.Header())
		}
		if r.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS allowed")
		}
	}
	checkAPIError(t, perform(h, "OPTIONS", path, "", nil), 405, "method_not_allowed")
	hdr := writeHeaders(token)
	delete(hdr, "Origin")
	hdr["Content-Type"] = "application/json; charset=utf-8"
	checkStatus(t, perform(h, "POST", path, body, hdr), 202)
}
func TestAPIValidationAndErrors(t *testing.T) {
	_, f, _, h, token := apiFixture(t)
	headers := writeHeaders(token)
	path := "/api/v1/decisions/demo/d1/answer"
	for _, body := range []string{"", `null`, `[]`, `{`, `{"unknown":1}`, `{"option":"A"} {}`, `{"option":1}`, `{"text":"` + strings.Repeat("x", 2001) + `","client_request_id":"c"}`, `{"option":"bad","client_request_id":"c"}`, `{"option":"A","client_request_id":""}`, string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'})} {
		t.Run(fmt.Sprintf("body-%d", len(body)), func(t *testing.T) { checkAPIError(t, perform(h, "POST", path, body, headers), 400, "invalid_answer") })
	}
	checkAPIError(t, perform(h, "POST", path, `{"text":"`+strings.Repeat("x", 16<<10)+`"}`, headers), 413, "body_too_large")
	checkAPIError(t, perform(h, "POST", "/api/v1/answers/demo/a1/retry", `{"text":"`+strings.Repeat("x", 16<<10)+`"}`, headers), 413, "body_too_large")
	for _, path := range []string{"/api/v1/missing", "/missing.js", "/dir/", "/api/v1/tasks/demo/missing", "/api/v1/tasks/missing/task", "/api/v1/tasks/demo/task/reports/missing"} {
		checkAPIError(t, perform(h, "GET", path, "", nil), 404, "not_found")
	}
	for _, path := range []string{"/api/v1/tasks?column=bad", "/api/v1/decisions?state=bad", "/api/v1/tasks/demo/task/terminal?lines=x", "/api/v1/tasks/demo/task/terminal?lines=-1", "/api/v1/tasks/demo/task/terminal?source=other"} {
		r := perform(h, "GET", path, "", nil)
		checkStatus(t, r, 400)
	}
	checkAPIError(t, perform(h, "GET", path, "", nil), 405, "method_not_allowed")
	checkAPIError(t, perform(h, "POST", "/api/v1/decisions/demo/missing/answer", `{"option":"A","client_request_id":"c"}`, headers), 404, "not_found")
	checkAPIError(t, perform(h, "POST", "/api/v1/answers/demo/missing/retry", "", headers), 404, "not_found")
	f.mu.Lock()
	f.pd.Source = model.SourceLedger
	f.mu.Unlock()
	checkAPIError(t, perform(h, "POST", path, `{"option":"A","client_request_id":"c"}`, headers), 409, "protocol_unavailable")
	f.mu.Lock()
	f.pd.Source = model.SourceState
	f.pd.Decisions[0].Status = model.DecisionExpired
	f.mu.Unlock()
	rec := perform(h, "POST", path, `{"option":"A","client_request_id":"c"}`, headers)
	checkAPIError(t, rec, 409, "already_answered")
	var conflict map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &conflict)
	if conflict["current"] == nil {
		t.Fatal("409 missing current view")
	}
	f.mu.Lock()
	f.reportErr = errors.New("secret-value failure")
	f.mu.Unlock()
	checkAPIError(t, perform(h, "GET", "/api/v1/tasks/demo/task", "", nil), 500, "internal_error")
}

func TestAnswerFlowHTTPToPromptToAckAndRetry(t *testing.T) {
	svc, f, herdr, h, token := apiFixture(t)
	headers := writeHeaders(token)
	path := "/api/v1/decisions/demo/d1/answer"
	rec := perform(h, "POST", path, `{"option":"A","text":"do it","client_request_id":"c-flow"}`, headers)
	checkStatus(t, rec, 202)
	var answer model.DecisionView
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || answer.Answer == nil || answer.State != model.DStateQueued {
		t.Fatal(answer, err)
	}
	t.Logf("POST %s -> 202 state=%s answer_id=%s", path, answer.State, answer.Answer.AnswerID)
	eventually(t, func() bool { return svc.Overview().Decisions[0].State == model.DStateDelivered })
	herdr.mu.Lock()
	if len(herdr.prompts) != 1 || !strings.Contains(herdr.prompts[0], "【决策回传】") {
		t.Fatal("prompt missing")
	}
	t.Logf("fake herdr prompt: %s", herdr.prompts[0])
	herdr.mu.Unlock()
	same := perform(h, "POST", path, `{"option":"A","client_request_id":"c-flow"}`, headers)
	checkStatus(t, same, 202)
	checkAPIError(t, perform(h, "POST", path, `{"option":"A","client_request_id":"c-other"}`, headers), 409, "already_answered")
	// Persisted failed fixture exercises manual retry without waiting for real time.
	f.mu.Lock()
	f.pd.Answers[0].Delivery = model.DeliveryFailed
	f.pd.Answers[0].Attempts = 5
	f.mu.Unlock()
	retryPath := "/api/v1/answers/demo/" + answer.Answer.AnswerID + "/retry"
	checkStatus(t, perform(h, "POST", retryPath, `{}`, headers), 202)
	eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.pd.Answers[0].Delivery == model.DeliveryDelivered && f.pd.Answers[0].Attempts == 6
	})
	f.mu.Lock()
	f.pd.Events = append(f.pd.Events, model.Event{TS: time.Now(), Type: model.EventDecisionAck, DecisionID: "d1", AnswerID: answer.Answer.AnswerID, TaskID: "task", Summary: "acknowledged"})
	f.mu.Unlock()
	eventually(t, func() bool { return svc.Overview().Decisions[0].State == model.DStateAcked })
	rec = perform(h, "GET", "/api/v1/overview", "", nil)
	checkStatus(t, rec, 200)
	t.Log("GET /api/v1/overview -> 200 state=acked (fake store persisted decision_ack)")
	checkAPIError(t, perform(h, "POST", retryPath, "", headers), 409, "already_answered")
}

// This ResponseWriter captures streaming frames without binding a TCP listener.
// Only the handler goroutine touches headers; the test consumes immutable chunks.
type streamWriter struct {
	headers  http.Header
	frames   chan string
	status   int
	writeErr bool
}

func (w *streamWriter) Header() http.Header { return w.headers }
func (w *streamWriter) WriteHeader(n int)   { w.status = n }
func (w *streamWriter) Write(p []byte) (int, error) {
	if w.writeErr {
		return 0, io.ErrClosedPipe
	}
	w.frames <- string(p)
	return len(p), nil
}
func (w *streamWriter) Flush() {}
func nextFrame(t *testing.T, w *streamWriter) string {
	t.Helper()
	select {
	case frame := <-w.frames:
		return frame
	case <-time.After(3 * time.Second):
		t.Fatal("no SSE frame")
		return ""
	}
}
func TestSSEInitialChangesHeartbeatCancellationAndLimit(t *testing.T) {
	svc, _, _, _, _ := apiFixture(t)
	h := &handler{svc: svc, token: "token", port: "7788", heartbeat: 2 * time.Millisecond}
	routes := h.routes()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "http://127.0.0.1:7788/api/v1/stream", nil).WithContext(ctx)
	w := &streamWriter{headers: http.Header{}, frames: make(chan string, 64)}
	done := make(chan struct{})
	go func() { defer close(done); routes.ServeHTTP(w, req) }()
	first := nextFrame(t, w)
	if !strings.HasPrefix(first, "event: version\ndata: {") || !strings.Contains(first, svc.Overview().Version) {
		t.Fatal(first)
	}
	if frame := nextFrame(t, w); frame != ": keepalive\n\n" {
		t.Fatal(frame)
	}
	before := svc.Overview().Version
	_, err := svc.Answer(context.Background(), "demo", "d1", service.AnswerInput{Option: "A", ClientRequestID: "c-sse"})
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for !changed {
		frame := nextFrame(t, w)
		changed = strings.HasPrefix(frame, "event: version") && !strings.Contains(frame, before)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE did not cancel")
	}
	if w.headers.Get("Content-Type") != "text/event-stream" || h.streams.Load() != 0 {
		t.Fatal("SSE headers/slot leak")
	}
	h.streams.Store(16)
	checkAPIError(t, perform(routes, "GET", "/api/v1/stream", "", nil), 503, "stream_limit")
	if h.streams.Load() != 16 {
		t.Fatal("limit counter changed")
	}
	h.streams.Store(0)
	bad := &streamWriter{headers: http.Header{}, frames: make(chan string, 1), writeErr: true}
	routes.ServeHTTP(bad, httptest.NewRequest("GET", "http://127.0.0.1:7788/api/v1/stream", nil))
	if h.streams.Load() != 0 {
		t.Fatal("write error leaked slot")
	}
}

type nonFlushingWriter struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (w *nonFlushingWriter) Header() http.Header         { return w.header }
func (w *nonFlushingWriter) WriteHeader(n int)           { w.code = n }
func (w *nonFlushingWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func TestServerFallbacksAndErrorNormalization(t *testing.T) {
	h := New(nil, fstest.MapFS{}, Options{})
	checkAPIError(t, perform(h, "GET", "/", "", nil), 404, "not_found")
	checkStatus(t, perform(h, "GET", "/api/v1/overview", "", nil), 200)
	serviceHandler := &handler{svc: service.New(service.Deps{}, service.Options{}), heartbeat: time.Second}
	w := &nonFlushingWriter{header: http.Header{}}
	serviceHandler.stream(w, httptest.NewRequest("GET", "/api/v1/stream", nil))
	if w.code != 500 || !strings.Contains(w.body.String(), "stream_unavailable") {
		t.Fatal(w)
	}
	for _, err := range []error{store.ErrProjectNotFound, store.ErrReportNotFound, fmt.Errorf("wrapped: %w", store.ErrReportNotFound)} {
		rec := httptest.NewRecorder()
		writeError(rec, err)
		checkAPIError(t, rec, 404, "not_found")
	}
	// Validate body read failures, not only decode and size errors.
	req := httptest.NewRequest("POST", "/", nil)
	req.Body = io.NopCloser(errorReader{})
	rec := httptest.NewRecorder()
	if readJSON(rec, req, new(service.AnswerInput)) {
		t.Fatal("read failure accepted")
	}
	checkAPIError(t, rec, 400, "invalid_answer")
	for _, addr := range []string{"bad", "127.0.0.1:bad", "127.0.0.1:0"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid address accepted", addr)
				}
			}()
			New(nil, fstest.MapFS{}, Options{Addr: addr})
		}()
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
