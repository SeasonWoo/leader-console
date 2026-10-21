package model

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 契约测试：testdata/fixtures 下的示例（与 docs/design.md 中的示例一致）必须能被严格解码，
// 防止文档、fixture 与 Go 结构体三者漂移。

const demoDir = "../../testdata/fixtures/leader-root/demo"

func strictDecode(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %T: %v\n%s", v, err, data)
	}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(demoDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func eachLine(t *testing.T, name string, fn func([]byte)) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(readFile(t, name)))
	for sc.Scan() {
		if line := bytes.TrimSpace(sc.Bytes()); len(line) > 0 {
			fn(line)
		}
	}
}

func TestFixturesMatchContract(t *testing.T) {
	var li LeaderInfo
	strictDecode(t, readFile(t, "leader.json"), &li)
	if li.SchemaVersion != SchemaVersion || li.ProtocolVersion != ProtocolVersion {
		t.Errorf("leader.json version = %d/%s", li.SchemaVersion, li.ProtocolVersion)
	}

	var st State
	strictDecode(t, readFile(t, "state.json"), &st)
	if len(st.Tasks) == 0 {
		t.Error("state.json has no tasks")
	}

	eachLine(t, "events.jsonl", func(b []byte) { var e Event; strictDecode(t, b, &e) })
	eachLine(t, "decisions.jsonl", func(b []byte) { var d Decision; strictDecode(t, b, &d) })
	eachLine(t, "answers.jsonl", func(b []byte) { var a AnswerLine; strictDecode(t, b, &a) })
}
