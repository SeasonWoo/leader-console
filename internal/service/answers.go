package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
	"leader-console/internal/reconcile"
)

type AnswerInput struct {
	Option          string `json:"option"`
	Text            string `json:"text"`
	ClientRequestID string `json:"client_request_id"`
}

func findDecision(pd *model.ProjectData, id string) (model.Decision, bool) {
	for _, d := range pd.Decisions {
		if d.ID == id {
			return d, true
		}
	}
	return model.Decision{}, false
}
func (s *Service) conflict(pd *model.ProjectData, d model.Decision, code, message string) error {
	current := clone(s.decisionView(pd, d, s.deps.Now()))
	s.redactDecision(&current)
	return &Error{Status: 409, Code: code, Message: message, Current: &current}
}
func (s *Service) acceptedLocked(pd *model.ProjectData, d model.Decision) model.DecisionView {
	s.projects[pd.Name] = pd
	s.publishLocked()
	s.signal()
	out := clone(s.decisionView(pd, d, s.deps.Now()))
	s.redactDecision(&out)
	return out
}

func (s *Service) Answer(ctx context.Context, project, decisionID string, in AnswerInput) (model.DecisionView, error) {
	var zero model.DecisionView
	if !validID(decisionID) {
		return zero, notFound()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	pd, err := s.loadLocked(ctx, project)
	if err != nil {
		return zero, err
	}
	if !protocolAvailable(pd) {
		return zero, problem(409, "protocol_unavailable", "Project does not support panel answers")
	}
	d, ok := findDecision(pd, decisionID)
	if !ok {
		return zero, notFound()
	}
	if !validID(in.ClientRequestID) {
		return zero, problem(400, "invalid_answer", "A valid client_request_id is required")
	}
	// Idempotency precedes the decision status and input validation: a retry after
	// leader acknowledgement must return the original, now-advanced answer.
	for _, a := range pd.Answers {
		if a.ClientRequestID == in.ClientRequestID {
			if a.DecisionID != decisionID {
				return zero, s.conflict(pd, d, "already_answered", "client_request_id belongs to a different decision")
			}
			return s.acceptedLocked(pd, d), nil
		}
	}
	if d.Status != model.DecisionOpen || panelAnswer(pd, decisionID) != nil {
		return zero, s.conflict(pd, d, "already_answered", "Decision already has an answer or is closed")
	}
	in.Text = strings.TrimSpace(in.Text)
	if !utf8.ValidString(in.Text) || utf8.RuneCountInString(in.Text) > 2000 || (in.Option == "" && in.Text == "") || (in.Text != "" && !d.AllowFreeText) {
		return zero, problem(400, "invalid_answer", "Invalid answer text")
	}
	if in.Option != "" {
		found := false
		for _, o := range d.Options {
			if o.Key == in.Option {
				found = true
				break
			}
		}
		if !found {
			return zero, problem(400, "invalid_answer", "Unknown option")
		}
	}
	now := s.deps.Now()
	var id string
	for {
		var entropy [2]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return zero, err
		}
		id = "a-" + now.Format("20060102-150405") + "-" + hex.EncodeToString(entropy[:])
		exists := false
		for _, a := range pd.Answers {
			if a.AnswerID == id {
				exists = true
				break
			}
		}
		if !exists {
			break
		}
	}
	line := model.AnswerLine{Type: model.LineAnswer, AnswerID: id, At: now, DecisionID: decisionID, Option: in.Option, Text: in.Text, By: "panel", ClientRequestID: in.ClientRequestID}
	if err := s.deps.Store.AppendAnswer(ctx, project, line); err != nil {
		return zero, err
	}
	pd.Answers = append(pd.Answers, model.Answer{AnswerID: id, DecisionID: decisionID, Option: in.Option, Text: in.Text, ClientRequestID: in.ClientRequestID, AnsweredAt: now, UpdatedAt: now, Delivery: model.DeliveryQueued})
	return s.acceptedLocked(pd, d), nil
}

func (s *Service) Retry(ctx context.Context, project, answerID string) (model.DecisionView, error) {
	var zero model.DecisionView
	if !validID(answerID) {
		return zero, notFound()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pd, err := s.loadLocked(ctx, project)
	if err != nil {
		return zero, err
	}
	for i := range pd.Answers {
		a := &pd.Answers[i]
		if a.AnswerID != answerID {
			continue
		}
		d, ok := findDecision(pd, a.DecisionID)
		if !ok {
			return zero, notFound()
		}
		if a.Delivery != model.DeliveryFailed || decisionState(pd, d, a) != model.DStateDeliveryFailed {
			return zero, s.conflict(pd, d, "already_answered", "Only failed deliveries can be retried")
		}
		line := model.AnswerLine{Type: model.LineDelivery, AnswerID: answerID, At: s.deps.Now(), Delivery: model.DeliveryQueued, Attempt: a.Attempts}
		if err := s.deps.Store.AppendAnswer(ctx, project, line); err != nil {
			return zero, err
		}
		a.Delivery = model.DeliveryQueued
		a.LastError = ""
		a.Target = ""
		a.UpdatedAt = line.At
		delete(s.due, project+"/"+answerID)
		return s.acceptedLocked(pd, d), nil
	}
	return zero, notFound()
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	return [...]time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second}[(attempt-1)%5]
}

type deliveryJob struct {
	project string
	answer  string
}

// deliver is owned by one worker. All durable queued/retry answers are considered
// each pass, including those loaded at startup; due only tracks offline deferrals.
func (s *Service) deliver(ctx context.Context) {
	s.mu.Lock()
	jobs := []deliveryJob{}
	for name, pd := range s.projects {
		for _, a := range pd.Answers {
			if a.Delivery == "" || a.Delivery == model.DeliveryQueued || a.Delivery == model.DeliveryRetry {
				jobs = append(jobs, deliveryJob{name, a.AnswerID})
			}
		}
	}
	s.mu.Unlock()
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		s.deliverOne(ctx, j)
	}
}

func (s *Service) prepare(ctx context.Context, j deliveryJob) (*model.ProjectData, model.Decision, *model.Answer, model.LeaderPresence, bool) {
	pd, err := s.loadLocked(ctx, j.project)
	if err != nil {
		return nil, model.Decision{}, nil, model.LeaderPresence{}, false
	}
	// Publish leader-written acknowledgements even when no new delivery is needed.
	s.projects[j.project] = pd
	for i := range pd.Answers {
		a := &pd.Answers[i]
		if a.AnswerID != j.answer {
			continue
		}
		d, ok := findDecision(pd, a.DecisionID)
		if !ok {
			return pd, d, a, model.LeaderPresence{}, false
		}
		state := decisionState(pd, d, a)
		if state != model.DStateQueued {
			delete(s.due, j.project+"/"+j.answer)
			return pd, d, a, model.LeaderPresence{}, false
		}
		presence, _ := s.deps.Reconcile(clone(pd), append([]herdr.Agent(nil), s.agents...), s.health.OK, s.deps.Now())
		return pd, d, a, presence, true
	}
	return pd, model.Decision{}, nil, model.LeaderPresence{}, false
}
func (s *Service) deferDelivery(key string) { s.due[key] = s.deps.Now().Add(15 * time.Second) }
func (s *Service) deliverOne(ctx context.Context, j deliveryJob) {
	key := j.project + "/" + j.answer
	s.mu.Lock()
	if s.deps.Now().Before(s.due[key]) {
		s.mu.Unlock()
		return
	}
	pd, d, a, p, ok := s.prepare(ctx, j)
	if !ok {
		s.publishLocked()
		s.mu.Unlock()
		return
	}
	if a.Delivery == model.DeliveryRetry && s.deps.Now().Before(a.UpdatedAt.Add(backoff(a.Attempts))) {
		s.mu.Unlock()
		return
	}
	if !p.Online || p.AgentStatus == "blocked" || !targetPattern.MatchString(p.Target) || !protocolAvailable(pd) || s.deps.Herdr == nil {
		s.deferDelivery(key)
		s.publishLocked()
		s.mu.Unlock()
		return
	}
	expected := clone(pd.Leader)
	s.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	agent, err := s.deps.Herdr.Get(callCtx, p.Target)
	cancel()
	if err == nil && !identityMatches(expected, p.Target, agent) {
		s.mu.Lock()
		s.deferDelivery(key)
		s.mu.Unlock()
		return
	}
	if err == nil && agent.AgentStatus == "blocked" {
		s.mu.Lock()
		s.deferDelivery(key)
		s.mu.Unlock()
		return
	}
	if err == nil {
		// Reload after Get: a leader may have acknowledged, closed, or changed identity
		// while the network call was in flight. Never prompt that stale target.
		s.mu.Lock()
		current, decision, answer, presence, ready := s.prepare(ctx, j)
		ready = ready && protocolAvailable(current) && presence.Online && presence.Target == p.Target && identityMatches(current.Leader, p.Target, agent)
		if ready {
			pd = current
			d = decision
			a = answer
		}
		if !ready {
			s.deferDelivery(key)
			s.publishLocked()
			s.mu.Unlock()
			return
		}
		text := promptText(pd.Name, d, *a)
		s.mu.Unlock()
		promptCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		err = s.deps.Herdr.Prompt(promptCtx, p.Target, text)
		stop()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	} // a crash/cancel can safely redeliver the same ID
	var he *herdr.Error
	if errors.As(err, &he) && (he.Code == herdr.CodeAgentBlocked || he.Code == herdr.CodeAgentNotFound) {
		s.deferDelivery(key)
		return
	}
	status := model.DeliveryDelivered
	attempt := a.Attempts + 1
	if err != nil {
		status = model.DeliveryRetry
		if attempt%5 == 0 {
			status = model.DeliveryFailed
		}
	}
	line := model.AnswerLine{Type: model.LineDelivery, AnswerID: a.AnswerID, At: s.deps.Now(), Delivery: status, Attempt: attempt, Target: p.Target}
	if err != nil {
		line.Error = s.deps.Redact(cleanTerminal(err.Error()))
	}
	if appendErr := s.deps.Store.AppendAnswer(ctx, j.project, line); appendErr != nil {
		s.deferDelivery(key)
		return
	}
	// Refresh after appending, preserving any leader events written during Prompt.
	if latest, loadErr := s.loadLocked(ctx, j.project); loadErr == nil {
		s.projects[j.project] = latest
	}
	delete(s.due, key)
	s.publishLocked()
}

// identityMatches is the delivery-time recheck; it shares reconcile's single identity rule.
func identityMatches(li *model.LeaderInfo, target string, a herdr.Agent) bool {
	return reconcile.VerifyLeader(li, target, a)
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
func promptText(project string, d model.Decision, a model.Answer) string {
	label := ""
	for _, o := range d.Options {
		if o.Key == a.Option {
			label = o.Label
			break
		}
	}
	prefix := fmt.Sprintf("【决策回传】project=%s decision=%s answer=%s", oneLine(project), oneLine(d.ID), oneLine(a.AnswerID))
	if utf8.RuneCountInString(prefix) > 300 {
		// At the maximum legal ID lengths even the mandatory fields exceed
		// 300 characters. Preserve the project and answer lookup keys; the
		// complete decision ID is in that answer's durable record.
		return truncateNoEllipsis(fmt.Sprintf("【决策回传】project=%s answer=%s 详见该项目 answers.jsonl（完整 decision_id 见回答行）", oneLine(project), oneLine(a.AnswerID)), 300)
	}
	tail := " 详见 ~/.leader/" + oneLine(project) + "/answers.jsonl"
	summary := " 选择=" + oneLine(a.Option) + " " + truncate(oneLine(label), 40)
	if a.Text != "" {
		summary += " 补充=" + truncate(oneLine(a.Text), 80)
	}
	// Prioritize the lookup keys over summaries when the budget is exhausted.
	return truncateNoEllipsis(prefix+summary+tail, 300)
}
func truncateNoEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
