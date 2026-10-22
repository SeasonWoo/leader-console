package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/reconcile"
)

var bg = context.Background()

// setupReports uses the real reconcile rules, so the scheduler sees the same identity
// decision as the board and the answer delivery.
func setupReports(t *testing.T) (*Service, *fakeStore, *fakeHerdr, *testClock) {
	t.Helper()
	s, f, h, c := setup(t)
	s.deps.Reconcile = func(pd *model.ProjectData, agents []herdr.Agent, ok bool, now time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
		r := reconcile.Project(pd, agents, ok, now)
		return r.Leader, r.Tasks
	}
	s.opts.Reports = ReportOptions{Enabled: true, StartupDelay: time.Minute}
	f.edit(func(pd *model.ProjectData) { pd.Leader.ReportIntervalMin = 10 })
	if err := s.scan(bg); err != nil {
		t.Fatal(err)
	}
	return s, f, h, c
}

// setLeaderAgent changes what herdr reports for the leader in both list and get.
func setLeaderAgent(s *Service, h *fakeHerdr, fn func(*herdr.Agent)) {
	h.mu.Lock()
	fn(&h.agent)
	h.agents = []herdr.Agent{h.agent}
	h.mu.Unlock()
	s.refreshHerdr(bg)
}

func reportPrompts(h *fakeHerdr) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for i, p := range h.prompts {
		if p == reportText("demo") {
			out = append(out, h.targets[i])
		}
	}
	return out
}

func presence(s *Service) model.LeaderPresence {
	for _, p := range s.Overview().Projects {
		if p.Name == "demo" {
			return p.Leader
		}
	}
	return model.LeaderPresence{}
}

// reportResults returns persisted report outcomes (intent lines excluded).
func reportResults(f *fakeStore, delivery model.DeliveryStatus) []model.PanelLine {
	var out []model.PanelLine
	for _, l := range f.panelLines(model.PanelReport) {
		if l.Delivery == delivery {
			out = append(out, l)
		}
	}
	return out
}

func heartbeat(s *Service, f *fakeStore, at time.Time) {
	f.edit(func(pd *model.ProjectData) { pd.Leader.HeartbeatAt = at; pd.Version += "h" })
	_ = s.scan(bg)
}

func TestReportIdleDeliveryAndCycleDedup(t *testing.T) {
	s, f, h, c := setupReports(t)
	hb := c.now()
	c.advance(10*time.Minute - time.Nanosecond)
	s.reports(bg)
	if n := len(reportPrompts(h)); n != 0 {
		t.Fatal("reported before the interval elapsed")
	}
	if p := presence(s).Report; p == nil || !p.NextDueAt.Equal(hb.Add(10*time.Minute)) || p.IntervalMin != 10 {
		t.Fatalf("schedule = %+v", p)
	}
	c.advance(time.Nanosecond)
	s.reports(bg)
	s.reports(bg)
	if got := reportPrompts(h); len(got) != 1 || got[0] != "leader-demo" {
		t.Fatalf("report prompts = %v", got)
	}
	// Intent first, then the result: the cycle is consumed before anything is typed.
	lines := f.panelLines(model.PanelReport)
	if len(lines) != 2 || lines[0].Delivery != model.DeliveryQueued || lines[1].Delivery != model.DeliveryDelivered || lines[1].Target != "leader-demo" {
		t.Fatalf("audit = %+v", lines)
	}
	// The leader has not refreshed heartbeat_at: the next report waits one more full
	// interval (never stacked), then each further unacknowledged one doubles the wait.
	c.advance(10*time.Minute - time.Nanosecond)
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("stacked a report onto an unacknowledged one")
	}
	c.advance(time.Nanosecond)
	s.reports(bg)
	if len(reportPrompts(h)) != 2 {
		t.Fatal("missing second cycle")
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 2 || presence(s).Report.Unacked != 2 {
		t.Fatalf("no backoff for unresponsive leader: %+v", presence(s).Report)
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 3 {
		t.Fatal("backoff never ends")
	}
	// The leader handles the report and refreshes heartbeat: normal cadence resumes and
	// the panel itself never wrote leader.json.
	c.advance(time.Minute)
	hb = c.now()
	heartbeat(s, f, hb)
	if p := presence(s).Report; p.Unacked != 0 || !p.NextDueAt.Equal(hb.Add(10*time.Minute)) {
		t.Fatalf("after ack = %+v", p)
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 4 {
		t.Fatal("cadence not resumed")
	}
	if len(f.lines) != 0 {
		t.Fatal("report touched answers.jsonl")
	}
}

func TestReportDefersBusyBlockedOfflineAndForeignAgents(t *testing.T) {
	for _, tc := range []struct {
		name string
		wait string
		mod  func(*Service, *fakeStore, *fakeHerdr)
	}{
		{"working", "leader_busy", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "working" })
		}},
		{"unknown", "leader_busy", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "unknown" })
		}},
		{"blocked", "leader_blocked", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.AgentStatus = "blocked" })
		}},
		{"busy at get", "leader_busy", func(_ *Service, _ *fakeStore, h *fakeHerdr) { h.agent.AgentStatus = "working" }},
		{"herdr down", "not_identified", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			h.listErr = errors.New("down")
			s.refreshHerdr(bg)
		}},
		{"leader exited", "not_identified", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			h.agents = nil
			s.refreshHerdr(bg)
		}},
		{"other kind", "not_identified", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.Agent = "codex"; a.SessionID = "" })
		}},
		{"other session", "not_identified", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.SessionID = "other" })
		}},
		{"other cwd", "not_identified", func(s *Service, _ *fakeStore, h *fakeHerdr) {
			setLeaderAgent(s, h, func(a *herdr.Agent) { a.Cwd = "/elsewhere" })
		}},
		{"impostor at get", "not_identified", func(_ *Service, _ *fakeStore, h *fakeHerdr) { h.agent.Name = "impostor" }},
		{"get fails", "not_identified", func(_ *Service, _ *fakeStore, h *fakeHerdr) {
			h.getErr = &herdr.Error{Code: herdr.CodeAgentNotFound}
		}},
		{"blocked at prompt", "leader_blocked", func(_ *Service, _ *fakeStore, h *fakeHerdr) {
			h.promptErrors = []error{&herdr.Error{Code: herdr.CodeAgentBlocked}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, h, c := setupReports(t)
			tc.mod(s, f, h)
			c.advance(10 * time.Minute)
			s.reports(bg)
			want := 0
			if tc.name == "blocked at prompt" {
				want = 1 // herdr refused it; nothing was typed
			}
			if len(h.prompts) != want || len(reportResults(f, model.DeliveryDelivered)) != 0 || len(reportResults(f, model.DeliveryFailed)) != 0 {
				t.Fatalf("unsafe report: prompts=%v panel=%+v", h.prompts, f.panel)
			}
			if p := presence(s); p.Report == nil || p.Report.Waiting != tc.wait {
				t.Fatalf("waiting = %+v", p.Report)
			}
			// Back to an idle, verified leader: the deferral is honoured, then exactly one report.
			h.mu.Lock()
			h.agent = herdr.Agent{Name: "leader-demo", Agent: "claude", Cwd: "/work/demo", PaneID: "w1:p1", SessionID: "session", AgentStatus: "idle"}
			h.agents, h.listErr, h.getErr, h.promptErrors = []herdr.Agent{h.agent}, nil, nil, nil
			h.mu.Unlock()
			s.refreshHerdr(bg)
			s.reports(bg)
			if len(reportPrompts(h)) != want {
				t.Fatal("ignored deferral")
			}
			c.advance(15 * time.Second)
			s.reports(bg)
			s.reports(bg)
			if got := reportPrompts(h); len(got) != want+1 {
				t.Fatalf("after recovery = %v", got)
			}
			if p := presence(s).Report; p.Waiting != "" {
				t.Fatalf("stale waiting = %+v", p)
			}
		})
	}
}

// A stale heartbeat (e.g. the leader's own cron expired) must not stop reports: the
// report is what revives the heartbeat. The panel still shows the leader offline.
func TestReportRevivesStaleLeaderWithoutWritingHeartbeat(t *testing.T) {
	s, f, h, c := setupReports(t)
	hb := c.now()
	c.advance(3 * time.Hour)
	s.refreshHerdr(bg)
	if p := presence(s); p.Online || p.Reason != "heartbeat_stale" || !p.Identified {
		t.Fatalf("presence = %+v", p)
	}
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("stale but identified leader not woken")
	}
	pd, _ := f.LoadProject(bg, "demo")
	if !pd.Leader.HeartbeatAt.Equal(hb) || presence(s).Online {
		t.Fatal("panel faked the heartbeat")
	}
	// Answers keep their existing gate: queued until the leader is online again.
	submit(t, s)
	s.deliver(bg)
	if len(f.lines) != 1 {
		t.Fatal("answer delivered to an offline leader")
	}
}

func TestReportRestartDoesNotReplay(t *testing.T) {
	s, f, h, c := setupReports(t)
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("first report")
	}
	restart := func() *Service {
		fresh := New(s.deps, Options{LockPath: filepath.Join(t.TempDir(), "lock"), Reports: s.opts.Reports})
		fresh.refreshHerdr(bg)
		if err := fresh.scan(bg); err != nil {
			t.Fatal(err)
		}
		return fresh
	}
	// Restart within the cycle: the durable panel.jsonl record keeps the schedule.
	c.advance(3 * time.Minute)
	fresh := restart()
	c.advance(2 * time.Minute)
	fresh.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("restart replayed the current cycle")
	}
	c.advance(5 * time.Minute)
	fresh.reports(bg)
	if len(reportPrompts(h)) != 2 {
		t.Fatal("restart lost the cycle")
	}
	// Panel down for hours, many cycles missed: after the startup delay exactly one report.
	c.advance(5 * time.Hour)
	fresh = restart()
	fresh.reports(bg)
	if len(reportPrompts(h)) != 2 {
		t.Fatal("reported immediately on startup")
	}
	c.advance(time.Minute)
	for i := 0; i < 5; i++ {
		fresh.reports(bg)
	}
	if len(reportPrompts(h)) != 3 || len(reportResults(f, model.DeliveryDelivered)) != 3 {
		t.Fatal("replayed missed cycles")
	}
}

// With grace the leader's own CronCreate keeps priority; the panel is only a backstop.
func TestReportGraceYieldsToLeaderCron(t *testing.T) {
	s, f, h, c := setupReports(t)
	s.opts.Reports.Grace = 5 * time.Minute
	for i := 0; i < 3; i++ {
		c.advance(10 * time.Minute) // the leader's cron fires and it refreshes heartbeat
		s.reports(bg)
		heartbeat(s, f, c.now())
	}
	if len(reportPrompts(h)) != 0 {
		t.Fatal("duplicated the leader's own report")
	}
	c.advance(15 * time.Minute) // cron stopped (e.g. 7-day expiry)
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("backstop did not fire")
	}
}

// A prompt that timed out or stalled may have typed the text: the outcome is unknown,
// so the cycle is used and nothing is resent before the next one.
func TestReportUnknownOutcomeConsumesCycle(t *testing.T) {
	s, f, h, c := setupReports(t)
	h.promptErrors = []error{&herdr.Error{Code: herdr.CodeAgentPromptStalled, Message: "secret-value"}}
	c.advance(10 * time.Minute)
	s.reports(bg)
	failed := reportResults(f, model.DeliveryFailed)
	if len(failed) != 1 || failed[0].Error != "herdr: agent_prompt_stalled: ****" || presence(s).Report.LastError == "" {
		t.Fatalf("failed = %+v", failed)
	}
	c.advance(10*time.Minute - time.Nanosecond)
	for i := 0; i < 3; i++ {
		s.reports(bg)
	}
	if len(h.prompts) != 1 {
		t.Fatal("resent within the cycle")
	}
	c.advance(time.Nanosecond)
	s.reports(bg)
	if got := reportResults(f, model.DeliveryDelivered); len(h.prompts) != 2 || len(got) != 1 {
		t.Fatalf("next cycle = %+v", got)
	}
}

// Review R2: the prompt succeeded but its result could not be recorded. The panel
// keeps the outcome in memory, retries only the audit, and never prompts again.
func TestReportAuditFailureDoesNotResend(t *testing.T) {
	s, f, h, c := setupReports(t)
	f.panelFail = func(l model.PanelLine) error {
		if l.Type == model.PanelReport && l.Delivery != model.DeliveryQueued {
			return errors.New("disk full")
		}
		return nil
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	for i := 0; i < 8; i++ {
		c.advance(15 * time.Second)
		s.reports(bg)
	}
	if n := len(reportPrompts(h)); n != 1 {
		t.Fatalf("resent %d times while the audit failed", n)
	}
	if p := presence(s).Report; p.Waiting != "audit_pending" || p.LastError != "disk full" || p.LastSentAt == nil {
		t.Fatalf("schedule = %+v", p)
	}
	f.panelFail = nil
	c.advance(15 * time.Second)
	s.reports(bg)
	if got := reportResults(f, model.DeliveryDelivered); len(got) != 1 || len(reportPrompts(h)) != 1 || len(s.reportAudit) != 0 {
		t.Fatalf("audit not flushed: %+v", got)
	}
	if p := presence(s).Report; p.Waiting != "" || p.LastError != "" {
		t.Fatalf("after flush = %+v", p)
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if len(reportPrompts(h)) != 2 {
		t.Fatal("next cycle blocked after the audit recovered")
	}
}

// Without a durable intent the panel cannot guarantee one report per cycle: fail closed.
func TestReportIntentFailureFailsClosed(t *testing.T) {
	s, f, h, c := setupReports(t)
	f.panelErr = errors.New("read-only")
	c.advance(time.Hour)
	for i := 0; i < 5; i++ {
		s.reports(bg)
		c.advance(15 * time.Second)
	}
	if len(h.prompts) != 0 {
		t.Fatal("prompted without a durable intent")
	}
	if p := presence(s).Report; p.Waiting != "audit_failed" || p.LastError != "read-only" {
		t.Fatalf("schedule = %+v", p)
	}
	f.panelErr = nil
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("did not recover")
	}
}

// Crash between the intent and the result: after restart the cycle counts as used.
func TestReportOrphanIntentAfterRestart(t *testing.T) {
	s, f, h, c := setupReports(t)
	c.advance(10 * time.Minute)
	f.edit(func(pd *model.ProjectData) {
		pd.Panel.Reports = append(pd.Panel.Reports, model.PanelLine{Type: model.PanelReport, At: c.now(), Delivery: model.DeliveryQueued, Target: "leader-demo"})
		pd.Version += "i"
	})
	fresh := New(s.deps, Options{LockPath: filepath.Join(t.TempDir(), "lock"), Reports: s.opts.Reports})
	fresh.refreshHerdr(bg)
	_ = fresh.scan(bg)
	c.advance(5 * time.Minute)
	fresh.reports(bg)
	if len(h.prompts) != 0 {
		t.Fatal("replayed an attempt with unknown outcome")
	}
	c.advance(5 * time.Minute)
	fresh.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("next cycle lost")
	}
}

func TestReportSchedulerSwitches(t *testing.T) {
	s, f, h, c := setupReports(t)
	s.opts.Reports.Enabled = false
	s.mu.Lock()
	s.publishLocked()
	s.mu.Unlock()
	c.advance(time.Hour)
	s.reports(bg)
	if len(h.prompts) != 0 || presence(s).Report != nil || s.Overview().ReportScheduler.Enabled {
		t.Fatal("disabled scheduler acted")
	}
	s.opts.Reports = ReportOptions{Enabled: true, Projects: []string{"other"}, StartupDelay: time.Minute}
	s.reports(bg)
	s.mu.Lock()
	s.publishLocked()
	s.mu.Unlock()
	if len(h.prompts) != 0 || presence(s).Report != nil {
		t.Fatal("project filter ignored")
	}
	s.opts.Reports.Projects = []string{"demo"}
	s.reports(bg)
	if len(reportPrompts(h)) != 1 || !s.Overview().ReportScheduler.Enabled {
		t.Fatal("allowed project not reported")
	}
	f.edit(func(pd *model.ProjectData) { pd.Source = model.SourceLedger; pd.Version += "l" })
	_ = s.scan(bg)
	c.advance(time.Hour)
	s.reports(bg)
	if len(reportPrompts(h)) != 1 {
		t.Fatal("reported to a project without the lc-1 protocol")
	}
}

// TestLeaderSwitchClaudeToCodex walks the whole switch: the claude leader exits, codex
// starts in the same pane, and until codex rewrites leader.json nothing is typed into
// it; answers stay queued. Afterwards both channels resume and the change is recorded.
func TestLeaderSwitchClaudeToCodex(t *testing.T) {
	s, f, h, c := setupReports(t)
	if leaders := f.panelLines(model.PanelLeader); len(leaders) != 1 || leaders[0].Leader.Agent != "claude" || presence(s).Change != nil {
		t.Fatalf("baseline = %+v", leaders)
	}
	setLeaderAgent(s, h, func(a *herdr.Agent) {
		*a = herdr.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: "/work/demo", AgentStatus: "idle"}
	})
	if p := presence(s); p.Identified || p.Reason != "agent_kind_mismatch" || p.ObservedAgent != "codex" {
		t.Fatalf("switching presence = %+v", p)
	}
	v := submit(t, s)
	c.advance(10 * time.Minute)
	s.deliver(bg)
	s.reports(bg)
	if len(h.prompts) != 0 {
		t.Fatalf("typed into the new CLI before it registered: %v", h.prompts)
	}
	// Codex finishes its startup and rewrites leader.json (no session id).
	setLeaderAgent(s, h, func(a *herdr.Agent) { a.Name = "leader-demo" })
	c.advance(time.Minute)
	f.edit(func(pd *model.ProjectData) {
		pd.Leader.Leader = model.LeaderAgent{Agent: "codex", HerdrName: "leader-demo", PaneID: "w1:p1"}
		pd.Leader.StartedAt = c.now()
		pd.Leader.HeartbeatAt = c.now()
		pd.Version += "switch"
	})
	if err := s.scan(bg); err != nil {
		t.Fatal(err)
	}
	p := presence(s)
	if !p.Online || p.Agent != "codex" || p.Change == nil || !p.Change.Switched || p.Change.From.Agent != "claude" || p.Change.To.Agent != "codex" {
		t.Fatalf("after switch = %+v change=%+v", p, p.Change)
	}
	c.advance(15 * time.Second)
	s.deliver(bg)
	pd, _ := f.LoadProject(bg, "demo")
	if a := panelAnswer(pd, "d1"); a.AnswerID != v.Answer.AnswerID || a.Delivery != model.DeliveryDelivered || a.Target != "leader-demo" {
		t.Fatalf("queued answer after switch = %+v", a)
	}
	// The new leader just started (heartbeat fresh): the next report follows its cadence.
	c.advance(10 * time.Minute)
	s.reports(bg)
	if got := reportPrompts(h); len(got) != 1 || got[0] != "leader-demo" {
		t.Fatalf("report after switch = %v", got)
	}
	// Re-scanning the unchanged leader.json records nothing new.
	_ = s.scan(bg)
	if len(f.panelLines(model.PanelLeader)) != 2 {
		t.Fatal("duplicate leader observation")
	}
}

// Session resume dropped the herdr name: answers and reports reach the same leader by pane.
func TestDeliveryFallsBackToPaneWhenNameLost(t *testing.T) {
	s, f, h, c := setupReports(t)
	setLeaderAgent(s, h, func(a *herdr.Agent) { a.Name = "" })
	if p := presence(s); !p.Online || p.Via != "pane" || p.Target != "w1:p1" {
		t.Fatalf("presence = %+v", p)
	}
	submit(t, s)
	s.deliver(bg)
	if len(f.lines) != 2 || f.lines[1].Delivery != model.DeliveryDelivered || f.lines[1].Target != "w1:p1" {
		t.Fatalf("answer = %+v", f.lines)
	}
	c.advance(10 * time.Minute)
	s.reports(bg)
	if got := reportPrompts(h); len(got) != 1 || got[0] != "w1:p1" {
		t.Fatalf("report = %v", got)
	}
}

func TestObserveLeaderAppendFailureIsAWarning(t *testing.T) {
	s, f, _, _ := setupReports(t)
	f.panelErr = errors.New("read-only")
	f.edit(func(pd *model.ProjectData) { pd.Leader.Leader.PaneID = "w1:p9"; pd.Version += "x" })
	if err := s.scan(bg); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Overview().Projects {
		if p.Name == "demo" && len(p.Warnings) == 0 {
			t.Fatal("missing warning")
		}
	}
}
