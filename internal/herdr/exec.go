package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"time"
	"unicode"
)

type execClient struct {
	cmd     []string
	timeout time.Duration
}

// NewExec copies the command prefix. A nonpositive timeout defaults to five seconds.
func NewExec(cmd []string, timeout time.Duration) Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &execClient{cmd: append([]string(nil), cmd...), timeout: timeout}
}

var targetPattern = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,64}$`)

func checkTarget(target string) error {
	if !targetPattern.MatchString(target) {
		return &Error{Code: CodeExec, Message: "invalid agent target"}
	}
	return nil
}

func (c *execClient) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(c.cmd) == 0 || c.cmd[0] == "" {
		return nil, &Error{Code: CodeExec, Message: "empty command"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	argv := append(append([]string(nil), c.cmd[1:]...), args...)
	cmd := exec.CommandContext(ctx, c.cmd[0], argv...)
	// A descendant must not keep inherited output pipes open after the deadline.
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, &Error{Code: CodeTimeout, Message: ctx.Err().Error()}
	}
	for _, output := range [][]byte{stdout.Bytes(), stderr.Bytes()} {
		// CLI diagnostics may precede or follow its JSON error envelope.
		for start := bytes.IndexByte(output, '{'); start >= 0; {
			var envelope struct {
				Error *struct{ Code, Message string } `json:"error"`
			}
			if json.NewDecoder(bytes.NewReader(output[start:])).Decode(&envelope) == nil && envelope.Error != nil && envelope.Error.Code != "" {
				return nil, &Error{Code: envelope.Error.Code, Message: envelope.Error.Message}
			}
			output = output[start+1:]
			start = bytes.IndexByte(output, '{')
		}
	}
	return nil, &Error{Code: CodeExec, Message: err.Error()}
}

type wireAgent struct {
	Agent
	Title   string `json:"terminal_title_stripped"`
	Session struct {
		Value string `json:"value"`
	} `json:"agent_session"`
}

func (a wireAgent) model() Agent {
	a.Agent.TerminalTitle = a.Title
	a.Agent.SessionID = a.Session.Value
	return a.Agent
}

func (c *execClient) List(ctx context.Context) ([]Agent, error) {
	b, err := c.run(ctx, "agent", "list")
	if err != nil {
		return nil, err
	}
	var v struct {
		Result *struct {
			Agents *[]wireAgent `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, &Error{Code: CodeDecode, Message: err.Error()}
	}
	if v.Result == nil || v.Result.Agents == nil {
		return nil, &Error{Code: CodeDecode, Message: "missing result.agents"}
	}
	agents := make([]Agent, 0, len(*v.Result.Agents))
	for _, a := range *v.Result.Agents {
		agents = append(agents, a.model())
	}
	return agents, nil
}

func (c *execClient) Get(ctx context.Context, target string) (Agent, error) {
	if err := checkTarget(target); err != nil {
		return Agent{}, err
	}
	b, err := c.run(ctx, "agent", "get", target)
	if err != nil {
		return Agent{}, err
	}
	var v struct {
		Result *struct {
			Agent *wireAgent `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return Agent{}, &Error{Code: CodeDecode, Message: err.Error()}
	}
	if v.Result == nil || v.Result.Agent == nil {
		return Agent{}, &Error{Code: CodeDecode, Message: "missing result.agent"}
	}
	return v.Result.Agent.model(), nil
}

func (c *execClient) Read(ctx context.Context, target string, opts ReadOptions) (string, error) {
	if err := checkTarget(target); err != nil {
		return "", err
	}
	if opts.Source == "" {
		opts.Source = "recent"
	}
	if opts.Source != "recent" && opts.Source != "visible" {
		return "", &Error{Code: CodeExec, Message: "invalid read source"}
	}
	if opts.Lines <= 0 {
		opts.Lines = 80
	}
	if opts.Lines > 400 {
		opts.Lines = 400
	}
	b, err := c.run(ctx, "agent", "read", target, "--source", opts.Source, "--lines", strconv.Itoa(opts.Lines), "--format", "text")
	if err != nil {
		return "", err
	}
	return cleanTerminal(string(b)), nil
}

func (c *execClient) Prompt(ctx context.Context, target, text string) error {
	if err := checkTarget(target); err != nil {
		return err
	}
	_, err := c.run(ctx, "agent", "prompt", target, text)
	return err
}

// cleanTerminal drops CSI, OSC, DCS/APC/PM/SOS strings and other terminal controls.
// Unterminated escape sequences consume the remainder rather than exposing payloads.
func cleanTerminal(s string) string {
	var out bytes.Buffer
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\x1b' {
			i++
			if i == len(runes) {
				break
			}
			r = runes[i]
			if r != '[' && r != ']' && r != 'P' && r != '_' && r != '^' && r != 'X' {
				for i < len(runes) && runes[i] >= 0x20 && runes[i] <= 0x2f {
					i++
				}
				continue
			}
		} else {
			switch r {
			case '\u009b':
				r = '['
			case '\u009d', '\u0090', '\u009f', '\u009e', '\u0098':
				r = ']'
			default:
				if r == '\n' || r == '\t' || !unicode.IsControl(r) {
					out.WriteRune(r)
				}
				continue
			}
		}
		if r == '[' {
			for i++; i < len(runes); i++ {
				if runes[i] >= 0x40 && runes[i] <= 0x7e {
					break
				}
			}
		} else {
			for i++; i < len(runes); i++ {
				if runes[i] == '\a' || runes[i] == '\u009c' {
					break
				}
				if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '\\' {
					i++
					break
				}
			}
		}
	}
	return out.String()
}

var _ Client = (*execClient)(nil)
