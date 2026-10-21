package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"leader-console/internal/model"
)

func readStatic(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(Static(), name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// 前端源码目录（相对本包）。mock JSON 与渲染安全检查直接读源码，不依赖构建产物。
const frontendSrc = "../frontend/src"

func readSource(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(frontendSrc, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func strictDecode(t *testing.T, name string, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(readSource(t, name)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("strict decode %s into %T: %v", name, v, err)
	}
	if dec.More() {
		t.Fatalf("%s: trailing data", name)
	}
}

var pages = []string{"index.html", "board/index.html", "task/index.html", "404.html"}

func TestEmbeddedFiles(t *testing.T) {
	for _, name := range append([]string{"favicon.png", "theme-init.js"}, pages...) {
		if _, err := fs.Stat(Static(), name); err != nil {
			t.Errorf("missing embedded file %s: %v", name, err)
		}
	}
	exts := map[string]int{}
	err := fs.WalkDir(Static(), "_next/static", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			exts[filepath.Ext(path)]++
		}
		return err
	})
	if err != nil || exts[".js"] == 0 || exts[".css"] == 0 {
		t.Errorf("_next/static must contain js and css chunks (got %v, err %v)", exts, err)
	}
}

// 页面 HTML：服务端在 <head> 开头注入 lc-token meta，并按哈希放行页面自带的内联脚本（internal/server）。
// CSP 仍是 style-src 'self'：不能有内联样式和内联事件；手动外观切换走外链脚本。
func TestPagesHTML(t *testing.T) {
	for _, name := range pages {
		html := string(readStatic(t, name))
		if !regexp.MustCompile(`(?i)<head(\s[^>]*)?>`).MatchString(html) {
			t.Errorf("%s: missing <head> (token injection point)", name)
		}
		if strings.Contains(html, "lc-token") || strings.Contains(html, "__LC_TOKEN__") {
			t.Errorf("%s: the token meta is injected by the server and must not be in the export", name)
		}
		if regexp.MustCompile(`(?i)<style|\sstyle=|\son[a-z]+=`).MatchString(html) {
			t.Errorf("%s: must not contain inline styles or event handlers", name)
		}
		if !strings.Contains(html, `<script src="/theme-init.js">`) {
			t.Errorf("%s: theme-init.js must be loaded as an external, synchronous script", name)
		}
	}
}

// 所有 <script src> / <link href> 必须是同源相对路径，且文件已嵌入。
func TestSameOriginAssets(t *testing.T) {
	for _, name := range pages {
		html := string(readStatic(t, name))
		refs := regexp.MustCompile(`<(?:script|link)\b[^>]*\s(?:src|href)="([^"]*)"`).FindAllStringSubmatch(html, -1)
		if len(refs) < 2 {
			t.Fatalf("%s: expected script and stylesheet references, got %v", name, refs)
		}
		for _, m := range refs {
			u, _, _ := strings.Cut(m[1], "?")
			if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
				t.Errorf("%s: asset %q is not a same-origin relative path", name, m[1])
				continue
			}
			if _, err := fs.Stat(Static(), strings.TrimPrefix(u, "/")); err != nil {
				t.Errorf("%s: asset %q is not embedded: %v", name, m[1], err)
			}
		}
	}
}

// HTML / CSS 中不得出现 http(s):// 外链（字体、样式、脚本一律本地）。JS 包里 React/Next 的报错文案含文档链接，
// 不在此检查范围；运行时由 CSP default-src 'self' / connect-src 'self' 阻止任何外部加载。
func TestNoExternalReferences(t *testing.T) {
	ext := regexp.MustCompile(`(?i)https?://`)
	err := fs.WalkDir(Static(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".css")) {
			return err
		}
		b := readStatic(t, path)
		if loc := ext.FindIndex(b); loc != nil {
			t.Errorf("%s: external reference %q", path, b[loc[0]:min(len(b), loc[1]+40)])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sourceFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(frontendSrc, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			rel, _ := filepath.Rel(frontendSrc, path)
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			out[filepath.ToSlash(rel)] = string(b)
		}
		return err
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("read frontend sources: %v (%d files)", err, len(out))
	}
	return out
}

// 渲染安全（design §9）：唯一的 dangerouslySetInnerHTML 在 components/server-html.tsx，
// 调用方只传服务端渲染的 *_html / html 字段；不使用其他会解析 HTML 字符串或执行代码的 API。
func TestRenderingSafety(t *testing.T) {
	banned := regexp.MustCompile(`\.innerHTML\b|\.outerHTML\b|insertAdjacentHTML|document\.write|\beval\(|new Function\(|createContextualFragment|DOMParser`)
	inner := regexp.MustCompile(`dangerouslySetInnerHTML`)
	call := regexp.MustCompile(`<ServerHTML\b[^>]*\shtml=\{([^}]*)\}`)
	allowedArg := regexp.MustCompile(`^[\w.?]+\.(?:context_html|html)$`)
	calls := 0
	for path, src := range sourceFiles(t) {
		if loc := banned.FindStringIndex(src); loc != nil {
			t.Errorf("%s: banned DOM API %q", path, src[loc[0]:loc[1]])
		}
		n := len(inner.FindAllStringIndex(src, -1))
		if path == "components/server-html.tsx" {
			if n != 1 || !strings.Contains(src, "dangerouslySetInnerHTML={{ __html: html }}") {
				t.Errorf("server-html.tsx: dangerouslySetInnerHTML must appear exactly once, on the ServerHTML root (found %d)", n)
			}
			continue
		}
		if n > 0 {
			t.Errorf("%s: dangerouslySetInnerHTML is only allowed in components/server-html.tsx", path)
		}
		for _, m := range call.FindAllStringSubmatch(src, -1) {
			calls++
			if !allowedArg.MatchString(strings.TrimSpace(m[1])) {
				t.Errorf("%s: <ServerHTML html={%s}> must receive a server-rendered *_html field", path, m[1])
			}
		}
	}
	if calls < 2 {
		t.Errorf("expected ServerHTML call sites for context_html and report html, got %d", calls)
	}
}

// ---- mock JSON 与视图契约 ----

type tasksResponse struct {
	Version string           `json:"version"`
	Tasks   []model.TaskView `json:"tasks"`
}

type reportResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Truncated bool   `json:"truncated"`
}

type terminalResponse struct {
	Text        string    `json:"text"`
	AgentStatus string    `json:"agent_status"`
	FetchedAt   time.Time `json:"fetched_at"`
}

type mockData struct {
	overviews map[string]model.Overview
	tasks     tasksResponse
	details   map[string]model.TaskDetail
	reports   map[string]reportResponse
	terminal  map[string]map[string]terminalResponse
}

func loadMock(t *testing.T) mockData {
	t.Helper()
	m := mockData{overviews: map[string]model.Overview{}}
	for _, name := range []string{"overview", "overview-herdr-down"} {
		var ov model.Overview
		strictDecode(t, "mock/"+name+".json", &ov)
		m.overviews[name] = ov
	}
	strictDecode(t, "mock/tasks.json", &m.tasks)
	strictDecode(t, "mock/details.json", &m.details)
	strictDecode(t, "mock/reports.json", &m.reports)
	strictDecode(t, "mock/terminal.json", &m.terminal)
	return m
}

func TestMockJSONMatchesViewContract(t *testing.T) {
	m := loadMock(t)
	tasks := map[string]model.TaskView{}
	for _, tv := range m.tasks.Tasks {
		tasks[tv.Project+"/"+tv.Task.ID] = tv
	}
	for key, d := range m.details {
		if key != d.Project+"/"+d.Task.ID {
			t.Errorf("details key %q does not match %s/%s", key, d.Project, d.Task.ID)
		}
		if _, ok := tasks[key]; !ok {
			t.Errorf("details %q has no matching entry in tasks.json", key)
		}
		for _, r := range d.Reports {
			if _, ok := m.reports[key+"/"+r.ID]; !ok {
				t.Errorf("report %s/%s listed in details but missing from reports.json", key, r.ID)
			}
		}
	}
	for key, r := range m.reports {
		if !strings.HasSuffix(key, "/"+r.ID) {
			t.Errorf("reports key %q does not end with id %q", key, r.ID)
		}
	}
	for key, srcs := range m.terminal {
		if _, ok := tasks[key]; !ok {
			t.Errorf("terminal %q has no matching task", key)
		}
		for _, s := range []string{"visible", "recent"} {
			if _, ok := srcs[s]; !ok {
				t.Errorf("terminal %q missing source %q", key, s)
			}
		}
	}

	ov := m.overviews["overview"]
	projects := map[string]model.ProjectSummary{}
	for _, p := range ov.Projects {
		projects[p.Name] = p
	}
	open := map[string]int{}
	for _, d := range ov.Decisions {
		if _, ok := projects[d.Project]; !ok {
			t.Errorf("decision %s references unknown project %q", d.Decision.ID, d.Project)
		}
		if d.State == model.DStateOpen {
			open[d.Project]++
		}
		if d.Answer != nil && d.Answer.DecisionID != d.Decision.ID {
			t.Errorf("decision %s answer belongs to %s", d.Decision.ID, d.Answer.DecisionID)
		}
		if d.CanAnswer != (d.State == model.DStateOpen) {
			t.Errorf("decision %s: can_answer=%v with state %s", d.Decision.ID, d.CanAnswer, d.State)
		}
	}
	counts := map[string]map[string]int{}
	for _, tv := range m.tasks.Tasks {
		if _, ok := projects[tv.Project]; !ok {
			t.Errorf("task %s references unknown project %q", tv.Task.ID, tv.Project)
		}
		if counts[tv.Project] == nil {
			counts[tv.Project] = map[string]int{}
		}
		counts[tv.Project][string(tv.Column)]++
	}
	for _, p := range ov.Projects {
		if p.OpenDecisions != open[p.Name] {
			t.Errorf("project %s open_decisions=%d, decisions list has %d", p.Name, p.OpenDecisions, open[p.Name])
		}
		for col, n := range p.TaskCounts {
			if counts[p.Name][col] != n {
				t.Errorf("project %s task_counts[%s]=%d, tasks.json has %d", p.Name, col, n, counts[p.Name][col])
			}
		}
	}
}

// mock 必须覆盖所有展示状态（docs/tasks.md 任务 C 第 7 点）。
func TestMockCoversAllStates(t *testing.T) {
	m := loadMock(t)
	ov := m.overviews["overview"]

	states := map[model.DecisionState]bool{}
	for _, d := range ov.Decisions {
		states[d.State] = true
	}
	for _, s := range []model.DecisionState{
		model.DStateOpen, model.DStateQueued, model.DStateDelivered, model.DStateAcked, model.DStateExecuting,
		model.DStateDone, model.DStateDeliveryFailed, model.DStateInChat, model.DStateExpired, model.DStateSuperseded,
	} {
		if !states[s] {
			t.Errorf("mock overview has no decision in state %q", s)
		}
	}

	var offline, legacy bool
	for _, p := range ov.Projects {
		offline = offline || !p.Leader.Online
		legacy = legacy || p.Source == model.SourceLedger
	}
	if !offline {
		t.Error("mock overview has no offline leader")
	}
	if !legacy {
		t.Error("mock overview has no ledger.md (legacy) project")
	}
	if len(ov.LegacyHints) == 0 {
		t.Error("mock overview has no legacy hints")
	}
	if !ov.Herdr.OK {
		t.Error("default mock overview should have herdr available")
	}

	down := m.overviews["overview-herdr-down"]
	if down.Herdr.OK || down.Herdr.Error == "" {
		t.Error("overview-herdr-down must have herdr.ok=false with an error")
	}
	for _, p := range down.Projects {
		if p.Source == model.SourceState && (p.Leader.Online || p.Leader.Reason != "herdr_unavailable") {
			t.Errorf("herdr-down project %s leader = %+v", p.Name, p.Leader)
		}
	}

	var lost, failed bool
	cols := map[model.BoardColumn]bool{}
	for _, tv := range m.tasks.Tasks {
		lost = lost || tv.Live.Lost
		failed = failed || tv.Task.Status == model.TaskFailed
		cols[tv.Column] = true
		if tv.Live.Lost && tv.Column != model.ColumnBlocked {
			t.Errorf("lost task %s/%s must be in the blocked column", tv.Project, tv.Task.ID)
		}
	}
	if !lost || !failed {
		t.Errorf("mock tasks must include lost (%v) and failed (%v) tasks", lost, failed)
	}
	for _, c := range []model.BoardColumn{model.ColumnInProgress, model.ColumnBlocked, model.ColumnReviewing, model.ColumnToMerge, model.ColumnDone} {
		if !cols[c] {
			t.Errorf("mock tasks have no card in column %q", c)
		}
	}
}

// mock JSON 会被打包进前端：外部链接只允许出现在键名为 url 的链接字段里。
func TestMockNoExternalLinksOutsideURL(t *testing.T) {
	ext := regexp.MustCompile(`(?i)https?://`)
	for _, name := range []string{"overview", "overview-herdr-down", "tasks", "details", "reports", "terminal"} {
		var v any
		if err := json.Unmarshal(readSource(t, "mock/"+name+".json"), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		walkJSON(v, "", func(key, s string) {
			if ext.MatchString(s) && key != "url" {
				t.Errorf("mock/%s.json: external link outside a url field (key %q): %q", name, key, s)
			}
		})
	}
}

func walkJSON(v any, key string, fn func(key, s string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			walkJSON(c, k, fn)
		}
	case []any:
		for _, c := range x {
			walkJSON(c, key, fn)
		}
	case string:
		fn(key, x)
	}
}
