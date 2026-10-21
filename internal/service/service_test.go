package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/store"
)

type fakeStore struct {
	mu                                     sync.Mutex
	projects                               map[string]*model.ProjectData
	lines                                  []model.AnswerLine
	listErr, loadErr, appendErr, reportErr error
	panelErr                               error
	panelFail                              func(model.PanelLine) error
	panel                                  []model.PanelLine
	report                                 []byte
	refs                                   []model.ReportRef
}

func (f *fakeStore) ListProjects(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := []string{}
	for n := range f.projects {
		names = append(names, n)
	}
	return names, f.listErr
}
func (f *fakeStore) LoadProject(_ context.Context, p string) (*model.ProjectData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	pd, ok := f.projects[p]
	if !ok {
		return nil, store.ErrProjectNotFound
	}
	return clone(pd), nil
}
func (f *fakeStore) ListReports(context.Context, string, string) ([]model.ReportRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.refs), f.reportErr
}
func (f *fakeStore) ReadReport(context.Context, string, string, string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.report...), f.reportErr
}
func (f *fakeStore) AppendAnswer(_ context.Context, p string, l model.AnswerLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.appendErr != nil {
		return f.appendErr
	}
	pd := f.projects[p]
	if pd == nil {
		return store.ErrProjectNotFound
	}
	f.lines = append(f.lines, l)
	if l.Type == model.LineAnswer {
		pd.Answers = append(pd.Answers, model.Answer{AnswerID: l.AnswerID, DecisionID: l.DecisionID, Option: l.Option, Text: l.Text, ClientRequestID: l.ClientRequestID, AnsweredAt: l.At, UpdatedAt: l.At, Delivery: model.DeliveryQueued})
	} else {
		for i := range pd.Answers {
			a := &pd.Answers[i]
			if a.AnswerID == l.AnswerID {
				a.Delivery = l.Delivery
				a.Attempts = l.Attempt
				a.Target = l.Target
				a.LastError = l.Error
				a.UpdatedAt = l.At
			}
		}
	}
	pd.Version = fmt.Sprint(len(f.lines))
	return nil
}
func (f *fakeStore) AppendPanel(_ context.Context, p string, l model.PanelLine) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panelErr != nil {
		return f.panelErr
	}
	if f.panelFail != nil {
		if err := f.panelFail(l); err != nil {
			return err
		}
	}
	pd := f.projects[p]
	if pd == nil {
		return store.ErrProjectNotFound
	}
	f.panel = append(f.panel, l)
	if l.Type == model.PanelReport {
		pd.Panel.Reports = append(pd.Panel.Reports, l)
	} else {
		pd.Panel.Leaders = append(pd.Panel.Leaders, l)
	}
	pd.Version = fmt.Sprint("p", len(f.panel))
	return nil
}
func (f *fakeStore) panelLines(kind model.PanelLineType) []model.PanelLine {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.PanelLine
	for _, l := range f.panel {
		if l.Type == kind {
			out = append(out, l)
		}
	}
	return out
}
func (f *fakeStore) edit(fn func(*model.ProjectData)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.projects["demo"])
}

type fakeHerdr struct {
	mu                       sync.Mutex
	agent                    herdr.Agent
	agents                   []herdr.Agent
	listErr, getErr, readErr error
	promptErrors             []error
	prompts                  []string
	targets                  []string
	reads                    int
	text                     string
	readOptions              herdr.ReadOptions
	getHook                  func()
}

func (f *fakeHerdr) List(context.Context) ([]herdr.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdr.Agent(nil), f.agents...), f.listErr
}
func (f *fakeHerdr) Get(context.Context, string) (herdr.Agent, error) {
	f.mu.Lock()
	hook := f.getHook
	a, err := f.agent, f.getErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return a, err
}
func (f *fakeHerdr) Read(_ context.Context, _ string, o herdr.ReadOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	f.readOptions = o
	return f.text, f.readErr
}
func (f *fakeHerdr) Prompt(ctx context.Context, target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, text)
	f.targets = append(f.targets, target)
	if len(f.promptErrors) > 0 {
		err := f.promptErrors[0]
		f.promptErrors = f.promptErrors[1:]
		return err
	}
	return ctx.Err()
}

type testClock struct{ nanos atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.nanos.Load()).UTC() }
func (c *testClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }
func setup(t *testing.T) (*Service, *fakeStore, *fakeHerdr, *testClock) {
	t.Helper()
	c := &testClock{}
	c.nanos.Store(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC).UnixNano())
	now := c.now()
	pd := &model.ProjectData{Name: "demo", Source: model.SourceState, Version: "1", Leader: &model.LeaderInfo{SchemaVersion: 1, ProtocolVersion: "lc-1", Cwd: "/work/demo", Leader: model.LeaderAgent{Agent: "claude", HerdrName: "leader-demo", PaneID: "w1:p1", SessionID: "session"}, HeartbeatAt: now}, Tasks: []model.Task{{ID: "task", Name: "worker secret-value", Status: model.TaskWorking, AgentName: "worker", CreatedAt: now.Add(-time.Hour), Goal: "goal secret-value"}}, Decisions: []model.Decision{{ID: "d1", TaskID: "task", Question: "question secret-value", Context: "<context> secret-value", Status: model.DecisionOpen, AllowFreeText: true, Options: []model.Option{{Key: "A", Label: "Do it", Impact: "impact secret-value", Detail: "detail secret-value"}}, CreatedAt: now, UpdatedAt: now}}}
	f := &fakeStore{projects: map[string]*model.ProjectData{"demo": pd}, report: []byte("# report secret-value"), refs: []model.ReportRef{{ID: "r-one", Title: "report secret-value", Size: 21}}}
	h := &fakeHerdr{agent: herdr.Agent{Name: "leader-demo", Agent: "claude", Cwd: "/work/demo", PaneID: "w1:p1", SessionID: "session", AgentStatus: "idle"}, text: "\x1b[31mhello\x1b[0m secret-value\x00\n\tend"}
	h.agents = []herdr.Agent{h.agent}
	reconcile := func(pd *model.ProjectData, agents []herdr.Agent, ok bool, _ time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
		p := model.LeaderPresence{Reason: "herdr_unavailable"}
		if pd.Leader == nil {
			p.Reason = "no_leader_json"
		} else if ok && len(agents) > 0 {
			p = model.LeaderPresence{Online: true, Reason: "ok", Target: pd.Leader.Leader.HerdrName, AgentStatus: agents[0].AgentStatus}
			if p.Target == "" {
				p.Target = pd.Leader.Leader.PaneID
			}
		}
		return p, map[string]model.TaskLive{"task": {AgentFound: ok, AgentStatus: "working", Activity: "activity secret-value"}}
	}
	s := New(Deps{Store: f, Herdr: h, Now: c.now, Reconcile: reconcile, Redact: func(v string) string { return strings.ReplaceAll(v, "secret-value", "****") }, Render: func(v string) string { return "<p>" + html.EscapeString(v) + "</p>" }, ToMerge: func(t model.Task) bool {
		return t.Status == model.TaskDone && strings.Contains(t.ResultSummary, "merge")
	}}, Options{LockPath: filepath.Join(t.TempDir(), "instance.lock")})
	s.refreshHerdr(context.Background())
	if err := s.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, f, h, c
}
func submit(t *testing.T, s *Service) model.DecisionView {
	t.Helper()
	v, err := s.Answer(context.Background(), "demo", "d1", AnswerInput{Option: "A", Text: "note", ClientRequestID: "c-one"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func assertCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("error=%v, want %d %s", err, status, code)
	}
}

func TestDecisionStatePriority(t *testing.T) {
	type scenario struct {
		name     string
		status   model.DecisionStatus
		delivery model.DeliveryStatus
		panel    bool
		via      model.AnswerVia
		ackID    string
		events   []model.EventType
		want     model.DecisionState
	}
	cases := []scenario{
		{name: "open", status: model.DecisionOpen, want: model.DStateOpen},
		{name: "queued", status: model.DecisionOpen, panel: true, want: model.DStateQueued},
		{name: "retry", status: model.DecisionOpen, panel: true, delivery: model.DeliveryRetry, want: model.DStateQueued},
		{name: "delivered", status: model.DecisionOpen, panel: true, delivery: model.DeliveryDelivered, want: model.DStateDelivered},
		{name: "failed", status: model.DecisionOpen, panel: true, delivery: model.DeliveryFailed, want: model.DStateDeliveryFailed},
		{name: "acked by row", status: model.DecisionAnswered, panel: true, via: model.ViaPanel, ackID: "a1", want: model.DStateAcked},
		{name: "wrong row answer", status: model.DecisionAnswered, panel: true, via: model.ViaPanel, ackID: "other", want: model.DStateQueued},
		{name: "ack from queued", status: model.DecisionOpen, panel: true, events: []model.EventType{model.EventDecisionAck}, want: model.DStateAcked},
		{name: "ack from failed", status: model.DecisionOpen, panel: true, delivery: model.DeliveryFailed, events: []model.EventType{model.EventDecisionAck}, want: model.DStateAcked},
		{name: "ack from delivered", status: model.DecisionOpen, panel: true, delivery: model.DeliveryDelivered, events: []model.EventType{model.EventDecisionAck}, want: model.DStateAcked},
		{name: "executing", status: model.DecisionOpen, panel: true, events: []model.EventType{model.EventDecisionExecuting, model.EventDecisionAck}, want: model.DStateExecuting},
		{name: "done from executing", status: model.DecisionOpen, panel: true, events: []model.EventType{model.EventDecisionDone, model.EventDecisionExecuting}, want: model.DStateDone},
		{name: "done from ack", status: model.DecisionOpen, panel: true, events: []model.EventType{model.EventDecisionAck, model.EventDecisionDone}, want: model.DStateDone},
		{name: "chat", status: model.DecisionAnswered, via: model.ViaChat, want: model.DStateInChat},
		{name: "chat overrides panel", status: model.DecisionAnswered, panel: true, via: model.ViaChat, events: []model.EventType{model.EventDecisionDone}, want: model.DStateInChat},
		{name: "expired", status: model.DecisionExpired, want: model.DStateExpired},
		{name: "expired with panel", status: model.DecisionExpired, panel: true, want: model.DStateQueued},
		{name: "superseded overrides done", status: model.DecisionSuperseded, panel: true, events: []model.EventType{model.EventDecisionDone}, want: model.DStateSuperseded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pd := &model.ProjectData{}
			d := model.Decision{ID: "d", Status: tc.status}
			var a *model.Answer
			if tc.via != "" {
				d.Answer = &model.DecisionAnswer{Via: tc.via, AnswerID: tc.ackID}
			}
			if tc.panel {
				a = &model.Answer{AnswerID: "a1", DecisionID: "d", Delivery: tc.delivery}
			}
			for _, kind := range tc.events {
				pd.Events = append(pd.Events, model.Event{Type: kind, DecisionID: "d", AnswerID: "a1"})
			}
			// Unrelated events must never advance this answer.
			pd.Events = append(pd.Events, model.Event{Type: model.EventDecisionDone, DecisionID: "other", AnswerID: "a1"}, model.Event{Type: model.EventDecisionDone, DecisionID: "d", AnswerID: "other"})
			if got := decisionState(pd, d, a); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestAnswerIdempotencyAndConflicts(t *testing.T) {
	s, f, _, _ := setup(t)
	first := submit(t, s)
	if first.State != model.DStateQueued || first.Answer == nil {
		t.Fatalf("%+v", first)
	}
	f.edit(func(pd *model.ProjectData) {
		pd.Decisions[0].Status = model.DecisionAnswered
		pd.Decisions[0].Answer = &model.DecisionAnswer{Via: model.ViaPanel, AnswerID: first.Answer.AnswerID}
	})
	again, err := s.Answer(context.Background(), "demo", "d1", AnswerInput{ClientRequestID: "c-one"})
	if err != nil || again.Answer.AnswerID != first.Answer.AnswerID || again.State != model.DStateAcked || len(f.lines) != 1 {
		t.Fatalf("idempotency: %+v %v", again, err)
	}
	_, err = s.Answer(context.Background(), "demo", "d1", AnswerInput{Option: "A", ClientRequestID: "c-new"})
	assertCode(t, err, 409, "already_answered")
	var e *Error
	_ = errors.As(err, &e)
	if e.Current == nil || strings.Contains(e.Current.Decision.Question, "secret-value") {
		t.Fatal("missing/sensitive conflict current")
	}
	f.edit(func(pd *model.ProjectData) {
		d := pd.Decisions[0]
		d.ID = "d2"
		d.Status = model.DecisionOpen
		pd.Decisions = append(pd.Decisions, d)
	})
	_, err = s.Answer(context.Background(), "demo", "d2", AnswerInput{Option: "A", ClientRequestID: "c-one"})
	assertCode(t, err, 409, "already_answered")
}
func TestAnswerValidation(t *testing.T) {
	cases := []struct {
		name        string
		in          AnswerInput
		edit        func(*model.ProjectData)
		project, id string
		status      int
		code        string
	}{
		{name: "empty", in: AnswerInput{ClientRequestID: "c"}, status: 400, code: "invalid_answer"},
		{name: "unknown option", in: AnswerInput{Option: "X", ClientRequestID: "c"}, status: 400, code: "invalid_answer"},
		{name: "long unicode", in: AnswerInput{Text: strings.Repeat("中", 2001), ClientRequestID: "c"}, status: 400, code: "invalid_answer"},
		{name: "no client id", in: AnswerInput{Option: "A"}, status: 400, code: "invalid_answer"},
		{name: "bad client id", in: AnswerInput{Option: "A", ClientRequestID: "../x"}, status: 400, code: "invalid_answer"},
		{name: "free text forbidden", in: AnswerInput{Text: "x", ClientRequestID: "c"}, edit: func(pd *model.ProjectData) { pd.Decisions[0].AllowFreeText = false }, status: 400, code: "invalid_answer"},
		{name: "closed", in: AnswerInput{Option: "A", ClientRequestID: "c"}, edit: func(pd *model.ProjectData) { pd.Decisions[0].Status = model.DecisionExpired }, status: 409, code: "already_answered"},
		{name: "legacy", in: AnswerInput{Option: "A", ClientRequestID: "c"}, edit: func(pd *model.ProjectData) { pd.Source = model.SourceLedger }, status: 409, code: "protocol_unavailable"},
		{name: "missing", project: "absent", id: "d1", status: 404, code: "not_found"},
		{name: "bad project", project: "../", id: "d1", status: 404, code: "not_found"},
		{name: "bad decision", id: "../", status: 404, code: "not_found"},
		{name: "missing decision", id: "missing", status: 404, code: "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _, _ := setup(t)
			if tc.edit != nil {
				f.edit(tc.edit)
			}
			p, id := tc.project, tc.id
			if p == "" {
				p = "demo"
			}
			if id == "" {
				id = "d1"
			}
			_, err := s.Answer(context.Background(), p, id, tc.in)
			assertCode(t, err, tc.status, tc.code)
			if len(f.lines) != 0 {
				t.Fatal("invalid answer persisted")
			}
		})
	}
	s, _, _, _ := setup(t)
	v, err := s.Answer(context.Background(), "demo", "d1", AnswerInput{Text: strings.Repeat("中", 2000), ClientRequestID: "c-max"})
	if err != nil || utf8.RuneCountInString(v.Answer.Text) != 2000 {
		t.Fatal(v, err)
	}
}
func TestConcurrentAnswerOnlyOnePersists(t *testing.T) {
	s, f, _, _ := setup(t)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Answer(context.Background(), "demo", "d1", AnswerInput{Option: "A", ClientRequestID: fmt.Sprintf("c-%d", i)})
			if err == nil {
				successes.Add(1)
			} else {
				assertCode(t, err, 409, "already_answered")
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 || len(f.lines) != 1 {
		t.Fatalf("success=%d lines=%d", successes.Load(), len(f.lines))
	}
}

func TestOfflineBlockedAndIdentityChecks(t *testing.T) {
	for _, mode := range []string{"offline", "blocked-list", "blocked-get", "blocked-prompt", "cwd", "session", "name", "type", "pane", "get-not-found"} {
		t.Run(mode, func(t *testing.T) {
			s, f, h, c := setup(t)
			submit(t, s)
			switch mode {
			case "offline":
				h.listErr = errors.New("offline")
				s.refreshHerdr(context.Background())
			case "blocked-list":
				h.agents[0].AgentStatus = "blocked"
				s.refreshHerdr(context.Background())
			case "blocked-get":
				h.agent.AgentStatus = "blocked"
			case "blocked-prompt":
				h.promptErrors = []error{&herdr.Error{Code: herdr.CodeAgentBlocked}}
			case "cwd":
				h.agent.Cwd = "/another"
			case "session":
				h.agent.SessionID = "new-session"
			case "name":
				h.agent.Name = "impostor"
			case "type":
				h.agent.Agent = "codex"
			case "pane":
				f.edit(func(pd *model.ProjectData) { pd.Leader.Leader.HerdrName = "" })
				h.agent.PaneID = "w2:p2"
			case "get-not-found":
				h.getErr = &herdr.Error{Code: herdr.CodeAgentNotFound}
			}
			s.deliver(context.Background())
			if len(f.lines) != 1 {
				t.Fatalf("offline/blocked/mismatch consumed attempt: %+v", f.lines)
			}
			if mode == "blocked-prompt" && len(h.prompts) != 1 {
				t.Fatal("missing blocked prompt")
			}
			if mode != "blocked-prompt" && len(h.prompts) != 0 {
				t.Fatal("unsafe prompt")
			}
			h.agent = herdr.Agent{Name: "leader-demo", Agent: "claude", Cwd: "/work/demo", PaneID: "w1:p1", SessionID: "session", AgentStatus: "idle"}
			h.agents = []herdr.Agent{h.agent}
			h.listErr = nil
			h.getErr = nil
			f.edit(func(pd *model.ProjectData) { pd.Leader.Leader.HerdrName = "leader-demo" })
			s.refreshHerdr(context.Background())
			s.deliver(context.Background())
			if len(f.lines) != 1 {
				t.Fatal("ignored offline cooldown")
			}
			c.advance(15 * time.Second)
			s.deliver(context.Background())
			if len(f.lines) != 2 || f.lines[1].Delivery != model.DeliveryDelivered || f.lines[1].Attempt != 1 {
				t.Fatalf("online delivery: %+v", f.lines)
			}
		})
	}
}
func TestDeliveryBackoffFailureRetryAndRecovery(t *testing.T) {
	s, f, h, c := setup(t)
	first := submit(t, s)
	for i := 0; i < 5; i++ {
		h.promptErrors = append(h.promptErrors, &herdr.Error{Code: herdr.CodeAgentPromptStalled, Message: "secret-value"})
	}
	for i := 1; i <= 5; i++ {
		s.deliver(context.Background())
		if len(f.lines) != i+1 || f.lines[i].Attempt != i {
			t.Fatalf("attempt %d: %+v", i, f.lines)
		}
		want := model.DeliveryRetry
		if i == 5 {
			want = model.DeliveryFailed
		}
		if f.lines[i].Delivery != want {
			t.Fatalf("delivery=%s", f.lines[i].Delivery)
		}
		if strings.Contains(f.lines[i].Error, "secret-value") {
			t.Fatal("error leaked")
		}
		s.deliver(context.Background())
		if len(f.lines) != i+1 {
			t.Fatal("retry too early")
		}
		if i < 5 {
			c.advance(backoff(i) - time.Nanosecond)
			s.deliver(context.Background())
			if len(f.lines) != i+1 {
				t.Fatal("backoff boundary")
			}
			c.advance(time.Nanosecond)
		}
	}
	retry, err := s.Retry(context.Background(), "demo", first.Answer.AnswerID)
	if err != nil || retry.State != model.DStateQueued {
		t.Fatal(retry, err)
	}
	if f.lines[6].Attempt != 5 {
		t.Fatal("manual retry reset durable attempt counter")
	}
	s.deliver(context.Background())
	if f.lines[7].Delivery != model.DeliveryDelivered || f.lines[7].Attempt != 6 {
		t.Fatalf("%+v", f.lines)
	}
	c.advance(time.Hour)
	s.deliver(context.Background())
	if len(h.prompts) != 6 {
		t.Fatal("delivered answer automatically resent")
	}
	_, err = s.Retry(context.Background(), "demo", first.Answer.AnswerID)
	assertCode(t, err, 409, "already_answered")
	_, err = s.Retry(context.Background(), "demo", "missing")
	assertCode(t, err, 404, "not_found")
	_, err = s.Retry(context.Background(), "demo", "../")
	assertCode(t, err, 404, "not_found")
}
func TestRestartRecovery(t *testing.T) {
	for _, status := range []model.DeliveryStatus{model.DeliveryQueued, model.DeliveryRetry} {
		t.Run(string(status), func(t *testing.T) {
			s, f, h, c := setup(t)
			v := submit(t, s)
			if status == model.DeliveryRetry {
				h.promptErrors = []error{errors.New("try again")}
				s.deliver(context.Background())
			}
			fresh := New(s.deps, Options{LockPath: filepath.Join(t.TempDir(), "instance.lock")})
			fresh.refreshHerdr(context.Background())
			if err := fresh.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			if status == model.DeliveryRetry {
				fresh.deliver(context.Background())
				if len(h.prompts) != 1 {
					t.Fatal("restart lost backoff")
				}
				c.advance(5 * time.Second)
			}
			fresh.deliver(context.Background())
			pd, _ := f.LoadProject(context.Background(), "demo")
			a := panelAnswer(pd, "d1")
			if a.AnswerID != v.Answer.AnswerID || a.Delivery != model.DeliveryDelivered {
				t.Fatal(a)
			}
		})
	}
}
func TestLeaderAcknowledgementStopsDelivery(t *testing.T) {
	for _, kind := range []model.EventType{model.EventDecisionAck, model.EventDecisionExecuting, model.EventDecisionDone} {
		t.Run(string(kind), func(t *testing.T) {
			s, f, h, _ := setup(t)
			v := submit(t, s)
			f.edit(func(pd *model.ProjectData) {
				pd.Events = append(pd.Events, model.Event{DecisionID: "d1", AnswerID: v.Answer.AnswerID, Type: kind})
			})
			s.deliver(context.Background())
			if len(h.prompts) != 0 {
				t.Fatal("prompted acknowledged answer")
			}
		})
	}
	s, f, h, _ := setup(t)
	submit(t, s)
	h.getHook = func() {
		f.edit(func(pd *model.ProjectData) {
			pd.Decisions[0].Status = model.DecisionAnswered
			pd.Decisions[0].Answer = &model.DecisionAnswer{Via: model.ViaChat}
		})
	}
	s.deliver(context.Background())
	if len(h.prompts) != 0 {
		t.Fatal("prompted after chat takeover")
	}
}
func TestPromptCleaningAndIdentity(t *testing.T) {
	s, _, h, _ := setup(t)
	v, err := s.Answer(context.Background(), "demo", "d1", AnswerInput{Option: "A", Text: "\n\r\x00\x1b[31m" + strings.Repeat("补充", 700) + "\n$(echo injected)", ClientRequestID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	s.deliver(context.Background())
	p := h.prompts[0]
	if strings.ContainsAny(p, "\n\r\x00\x1b\t") || utf8.RuneCountInString(p) > 300 || !strings.Contains(p, "answer="+v.Answer.AnswerID) || !strings.Contains(p, "详见 ~/.leader/demo/answers.jsonl") {
		t.Fatalf("bad prompt %q", p)
	}
	if got := oneLine("\x1b]title\x07x\u202ey\n\tz\x01"); got != "xy z" {
		t.Fatalf("clean=%q", got)
	}
	li := &model.LeaderInfo{Cwd: "/x", Leader: model.LeaderAgent{PaneID: "w1:p1", SessionID: "s"}}
	if !identityMatches(li, "w1:p1", herdr.Agent{Cwd: "/x", PaneID: "w1:p1", SessionID: "s"}) || identityMatches(li, "other", herdr.Agent{Cwd: "/x", PaneID: "w1:p1", SessionID: "s"}) {
		t.Fatal("pane identity")
	}
	// Pane targets need a verifiable session (review R1).
	if identityMatches(li, "w1:p1", herdr.Agent{Cwd: "/x", PaneID: "w1:p1"}) || identityMatches(&model.LeaderInfo{Cwd: "/x", Leader: model.LeaderAgent{PaneID: "w1:p1"}}, "w1:p1", herdr.Agent{Cwd: "/x", PaneID: "w1:p1"}) {
		t.Fatal("sessionless pane identity")
	}
	longAnswer := strings.Repeat("a", 128)
	longPrompt := promptText(strings.Repeat("p", 128), model.Decision{ID: strings.Repeat("d", 128)}, model.Answer{AnswerID: longAnswer})
	if len([]rune(longPrompt)) > 300 || !strings.Contains(longPrompt, "answer="+longAnswer) {
		t.Fatal("long IDs bypass prompt limit")
	}
}

func TestViewsSnapshotsRedactionAndFilters(t *testing.T) {
	s, f, _, c := setup(t)
	f.edit(func(pd *model.ProjectData) {
		pd.Leader.Title = "title secret-value"
		pd.Warnings = []string{"warning secret-value"}
		pd.LegacyHints = []model.LegacyHint{{TaskID: "task", Text: "hint secret-value"}}
		pd.Tasks[0].Links = []model.Link{{Title: "link secret-value", URL: "javascript:alert(1)"}, {URL: "https://example.com/secret-value"}}
		pd.Tasks[0].Reports = []string{"report secret-value"}
		pd.Tasks[0].ResultSummary = "result secret-value"
		pd.Tasks[0].Branch = "branch secret-value"
		pd.Tasks[0].Repo = "repo secret-value"
		pd.Tasks[0].Worktree = "worktree secret-value"
		pd.Decisions[0].RecommendReason = "reason secret-value"
		pd.Decisions[0].ReportRefs = []string{"ref secret-value"}
		deadline := c.now().Add(-time.Minute)
		pd.Decisions[0].Deadline = &deadline
		pd.Events = []model.Event{{TaskID: "task", TS: c.now().Add(-time.Minute), Summary: "old secret-value"}, {TaskID: "task", TS: c.now(), Summary: "new secret-value", Actor: "actor secret-value", Data: json.RawMessage(`{"secret-value":["secret-value",{"nested":"secret-value"}]}`)}, {TaskID: "other", Summary: "unrelated"}}
	})
	if err := s.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	ov := s.Overview()
	if ov.Projects[0].OpenDecisions != 1 || !ov.Decisions[0].Overdue || !ov.Decisions[0].CanAnswer {
		t.Fatalf("%+v", ov)
	}
	detail, err := s.TaskDetail(context.Background(), "demo", "task")
	if err != nil || len(detail.Events) != 2 || !strings.HasPrefix(detail.Events[0].Summary, "new") {
		t.Fatal(detail, err)
	}
	report, err := s.Report(context.Background(), "demo", "task", "r-one")
	if err != nil || strings.Contains(report.HTML, "secret-value") {
		t.Fatal(report, err)
	}
	cards, err := s.Tasks("demo", "")
	if err != nil || len(cards.Tasks) != 1 || cards.Tasks[0].RunningFor != 3600 || cards.Tasks[0].Task.Links[0].URL != "" {
		t.Fatal(cards, err)
	}
	data, _ := json.Marshal([]any{ov, detail, report, cards})
	if strings.Contains(string(data), "secret-value") {
		t.Fatalf("credential leaked: %s", data)
	}
	if !strings.Contains(ov.Decisions[0].ContextHTML, "&lt;context&gt;") {
		t.Fatal("context not rendered")
	}
	// Public views are deep copies, including raw JSON event payloads and options.
	ov.Projects[0].TaskCounts["in_progress"] = 99
	ov.Decisions[0].Decision.Options[0].Label = "mutated"
	detail.Events[0].Data[0] = 'x'
	cards.Tasks[0].Task.Links[0].Title = "mutated"
	if s.Overview().Projects[0].TaskCounts["in_progress"] != 1 || s.Overview().Decisions[0].Decision.Options[0].Label == "mutated" {
		t.Fatal("mutable snapshot escaped")
	}
	pd, _ := f.LoadProject(context.Background(), "demo")
	if !strings.Contains(pd.Tasks[0].Goal, "secret-value") {
		t.Fatal("redaction mutated store")
	}
	c.advance(time.Second)
	cards, _ = s.Tasks("demo", "")
	if cards.Tasks[0].RunningFor != 3601 {
		t.Fatal("running duration did not advance")
	}
	_, err = s.Tasks("missing", "")
	assertCode(t, err, 404, "not_found")
	_, err = s.Tasks("demo", "nonsense")
	assertCode(t, err, 400, "invalid_filter")
	_, err = s.TaskDetail(context.Background(), "demo", "../x")
	assertCode(t, err, 404, "not_found")
	_, err = s.Decisions("demo", "bad")
	assertCode(t, err, 400, "invalid_filter")
	_, err = s.Decisions("../x", "")
	assertCode(t, err, 404, "not_found")
	ds, _ := s.Decisions("demo", "open")
	if len(ds.Decisions) != 1 {
		t.Fatal(ds)
	}
	ds, _ = s.Decisions("demo", "done")
	if len(ds.Decisions) != 0 {
		t.Fatal(ds)
	}
}
func TestBoardColumnsAndRuntime(t *testing.T) {
	s, _, _, c := setup(t)
	start := c.now().Add(-10 * time.Second)
	cases := []struct {
		status  model.TaskStatus
		lost    bool
		source  model.DataSource
		summary string
		want    model.BoardColumn
		runtime int64
	}{
		{model.TaskQueued, false, model.SourceState, "", model.ColumnInProgress, 3600},
		{model.TaskWorking, false, model.SourceState, "", model.ColumnInProgress, 3600},
		{model.TaskBlocked, false, model.SourceState, "", model.ColumnBlocked, 3600},
		{model.TaskNeedsUser, false, model.SourceState, "", model.ColumnBlocked, 3600},
		{model.TaskFailed, false, model.SourceState, "", model.ColumnBlocked, 0},
		{model.TaskLost, false, model.SourceState, "", model.ColumnBlocked, 3600},
		{model.TaskReviewing, false, model.SourceState, "", model.ColumnReviewing, 3600},
		{model.TaskReadyToMerge, false, model.SourceState, "", model.ColumnToMerge, 3600},
		{model.TaskDone, false, model.SourceState, "", model.ColumnDone, 0},
		{model.TaskDone, false, model.SourceLedger, "merge", model.ColumnToMerge, 0},
		{model.TaskWorking, true, model.SourceState, "", model.ColumnBlocked, 3600},
		{model.TaskReviewing, true, model.SourceState, "", model.ColumnBlocked, 3600},
		{model.TaskDone, true, model.SourceState, "", model.ColumnDone, 0},
	}
	for _, tc := range cases {
		v := s.taskView("demo", model.Task{Status: tc.status, CreatedAt: c.now().Add(-time.Hour), ResultSummary: tc.summary}, model.TaskLive{Lost: tc.lost}, tc.source, c.now())
		if v.Column != tc.want || v.RunningFor != tc.runtime {
			t.Fatalf("%+v => %+v", tc, v)
		}
	}
	if runningFor(model.Task{Status: model.TaskWorking, StartedAt: &start}, c.now()) != 10 || runningFor(model.Task{}, c.now()) != 0 || runningFor(model.Task{CreatedAt: c.now().Add(time.Second)}, c.now()) != 0 {
		t.Fatal("runtime boundaries")
	}
}
func TestDecisionSortAndClosedWindow(t *testing.T) {
	s, f, _, c := setup(t)
	now := c.now()
	soon := now.Add(time.Hour)
	later := soon.Add(time.Hour)
	f.edit(func(pd *model.ProjectData) {
		pd.Decisions = []model.Decision{
			{ID: "queued-high", Status: model.DecisionOpen, Urgency: model.UrgencyHigh, CreatedAt: now, UpdatedAt: now},
			{ID: "low", Status: model.DecisionOpen, Urgency: model.UrgencyLow, CreatedAt: now},
			{ID: "normal", Status: model.DecisionOpen, CreatedAt: now},
			{ID: "high-no-deadline", Status: model.DecisionOpen, Urgency: model.UrgencyHigh, CreatedAt: now},
			{ID: "high-later", Status: model.DecisionOpen, Urgency: model.UrgencyHigh, Deadline: &later, CreatedAt: now},
			{ID: "high-soon", Status: model.DecisionOpen, Urgency: model.UrgencyHigh, Deadline: &soon, CreatedAt: now},
			{ID: "old-closed", Status: model.DecisionExpired, UpdatedAt: now.Add(-25 * time.Hour)},
			{ID: "recent-closed", Status: model.DecisionExpired, UpdatedAt: now.Add(-time.Hour)},
			{ID: "boundary-closed", Status: model.DecisionExpired, UpdatedAt: now.Add(-24 * time.Hour)},
		}
		pd.Answers = []model.Answer{{AnswerID: "aq", DecisionID: "queued-high", AnsweredAt: now}}
	})
	_ = s.scan(context.Background())
	ov := s.Overview()
	if len(ov.Decisions) != 8 || ov.Decisions[0].Decision.ID != "high-soon" || ov.Decisions[1].Decision.ID != "high-later" || ov.Decisions[2].Decision.ID != "high-no-deadline" {
		t.Fatalf("sort/window: %+v", ov.Decisions)
	}
	ds, _ := s.Decisions("", "")
	if len(ds.Decisions) != 6 {
		t.Fatal(ds)
	}
	ds, _ = s.Decisions("", "all")
	if len(ds.Decisions) != 9 {
		t.Fatal(ds)
	}
	a := []model.DecisionView{{Project: "z", Decision: model.Decision{ID: "z", CreatedAt: now}}, {Project: "a", Decision: model.Decision{ID: "b", CreatedAt: now}}, {Project: "a", Decision: model.Decision{ID: "a", CreatedAt: now}}, {Project: "a", Decision: model.Decision{ID: "old", CreatedAt: now.Add(-time.Hour)}}}
	sortDecisions(a)
	if a[0].Decision.ID != "old" || a[1].Decision.ID != "a" {
		t.Fatal(a)
	}
}
func TestTerminalThrottlingAndReports(t *testing.T) {
	s, f, h, c := setup(t)
	ctx := context.Background()
	out, err := s.Terminal(ctx, "demo", "task", herdr.ReadOptions{})
	if err != nil || out.Text != "hello ****\n\tend" {
		t.Fatal(out, err)
	}
	again, err := s.Terminal(ctx, "demo", "task", herdr.ReadOptions{})
	if err != nil || again != out || h.reads != 1 {
		t.Fatal("terminal cache")
	}
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{Source: "visible"})
	assertCode(t, err, 429, "terminal_throttled")
	c.advance(5 * time.Second)
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{Source: "visible", Lines: 999})
	if err != nil || h.readOptions.Lines != 400 {
		t.Fatal(err, h.readOptions)
	}
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{Source: "bad"})
	assertCode(t, err, 400, "invalid_terminal")
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{Lines: -1})
	assertCode(t, err, 400, "invalid_terminal")
	c.advance(5 * time.Second)
	h.readErr = errors.New("sensitive error")
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{})
	assertCode(t, err, 503, "herdr_unavailable")
	f.edit(func(pd *model.ProjectData) { pd.Tasks[0].AgentName = ""; pd.Tasks[0].PaneID = "w1:p2" })
	_ = s.scan(ctx)
	c.advance(5 * time.Second)
	h.readErr = nil
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.edit(func(pd *model.ProjectData) { pd.Tasks[0].PaneID = "" })
	_ = s.scan(ctx)
	_, err = s.Terminal(ctx, "demo", "task", herdr.ReadOptions{})
	assertCode(t, err, 404, "not_found")
	_, err = s.Terminal(ctx, "missing", "task", herdr.ReadOptions{})
	assertCode(t, err, 404, "not_found")
	_, err = s.Report(ctx, "demo", "task", "../x")
	assertCode(t, err, 404, "not_found")
	_, err = s.Report(ctx, "demo", "task", "missing")
	assertCode(t, err, 404, "not_found")
	f.report = []byte(strings.Repeat("x", (1<<20)+10))
	f.refs[0].Size = int64(len(f.report))
	r, err := s.Report(ctx, "demo", "task", "r-one")
	if err != nil || !r.Truncated || len(r.HTML) > (1<<20)+7 {
		t.Fatal(err, r.Truncated)
	}
	f.reportErr = store.ErrReportNotFound
	_, err = s.Report(ctx, "demo", "task", "r-one")
	if !errors.Is(err, store.ErrReportNotFound) {
		t.Fatal(err)
	}
	_, err = s.TaskDetail(ctx, "demo", "task")
	if !errors.Is(err, store.ErrReportNotFound) {
		t.Fatal(err)
	}
	if safeURL("https://u:p@example.com") == "" && safeURL("https://example.com") == "https://example.com" && safeURL("%%%") == "" {
	} else {
		t.Fatal("unsafe links")
	}
}
func TestVersionSubscriptions(t *testing.T) {
	s, f, h, c := setup(t)
	ch, cancel := s.Subscribe()
	defer cancel()
	first := <-ch
	if first == "" {
		t.Fatal("no initial version")
	}
	c.advance(time.Second)
	_ = s.scan(context.Background())
	s.refreshHerdr(context.Background())
	select {
	case v := <-ch:
		t.Fatalf("unchanged snapshot notified %s", v)
	default:
	}
	for i := 0; i < 10; i++ {
		f.edit(func(pd *model.ProjectData) { pd.Version = fmt.Sprint(i); pd.Tasks[0].Goal = fmt.Sprint(i) })
		_ = s.scan(context.Background())
	}
	latest := <-ch
	if latest == first || latest != s.Overview().Version {
		t.Fatal("slow subscriber missed latest")
	}
	h.agents[0].AgentStatus = "working"
	s.refreshHerdr(context.Background())
	if <-ch == latest {
		t.Fatal("agent change did not notify")
	}
	cancel()
	cancel()
	f.edit(func(pd *model.ProjectData) { pd.Version = "changed" })
	_ = s.scan(context.Background())
	select {
	case <-ch:
		t.Fatal("cancelled subscription notified")
	default:
	}
}
func TestStoreErrorsAreSafeAndRetainSnapshot(t *testing.T) {
	s, f, _, _ := setup(t)
	ctx := context.Background()
	f.loadErr = errors.New("load failed")
	if err := s.scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.Overview().Projects) != 1 || len(s.Overview().Projects[0].Warnings) == 0 {
		t.Fatal("load failure dropped snapshot")
	}
	_ = s.scan(ctx)
	if len(s.Overview().Projects[0].Warnings) != 1 {
		t.Fatal("repeated failures accumulated duplicate warnings")
	}
	_, err := s.Answer(ctx, "demo", "d1", AnswerInput{Option: "A", ClientRequestID: "c"})
	if !errors.Is(err, f.loadErr) {
		t.Fatal(err)
	}
	f.loadErr = nil
	f.appendErr = errors.New("disk full")
	_, err = s.Answer(ctx, "demo", "d1", AnswerInput{Option: "A", ClientRequestID: "c"})
	if !errors.Is(err, f.appendErr) || len(f.lines) != 0 {
		t.Fatal(err)
	}
	f.appendErr = nil
	submit(t, s)
	f.appendErr = errors.New("disk full")
	s.deliver(ctx)
	if len(f.lines) != 1 {
		t.Fatal("failed persistence advanced state")
	}
	f.listErr = errors.New("list secret-value")
	if err := s.scan(ctx); err == nil {
		t.Fatal("list error lost")
	}
	s.scanWarning(f.listErr)
	if strings.Contains(strings.Join(s.Overview().Projects[0].Warnings, " "), "secret-value") {
		t.Fatal("scan error leaked")
	}
	f.listErr = nil
	delete(f.projects, "demo")
	_ = s.scan(ctx)
	if len(s.Overview().Projects) != 0 {
		t.Fatal("deleted project retained")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.Answer(cancelled, "demo", "d1", AnswerInput{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		case <-ticker.C:
		}
	}
}
func TestRunLifecycleSingleInstanceAndAutomaticDelivery(t *testing.T) {
	s, f, _, _ := setup(t)
	s.opts.ScanInterval = time.Millisecond * 5
	s.opts.HerdrInterval = time.Millisecond * 5
	s.opts.QueueInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitFor(t, func() bool { _, err := os.Stat(s.opts.LockPath); return err == nil && s.running.Load() })
	// Acquire-probe establishes that the first Run owns the flock, not only that
	// it created the inode (avoids a timing-dependent second-instance test).
	waitFor(t, func() bool {
		probe, err := s.instanceLock()
		if err != nil {
			return true
		}
		probe.Close()
		return false
	})
	if err := s.Run(ctx); err == nil {
		t.Fatal("same Service ran twice")
	}
	other := New(s.deps, s.opts)
	if err := other.Run(ctx); err == nil || !strings.Contains(err.Error(), "已有实例") {
		t.Fatal("second instance not rejected", err)
	}
	submit(t, s)
	waitFor(t, func() bool {
		pd, _ := f.LoadProject(ctx, "demo")
		return len(pd.Answers) > 0 && pd.Answers[0].Delivery == model.DeliveryDelivered
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	lock, err := s.instanceLock()
	if err != nil {
		t.Fatal("lock not released", err)
	}
	lock.Close()
	if err := New(Deps{}, Options{}).Run(context.Background()); err == nil {
		t.Fatal("missing Store accepted")
	}
}
func TestDefaultsLockPathsAndStartupFailure(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := New(Deps{}, Options{})
	lock, err := s.instanceLock()
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "leader-console", "instance.lock")); err != nil {
		t.Fatal(err)
	}
	s, f, _, _ := setup(t)
	f.listErr = errors.New("startup read failed")
	if err := s.Run(context.Background()); !errors.Is(err, f.listErr) {
		t.Fatal(err)
	}
	invalid := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(invalid, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s.opts.LockPath = filepath.Join(invalid, "lock")
	if _, err := s.instanceLock(); err == nil {
		t.Fatal("invalid lock path accepted")
	}
	empty := New(Deps{}, Options{})
	ov := empty.Overview()
	if ov.Version == "" || len(ov.Projects) != 0 {
		t.Fatal(ov)
	}
	if empty.deps.Redact("secret") != "[redaction unavailable]" || empty.deps.Redact("") != "" || empty.deps.Render("<x>") != "&lt;x&gt;" || empty.deps.ToMerge(model.Task{}) {
		t.Fatal("unsafe defaults")
	}
	p, _ := empty.deps.Reconcile(&model.ProjectData{}, nil, false, time.Now())
	if p.Reason != "no_leader_json" {
		t.Fatal(p)
	}
	p, _ = empty.deps.Reconcile(&model.ProjectData{Leader: &model.LeaderInfo{}}, nil, false, time.Now())
	if p.Reason != "herdr_unavailable" {
		t.Fatal(p)
	}
	empty.refreshHerdr(context.Background())
	if empty.Overview().Herdr.OK {
		t.Fatal("nil Herdr healthy")
	}
}
