package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/reconcile"
	"leader-console/internal/store"
)

// Regression tests for the cross-review of feat/lc-leader-scheduler (review-codex.md §6):
// each reproduction is kept as a positive assertion of the fixed behaviour.

func realReconcile(pd *model.ProjectData, agents []herdr.Agent, ok bool, now time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
	r := reconcile.Project(pd, agents, ok, now)
	return r.Leader, r.Tasks
}

// fsService runs the service on a real FS store in a temporary leader root.
func fsService(t *testing.T, leaderJSON string, files map[string]string) (*Service, *fakeHerdr, *testClock, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	files["leader.json"] = leaderJSON
	if _, ok := files["state.json"]; !ok {
		files["state.json"] = `{"schema_version":1,"project":"demo","tasks":[]}`
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := store.NewFS(root, store.FSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := &testClock{}
	c.nanos.Store(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC).UnixNano())
	h := &fakeHerdr{agent: herdr.Agent{Name: "leader-demo", Agent: "claude", Cwd: "/work/demo", PaneID: "w1:p1", SessionID: "session", AgentStatus: "idle"}}
	h.agents = []herdr.Agent{h.agent}
	s := New(Deps{Store: fs, Herdr: h, Now: c.now, Reconcile: realReconcile, Redact: func(v string) string { return v }},
		Options{LockPath: filepath.Join(t.TempDir(), "lock"), Reports: ReportOptions{Enabled: true}})
	s.refreshHerdr(bg)
	if err := s.scan(bg); err != nil {
		t.Fatal(err)
	}
	return s, h, c, dir
}

const claudeLeader = `{"schema_version":1,"protocol_version":"lc-1","project":"demo","cwd":"/work/demo","leader":{"agent":"claude","herdr_name":"leader-demo","pane_id":"w1:p1","session_id":"session"},"started_at":"2026-10-21T07:00:00Z","heartbeat_at":"2026-10-21T07:50:00Z","report_interval_min":10}`

// R3: a half line left by a crash must not swallow the next report record; otherwise
// the scheduler forgets the report and sends again on the next tick.
func TestReviewPartialTailKeepsDeliveryRecord(t *testing.T) {
	baseline := `{"type":"leader","at":"2026-10-21T07:00:00Z","leader":{"agent":"claude","herdr_name":"leader-demo","pane_id":"w1:p1","session_id":"session"},"started_at":"2026-10-21T07:00:00Z"}` + "\n"
	s, h, c, dir := fsService(t, claudeLeader, map[string]string{"panel.jsonl": baseline + `{"type":"report","at":`})
	c.advance(time.Minute)
	s.reports(bg)
	s.reports(bg)
	c.advance(time.Second)
	s.reports(bg)
	if n := len(reportPrompts(h)); n != 1 {
		t.Fatalf("prompts = %d", n)
	}
	pd, err := s.deps.Store.LoadProject(bg, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(pd.Panel.Reports) != 2 || pd.Panel.Reports[1].Delivery != model.DeliveryDelivered || len(pd.Warnings) != 0 {
		t.Fatalf("reports = %+v warnings = %v", pd.Panel.Reports, pd.Warnings)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "panel.jsonl")); strings.Contains(string(b), `"at":{`) || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("panel.jsonl = %s", b)
	}
}

// R1: a sessionless codex leader exited and an unrelated unnamed codex reuses its pane
// in the same directory. Neither the queued answer nor a report may reach it.
func TestReviewSessionlessReusedPaneGetsNothing(t *testing.T) {
	s, f, h, c := setupReports(t)
	f.edit(func(pd *model.ProjectData) {
		pd.Leader.Leader = model.LeaderAgent{Agent: "codex", HerdrName: "leader-demo", PaneID: "w1:p1"}
		pd.Version += "codex"
	})
	setLeaderAgent(s, h, func(a *herdr.Agent) {
		*a = herdr.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: "/work/demo", AgentStatus: "idle"}
	})
	_ = s.scan(bg)
	if p := presence(s); p.Identified || p.Reason != "agent_not_found" {
		t.Fatalf("presence = %+v", p)
	}
	submit(t, s)
	for i := 0; i < 4; i++ {
		c.advance(time.Hour)
		s.deliver(bg)
		s.reports(bg)
	}
	if len(h.prompts) != 0 {
		t.Fatalf("typed into a reused pane: %v -> %v", h.targets, h.prompts)
	}
	// The leader restores its name (protocol A4): both channels resume.
	setLeaderAgent(s, h, func(a *herdr.Agent) { a.Name = "leader-demo" })
	f.edit(func(pd *model.ProjectData) { pd.Leader.HeartbeatAt = c.now(); pd.Version += "hb" })
	_ = s.scan(bg)
	c.advance(15 * time.Second)
	s.deliver(bg)
	if len(h.prompts) != 1 || h.targets[0] != "leader-demo" {
		t.Fatalf("after rename = %v", h.targets)
	}
}

// R5: another process holding panel.jsonl.lock must neither stall the service mutex
// nor ignore cancellation.
func TestReviewPanelLockDoesNotBlockService(t *testing.T) {
	s, _, _, dir := fsService(t, claudeLeader, map[string]string{})
	if err := os.WriteFile(filepath.Join(dir, "leader.json"), []byte(strings.Replace(claudeLeader, `"w1:p1"`, `"w1:p9"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "panel.jsonl.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- s.scan(ctx) }()
	time.Sleep(50 * time.Millisecond)
	if !s.mu.TryLock() {
		t.Fatal("service mutex held while waiting for panel.jsonl.lock")
	}
	s.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scan ignored cancellation")
	}
	for _, p := range s.Overview().Projects {
		if p.Name == "demo" && len(p.Warnings) == 0 {
			t.Fatal("failed observation not reported")
		}
	}
}

// The leader's own cron refreshes heartbeat_at while Get is in flight: no report.
func TestReviewHeartbeatDuringGetSuppressesReport(t *testing.T) {
	s, f, h, c := setupReports(t)
	c.advance(10 * time.Minute)
	h.getHook = func() { f.edit(func(pd *model.ProjectData) { pd.Leader.HeartbeatAt = c.now(); pd.Version += "cron" }) }
	s.reports(bg)
	if len(h.prompts) != 0 || len(f.panelLines(model.PanelReport)) != 0 {
		t.Fatal("reported although the leader just handled its own report")
	}
}

// protocol_version other than lc-1: no answers, no reports, no identity records.
func TestReviewNonLc1LeaderIsNeverPrompted(t *testing.T) {
	s, f, h, c := setupReports(t)
	v := submit(t, s)
	f.edit(func(pd *model.ProjectData) {
		pd.Leader.ProtocolVersion = "lc-2"
		pd.Leader.Leader.PaneID = "w1:p7"
		pd.Version += "lc2"
	})
	_ = s.scan(bg)
	before := len(f.panelLines(model.PanelLeader))
	c.advance(time.Hour)
	s.deliver(bg)
	s.reports(bg)
	_ = s.scan(bg)
	if len(h.prompts) != 0 || len(f.panelLines(model.PanelLeader)) != before || presence(s).Report != nil {
		t.Fatalf("prompted a non-lc-1 leader: %v", h.prompts)
	}
	if _, err := s.Answer(bg, "demo", "d1", AnswerInput{Option: "A", ClientRequestID: "c-other"}); err == nil {
		t.Fatal("answer admitted for a non-lc-1 leader")
	}
	for _, d := range s.Overview().Decisions {
		if d.CanAnswer {
			t.Fatal("can_answer for a non-lc-1 leader")
		}
	}
	_ = v
}
