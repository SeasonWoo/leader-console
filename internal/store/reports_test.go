package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportsOrderingIDsAndLimits(t *testing.T) {
	f, root := fixture(t, FSOptions{MaxReportBytes: 4})
	dir := filepath.Join(root, "demo", "reports", "uih-copilot")
	stamp(t, filepath.Join(dir, "report.md"), 10)
	put(t, filepath.Join(dir, "review.md"), "new review")
	stamp(t, filepath.Join(dir, "review.md"), 20)
	put(t, filepath.Join(dir, "ignored.txt"), "no")
	put(t, filepath.Join(dir, "subdir.md", "ignored.md"), "no")
	refs, err := f.ListReports(bg, "demo", "uih-copilot")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Title != "review.md" || refs[0].Size != 10 || refs[1].Title != "report.md" {
		t.Fatal(refs)
	}
	if !strings.HasPrefix(refs[0].ID, "r-") || len(refs[0].ID) != 12 || strings.Contains(refs[0].ID, root) {
		t.Fatal(refs)
	}
	b, err := f.ReadReport(bg, "demo", "uih-copilot", refs[0].ID)
	if err != nil || string(b) != "new " {
		t.Fatalf("got %q: %v", b, err)
	}
	again, err := f.ListReports(bg, "demo", "uih-copilot")
	if err != nil || again[0] != refs[0] {
		t.Fatal(again, err)
	}
	stamp(t, filepath.Join(dir, "report.md"), 20)
	again, err = f.ListReports(bg, "demo", "uih-copilot")
	if err != nil || again[0].ID > again[1].ID {
		t.Fatal(again, err)
	}
	if _, err := f.ReadReport(bg, "demo", "uih-copilot", "r-unknown"); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
	if _, err := f.ListReports(bg, "demo", "unknown-task"); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
	if _, err := f.ReadReport(bg, "demo", "unknown-task", refs[0].ID); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
	// A valid task with no reports returns an empty slice, and cannot borrow
	// another task's report ID.
	put(t, filepath.Join(root, "demo", "state.json"), `{"schema_version":1,"tasks":[{"id":"uih-copilot"},{"id":"other"}]}`)
	if other, err := f.ListReports(bg, "demo", "other"); err != nil || len(other) != 0 || other == nil {
		t.Fatal(other, err)
	}
	if _, err := f.ReadReport(bg, "demo", "other", refs[0].ID); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
	remove(t, filepath.Join(dir, "review.md"))
	if _, err := f.ReadReport(bg, "demo", "uih-copilot", refs[0].ID); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
	defaultFS, err := NewFS(root, FSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "big.md"), strings.Repeat("x", (1<<20)+100))
	stamp(t, filepath.Join(dir, "big.md"), 30)
	refs, err = defaultFS.ListReports(bg, "demo", "uih-copilot")
	if err != nil {
		t.Fatal(err)
	}
	b, err = defaultFS.ReadReport(bg, "demo", "uih-copilot", refs[0].ID)
	if err != nil || len(b) != 1<<20 || refs[0].Size != (1<<20)+100 {
		t.Fatalf("default limit: %d %+v %v", len(b), refs[0], err)
	}
}

func TestReportSymlinksAndTraversal(t *testing.T) {
	for _, part := range []string{"reports", "reports/uih-copilot", "reports/uih-copilot/report.md"} {
		t.Run(part, func(t *testing.T) {
			f, root := fixture(t, FSOptions{})
			refs, err := f.ListReports(bg, "demo", "uih-copilot")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "demo", part)
			// Move the original into this project's tree; even symlinks which
			// remain within the allowed root must be refused.
			moved := filepath.Join(root, "demo", "moved")
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(filepath.Dir(path), moved)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(rel, path); err != nil {
				t.Fatal(err)
			}
			if got, err := f.ListReports(bg, "demo", "uih-copilot"); err != nil || len(got) != 0 {
				t.Fatalf("symlink read: %v %v", got, err)
			}
			if _, err := f.ReadReport(bg, "demo", "uih-copilot", refs[0].ID); !errors.Is(err, ErrReportNotFound) {
				t.Fatal(err)
			}
		})
	}
	f, root := fixture(t, FSOptions{})
	outside := t.TempDir()
	put(t, filepath.Join(outside, "private.md"), "secret")
	dir := filepath.Join(root, "demo", "reports", "uih-copilot")
	if err := os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(dir, "external.md")); err != nil {
		t.Fatal(err)
	}
	refs, err := f.ListReports(bg, "demo", "uih-copilot")
	if err != nil || len(refs) != 1 {
		t.Fatalf("external symlink: %v %v", refs, err)
	}
	// State references never authorize arbitrary reports, cross-task reports,
	// path traversal, or even /tmp files reserved for the legacy reader.
	put(t, filepath.Join(root, "demo", "state.json"), `{"schema_version":1,"tasks":[{"id":"uih-copilot","reports":["../../private.md","/etc/passwd","reports/other/private.md","/tmp/private.md"]}]}`)
	if got, err := f.ListReports(bg, "demo", "uih-copilot"); err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
}

func tempReport(t *testing.T, data string) string {
	t.Helper()
	f, err := os.CreateTemp("/tmp", "lc-a-report-*.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/tmp", filepath.Base(f.Name()))
	t.Cleanup(func() { os.Remove(path) })
	if _, err := f.WriteString(data); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLegacyTmpReportAuthorization(t *testing.T) {
	f, root := fixture(t, FSOptions{MaxReportBytes: 5})
	path := tempReport(t, "legacy text")
	other := tempReport(t, "unreferenced")
	missing := tempReport(t, "deleted")
	remove(t, missing)
	link := tempReport(t, "link placeholder")
	remove(t, link)
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "legacy-demo", "ledger.md"), "| name | 状态 | 结果 |\n|---|---|---|\n| legacy | done | "+path+" "+path+" "+missing+" "+link+" |\n| other | done | no report |\n")
	refs, err := f.ListReports(bg, "legacy-demo", "legacy")
	if err != nil || len(refs) != 1 || refs[0].ID != reportID(path) || refs[0].Title != filepath.Base(path) {
		t.Fatalf("legacy reports: %v %v", refs, err)
	}
	b, err := f.ReadReport(bg, "legacy-demo", "legacy", refs[0].ID)
	if err != nil || string(b) != "legac" {
		t.Fatalf("read: %q %v", b, err)
	}
	for _, tc := range []struct{ task, id string }{{"legacy", reportID(other)}, {"other", refs[0].ID}, {"legacy", reportID(link)}} {
		if _, err := f.ReadReport(bg, "legacy-demo", tc.task, tc.id); !errors.Is(err, ErrReportNotFound) {
			t.Fatal(err)
		}
	}
	// Even a state task explicitly referring to the same /tmp file cannot read it.
	put(t, filepath.Join(root, "demo", "state.json"), fmtStateReport(path))
	if got, err := f.ListReports(bg, "demo", "legacy"); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	remove(t, path)
	if _, err := f.ReadReport(bg, "legacy-demo", "legacy", refs[0].ID); !errors.Is(err, ErrReportNotFound) {
		t.Fatal(err)
	}
}

func fmtStateReport(path string) string {
	// tempReport only produces filenames in the restricted ASCII allowlist.
	return `{"schema_version":1,"tasks":[{"id":"legacy","reports":["` + path + `"]}]}`
}

func TestReportFileValidation(t *testing.T) {
	f, root := fixture(t, FSOptions{})
	if err := os.Rename(filepath.Join(root, "demo", "reports"), filepath.Join(root, "demo", "saved-reports")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "demo", "reports"), "not a directory")
	if got, err := f.ListReports(bg, "demo", "uih-copilot"); err != nil || len(got) != 0 {
		t.Fatalf("non-directory reports: %v %v", got, err)
	}
}
