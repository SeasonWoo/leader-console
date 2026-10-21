package store

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"leader-console/internal/jsonl"
	"leader-console/internal/ledgercompat"
	"leader-console/internal/model"
)

var (
	idRE        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	tmpReportRE = regexp.MustCompile(`^/tmp/[A-Za-z0-9._-]+\.md$`)
)

var sources = []string{"leader.json", "state.json", "ledger.md", "events.jsonl", "decisions.jsonl", "answers.jsonl", "panel.jsonl"}

// FSOptions configures legacy timestamps and report reads.
type FSOptions struct {
	Location       *time.Location // nil means time.Local.
	MaxReportBytes int64          // 0 means 1 MiB.
}

// FS reads a leader root. Cached ProjectData values are immutable and shared;
// callers must not modify them. All operations are safe for concurrent use.
type FS struct {
	root  string
	opts  FSOptions
	mu    sync.Mutex
	cache map[string]*model.ProjectData
}

var _ Store = (*FS)(nil)

func NewFS(root string, opts FSOptions) (*FS, error) {
	if root == "" || opts.MaxReportBytes < 0 {
		return nil, errors.New("store: root is empty or report limit is negative")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err == nil && !info.IsDir() {
		return nil, errors.New("store: root is not a directory")
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.MaxReportBytes == 0 {
		opts.MaxReportBytes = 1 << 20
	}
	return &FS{root: abs, opts: opts, cache: make(map[string]*model.ProjectData)}, nil
}

func (f *FS) ListProjects(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(f.root)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	projects := []string{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !e.IsDir() || !idRE.MatchString(e.Name()) {
			continue
		}
		if hasProject(root, e.Name()) {
			projects = append(projects, e.Name())
		}
	}
	sort.Strings(projects)
	return projects, nil
}

func hasProject(root *os.Root, prefix string) bool {
	for _, name := range sources[:3] {
		if info, err := root.Lstat(filepath.Join(prefix, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func (f *FS) openProject(ctx context.Context, project string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !idRE.MatchString(project) {
		return nil, ErrProjectNotFound
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(project)
	if err != nil || !info.IsDir() || !hasProject(root, project) {
		return nil, ErrProjectNotFound
	}
	return root.OpenRoot(project)
}

func (f *FS) LoadProject(ctx context.Context, project string) (*model.ProjectData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	root, err := f.openProject(ctx, project)
	if err != nil {
		delete(f.cache, project)
		return nil, err
	}
	defer root.Close()
	version := fingerprint(root)
	if pd := f.cache[project]; pd != nil && pd.Version == version {
		return pd, nil
	}
	pd := &model.ProjectData{Name: project, Dir: filepath.Join(f.root, project), Source: model.SourceNone, Version: version, LoadedAt: time.Now(), Tasks: []model.Task{}, Events: []model.Event{}, Decisions: []model.Decision{}, Answers: []model.Answer{}}
	var li model.LeaderInfo
	if readJSON(root, "leader.json", &li, &pd.Warnings) {
		pd.Leader = &li
	}
	// Only absence permits legacy fallback; malformed or future state is not
	// silently replaced with potentially stale legacy data.
	if _, err := root.Lstat("state.json"); !errors.Is(err, os.ErrNotExist) {
		var st model.State
		if readJSON(root, "state.json", &st, &pd.Warnings) {
			pd.Source = model.SourceState
			pd.Tasks = st.Tasks
		}
	} else if ledger, err := safeOpen(root, "ledger.md"); err == nil {
		pd.Source = model.SourceLedger
		var warnings []string
		pd.Tasks, pd.LegacyHints, warnings = ledgercompat.Parse(ledger, f.opts.Location)
		pd.Warnings = append(pd.Warnings, warnings...)
		ledger.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		pd.Warnings = append(pd.Warnings, fmt.Sprintf("ledger.md: %v", err))
	}
	for _, row := range readRows(root, "events.jsonl", &pd.Warnings) {
		var e model.Event
		if decodeRow("events.jsonl", row, &e, &pd.Warnings) {
			if !knownEvent(e.Type) {
				e.Type = model.EventOther
			}
			pd.Events = append(pd.Events, e)
		}
	}
	decisions := make(map[string]model.Decision)
	for _, row := range readRows(root, "decisions.jsonl", &pd.Warnings) {
		var d model.Decision
		if !decodeRow("decisions.jsonl", row, &d, &pd.Warnings) {
			continue
		}
		if !idRE.MatchString(d.ID) {
			pd.Warnings = append(pd.Warnings, fmt.Sprintf("decisions.jsonl:%d: invalid id", row.number))
			continue
		}
		if prev, ok := decisions[d.ID]; ok && (prev.Status == model.DecisionAnswered || prev.Status == model.DecisionExpired || prev.Status == model.DecisionSuperseded) && d.Status == model.DecisionOpen {
			pd.Warnings = append(pd.Warnings, fmt.Sprintf("decisions.jsonl:%d: terminal decision %q reopened", row.number, d.ID))
		}
		decisions[d.ID] = d
	}
	for _, d := range decisions {
		pd.Decisions = append(pd.Decisions, d)
	}
	sort.Slice(pd.Decisions, func(i, j int) bool {
		a, b := pd.Decisions[i], pd.Decisions[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	answers := make(map[string]model.Answer)
	deliveries := make(map[string]model.AnswerLine)
	for _, row := range readRows(root, "answers.jsonl", &pd.Warnings) {
		var a model.AnswerLine
		if !decodeRow("answers.jsonl", row, &a, &pd.Warnings) {
			continue
		}
		if !idRE.MatchString(a.AnswerID) {
			pd.Warnings = append(pd.Warnings, fmt.Sprintf("answers.jsonl:%d: invalid answer_id", row.number))
			continue
		}
		switch a.Type {
		case model.LineAnswer:
			answers[a.AnswerID] = model.Answer{AnswerID: a.AnswerID, DecisionID: a.DecisionID, Option: a.Option, Text: a.Text, ClientRequestID: a.ClientRequestID, AnsweredAt: a.At, UpdatedAt: a.At, Delivery: model.DeliveryQueued}
		case model.LineDelivery:
			if validDelivery(a.Delivery) {
				deliveries[a.AnswerID] = a
			} else {
				pd.Warnings = append(pd.Warnings, fmt.Sprintf("answers.jsonl:%d: invalid delivery", row.number))
			}
		default:
			pd.Warnings = append(pd.Warnings, fmt.Sprintf("answers.jsonl:%d: unknown type %q", row.number, a.Type))
		}
	}
	for id, a := range answers {
		if d, ok := deliveries[id]; ok {
			a.Delivery, a.Attempts, a.Target, a.LastError, a.UpdatedAt = d.Delivery, d.Attempt, d.Target, d.Error, d.At
		}
		pd.Answers = append(pd.Answers, a)
	}
	orphans := make([]string, 0)
	for id := range deliveries {
		if _, ok := answers[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		pd.Warnings = append(pd.Warnings, fmt.Sprintf("answers.jsonl: delivery without answer %q", id))
	}
	sort.Slice(pd.Answers, func(i, j int) bool {
		a, b := pd.Answers[i], pd.Answers[j]
		if a.AnsweredAt.Equal(b.AnsweredAt) {
			return a.AnswerID < b.AnswerID
		}
		return a.AnsweredAt.Before(b.AnsweredAt)
	})
	for _, row := range readRows(root, "panel.jsonl", &pd.Warnings) {
		var l model.PanelLine
		if !decodeRow("panel.jsonl", row, &l, &pd.Warnings) {
			continue
		}
		switch {
		case l.Type == model.PanelReport && validDelivery(l.Delivery):
			pd.Panel.Reports = keepTail(append(pd.Panel.Reports, l))
		case l.Type == model.PanelLeader && l.Leader != nil:
			pd.Panel.Leaders = keepTail(append(pd.Panel.Leaders, l))
		default:
			pd.Warnings = append(pd.Warnings, fmt.Sprintf("panel.jsonl:%d: invalid line", row.number))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.cache[project] = pd
	return pd, nil
}

func validDelivery(d model.DeliveryStatus) bool {
	switch d {
	case model.DeliveryQueued, model.DeliveryRetry, model.DeliveryDelivered, model.DeliveryFailed:
		return true
	}
	return false
}

func keepTail(lines []model.PanelLine) []model.PanelLine {
	if len(lines) > model.PanelKeep {
		return append([]model.PanelLine(nil), lines[len(lines)-model.PanelKeep:]...)
	}
	return lines
}

// fingerprint includes report directories and files: editing an existing report
// changes its stat even when the enclosing directory mtime is unchanged.
func fingerprint(root *os.Root) string {
	h := sha1.New()
	stat := func(path string) os.FileInfo {
		info, err := root.Lstat(path)
		if err != nil {
			fmt.Fprintf(h, "%s: %v\n", path, err)
			return nil
		}
		fmt.Fprintf(h, "%s:%d:%d:%d\n", path, info.Size(), info.ModTime().UnixNano(), info.Mode())
		return info
	}
	for _, name := range sources {
		stat(name)
	}
	if info := stat("reports"); info != nil && info.IsDir() {
		if tasks, err := readDir(root, "reports"); err == nil {
			for _, task := range tasks {
				p := filepath.Join("reports", task.Name())
				info := stat(p)
				if info != nil && info.IsDir() {
					if files, err := readDir(root, p); err == nil {
						for _, file := range files {
							stat(filepath.Join(p, file.Name()))
						}
					}
				}
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func safeOpen(root *os.Root, path string) (*os.File, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	var info os.FileInfo
	for i := range parts {
		if parts[i] == "" || parts[i] == "." || parts[i] == ".." {
			return nil, os.ErrPermission
		}
		var err error
		info, err = root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return nil, os.ErrPermission
		}
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, os.ErrPermission
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, os.ErrPermission
	}
	return f, nil
}

func readDir(root *os.Root, path string) ([]os.DirEntry, error) {
	dir, err := safeOpen(root, path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}

func readJSON(root *os.Root, path string, out any, warnings *[]string) bool {
	f, err := safeOpen(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err == nil {
		defer f.Close()
		var b []byte
		b, err = io.ReadAll(f)
		if err == nil {
			if future(b) {
				*warnings = append(*warnings, path+": schema_version > 1, skipped")
				return false
			}
			err = object(b, out)
		}
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: %v", path, err))
	}
	return err == nil
}

type numberedRow struct {
	number int
	data   []byte
}

func readRows(root *os.Root, path string, warnings *[]string) []numberedRow {
	f, err := safeOpen(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var lines [][]byte
	if err == nil {
		defer f.Close()
		lines, err = jsonl.ReadLinesFrom(f)
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: %v", path, err))
		return nil
	}
	rows := make([]numberedRow, 0, len(lines))
	for i, line := range lines {
		if future(line) {
			*warnings = append(*warnings, fmt.Sprintf("%s:%d: schema_version > 1, skipped file", path, i+1))
			return nil
		}
		rows = append(rows, numberedRow{number: i + 1, data: line})
	}
	return rows
}

func future(b []byte) bool {
	var version struct {
		SchemaVersion int `json:"schema_version"`
	}
	return json.Unmarshal(b, &version) == nil && version.SchemaVersion > model.SchemaVersion
}

func object(b []byte, out any) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return errors.New("expected JSON object")
	}
	return json.Unmarshal(b, out)
}

func decodeRow(path string, row numberedRow, out any, warnings *[]string) bool {
	if err := object(row.data, out); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s:%d: %v", path, row.number, err))
		return false
	}
	return true
}

func knownEvent(kind model.EventType) bool {
	switch kind {
	case model.EventTaskCreated, model.EventDispatched, model.EventStarted, model.EventProgress, model.EventBlocked, model.EventUnblocked, model.EventReviewRequested, model.EventReviewPassed, model.EventReviewFailed, model.EventRework, model.EventMerged, model.EventDone, model.EventFailed, model.EventLost, model.EventReportWritten, model.EventNotified, model.EventDecisionOpened, model.EventDecisionAck, model.EventDecisionExecuting, model.EventDecisionDone, model.EventOther:
		return true
	}
	return false
}

func (f *FS) AppendAnswer(ctx context.Context, project string, line model.AnswerLine) error {
	return f.appendLine(ctx, project, "answers.jsonl", line)
}

func (f *FS) AppendPanel(ctx context.Context, project string, line model.PanelLine) error {
	return f.appendLine(ctx, project, "panel.jsonl", line)
}

func (f *FS) appendLine(ctx context.Context, project, name string, line any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	root, err := f.openProject(ctx, project)
	if err != nil {
		return err
	}
	defer root.Close()
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := jsonl.AppendRoot(root, name, name+".lock", b); err != nil {
		return err
	}
	delete(f.cache, project)
	return nil
}
