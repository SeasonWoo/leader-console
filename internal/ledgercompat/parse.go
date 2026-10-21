package ledgercompat

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"leader-console/internal/model"
)

var (
	paneRE      = regexp.MustCompile(`w[0-9A-Za-z]+:p[0-9A-Za-z]+`)
	wordRE      = regexp.MustCompile(`[A-Za-z][A-Za-z_]*`)
	reportRE    = regexp.MustCompile(`/tmp/[A-Za-z0-9._-]+\.md\b`)
	mergeRE     = regexp.MustCompile(`等(待)?用户(确认|授权)[^；;]*(提交|合入|MR)|交.{0,10}合入`)
	hintRE      = regexp.MustCompile(`等(待)?用户|待用户(决定|确认)|需要用户`)
	separatorRE = regexp.MustCompile(`^:?-{3,}:?$`)
)

// Parse reads the first task table only. Closed panes are cleared so callers do
// not expect a live worker for them. It does not read any other project files.
func Parse(r io.Reader, loc *time.Location) (tasks []model.Task, hints []model.LegacyHint, warnings []string) {
	if loc == nil {
		loc = time.Local
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 4<<20)
	var header []string
	active := false
	counts := make(map[string]int)
	used := make(map[string]bool)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, "|") {
			if active {
				break
			}
			header = nil
			continue
		}
		cells := splitRow(line)
		if !active {
			if header != nil && separator(cells, len(header)) {
				active = true
				continue
			}
			header = nil
			if has(cells, "name") && (has(cells, "状态") || has(cells, "status")) {
				header = cells
			}
			continue
		}
		if len(cells) != len(header) {
			warnings = append(warnings, fmt.Sprintf("ledger.md:%d: column count %d, expected %d", lineNo, len(cells), len(header)))
			if len(cells) > len(header) {
				cells = append(cells[:len(header)-1], strings.Join(cells[len(header)-1:], "|"))
			}
			for len(cells) < len(header) {
				cells = append(cells, "")
			}
		}
		fields := make(map[string]string, len(header))
		for i, h := range header {
			fields[h] = cells[i]
		}
		get := func(names ...string) string {
			for _, n := range names {
				if v, ok := fields[n]; ok {
					return v
				}
			}
			return ""
		}
		name := get("name")
		if name == "" {
			warnings = append(warnings, fmt.Sprintf("ledger.md:%d: empty name, skipped", lineNo))
			continue
		}
		counts[name]++
		id := name
		if used[id] {
			if counts[name] < 2 {
				counts[name] = 2
			}
			for {
				id = fmt.Sprintf("%s#%d", name, counts[name])
				if !used[id] {
					break
				}
				counts[name]++
			}
			warnings = append(warnings, fmt.Sprintf("ledger.md:%d: duplicate name %q, id %q", lineNo, name, id))
		}
		used[id] = true
		t := model.Task{ID: id, Name: name, AgentName: name, Kind: strings.ToLower(get("kind")), Goal: get("目标", "goal"), ResultSummary: get("结果", "result")}
		pane := get("pane")
		if !strings.Contains(pane, "已关闭") {
			t.PaneID = paneRE.FindString(pane)
		}
		wt := get("worktree")
		switch {
		case strings.HasPrefix(wt, "/"):
			t.Worktree = wt
		case strings.HasPrefix(wt, "~/"):
			home, err := os.UserHomeDir()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("ledger.md:%d: expand home: %v", lineNo, err))
			} else {
				t.Worktree = filepath.Join(home, wt[2:])
			}
		default:
			t.Branch = wt
		}
		t.Status = model.TaskStatus(strings.ToLower(wordRE.FindString(get("状态", "status"))))
		switch t.Status {
		case model.TaskQueued, model.TaskWorking, model.TaskBlocked, model.TaskNeedsUser, model.TaskReviewing, model.TaskReadyToMerge, model.TaskDone, model.TaskFailed, model.TaskLost:
		default:
			warnings = append(warnings, fmt.Sprintf("ledger.md:%d: unknown status %q, using working", lineNo, get("状态", "status")))
			t.Status = model.TaskWorking
		}
		t.Reports = reportRE.FindAllString(t.ResultSummary, -1)
		t.UpdatedAt, _ = time.ParseInLocation("2006-01-02 15:04", get("更新时间", "updated"), loc)
		if n := get("已通知", "notified"); len(n) >= 16 {
			if parsed, err := time.ParseInLocation("2006-01-02 15:04", n[:16], loc); err == nil {
				t.NotifiedAt = &parsed
			}
		}
		tasks = append(tasks, t)
		if t.Status != model.TaskFailed && t.Status != model.TaskLost && hintRE.MatchString(t.ResultSummary) {
			hints = append(hints, model.LegacyHint{TaskID: t.ID, Text: t.ResultSummary})
		}
	}
	if err := s.Err(); err != nil {
		warnings = append(warnings, fmt.Sprintf("ledger.md:%d: read: %v", lineNo+1, err))
	}
	return
}

// LikelyToMerge projects the legacy done + waiting-for-merge convention.
func LikelyToMerge(t model.Task) bool {
	return t.Status == model.TaskDone && mergeRE.MatchString(t.ResultSummary)
}

func has(cells []string, name string) bool {
	for _, c := range cells {
		if c == name {
			return true
		}
	}
	return false
}

func separator(cells []string, n int) bool {
	if len(cells) != n {
		return false
	}
	for _, c := range cells {
		if !separatorRE.MatchString(c) {
			return false
		}
	}
	return true
}

func splitRow(line string) []string {
	line = strings.TrimPrefix(line, "|")
	// A final escaped pipe is cell content, not a table delimiter.
	if strings.HasSuffix(line, "|") {
		slashes := 0
		for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
			slashes++
		}
		if slashes%2 == 0 {
			line = line[:len(line)-1]
		}
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && (line[i+1] == '|' || line[i+1] == '\\'):
			i++
			cell.WriteByte(line[i])
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}
