package service

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
)

// Regression tests for the second cross-review (review-codex-2.md): the window while
// the report intent is being written (file lock / fsync) and related final gates.

// onIntent runs fn once, while the intent line is being appended.
func onIntent(f *fakeStore, fn func()) {
	done := false
	f.panelHook = func(l model.PanelLine) {
		if l.Type == model.PanelReport && l.Delivery == model.DeliveryQueued && !done {
			done = true
			fn()
		}
	}
}

func notSent(f *fakeStore) []model.PanelLine { return reportResults(f, model.DeliveryRetry) }

// N1 window 1: the leader starts working while the intent waits for the lock.
func TestReview2IntentWaitLeaderTurnsBusy(t *testing.T) {
	s, f, h, c := setupReports(t)
	onIntent(f, func() { setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "working" }) })
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(h.prompts) != 0 {
		t.Fatal("typed into a leader that became busy during the intent write")
	}
	if ns := notSent(f); len(ns) != 1 || ns[0].Error != "not sent: leader_busy" || ns[0].ID == "" || ns[0].ID != f.panelLines(model.PanelReport)[0].ID {
		t.Fatalf("intent not settled as not sent: %+v", f.panelLines(model.PanelReport))
	}
	if p := presence(s).Report; p.Waiting != "leader_busy" || p.LastSentAt != nil {
		t.Fatalf("schedule = %+v", p)
	}
	// The cancelled intent did not use the cycle: once idle, the report goes out.
	setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "idle" })
	c.advance(15 * time.Second)
	s.reports(bg)
	if got := reportPrompts(h); len(got) != 1 {
		t.Fatalf("after idle = %v", got)
	}
}

// N1 window 2: a new leader registers while the intent waits. The old one is not typed at.
func TestReview2IntentWaitLeaderSwitched(t *testing.T) {
	s, f, h, c := setupReports(t)
	onIntent(f, func() {
		f.edit(func(pd *model.ProjectData) {
			pd.Leader.Leader = model.LeaderAgent{Agent: "codex", HerdrName: "new-leader", PaneID: "w1:p2"}
			pd.Leader.StartedAt = c.now()
			pd.Version += "switch"
		})
	})
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(h.prompts) != 0 {
		t.Fatalf("typed into the replaced leader: %v", h.targets)
	}
	if ns := notSent(f); len(ns) != 1 || ns[0].Error != "not sent: not_identified" {
		t.Fatalf("results = %+v", f.panelLines(model.PanelReport))
	}
}

// N1 window 3: the leader's own cron handles a report while the intent waits.
func TestReview2IntentWaitCronHeartbeat(t *testing.T) {
	s, f, h, c := setupReports(t)
	var hb time.Time
	onIntent(f, func() {
		hb = c.now()
		f.edit(func(pd *model.ProjectData) { pd.Leader.HeartbeatAt = hb; pd.Version += "cron" })
	})
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(h.prompts) != 0 {
		t.Fatal("duplicated the report the leader's cron just triggered")
	}
	if p := presence(s).Report; p.Waiting != "" || !p.NextDueAt.Equal(hb.Add(10*time.Minute)) {
		t.Fatalf("schedule after cron = %+v", p)
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("next cycle lost")
	}
}

// N1 with a real file lock: the intent append blocks on panel.jsonl.lock held by
// another process while the leader turns busy; nothing is typed after the release.
func TestReview2RealLockWaitLeaderTurnsBusy(t *testing.T) {
	s, h, c, dir := fsService(t, claudeLeader, map[string]string{})
	lock, err := os.OpenFile(filepath.Join(dir, "panel.jsonl.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	done := make(chan struct{})
	go func() { s.reports(bg); close(done) }()
	time.Sleep(100 * time.Millisecond) // Get done, intent waiting for the flock
	setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "working" })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reports did not finish")
	}
	if len(h.prompts) != 0 {
		t.Fatal("prompted after the lock wait although the leader is working")
	}
	pd, _ := s.deps.Store.LoadProject(bg, "demo")
	if n := len(pd.Panel.Reports); n != 2 || pd.Panel.Reports[1].Delivery != model.DeliveryRetry {
		t.Fatalf("panel = %+v", pd.Panel.Reports)
	}
}

// N3: protocol_version changes while the answer delivery's Get is in flight.
func TestReview2ProtocolChangeDuringAnswerGet(t *testing.T) {
	s, f, h, _ := setupReports(t)
	submit(t, s)
	h.getHook = func() {
		f.edit(func(pd *model.ProjectData) { pd.Leader.ProtocolVersion = "lc-2"; pd.Version += "lc2" })
	}
	s.deliver(bg)
	if len(h.prompts) != 0 {
		t.Fatal("answer delivered to a leader that switched to lc-2 during Get")
	}
	if len(f.lines) != 1 {
		t.Fatalf("delivery attempt consumed: %+v", f.lines)
	}
}

// N4: the result append landed but reported an error (fsync). The audit retry must not
// write it again, and the attempt counts once however often the result appears.
func TestReview2ResultLandedButErroredCountsOnce(t *testing.T) {
	s, f, h, c := setupReports(t)
	failed := false
	f.panelPostErr = func(l model.PanelLine) error {
		if l.Delivery == model.DeliveryDelivered && !failed {
			failed = true
			return errors.New("fsync failed after write")
		}
		return nil
	}
	c.advance(10 * time.Minute)
	sent := c.now()
	s.reports(bg)
	if p := presence(s).Report; p.Waiting != "audit_pending" || p.Unacked != 1 {
		t.Fatalf("pending = %+v", p)
	}
	c.advance(15 * time.Second)
	s.reports(bg)
	if got := reportResults(f, model.DeliveryDelivered); len(got) != 1 || len(reportPrompts(h)) != 1 || len(s.reportAudit) != 0 {
		t.Fatalf("audit retry rewrote the result: %+v", got)
	}
	if p := presence(s).Report; p.Unacked != 1 || !p.NextDueAt.Equal(sent.Add(10*time.Minute)) {
		t.Fatalf("after flush = %+v", p)
	}
	// Even a duplicated result on disk (older binary, manual replay) folds by ID.
	dup := reportResults(f, model.DeliveryDelivered)[0]
	f.edit(func(pd *model.ProjectData) {
		pd.Panel.Reports = append(pd.Panel.Reports, dup)
		pd.Version += "dup"
	})
	_ = s.scan(bg)
	if p := presence(s).Report; p.Unacked != 1 || !p.NextDueAt.Equal(sent.Add(10*time.Minute)) {
		t.Fatalf("duplicate counted twice: %+v", p)
	}
}

func TestReportAttemptsFold(t *testing.T) {
	at := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	q := func(id string, d model.DeliveryStatus, min int) model.PanelLine {
		return model.PanelLine{Type: model.PanelReport, ID: id, Delivery: d, At: at.Add(time.Duration(min) * time.Minute)}
	}
	got := reportAttempts([]model.PanelLine{
		q("a", model.DeliveryQueued, 0), q("a", model.DeliveryDelivered, 0), q("a", model.DeliveryDelivered, 1),
		q("b", model.DeliveryQueued, 10),
		q("", model.DeliveryDelivered, 20), q("", model.DeliveryDelivered, 21), // legacy lines: one attempt each
	})
	if len(got) != 4 || got[0].result.At != at.Add(time.Minute) || got[1].result != nil || got[2].intent != got[2].result {
		t.Fatalf("fold = %+v", got)
	}
}
