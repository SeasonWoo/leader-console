package jsonl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestAppendProcessHelper(t *testing.T) {
	if os.Getenv("LC_JSONL_HELPER") != "1" {
		return
	}
	path := os.Getenv("LC_JSONL_PATH")
	for i := 0; i < 200; i++ {
		b, err := json.Marshal(map[string]any{"process": os.Getenv("LC_JSONL_PROCESS"), "seq": i, "payload": strings.Repeat("中文\\\"|", 100)})
		if err != nil {
			t.Fatal(err)
		}
		if err := Append(path, path+".lock", b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppendMultiProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lines.jsonl")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const workers = 4
	cmds := make([]*exec.Cmd, workers)
	outputs := make([]bytes.Buffer, workers)
	for i := range cmds {
		cmds[i] = exec.Command(exe, "-test.run=^TestAppendProcessHelper$")
		cmds[i].Env = append(os.Environ(), "LC_JSONL_HELPER=1", "LC_JSONL_PATH="+path, fmt.Sprintf("LC_JSONL_PROCESS=%d", i))
		cmds[i].Stdout, cmds[i].Stderr = &outputs[i], &outputs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("worker %d: %v: %s", i, err, &outputs[i])
		}
	}
	lines, err := ReadLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != workers*200 {
		t.Fatalf("got %d lines, want %d", len(lines), workers*200)
	}
	seen := make(map[string]bool)
	for _, line := range lines {
		var row struct {
			Process string `json:"process"`
			Seq     int    `json:"seq"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatalf("interleaved line: %v", err)
		}
		key := fmt.Sprintf("%s/%d", row.Process, row.Seq)
		if seen[key] || row.Payload != strings.Repeat("中文\\\"|", 100) {
			t.Fatalf("duplicate or damaged row %s", key)
		}
		seen[key] = true
	}
}

func TestAppendGoroutinesAndNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Append(path, path+".lock", []byte("{}\n")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != strings.Repeat("{}\n", 16) {
		t.Fatalf("bad output %q", b)
	}
}

func TestAppendWaitsForLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	done := make(chan error, 1)
	go func() { done <- Append(path, path+".lock", []byte("{}")) }()
	select {
	case err := <-done:
		t.Fatalf("append passed held flock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("append did not resume")
	}
}

func TestReadLines(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"", nil}, {"unfinished", nil}, {"{}\n{", []string{"{}"}},
		{"{}\ninvalid\n\n{\"v\":1}\npartial", []string{"{}", "invalid", "", "{\"v\":1}"}},
		{"a\r\nb\n", []string{"a\r", "b"}},
		{strings.Repeat("x", 128<<10) + "\n", []string{strings.Repeat("x", 128<<10)}},
	} {
		path := filepath.Join(t.TempDir(), "data")
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		lines, err := ReadLines(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != len(tc.want) {
			t.Fatalf("got %d lines, want %d", len(lines), len(tc.want))
		}
		for i := range lines {
			if string(lines[i]) != tc.want[i] {
				t.Fatalf("line %d differs", i+1)
			}
		}
	}
	if _, err := ReadLines(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := ReadLinesFrom(errorReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestAppendErrorsAndRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	for _, input := range []string{"{}\n{}", "{}\r", "{}\n\n"} {
		if err := Append(path, path+".lock", []byte(input)); err == nil {
			t.Fatal("accepted embedded newline")
		}
	}
	if err := Append(path, path, []byte("{}")); err == nil {
		t.Fatal("accepted data as lock")
	}
	if err := Append(path, filepath.Join(dir, "missing", "lock"), []byte("{}")); err == nil {
		t.Fatal("missing lock dir")
	}
	if err := Append(filepath.Join(dir, "missing", "data"), path+".lock", []byte("{}")); err == nil {
		t.Fatal("missing data dir")
	}
	if err := Append(path, dir, []byte("{}")); err == nil {
		t.Fatal("directory lock")
	}
	if err := Append(dir, path+".lock", []byte("{}")); err == nil {
		t.Fatal("directory data")
	}
	if _, err := ReadLines(dir); err == nil {
		t.Fatal("directory read")
	}
	if err := os.WriteFile(path, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := Append(link, path+".lock", []byte("{}")); err == nil {
		t.Fatal("symlink data")
	}
	if err := Append(path, link, []byte("{}")); err == nil {
		t.Fatal("symlink lock")
	}
	if _, err := ReadLines(link); err == nil {
		t.Fatal("symlink read")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, fifo, []byte("{}")); err == nil {
		t.Fatal("fifo lock")
	}
	if _, err := ReadLines(fifo); err == nil {
		t.Fatal("fifo read")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := AppendRoot(root, "root.jsonl", "root.lock", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := AppendRoot(root, "../escape", "root.lock", []byte("{}")); err == nil {
		t.Fatal("escaped root")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := regular(file); err == nil {
		t.Fatal("closed file")
	}
}

// A crashed or short write leaves a fragment after the last newline; the next append
// must not glue its line onto it (the reader would drop both).
func TestAppendDropsPartialTail(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 10000) // fragment longer than the backwards scan buffer
	for name, tc := range map[string]struct{ before, want string }{
		"fragment after lines": {`{"a":1}` + "\n" + `{"type":"report","at":`, `{"a":1}` + "\n" + `{"b":2}` + "\n"},
		"only a fragment":      {`{"type":`, `{"b":2}` + "\n"},
		"long fragment":        {`{"a":1}` + "\n" + long, `{"a":1}` + "\n" + `{"b":2}` + "\n"},
		"long line kept":       {`{"a":"` + long + `"}` + "\n", `{"a":"` + long + `"}` + "\n" + `{"b":2}` + "\n"},
		"empty":                {"", `{"b":2}` + "\n"},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Append(path, path+".lock", []byte(`{"b":2}`)); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != tc.want {
			t.Fatalf("%s: %q", name, got)
		}
		lines, err := ReadLines(path)
		if err != nil || string(lines[len(lines)-1]) != `{"b":2}` {
			t.Fatalf("%s: appended line not readable: %q %v", name, lines, err)
		}
	}
}

func TestAppendLockWaitIsCancellable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = AppendRootContext(ctx, root, "data", "data.lock", []byte("{}"))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("lock wait not bounded: %v after %v", err, time.Since(start))
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatal("wrote without the lock")
	}
}

// Review 2 §3: concurrent first appends through separate os.Root handles race to
// create the lock file; on darwin the losers saw ENOENT and dropped their line.
func TestAppendConcurrentFirstCreate(t *testing.T) {
	for round := 0; round < 30; round++ {
		dir := t.TempDir()
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				root, err := os.OpenRoot(dir)
				if err != nil {
					errs <- err
					return
				}
				defer root.Close()
				errs <- AppendRoot(root, "a.jsonl", "a.jsonl.lock", []byte("{}"))
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if lines, err := ReadLines(filepath.Join(dir, "a.jsonl")); err != nil || len(lines) != 4 {
			t.Fatalf("round %d: %d lines, %v", round, len(lines), err)
		}
	}
}
