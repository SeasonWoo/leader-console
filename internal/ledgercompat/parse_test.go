package ledgercompat

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"leader-console/internal/model"
)

var update = flag.Bool("update", false, "update ledger parser golden files")

func TestParseGolden(t *testing.T) {
	cases := []struct{ name, path string }{
		{"legacy", "../../testdata/fixtures/leader-root/legacy-demo/ledger.md"},
		{"boundaries", "testdata/boundaries.md"},
		{"missing-columns", "testdata/missing-columns.md"},
		{"empty", "testdata/empty.md"},
		{"no-table", "testdata/no-table.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Open(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			tasks, hints, warnings := Parse(f, time.FixedZone("CST", 8*60*60))
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			for i := range tasks {
				if strings.HasPrefix(tasks[i].Worktree, home+string(os.PathSeparator)) {
					tasks[i].Worktree = "<home>" + strings.TrimPrefix(tasks[i].Worktree, home)
				}
			}
			got := struct {
				Tasks    []model.Task       `json:"tasks"`
				Hints    []model.LegacyHint `json:"hints"`
				Warnings []string           `json:"warnings"`
			}{tasks, hints, warnings}
			encoded, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			path := filepath.Join("testdata", tc.name+".golden.json")
			if *update {
				if err := os.WriteFile(path, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(encoded) {
				t.Fatalf("golden mismatch (-update to regenerate):\n%s", encoded)
			}
		})
	}
}

func TestLikelyToMerge(t *testing.T) {
	for _, tc := range []struct {
		status model.TaskStatus
		result string
		want   bool
	}{
		{model.TaskDone, "等用户确认提交", true},
		{model.TaskDone, "等待用户授权提 MR", true},
		{model.TaskDone, "交 leader 合入", true},
		{model.TaskDone, "交由 leader 进行下一阶段合入", false},
		{model.TaskDone, "已验收；等用户授权合入", true},
		{model.TaskDone, "等用户确认；代码已经提交", false},
		{model.TaskDone, "等用户授权;已提交", false},
		{model.TaskWorking, "等用户确认提交", false},
		{model.TaskDone, "全部完成", false},
	} {
		if got := LikelyToMerge(model.Task{Status: tc.status, ResultSummary: tc.result}); got != tc.want {
			t.Errorf("%s/%s: %v, want %v", tc.status, tc.result, got, tc.want)
		}
	}
}

func TestParseDetails(t *testing.T) {
	input := "| name | status | kind | pane | worktree | result | updated | notified |\n|---|---|---|---|---|---|---|---|\n| a | WORKING 正在做 | CoDeX | wA:pB（已关闭） | ~/code | 需要用户决定 /tmp/a.md | invalid | bad |\n| b | reviewing | custom | x w1:pZ y | /abs | 待用户确认 | 2026-10-21 15:00 | 2026-10-21 16:00（通知） |\n| c | failed | | | branch | 等待用户确认 | | |\n| d | lost | | | | 需要用户 | | |\n| e | queued | | | | | | |\n| f | blocked | | | | | | |\n| g | needs_user | | | | | | |\n| h | ready_to_merge | | | | | | |\n"
	tasks, hints, warnings := Parse(strings.NewReader(input), nil)
	if len(tasks) != 8 || len(hints) != 2 || len(warnings) != 0 {
		t.Fatalf("tasks=%d hints=%v warnings=%v", len(tasks), hints, warnings)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Kind != "codex" || tasks[0].PaneID != "" || tasks[0].Worktree != filepath.Join(home, "code") || !tasks[0].CreatedAt.IsZero() || !tasks[0].UpdatedAt.IsZero() || tasks[0].NotifiedAt != nil {
		t.Fatalf("bad first task: %+v", tasks[0])
	}
	if tasks[1].PaneID != "w1:pZ" || tasks[1].NotifiedAt == nil || tasks[1].UpdatedAt.Location() != time.Local || tasks[1].Kind != "custom" {
		t.Fatalf("bad second task: %+v", tasks[1])
	}
}

func TestSplitRow(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{`| a \| b | c |`, []string{"a | b", "c"}},
		{`| a \\| b |`, []string{`a \`, "b"}},
		{`| a \\\| b |`, []string{`a \| b`}},
		{`| a \|`, []string{"a |"}},
		{`| a | b`, []string{"a", "b"}},
	} {
		if got := splitRow(tc.input); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %q, want %q", tc.input, got, tc.want)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReaderErrors(t *testing.T) {
	for _, r := range []io.Reader{failingReader{}, strings.NewReader(strings.Repeat("x", (4<<20)+1))} {
		_, _, warnings := Parse(r, nil)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "read:") {
			t.Fatalf("warnings=%v", warnings)
		}
	}
}
