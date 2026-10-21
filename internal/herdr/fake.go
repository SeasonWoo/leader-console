package herdr

import (
	"context"
	"sync"
)

// PromptCall records the exact arguments supplied to Prompt.
type PromptCall struct {
	Target string
	Text   string
}

// Fake is a zero-value-ready in-memory Client. Configure exported fields before
// concurrent use. During use, mutate via SetAgents/SetError/SetRead and inspect
// prompts via RecordedPrompts. Errors are keyed by target; "list" controls List.
// Prompt attempts are recorded even when the configured target returns an error.
type Fake struct {
	mu      sync.Mutex
	Agents  []Agent
	Errors  map[string]error
	Reads   map[string]string
	Prompts []PromptCall
}

func (f *Fake) SetAgents(agents []Agent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Agents = append([]Agent(nil), agents...)
}

func (f *Fake) SetError(target string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Errors == nil {
		f.Errors = make(map[string]error)
	}
	f.Errors[target] = err
}

func (f *Fake) SetRead(target, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Reads == nil {
		f.Reads = make(map[string]string)
	}
	f.Reads[target] = text
}

func (f *Fake) RecordedPrompts() []PromptCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PromptCall(nil), f.Prompts...)
}

func (f *Fake) List(ctx context.Context) ([]Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.Errors["list"]; err != nil {
		return nil, err
	}
	return append([]Agent(nil), f.Agents...), nil
}

func (f *Fake) Get(ctx context.Context, target string) (Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.get(ctx, target)
}

func (f *Fake) get(ctx context.Context, target string) (Agent, error) {
	if err := ctx.Err(); err != nil {
		return Agent{}, err
	}
	if err := checkTarget(target); err != nil {
		return Agent{}, err
	}
	if err := f.Errors[target]; err != nil {
		return Agent{}, err
	}
	for _, a := range f.Agents {
		if a.Name == target || a.PaneID == target {
			return a, nil
		}
	}
	return Agent{}, &Error{Code: CodeAgentNotFound, Message: "agent not found"}
}

func (f *Fake) Read(ctx context.Context, target string, opts ReadOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.get(ctx, target); err != nil {
		return "", err
	}
	return cleanTerminal(f.Reads[target]), nil
}

func (f *Fake) Prompt(ctx context.Context, target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Prompts = append(f.Prompts, PromptCall{Target: target, Text: text})
	_, err := f.get(ctx, target)
	return err
}

var _ Client = (*Fake)(nil)
