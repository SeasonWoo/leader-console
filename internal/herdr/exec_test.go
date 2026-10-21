package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v; want code %s", err, code)
	}
	if e.Error() == "" {
		t.Fatal("empty error string")
	}
}

func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecFixture(t *testing.T) {
	ctx := context.Background()
	client := NewExec([]string{"../../testdata/fake-herdr.sh"}, 0)
	agents, err := client.List(ctx)
	if err != nil || len(agents) != 3 {
		t.Fatalf("list = %+v, %v", agents, err)
	}
	if agents[0].TerminalTitle != "leader" || agents[0].SessionID != "e4916e66-03ee-408d-88c3-ff6c201bf695" || agents[1].SessionID != "" || !agents[1].InteractiveReady {
		t.Fatalf("wrong wire mapping: %+v", agents)
	}
	for _, target := range []string{"leader-demo", "w1:pA"} {
		a, err := client.Get(ctx, target)
		if err != nil || a.Name != agents[0].Name || a.SessionID != agents[0].SessionID || a.TerminalTitle != agents[0].TerminalTitle || a.Cwd != agents[0].Cwd {
			t.Fatalf("get = %+v, %v", a, err)
		}
	}
	text, err := client.Read(ctx, "leader-demo", ReadOptions{})
	if err != nil || strings.ContainsRune(text, '\x1b') || !strings.Contains(text, "● 正在跑单测\n") {
		t.Fatalf("read = %q, %v", text, err)
	}
	log := filepath.Join(t.TempDir(), "prompts.log")
	t.Setenv("FAKE_HERDR_LOG", log)
	prompt := "hello; space\n$(touch injected) `echo bad` 'quoted' \"double\""
	if err := client.Prompt(ctx, "leader-demo", prompt); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil || string(b) != "leader-demo\t"+prompt+"\n" {
		t.Fatalf("prompt changed: %q, %v", b, err)
	}
	_, err = client.Get(ctx, "missing")
	errorCode(t, err, CodeAgentNotFound)
	t.Setenv("FAKE_HERDR_FAIL", "1")
	_, err = client.List(ctx)
	errorCode(t, err, CodeAgentNotFound)
	_, err = client.Get(ctx, "leader-demo")
	errorCode(t, err, CodeAgentNotFound)
	_, err = client.Read(ctx, "leader-demo", ReadOptions{})
	errorCode(t, err, CodeAgentNotFound)
	errorCode(t, client.Prompt(ctx, "leader-demo", "x"), CodeAgentNotFound)
}

func TestExecArguments(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("ARG_LOG", log)
	p := script(t, `printf '%s\n' "$@" > "$ARG_LOG"`)
	cmd := []string{p, "prefix"}
	client := NewExec(cmd, 10*time.Second)
	cmd[0] = "does-not-exist" // The constructor owns its prefix.
	for _, tc := range []struct {
		opts   ReadOptions
		source string
		lines  string
	}{{ReadOptions{}, "recent", "80"}, {ReadOptions{Source: "visible", Lines: 999}, "visible", "400"}, {ReadOptions{Lines: -1}, "recent", "80"}, {ReadOptions{Lines: 12}, "recent", "12"}} {
		if _, err := client.Read(context.Background(), "w1:pA", tc.opts); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(log)
		want := []string{"prefix", "agent", "read", "w1:pA", "--source", tc.source, "--lines", tc.lines, "--format", "text"}
		if err != nil || !reflect.DeepEqual(strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"), want) {
			t.Fatalf("args = %q, %v; want %v", b, err, want)
		}
	}
	_, err := client.Read(context.Background(), "ok", ReadOptions{Source: "invalid"})
	errorCode(t, err, CodeExec)
	for _, target := range []string{"", "a b", "x;y", "x\ny", "$(x)", strings.Repeat("x", 65)} {
		_, err := client.Get(context.Background(), target)
		errorCode(t, err, CodeExec)
		_, err = client.Read(context.Background(), target, ReadOptions{})
		errorCode(t, err, CodeExec)
		errorCode(t, client.Prompt(context.Background(), target, "x"), CodeExec)
	}
}

func TestExecFailures(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ body, code string }{
		{`echo '{"error":{"code":"agent_blocked","message":"wait"}}'; exit 3`, CodeAgentBlocked},
		{`echo 'diagnostic {oops' >&2; echo '{"error":{"code":"agent_prompt_stalled","message":"wait"}}' >&2; exit 2`, CodeAgentPromptStalled},
		{`echo '{"error":{}}'; exit 2`, CodeExec},
		{`echo 'not json'; exit 2`, CodeExec},
	} {
		client := NewExec([]string{script(t, tc.body)}, 10*time.Second)
		errorCode(t, client.Prompt(ctx, "target", "x"), tc.code)
	}
	for _, body := range []string{`echo 'not json'`, `echo '{}'`, `echo '{"result":{}}'`, `echo '{"result":{"agents":null,"agent":null}}'`} {
		client := NewExec([]string{script(t, body)}, 10*time.Second)
		_, err := client.List(ctx)
		errorCode(t, err, CodeDecode)
		_, err = client.Get(ctx, "target")
		errorCode(t, err, CodeDecode)
	}
	client := NewExec([]string{script(t, `echo '{"result":{"agents":[]}}'`)}, 10*time.Second)
	if agents, err := client.List(ctx); err != nil || len(agents) != 0 || agents == nil {
		t.Fatalf("empty list = %v, %v", agents, err)
	}
	for _, cmd := range [][]string{nil, {""}, {"/nonexistent/herdr"}} {
		_, err := NewExec(cmd, time.Second).List(ctx)
		errorCode(t, err, CodeExec)
	}
	client = NewExec([]string{script(t, "exec sleep 10")}, 50*time.Millisecond)
	start := time.Now()
	_, err := client.List(ctx)
	errorCode(t, err, CodeTimeout)
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not bound execution")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client.List(cancelled)
	errorCode(t, err, CodeExec)
	// Success does not depend on the unknown real prompt stdout shape.
	if err := NewExec([]string{script(t, "echo unstructured-success")}, 0).Prompt(ctx, "target", "x"); err != nil {
		t.Fatal(err)
	}
}

func TestCleanTerminal(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\x1b[31m红\x1b[0mz\n\t\x00\r\b\x7f", "a红z\n\t"},
		{"a\x1b]0;title\ab\x1b]8;;url\x1b\\c", "abc"},
		{"a\x1bPpayload\x1b\\b\x1b^secret\ac", "abc"},
		{"a\u009b31mb\u009dtitle\u009cc", "abc"},
		{"a\x1b(Bb\x1b7c", "abc"},
		{"a\x1b", "a"}, {"a\x1b[", "a"}, {"a\x1b]hidden", "a"},
	} {
		if got := cleanTerminal(tc.in); got != tc.want {
			t.Errorf("clean(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestFake(t *testing.T) {
	ctx := context.Background()
	f := &Fake{}
	f.SetAgents([]Agent{{Name: "a", PaneID: "w1:pA"}})
	f.SetRead("a", "\x1b[31mhello")
	if text, err := f.Read(ctx, "a", ReadOptions{}); err != nil || text != "hello" {
		t.Fatalf("read = %q, %v", text, err)
	}
	if _, err := f.Get(ctx, "w1:pA"); err != nil {
		t.Fatal(err)
	}
	agents, _ := f.List(ctx)
	agents[0].Name = "mutated"
	if _, err := f.Get(ctx, "a"); err != nil {
		t.Fatal("List leaked backing array")
	}
	f.SetError("a", &Error{Code: CodeAgentBlocked})
	errorCode(t, f.Prompt(ctx, "a", "exact"), CodeAgentBlocked)
	_, err := f.Read(ctx, "a", ReadOptions{})
	errorCode(t, err, CodeAgentBlocked)
	f.SetError("a", nil)
	_, err = f.Get(ctx, "missing")
	errorCode(t, err, CodeAgentNotFound)
	_, err = f.Get(ctx, "bad target")
	errorCode(t, err, CodeExec)
	f.SetError("list", &Error{Code: CodeExec})
	_, err = f.List(ctx)
	errorCode(t, err, CodeExec)
	f.SetError("list", nil)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.List(cancelled); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := f.Get(cancelled, "a"); err != context.Canceled {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.SetAgents([]Agent{{Name: "a"}})
			f.SetRead("a", "text")
			f.SetError("a", nil)
			_, _ = f.List(ctx)
			_, _ = f.Get(ctx, "a")
			_, _ = f.Read(ctx, "a", ReadOptions{})
			_ = f.Prompt(ctx, "a", "hi")
			_ = f.RecordedPrompts()
		}()
	}
	wg.Wait()
	prompts := f.RecordedPrompts()
	if len(prompts) != 31 || prompts[0] != (PromptCall{Target: "a", Text: "exact"}) {
		t.Fatalf("prompts = %v", prompts)
	}
	prompts[0].Text = "mutated"
	if f.RecordedPrompts()[0].Text != "exact" {
		t.Fatal("RecordedPrompts leaked backing array")
	}
}
