package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"leader-console/internal/model"
)

var bg = context.Background()

func put(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, opts FSOptions) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../testdata/fixtures/leader-root")); err != nil {
		t.Fatal(err)
	}
	f, err := NewFS(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return f, root
}

func load(t *testing.T, f *FS, project string) *model.ProjectData {
	t.Helper()
	pd, err := f.LoadProject(bg, project)
	if err != nil {
		t.Fatal(err)
	}
	return pd
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func stamp(t *testing.T, path string, sec int64) {
	t.Helper()
	if err := os.Chtimes(path, time.Unix(sec, 0), time.Unix(sec, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestNewFSAndListProjects(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	put(t, filepath.Join(root, "empty", "irrelevant"), "")
	put(t, filepath.Join(root, "_invalid", "state.json"), "{}")
	put(t, filepath.Join(root, "a-valid", "leader.json"), "{}")
	put(t, filepath.Join(root, "z.valid", "ledger.md"), "")
	put(t, filepath.Join(root, "just-file"), "")
	if err := os.Symlink(filepath.Join(root, "demo"), filepath.Join(root, "project-link")); err != nil {
		t.Fatal(err)
	}
	projects, err := f.ListProjects(bg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a-valid", "demo", "legacy-demo", "z.valid"}; !reflect.DeepEqual(projects, want) {
		t.Fatalf("projects=%v want=%v", projects, want)
	}
	if f.opts.Location != time.Local || f.opts.MaxReportBytes != 1<<20 {
		t.Fatal(f.opts)
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	empty, err := NewFS(missing, FSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	projects, err = empty.ListProjects(bg)
	if err != nil || len(projects) != 0 || projects == nil {
		t.Fatalf("missing root: %v %v", projects, err)
	}
	if _, err := empty.LoadProject(bg, "demo"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root string
		opts FSOptions
	}{
		{"", FSOptions{}}, {root, FSOptions{MaxReportBytes: -1}}, {filepath.Join(root, "just-file"), FSOptions{}}, {filepath.Join(root, "just-file", "child"), FSOptions{}},
	} {
		if _, err := NewFS(tc.root, tc.opts); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestFixtureLoadAndPriority(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	f, root := fixture(t, FSOptions{Location: loc})
	pd := load(t, f, "demo")
	if pd.Source != model.SourceState || pd.Leader == nil || len(pd.Tasks) == 0 || len(pd.Decisions) == 0 || len(pd.Answers) == 0 || len(pd.Events) == 0 || len(pd.Warnings) != 0 {
		t.Fatalf("bad fixture: %+v", pd)
	}
	put(t, filepath.Join(root, "demo", "ledger.md"), "| name | 状态 |\n|---|---|\n| fallback | done |\n")
	if got := load(t, f, "demo"); got.Source != model.SourceState || got.Tasks[0].ID != pd.Tasks[0].ID {
		t.Fatal("state not preferred")
	}
	remove(t, filepath.Join(root, "demo", "state.json"))
	if got := load(t, f, "demo"); got.Source != model.SourceLedger || got.Tasks[0].ID != "fallback" {
		t.Fatalf("no fallback: %+v", got)
	}
	legacy := load(t, f, "legacy-demo")
	if legacy.Source != model.SourceLedger || len(legacy.Tasks) != 6 || len(legacy.LegacyHints) != 1 || len(legacy.Warnings) != 0 || legacy.Tasks[0].UpdatedAt.Location() != loc || legacy.Leader != nil {
		t.Fatalf("bad legacy: %+v", legacy)
	}
	put(t, filepath.Join(root, "leader-only", "leader.json"), `{"schema_version":1,"project":"leader-only"}`)
	if got := load(t, f, "leader-only"); got.Source != model.SourceNone || got.Leader == nil {
		t.Fatalf("leader-only: %+v", got)
	}
}

func TestCacheHitAndInvalidation(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	old := load(t, f, "demo")
	if again := load(t, f, "demo"); old != again || old.Version == "" || !old.LoadedAt.Equal(again.LoadedAt) {
		t.Fatal("cache miss")
	}
	for i, name := range sources {
		path := filepath.Join(root, "demo", name)
		if name == "ledger.md" {
			put(t, path, "# unused")
		}
		if name == "panel.jsonl" {
			put(t, path, "")
		}
		stamp(t, path, int64(100+i))
		next := load(t, f, "demo")
		if next == old || next.Version == old.Version {
			t.Fatalf("no invalidation for %s", name)
		}
		if again := load(t, f, "demo"); next != again {
			t.Fatalf("no hit after %s", name)
		}
		old = next
	}
	for _, name := range []string{"reports", "reports/uih-copilot", "reports/uih-copilot/report.md"} {
		stamp(t, filepath.Join(root, "demo", name), 200)
		next := load(t, f, "demo")
		if next == old || next.Version == old.Version {
			t.Fatalf("no invalidation for %s", name)
		}
		old = next
	}
	put(t, filepath.Join(root, "demo", "reports/uih-copilot/extra.md"), "new")
	if next := load(t, f, "demo"); next.Version == old.Version {
		t.Fatal("new report did not invalidate")
	}
	old = load(t, f, "demo")
	remove(t, filepath.Join(root, "demo", "reports/uih-copilot/extra.md"))
	if next := load(t, f, "demo"); next.Version == old.Version {
		t.Fatal("deleted report did not invalidate")
	}
	if err := os.Rename(filepath.Join(root, "demo"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.LoadProject(bg, "demo"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
}

func TestFoldAndWarnings(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	put(t, filepath.Join(root, "demo", "events.jsonl"), "{broken}\n\nnull\n[]\n{\"type\":\"new_event\",\"summary\":\"future\",\"extra\":true}\n{\"type\":\"done\"}\n{partial")
	put(t, filepath.Join(root, "demo", "decisions.jsonl"), `{"id":"z","question":"first","status":"answered","context":"discard me","options":[{"key":"A","label":"discard"}],"created_at":"2026-01-02T00:00:00Z"}
{"id":"a","question":"second","status":"open","created_at":"2026-01-01T00:00:00Z"}
{"id":"z","question":"replacement","status":"open","created_at":"2026-01-02T00:00:00Z"}
{"id":"b","question":"tie","status":"open","created_at":"2026-01-01T00:00:00Z"}
{"id":"../escape","status":"open"}
not-json
`)
	put(t, filepath.Join(root, "demo", "answers.jsonl"), `{"type":"answer","answer_id":"z","decision_id":"z","option":"A","at":"2026-01-02T00:00:00Z"}
{"type":"delivery","answer_id":"z","delivery":"retry","attempt":4,"target":"old","error":"old","at":"2026-01-03T00:00:00Z"}
{"type":"answer","answer_id":"a","decision_id":"a","text":"queued","at":"2026-01-01T00:00:00Z"}
{"type":"delivery","answer_id":"z","delivery":"delivered","attempt":1,"at":"2026-01-04T00:00:00Z"}
{"type":"answer","answer_id":"z","decision_id":"z","text":"replacement","at":"2026-01-02T00:00:00Z"}
{"type":"answer","answer_id":"b","decision_id":"a","at":"2026-01-01T00:00:00Z"}
{"type":"delivery","answer_id":"orphan","delivery":"queued"}
{"type":"delivery","answer_id":"z","delivery":"invalid"}
{"type":"future","answer_id":"z"}
{"type":"answer","answer_id":"../escape"}
invalid
{"type":"delivery","answer_id":"z","delivery":"failed"}`)
	pd := load(t, f, "demo")
	if len(pd.Events) != 2 || pd.Events[0].Type != model.EventOther || pd.Events[1].Type != model.EventDone {
		t.Fatal(pd.Events)
	}
	if len(pd.Decisions) != 3 || pd.Decisions[0].ID != "a" || pd.Decisions[1].ID != "b" || pd.Decisions[2].ID != "z" {
		t.Fatal(pd.Decisions)
	}
	d := pd.Decisions[2]
	if d.Question != "replacement" || d.Context != "" || len(d.Options) != 0 || d.Status != model.DecisionOpen {
		t.Fatal(d)
	}
	if len(pd.Answers) != 3 || pd.Answers[0].AnswerID != "a" || pd.Answers[1].AnswerID != "b" || pd.Answers[2].AnswerID != "z" {
		t.Fatal(pd.Answers)
	}
	a := pd.Answers[2]
	if a.Option != "" || a.Text != "replacement" || a.Delivery != model.DeliveryDelivered || a.Attempts != 1 || a.Target != "" || a.LastError != "" || a.UpdatedAt.Day() != 4 || pd.Answers[0].Delivery != model.DeliveryQueued {
		t.Fatal(a)
	}
	warnings := strings.Join(pd.Warnings, "\n")
	for _, want := range []string{"events.jsonl:1:", "events.jsonl:2:", "events.jsonl:3:", "events.jsonl:4:", "terminal decision", "invalid id", "delivery without answer", "invalid answer_id", "invalid delivery", "unknown type", "answers.jsonl:11:"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("missing warning %q: %s", want, warnings)
		}
	}
	if strings.Contains(warnings, "partial") || strings.Contains(warnings, "events.jsonl:7:") {
		t.Fatal("warned about unfinished line")
	}
}

func TestFutureVersionsAndCorruptFiles(t *testing.T) {
	for _, name := range []string{"leader.json", "state.json", "events.jsonl", "decisions.jsonl", "answers.jsonl"} {
		t.Run(name, func(t *testing.T) {
			f, root := fixture(t, FSOptions{})
			path := filepath.Join(root, "demo", name)
			put(t, filepath.Join(root, "demo", "ledger.md"), "| name | 状态 |\n|---|---|\n| stale | done |\n")
			data := `{"schema_version":2}`
			if strings.HasSuffix(name, ".jsonl") {
				old, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = string(old) + data + "\n"
			}
			put(t, path, data)
			pd := load(t, f, "demo")
			if !strings.Contains(strings.Join(pd.Warnings, "\n"), name+":") || !strings.Contains(strings.Join(pd.Warnings, "\n"), "schema_version > 1") {
				t.Fatal(pd.Warnings)
			}
			switch name {
			case "leader.json":
				if pd.Leader != nil {
					t.Fatal(pd.Leader)
				}
			case "state.json":
				if len(pd.Tasks) != 0 || pd.Source != model.SourceNone {
					t.Fatal(pd.Tasks)
				}
			case "events.jsonl":
				if len(pd.Events) != 0 {
					t.Fatal(pd.Events)
				}
			case "decisions.jsonl":
				if len(pd.Decisions) != 0 {
					t.Fatal(pd.Decisions)
				}
			case "answers.jsonl":
				if len(pd.Answers) != 0 {
					t.Fatal(pd.Answers)
				}
			}
		})
	}
	for _, content := range []string{"invalid", "null", "[]", `{"schema_version":1,"tasks":"invalid"}`} {
		f, root := fixture(t, FSOptions{})
		put(t, filepath.Join(root, "demo", "state.json"), content)
		put(t, filepath.Join(root, "demo", "leader.json"), "broken")
		pd := load(t, f, "demo")
		if pd.Source != model.SourceNone || pd.Leader != nil || len(pd.Warnings) != 2 {
			t.Fatalf("%s: %+v", content, pd)
		}
	}
}

func TestAppendAnswerConcurrentAndVisible(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	old := load(t, f, "demo")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			line := model.AnswerLine{Type: model.LineAnswer, AnswerID: fmt.Sprintf("a-%d", i), DecisionID: "d", Text: "answer\nwith line break", ClientRequestID: fmt.Sprintf("c-%d", i), At: time.Unix(int64(i+1), 0)}
			if err := f.AppendAnswer(bg, "demo", line); err != nil {
				t.Error(err)
			}
			if _, err := f.LoadProject(bg, "demo"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	pd := load(t, f, "demo")
	if len(pd.Answers) != len(old.Answers)+32 || pd.Version == old.Version || pd == old {
		t.Fatalf("answers=%d old=%d", len(pd.Answers), len(old.Answers))
	}
	if len(old.Answers) == len(pd.Answers) {
		t.Fatal("mutated prior snapshot")
	}
	line := model.AnswerLine{Type: model.LineDelivery, AnswerID: "a-0", Delivery: model.DeliveryFailed, Attempt: 5, Target: "leader-demo", Error: "failed", At: time.Unix(100, 0)}
	if err := f.AppendAnswer(bg, "demo", line); err != nil {
		t.Fatal(err)
	}
	pd = load(t, f, "demo")
	if pd.Answers[0].Delivery != model.DeliveryFailed || pd.Answers[0].Attempts != 5 || pd.Answers[0].ClientRequestID != "c-0" || pd.Answers[0].Text != "answer\nwith line break" {
		t.Fatal(pd.Answers[0])
	}
	if info, err := os.Lstat(filepath.Join(root, "demo", "answers.jsonl.lock")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("lock: %v %v", info, err)
	}
	if err := f.AppendAnswer(bg, "absent", line); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
	line.At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := f.AppendAnswer(bg, "demo", line); err == nil {
		t.Fatal("accepted invalid JSON time")
	}
}

func TestInvalidIDsAndCanceledContext(t *testing.T) {
	f, _ := fixture(t, FSOptions{})
	for _, id := range []string{"", "../demo", "/demo", "demo/..", ".hidden", "a#2", strings.Repeat("a", 129), "demo\n"} {
		if _, err := f.LoadProject(bg, id); !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("Load(%q): %v", id, err)
		}
		if err := f.AppendAnswer(bg, id, model.AnswerLine{}); !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("Append(%q): %v", id, err)
		}
		if _, err := f.ListReports(bg, id, "uih-copilot"); !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("List(%q): %v", id, err)
		}
		if _, err := f.ListReports(bg, "demo", id); !errors.Is(err, ErrReportNotFound) {
			t.Fatalf("List task(%q): %v", id, err)
		}
		if _, err := f.ReadReport(bg, "demo", "uih-copilot", id); !errors.Is(err, ErrReportNotFound) {
			t.Fatalf("Read id(%q): %v", id, err)
		}
		if _, err := f.ReadReport(bg, id, "uih-copilot", "r-abc"); !errors.Is(err, ErrProjectNotFound) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := f.ListProjects(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.LoadProject(ctx, "demo"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.ListReports(ctx, "demo", "uih-copilot"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.ReadReport(ctx, "demo", "uih-copilot", "r-abc"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := f.AppendAnswer(ctx, "demo", model.AnswerLine{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestProjectAndSourceSymlinks(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	outside := t.TempDir()
	put(t, filepath.Join(outside, "state.json"), `{"schema_version":1}`)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.LoadProject(bg, "escape"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
	if err := f.AppendAnswer(bg, "escape", model.AnswerLine{}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
	for _, name := range []string{"leader.json", "state.json", "events.jsonl", "decisions.jsonl", "answers.jsonl"} {
		t.Run(name, func(t *testing.T) {
			f, root := fixture(t, FSOptions{})
			path := filepath.Join(root, "demo", name)
			remove(t, path)
			if err := os.Symlink(filepath.Join(outside, "state.json"), path); err != nil {
				t.Fatal(err)
			}
			pd := load(t, f, "demo")
			if len(pd.Warnings) != 1 || !strings.Contains(pd.Warnings[0], name) {
				t.Fatal(pd.Warnings)
			}
		})
	}
	for _, name := range []string{"answers.jsonl", "answers.jsonl.lock"} {
		f, root := fixture(t, FSOptions{})
		path := filepath.Join(root, "demo", name)
		if name == "answers.jsonl" {
			remove(t, path)
		}
		target := filepath.Join(outside, "keep-"+name)
		put(t, target, "untouched")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if err := f.AppendAnswer(bg, "demo", model.AnswerLine{Type: model.LineAnswer, AnswerID: "a"}); err == nil {
			t.Fatal("followed symlink " + name)
		}
		if b, err := os.ReadFile(target); err != nil || string(b) != "untouched" {
			t.Fatalf("outside changed: %s %v", b, err)
		}
	}
	// Symlinked legacy sources are rejected even with a valid leader marker.
	put(t, filepath.Join(root, "legacy-demo", "leader.json"), "{}")
	remove(t, filepath.Join(root, "legacy-demo", "ledger.md"))
	if err := os.Symlink(filepath.Join(outside, "state.json"), filepath.Join(root, "legacy-demo", "ledger.md")); err != nil {
		t.Fatal(err)
	}
	if pd := load(t, f, "legacy-demo"); pd.Source != model.SourceNone || len(pd.Warnings) != 1 {
		t.Fatal(pd)
	}
}

func TestSpecialSourcesAndSafeOpen(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	path := filepath.Join(root, "demo", "events.jsonl")
	remove(t, path)
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if pd := load(t, f, "demo"); len(pd.Events) != 0 || len(pd.Warnings) != 1 {
		t.Fatal(pd.Warnings)
	}
	r, err := os.OpenRoot(filepath.Join(root, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, name := range []string{"../outside", "/abs", "a//b", "state.json/child"} {
		if file, err := safeOpen(r, name); err == nil {
			file.Close()
			t.Fatalf("opened %s", name)
		}
	}
	for _, kind := range []model.EventType{model.EventTaskCreated, model.EventDispatched, model.EventStarted, model.EventProgress, model.EventBlocked, model.EventUnblocked, model.EventReviewRequested, model.EventReviewPassed, model.EventReviewFailed, model.EventRework, model.EventMerged, model.EventDone, model.EventFailed, model.EventLost, model.EventReportWritten, model.EventNotified, model.EventDecisionOpened, model.EventDecisionAck, model.EventDecisionExecuting, model.EventDecisionDone, model.EventOther} {
		if !knownEvent(kind) {
			t.Fatal(kind)
		}
	}
	// Unknown fields remain forward compatible.
	put(t, filepath.Join(root, "demo", "state.json"), `{"schema_version":1,"tasks":[{"id":"a","status":"working","future":true}],"future":{}}`)
	if pd := load(t, f, "demo"); len(pd.Tasks) != 1 || pd.Tasks[0].ID != "a" {
		t.Fatal(pd)
	}
}

func TestAppendCreatesAnswerFiles(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	line := model.AnswerLine{Type: model.LineAnswer, AnswerID: "new", DecisionID: "d", At: time.Unix(5, 0)}
	if err := f.AppendAnswer(bg, "legacy-demo", line); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "legacy-demo", "answers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got model.AnswerLine
	if err := json.Unmarshal(b, &got); err != nil || got.AnswerID != line.AnswerID || len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("bad answer %s %v", b, err)
	}
}

func TestPanelLinesAppendAndFold(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	before := load(t, f, "demo")
	if len(before.Panel.Reports) != 0 || len(before.Panel.Leaders) != 0 {
		t.Fatalf("fixture has panel lines: %+v", before.Panel)
	}
	at := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	for i := 0; i < model.PanelKeep+3; i++ {
		if err := f.AppendPanel(bg, "demo", model.PanelLine{Type: model.PanelReport, At: at.Add(time.Duration(i) * time.Minute), Delivery: model.DeliveryDelivered, Target: "leader-demo"}); err != nil {
			t.Fatal(err)
		}
	}
	started := at
	if err := f.AppendPanel(bg, "demo", model.PanelLine{Type: model.PanelLeader, At: at, Leader: &model.LeaderAgent{Agent: "codex", HerdrName: "leader-demo"}, StartedAt: &started}); err != nil {
		t.Fatal(err)
	}
	f2, _ := NewFS(root, FSOptions{}) // a fresh reader (panel restart) sees the same history
	for _, fs := range []*FS{f, f2} {
		pd := load(t, fs, "demo")
		if pd.Version == before.Version || len(pd.Panel.Reports) != model.PanelKeep || !pd.Panel.Reports[model.PanelKeep-1].At.Equal(at.Add(time.Duration(model.PanelKeep+2)*time.Minute)) {
			t.Fatalf("reports = %+v", pd.Panel.Reports)
		}
		if len(pd.Panel.Leaders) != 1 || pd.Panel.Leaders[0].Leader.Agent != "codex" || len(pd.Warnings) != 0 {
			t.Fatalf("leaders = %+v warnings=%v", pd.Panel.Leaders, pd.Warnings)
		}
		if len(pd.Answers) != len(before.Answers) {
			t.Fatal("panel lines leaked into answers")
		}
	}
	put(t, filepath.Join(root, "demo", "panel.jsonl"), `{"type":"report","at":"2026-10-21T08:00:00Z","delivery":"bogus"}
{"type":"leader","at":"2026-10-21T08:00:00Z"}
{"type":"other","at":"2026-10-21T08:00:00Z"}
`)
	if pd := load(t, f, "demo"); len(pd.Warnings) != 3 || len(pd.Panel.Reports)+len(pd.Panel.Leaders) != 0 {
		t.Fatalf("invalid panel lines: %v %+v", pd.Warnings, pd.Panel)
	}
	if err := f.AppendPanel(bg, "absent", model.PanelLine{}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatal(err)
	}
}

// A crash mid-append left half a line; the next AppendPanel must still be readable.
func TestAppendPanelAfterPartialTail(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	path := filepath.Join(root, "demo", "panel.jsonl")
	put(t, path, `{"type":"leader","at":"2026-10-21T08:00:00Z","leader":{"agent":"claude"}}`+"\n"+`{"type":"report","at":`)
	if pd := load(t, f, "demo"); len(pd.Panel.Leaders) != 1 || len(pd.Warnings) != 0 {
		t.Fatalf("partial tail not ignored on read: %+v %v", pd.Panel, pd.Warnings)
	}
	at := time.Date(2026, 10, 21, 8, 10, 0, 0, time.UTC)
	if err := f.AppendPanel(bg, "demo", model.PanelLine{Type: model.PanelReport, At: at, Delivery: model.DeliveryDelivered, Target: "leader-demo"}); err != nil {
		t.Fatal(err)
	}
	pd := load(t, f, "demo")
	if len(pd.Panel.Reports) != 1 || !pd.Panel.Reports[0].At.Equal(at) || len(pd.Panel.Leaders) != 1 || len(pd.Warnings) != 0 {
		t.Fatalf("append after partial tail lost: %+v %v", pd.Panel, pd.Warnings)
	}
}

// Another process holding panel.jsonl.lock bounds the append and blocks no reads.
func TestAppendPanelLockWaitIsBounded(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	lock, err := os.OpenFile(filepath.Join(root, "demo", "panel.jsonl.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() {
		done <- f.AppendPanel(ctx, "demo", model.PanelLine{Type: model.PanelReport, Delivery: model.DeliveryDelivered})
	}()
	time.Sleep(30 * time.Millisecond)
	read := make(chan struct{})
	go func() { load(t, f, "demo"); load(t, f, "legacy-demo"); close(read) }()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("reads blocked behind a contended append")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("append ignored cancellation")
	}
	start := time.Now()
	if err := f.AppendPanel(bg, "demo", model.PanelLine{Type: model.PanelReport, Delivery: model.DeliveryDelivered}); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > appendLockTimeout+time.Second {
		t.Fatalf("append not bounded: %v after %v", err, time.Since(start))
	}
}
