package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/store"
)

// Deps are the frozen ports supplied by A and D at integration time.
type Deps struct {
	Store     store.Store
	Herdr     herdr.Client
	Reconcile func(*model.ProjectData, []herdr.Agent, bool, time.Time) (model.LeaderPresence, map[string]model.TaskLive)
	Redact    func(string) string
	Render    func(string) string
	ToMerge   func(model.Task) bool
	Now       func() time.Time
}

type Options struct {
	ScanInterval  time.Duration
	HerdrInterval time.Duration
	// QueueInterval bounds how quickly a newly queued answer is picked up (default 250ms).
	QueueInterval time.Duration
	// LockPath defaults to $XDG_STATE_HOME/leader-console/instance.lock.
	LockPath string
}

type snapshot struct {
	overview  model.Overview
	projects  map[string]*model.ProjectData
	tasks     []model.TaskView
	decisions []model.DecisionView
}

// Service publishes immutable snapshots. mu serializes authoritative store reads,
// answer admission and appends; network calls never hold it. Store.AppendAnswer
// owns the answers.jsonl flock, and Run holds the process-wide instance flock.
type Service struct {
	deps       Deps
	opts       Options
	snap       atomic.Pointer[snapshot]
	mu         sync.Mutex
	projects   map[string]*model.ProjectData
	agents     []herdr.Agent
	health     model.HerdrHealth
	due        map[string]time.Time
	wake       chan struct{}
	subMu      sync.Mutex
	subs       map[chan string]struct{}
	terminalMu sync.Mutex
	terminals  map[string]terminalEntry
	running    atomic.Bool
}

// Error is an HTTP-independent application error. Current accompanies conflicts.
type Error struct {
	Status  int
	Code    string
	Message string
	Current *model.DecisionView
}

func (e *Error) Error() string { return e.Message }
func problem(status int, code, message string) error {
	return &Error{Status: status, Code: code, Message: message}
}
func notFound() error { return problem(404, "not_found", "Resource not found") }

func New(deps Deps, opts Options) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	// Missing integration capabilities fail closed instead of displaying raw secrets
	// or treating arbitrary Markdown as HTML.
	if deps.Redact == nil {
		deps.Redact = func(s string) string {
			if s == "" {
				return ""
			}
			return "[redaction unavailable]"
		}
	}
	if deps.Render == nil {
		deps.Render = html.EscapeString
	}
	if deps.ToMerge == nil {
		deps.ToMerge = func(model.Task) bool { return false }
	}
	if deps.Reconcile == nil {
		deps.Reconcile = func(pd *model.ProjectData, _ []herdr.Agent, _ bool, _ time.Time) (model.LeaderPresence, map[string]model.TaskLive) {
			reason := "herdr_unavailable"
			if pd.Leader == nil {
				reason = "no_leader_json"
			}
			return model.LeaderPresence{Reason: reason}, nil
		}
	}
	if opts.ScanInterval <= 0 {
		opts.ScanInterval = 2 * time.Second
	}
	if opts.HerdrInterval <= 0 {
		opts.HerdrInterval = 5 * time.Second
	}
	if opts.QueueInterval <= 0 {
		opts.QueueInterval = 250 * time.Millisecond
	}
	s := &Service{deps: deps, opts: opts, projects: map[string]*model.ProjectData{}, due: map[string]time.Time{}, wake: make(chan struct{}, 1), subs: map[chan string]struct{}{}, terminals: map[string]terminalEntry{}}
	s.publishLocked()
	return s
}

// Run must be started before accepting writes in the production server. It owns
// the instance lock until all background work has stopped. Cancellation is normal.
func (s *Service) Run(ctx context.Context) error {
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("service already running")
	}
	defer s.running.Store(false)
	if s.deps.Store == nil {
		return errors.New("service: Store is required")
	}
	lock, err := s.instanceLock()
	if err != nil {
		return err
	}
	defer lock.Close() // closing releases flock; never unlink the inode
	s.refreshHerdr(ctx)
	if err := s.scan(ctx); err != nil {
		return err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.opts.QueueInterval)
		defer ticker.Stop()
		s.deliver(workerCtx)
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-s.wake:
				s.deliver(workerCtx)
			case <-ticker.C:
				s.deliver(workerCtx)
			}
		}
	}()
	defer func() { cancel(); <-done }()
	scanTicker := time.NewTicker(s.opts.ScanInterval)
	defer scanTicker.Stop()
	herdrTicker := time.NewTicker(s.opts.HerdrInterval)
	defer herdrTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-scanTicker.C:
			if err := s.scan(ctx); err != nil {
				s.scanWarning(err)
			}
		case <-herdrTicker.C:
			s.refreshHerdr(ctx)
		}
	}
}

func (s *Service) instanceLock() (*os.File, error) {
	path := s.opts.LockPath
	if path == "" {
		root := os.Getenv("XDG_STATE_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			root = filepath.Join(home, ".local", "state")
		}
		path = filepath.Join(root, "leader-console", "instance.lock")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有实例在运行: %w", err)
	}
	return f, nil
}

func (s *Service) refreshHerdr(ctx context.Context) {
	var agents []herdr.Agent
	var err error
	if s.deps.Herdr == nil {
		err = errors.New("herdr unavailable")
	} else {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		agents, err = s.deps.Herdr.List(callCtx)
		cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = append([]herdr.Agent(nil), agents...)
	s.health = model.HerdrHealth{OK: err == nil, CheckedAt: s.deps.Now(), Agents: len(agents)}
	if err != nil {
		s.agents = nil
		s.health.Agents = 0
		s.health.Error = s.deps.Redact(err.Error())
	}
	s.publishLocked()
}

// scan always calls the Store port rather than consulting the HTTP snapshot.
// The FS implementation checks source fingerprints on every LoadProject call.
func (s *Service) scan(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := s.deps.Store.ListProjects(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]*model.ProjectData, len(names))
	for _, name := range names {
		pd, err := s.deps.Store.LoadProject(ctx, name)
		if err != nil {
			if old := s.projects[name]; old != nil {
				pd = clone(old)
				pd.Warnings = appendWarning(pd.Warnings, s.deps.Redact(err.Error()))
				next[name] = pd
			}
			continue
		}
		next[name] = clone(pd)
	}
	s.projects = next
	s.publishLocked()
	return nil
}
func (s *Service) scanWarning(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, pd := range s.projects {
		pd = clone(pd)
		pd.Warnings = appendWarning(pd.Warnings, s.deps.Redact(err.Error()))
		s.projects[name] = pd
	}
	s.publishLocked()
}

func appendWarning(warnings []string, message string) []string {
	if slices.Contains(warnings, message) {
		return warnings
	}
	return append(warnings, message)
}

func (s *Service) loadLocked(ctx context.Context, project string) (*model.ProjectData, error) {
	if !validID(project) || s.deps.Store == nil {
		return nil, notFound()
	}
	pd, err := s.deps.Store.LoadProject(ctx, project)
	if errors.Is(err, store.ErrProjectNotFound) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	return clone(pd), nil
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Subscribe coalesces slow readers to the latest version and includes an initial
// version. Cancellation removes the subscriber; channels deliberately stay open.
func (s *Service) Subscribe() (<-chan string, func()) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	ch := make(chan string, 1)
	s.subs[ch] = struct{}{}
	ch <- s.snap.Load().overview.Version
	var once sync.Once
	return ch, func() { once.Do(func() { s.subMu.Lock(); delete(s.subs, ch); s.subMu.Unlock() }) }
}

func (s *Service) publishLocked() {
	now := s.deps.Now()
	out := &snapshot{projects: make(map[string]*model.ProjectData, len(s.projects)), tasks: []model.TaskView{}, decisions: []model.DecisionView{}}
	out.overview = model.Overview{GeneratedAt: now, Projects: []model.ProjectSummary{}, Decisions: []model.DecisionView{}, LegacyHints: []model.LegacyHintView{}, Herdr: s.health}
	names := make([]string, 0, len(s.projects))
	for name := range s.projects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pd := s.projects[name]
		out.projects[name] = pd
		leader, live := s.deps.Reconcile(clone(pd), append([]herdr.Agent(nil), s.agents...), s.health.OK, now)
		summary := model.ProjectSummary{Name: name, Source: pd.Source, Leader: leader, TaskCounts: map[string]int{"in_progress": 0, "blocked": 0, "reviewing": 0, "to_merge": 0, "done": 0}, Warnings: append([]string(nil), pd.Warnings...)}
		if pd.Leader != nil {
			summary.Title = pd.Leader.Title
		}
		for _, task := range pd.Tasks {
			tv := s.taskView(name, task, live[task.ID], pd.Source, now)
			summary.TaskCounts[string(tv.Column)]++
			out.tasks = append(out.tasks, tv)
		}
		for _, d := range pd.Decisions {
			dv := s.decisionView(pd, d, now)
			out.decisions = append(out.decisions, dv)
			if !closed(dv.State) {
				summary.OpenDecisions++
			}
			if !closed(dv.State) || !closedAt(pd, dv).Before(now.Add(-24*time.Hour)) {
				out.overview.Decisions = append(out.overview.Decisions, dv)
			}
		}
		for _, hint := range pd.LegacyHints {
			out.overview.LegacyHints = append(out.overview.LegacyHints, model.LegacyHintView{Project: name, LegacyHint: hint})
		}
		out.overview.Projects = append(out.overview.Projects, summary)
	}
	sortDecisions(out.decisions)
	sortDecisions(out.overview.Decisions)
	// Hash semantic content, not volatile generation/check timestamps. Include raw
	// project data and agents so report fingerprints and identity changes notify SSE.
	raw := struct {
		Projects  map[string]*model.ProjectData
		Agents    []herdr.Agent
		Overview  model.Overview
		Tasks     []model.TaskView
		Decisions []model.DecisionView
	}{out.projects, s.agents, out.overview, out.tasks, out.decisions}
	raw.Overview.GeneratedAt = time.Time{}
	raw.Overview.Herdr.CheckedAt = time.Time{}
	raw.Projects = make(map[string]*model.ProjectData, len(out.projects))
	for name, pd := range out.projects {
		cp := *pd
		cp.LoadedAt = time.Time{}
		raw.Projects[name] = &cp
	}
	// RunningFor is computed at query time, not a reason to invalidate every second.
	raw.Tasks = clone(out.tasks)
	for i := range raw.Tasks {
		raw.Tasks[i].RunningFor = 0
	}
	data, _ := json.Marshal(raw)
	sum := sha256.Sum256(data)
	out.overview.Version = hex.EncodeToString(sum[:12])
	previous := s.snap.Load()
	s.snap.Store(out)
	if previous != nil && previous.overview.Version == out.overview.Version {
		return
	}
	// Publication and subscription use this lock so a subscriber cannot miss a
	// version between reading the current snapshot and entering the subscriber set.
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for ch := range s.subs {
		select {
		case <-ch:
		default:
		}
		ch <- out.overview.Version
	}
}

func clone[T any](v T) T {
	// All domain contracts consist solely of JSON types. Deep copies prevent a
	// caller or a cache owned by another package from mutating a published view.
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
