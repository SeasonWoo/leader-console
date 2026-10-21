package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/store"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var targetPattern = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,64}$`)

func validID(s string) bool { return idPattern.MatchString(s) }

func closed(st model.DecisionState) bool {
	switch st {
	case model.DStateDone, model.DStateInChat, model.DStateExpired, model.DStateSuperseded:
		return true
	}
	return false
}
func panelAnswer(pd *model.ProjectData, id string) *model.Answer {
	var found *model.Answer
	for i := range pd.Answers {
		a := &pd.Answers[i]
		if a.DecisionID == id && (found == nil || a.AnsweredAt.After(found.AnsweredAt)) {
			found = a
		}
	}
	return found
}
func decisionState(pd *model.ProjectData, d model.Decision, a *model.Answer) model.DecisionState {
	if d.Status == model.DecisionSuperseded {
		return model.DStateSuperseded
	}
	if d.Status == model.DecisionExpired && a == nil {
		return model.DStateExpired
	}
	if d.Status == model.DecisionAnswered && d.Answer != nil && d.Answer.Via == model.ViaChat {
		return model.DStateInChat
	}
	if a == nil {
		return model.DStateOpen
	}
	ack, executing, done := false, false, false
	for _, e := range pd.Events {
		if e.DecisionID != d.ID || e.AnswerID != a.AnswerID {
			continue
		}
		switch e.Type {
		case model.EventDecisionAck:
			ack = true
		case model.EventDecisionExecuting:
			executing = true
		case model.EventDecisionDone:
			done = true
		}
	}
	if done {
		return model.DStateDone
	}
	if executing {
		return model.DStateExecuting
	}
	if ack || (d.Status == model.DecisionAnswered && d.Answer != nil && d.Answer.Via == model.ViaPanel && d.Answer.AnswerID == a.AnswerID) {
		return model.DStateAcked
	}
	switch a.Delivery {
	case model.DeliveryDelivered:
		return model.DStateDelivered
	case model.DeliveryFailed:
		return model.DStateDeliveryFailed
	}
	return model.DStateQueued
}
func (s *Service) decisionView(pd *model.ProjectData, d model.Decision, now time.Time) model.DecisionView {
	a := panelAnswer(pd, d.ID)
	state := decisionState(pd, d, a)
	return model.DecisionView{Project: pd.Name, Decision: d, State: state, Answer: a, Overdue: d.Deadline != nil && now.After(*d.Deadline) && !closed(state), CanAnswer: state == model.DStateOpen && protocolAvailable(pd), ContextHTML: s.deps.Render(s.deps.Redact(d.Context))}
}

// protocolAvailable reports whether the project speaks the lc-1 panel protocol: a
// structured state.json plus a leader.json declaring protocol_version lc-1. Answer
// admission and every automatic prompt (answer delivery, reports, identity records)
// depend on it, so a leader on an unknown protocol is never typed at.
func protocolAvailable(pd *model.ProjectData) bool {
	return pd != nil && pd.Source == model.SourceState && pd.Leader != nil && pd.Leader.ProtocolVersion == model.ProtocolVersion
}
func closedAt(pd *model.ProjectData, dv model.DecisionView) time.Time {
	t := dv.Decision.UpdatedAt
	if dv.Decision.Answer != nil && dv.Decision.Answer.AnsweredAt.After(t) {
		t = dv.Decision.Answer.AnsweredAt
	}
	if dv.Answer != nil {
		for _, e := range pd.Events {
			if e.DecisionID == dv.Decision.ID && e.AnswerID == dv.Answer.AnswerID && e.Type == model.EventDecisionDone && e.TS.After(t) {
				t = e.TS
			}
		}
	}
	return t
}
func sortDecisions(ds []model.DecisionView) {
	rank := func(u model.Urgency) int {
		switch u {
		case model.UrgencyHigh:
			return 0
		case model.UrgencyLow:
			return 2
		}
		return 1
	}
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if (a.State == model.DStateOpen) != (b.State == model.DStateOpen) {
			return a.State == model.DStateOpen
		}
		if rank(a.Decision.Urgency) != rank(b.Decision.Urgency) {
			return rank(a.Decision.Urgency) < rank(b.Decision.Urgency)
		}
		ad, bd := a.Decision.Deadline, b.Decision.Deadline
		if (ad == nil) != (bd == nil) {
			return ad != nil
		}
		if ad != nil && !ad.Equal(*bd) {
			return ad.Before(*bd)
		}
		if !a.Decision.CreatedAt.Equal(b.Decision.CreatedAt) {
			return a.Decision.CreatedAt.Before(b.Decision.CreatedAt)
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Decision.ID < b.Decision.ID
	})
}
func taskUpdatedAt(t model.Task) time.Time {
	if t.UpdatedAt.IsZero() {
		return t.CreatedAt
	}
	return t.UpdatedAt
}

// The board preserves API order when grouping cards into columns.
func sortTasks(tasks []model.TaskView) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		at, bt := taskUpdatedAt(a.Task), taskUpdatedAt(b.Task)
		if !at.Equal(bt) {
			return at.After(bt)
		}
		if a.Task.ID != b.Task.ID {
			return a.Task.ID < b.Task.ID
		}
		return a.Project < b.Project
	})
}

func (s *Service) taskView(project string, t model.Task, live model.TaskLive, source model.DataSource, now time.Time) model.TaskView {
	col := model.ColumnInProgress
	switch t.Status {
	case model.TaskBlocked, model.TaskNeedsUser, model.TaskFailed, model.TaskLost:
		col = model.ColumnBlocked
	case model.TaskReviewing:
		col = model.ColumnReviewing
	case model.TaskReadyToMerge:
		col = model.ColumnToMerge
	case model.TaskDone:
		col = model.ColumnDone
	}
	if source == model.SourceLedger && s.deps.ToMerge(t) {
		col = model.ColumnToMerge
	}
	if live.Lost && t.Status.Active() {
		col = model.ColumnBlocked
	}
	return model.TaskView{Project: project, Task: t, Live: live, Column: col, RunningFor: runningFor(t, now)}
}
func runningFor(t model.Task, now time.Time) int64 {
	if t.Status == model.TaskDone || t.Status == model.TaskFailed {
		return 0
	}
	start := t.CreatedAt
	if t.StartedAt != nil {
		start = *t.StartedAt
	}
	if start.IsZero() || start.After(now) {
		return 0
	}
	return int64(now.Sub(start).Seconds())
}

func (s *Service) Overview() model.Overview {
	out := clone(s.snap.Load().overview)
	out.GeneratedAt = s.deps.Now()
	for i := range out.Projects {
		p := &out.Projects[i]
		p.Title = s.deps.Redact(p.Title)
		p.Leader.Target = s.deps.Redact(p.Leader.Target)
		p.Leader.AgentStatus = s.deps.Redact(p.Leader.AgentStatus)
		for j := range p.Warnings {
			p.Warnings[j] = s.deps.Redact(p.Warnings[j])
		}
	}
	for i := range out.Decisions {
		s.redactDecision(&out.Decisions[i])
	}
	for i := range out.LegacyHints {
		out.LegacyHints[i].Text = s.deps.Redact(out.LegacyHints[i].Text)
	}
	return out
}

type TasksResult struct {
	Version string           `json:"version"`
	Tasks   []model.TaskView `json:"tasks"`
}

func (s *Service) Tasks(project, column string) (TasksResult, error) {
	snap := s.snap.Load()
	out := TasksResult{Version: snap.overview.Version, Tasks: []model.TaskView{}}
	if err := validateProject(snap, project); err != nil {
		return out, err
	}
	switch model.BoardColumn(column) {
	case "", model.ColumnInProgress, model.ColumnBlocked, model.ColumnReviewing, model.ColumnToMerge, model.ColumnDone:
	default:
		return out, problem(400, "invalid_filter", "Unknown board column")
	}
	for _, tv := range snap.tasks {
		if (project == "" || project == tv.Project) && (column == "" || column == string(tv.Column)) {
			tv = clone(tv)
			tv.RunningFor = runningFor(tv.Task, s.deps.Now())
			s.redactTask(&tv)
			out.Tasks = append(out.Tasks, tv)
		}
	}
	return out, nil
}
func validateProject(snap *snapshot, project string) error {
	if project != "" {
		if !validID(project) || snap.projects[project] == nil {
			return notFound()
		}
	}
	return nil
}
func findTask(snap *snapshot, project, id string) (model.TaskView, error) {
	if !validID(project) || !validID(id) {
		return model.TaskView{}, notFound()
	}
	for _, tv := range snap.tasks {
		if tv.Project == project && tv.Task.ID == id {
			return clone(tv), nil
		}
	}
	return model.TaskView{}, notFound()
}

func (s *Service) TaskDetail(ctx context.Context, project, id string) (model.TaskDetail, error) {
	snap := s.snap.Load()
	tv, err := findTask(snap, project, id)
	if err != nil {
		return model.TaskDetail{}, err
	}
	reports, err := s.deps.Store.ListReports(ctx, project, id)
	if err != nil {
		return model.TaskDetail{}, err
	}
	if reports == nil {
		reports = []model.ReportRef{}
	}
	reports = clone(reports)
	tv.RunningFor = runningFor(tv.Task, s.deps.Now())
	s.redactTask(&tv)
	out := model.TaskDetail{TaskView: tv, Events: []model.Event{}, Reports: reports, Decisions: []model.DecisionView{}}
	for _, e := range snap.projects[project].Events {
		if e.TaskID == id {
			e = clone(e)
			e.Actor = s.deps.Redact(e.Actor)
			e.Summary = s.deps.Redact(e.Summary)
			e.Data = s.redactData(e.Data)
			out.Events = append(out.Events, e)
		}
	}
	sort.SliceStable(out.Events, func(i, j int) bool { return out.Events[i].TS.After(out.Events[j].TS) })
	for i := range out.Reports {
		out.Reports[i].Title = s.deps.Redact(out.Reports[i].Title)
	}
	for _, dv := range snap.decisions {
		if dv.Project == project && dv.Decision.TaskID == id {
			dv = clone(dv)
			s.redactDecision(&dv)
			out.Decisions = append(out.Decisions, dv)
		}
	}
	return out, nil
}

type DecisionsResult struct {
	Version   string               `json:"version"`
	Decisions []model.DecisionView `json:"decisions"`
}

func (s *Service) Decisions(project, state string) (DecisionsResult, error) {
	snap := s.snap.Load()
	out := DecisionsResult{Version: snap.overview.Version, Decisions: []model.DecisionView{}}
	if err := validateProject(snap, project); err != nil {
		return out, err
	}
	switch model.DecisionState(state) {
	case "", "all", model.DStateOpen, model.DStateQueued, model.DStateDelivered, model.DStateAcked, model.DStateExecuting, model.DStateDone, model.DStateDeliveryFailed, model.DStateInChat, model.DStateExpired, model.DStateSuperseded:
	default:
		return out, problem(400, "invalid_filter", "Unknown decision state")
	}
	for _, dv := range snap.decisions {
		if project != "" && project != dv.Project {
			continue
		}
		if state == "" && closed(dv.State) {
			continue
		}
		if state != "" && state != "all" && state != string(dv.State) {
			continue
		}
		dv = clone(dv)
		s.redactDecision(&dv)
		out.Decisions = append(out.Decisions, dv)
	}
	return out, nil
}

type ReportView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Truncated bool   `json:"truncated"`
}

func (s *Service) Report(ctx context.Context, project, task, id string) (ReportView, error) {
	out := ReportView{ID: id}
	if _, err := findTask(s.snap.Load(), project, task); err != nil {
		return out, err
	}
	if !validID(id) {
		return out, notFound()
	}
	refs, err := s.deps.Store.ListReports(ctx, project, task)
	if err != nil {
		return out, err
	}
	var ref *model.ReportRef
	for i := range refs {
		if refs[i].ID == id {
			ref = &refs[i]
			break
		}
	}
	if ref == nil {
		return out, notFound()
	}
	data, err := s.deps.Store.ReadReport(ctx, project, task, id)
	if errors.Is(err, store.ErrReportNotFound) || errors.Is(err, store.ErrProjectNotFound) {
		return out, notFound()
	}
	if err != nil {
		return out, err
	}
	out.Title = s.deps.Redact(ref.Title)
	out.Truncated = ref.Size > int64(len(data)) || len(data) > 1<<20
	if len(data) > 1<<20 {
		data = data[:1<<20]
	}
	out.HTML = s.deps.Render(s.deps.Redact(strings.ToValidUTF8(string(data), "")))
	return out, nil
}

type TerminalView struct {
	Text        string    `json:"text"`
	AgentStatus string    `json:"agent_status"`
	FetchedAt   time.Time `json:"fetched_at"`
}
type terminalEntry struct {
	at   time.Time
	opts herdr.ReadOptions
	view TerminalView
	err  error
}

func (s *Service) Terminal(ctx context.Context, project, task string, opts herdr.ReadOptions) (TerminalView, error) {
	var zero TerminalView
	tv, err := findTask(s.snap.Load(), project, task)
	if err != nil {
		return zero, err
	}
	if opts.Source == "" {
		opts.Source = "recent"
	}
	if opts.Source != "recent" && opts.Source != "visible" {
		return zero, problem(400, "invalid_terminal", "Unknown terminal source")
	}
	if opts.Lines < 0 {
		return zero, problem(400, "invalid_terminal", "Invalid line count")
	}
	if opts.Lines == 0 {
		opts.Lines = 80
	}
	if opts.Lines > 400 {
		opts.Lines = 400
	}
	target := tv.Task.AgentName
	if target == "" {
		target = tv.Task.PaneID
	}
	if !targetPattern.MatchString(target) || s.deps.Herdr == nil || !tv.Live.AgentFound {
		return zero, notFound()
	}
	// Throttle by target, not by query string: changing lines/source cannot bypass it.
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	now := s.deps.Now()
	key := project + "/" + task + "/" + target
	if e, ok := s.terminals[key]; ok && now.Sub(e.at) < 5*time.Second {
		if e.opts == opts {
			return e.view, e.err
		}
		return zero, problem(429, "terminal_throttled", "Wait five seconds before refreshing")
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	text, err := s.deps.Herdr.Read(callCtx, target, opts)
	view := TerminalView{Text: s.deps.Redact(cleanTerminal(text)), AgentStatus: s.deps.Redact(tv.Live.AgentStatus), FetchedAt: now}
	if err != nil {
		err = problem(503, "herdr_unavailable", "Terminal unavailable")
		view = zero
	}
	// Expire stale targets so a long-lived console does not retain terminal history.
	for k, e := range s.terminals {
		if now.Sub(e.at) >= 5*time.Second {
			delete(s.terminals, k)
		}
	}
	s.terminals[key] = terminalEntry{at: now, opts: opts, view: view, err: err}
	return view, err
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-_]")

func cleanTerminal(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}
func oneLine(s string) string { return strings.Join(strings.Fields(cleanTerminal(s)), " ") }
func safeURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return s
}
func (s *Service) redactTask(tv *model.TaskView) {
	t := &tv.Task
	t.Name = s.deps.Redact(t.Name)
	t.Kind = s.deps.Redact(t.Kind)
	t.Role = s.deps.Redact(t.Role)
	t.AgentName = s.deps.Redact(t.AgentName)
	t.PaneID = s.deps.Redact(t.PaneID)
	t.Repo = s.deps.Redact(t.Repo)
	t.Branch = s.deps.Redact(t.Branch)
	t.Worktree = s.deps.Redact(t.Worktree)
	t.Goal = s.deps.Redact(t.Goal)
	t.ResultSummary = s.deps.Redact(t.ResultSummary)
	for i := range t.Reports {
		t.Reports[i] = s.deps.Redact(t.Reports[i])
	}
	for i := range t.Links {
		t.Links[i].Title = s.deps.Redact(t.Links[i].Title)
		t.Links[i].URL = safeURL(s.deps.Redact(t.Links[i].URL))
	}
	tv.Live.Activity = s.deps.Redact(tv.Live.Activity)
	tv.Live.AgentStatus = s.deps.Redact(tv.Live.AgentStatus)
}
func (s *Service) redactDecision(v *model.DecisionView) {
	d := &v.Decision
	d.Question = s.deps.Redact(d.Question)
	d.Context = s.deps.Redact(d.Context)
	d.RecommendReason = s.deps.Redact(d.RecommendReason)
	for i := range d.Options {
		o := &d.Options[i]
		o.Label = s.deps.Redact(o.Label)
		o.Detail = s.deps.Redact(o.Detail)
		o.Impact = s.deps.Redact(o.Impact)
	}
	for i := range d.ReportRefs {
		d.ReportRefs[i] = s.deps.Redact(d.ReportRefs[i])
	}
	if d.Answer != nil {
		d.Answer.Summary = s.deps.Redact(d.Answer.Summary)
	}
	if v.Answer != nil {
		v.Answer.Text = s.deps.Redact(v.Answer.Text)
		v.Answer.LastError = s.deps.Redact(v.Answer.LastError)
		v.Answer.Target = s.deps.Redact(v.Answer.Target)
	}
}
func (s *Service) redactData(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return s.deps.Redact(x)
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, v := range x {
				out[s.deps.Redact(k)] = walk(v)
			}
			return out
		}
		return v
	}
	out, _ := json.Marshal(walk(v))
	return out
}
