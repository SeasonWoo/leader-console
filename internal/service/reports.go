package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
)

// ReportOptions configures the panel-side 【定期汇报】 scheduler. The zero value disables it.
type ReportOptions struct {
	Enabled bool
	// Projects limits the scheduler to these projects; empty means every lc-1 project.
	Projects []string
	// Grace delays each report past heartbeat_at + report_interval_min so that a leader
	// still running its own CronCreate keeps priority; 0 once the leader relies on the panel.
	Grace time.Duration
	// StartupDelay is the minimum wait after the service starts before the first report
	// (the command-line default is 1 minute), so a restart never prompts immediately.
	StartupDelay time.Duration
}

// reportMaxBackoff caps how far unacknowledged reports stretch the interval.
const reportMaxBackoff = 6

func reportKey(project string) string { return project + "#report" }

func reportText(project string) string {
	return fmt.Sprintf("【定期汇报】project=%s source=leader-console", oneLine(project))
}

// acceptsInput reports whether a herdr status means the agent waits at its prompt.
// working/blocked/unknown are never typed into: the report would interrupt a turn,
// land in an approval dialog, or reach a shell.
func acceptsInput(status string) bool { return status == "idle" || status == "done" }

func (s *Service) reportsFor(project string) bool {
	o := s.opts.Reports
	return o.Enabled && (len(o.Projects) == 0 || slices.Contains(o.Projects, project))
}

// reportPlan derives the schedule purely from leader.json, panel.jsonl and report lines
// still waiting to be persisted, so a restart resumes the same cycle instead of
// replaying missed ones:
//
//   - every report attempt has an ID shared by its intent line (delivery=queued, written
//     before the prompt) and its result line. Lines are folded per attempt, so a result
//     written twice (an append that landed but reported an error, then the audit retry)
//     still counts once;
//   - delivered/failed, and an intent whose result is unknown (crash, audit failure),
//     consume the cycle: the panel never types twice into one cycle. Only retry, written
//     when nothing was typed (herdr refused, or the final gate failed), cancels it;
//   - a cycle starts at the later of heartbeat_at (the leader handled a report or an
//     answer) and the last consumed attempt; the next report is due one interval
//     (+ grace) later. Each further report the leader has not acknowledged by
//     refreshing heartbeat_at doubles the wait, up to reportMaxBackoff intervals.
func (s *Service) reportPlan(pd *model.ProjectData) (model.ReportSchedule, bool) {
	if !protocolAvailable(pd) || !s.reportsFor(pd.Name) {
		return model.ReportSchedule{}, false
	}
	li := pd.Leader
	interval := li.ReportIntervalMin
	if interval <= 0 {
		interval = 10
	}
	plan := model.ReportSchedule{IntervalMin: interval}
	base := li.HeartbeatAt
	consume := func(at time.Time) {
		if at.After(base) {
			base = at
		}
	}
	for _, a := range reportAttempts(append(append([]model.PanelLine(nil), pd.Panel.Reports...), s.reportAudit[pd.Name]...)) {
		if a.result == nil {
			consume(a.intent.At)
			continue
		}
		switch a.result.Delivery {
		case model.DeliveryDelivered:
			at := a.result.At
			plan.LastSentAt = &at
			if at.After(li.HeartbeatAt) {
				plan.Unacked++
			}
			consume(at)
		case model.DeliveryFailed:
			consume(a.result.At)
		}
		plan.LastError = a.result.Error
	}
	mult := 1
	if plan.Unacked > 1 {
		mult = min(1<<(plan.Unacked-1), reportMaxBackoff)
	}
	due := base.Add(time.Duration(interval*mult)*time.Minute + s.opts.Reports.Grace)
	if earliest := s.started.Add(s.opts.Reports.StartupDelay); due.Before(earliest) {
		due = earliest
	}
	plan.NextDueAt = due
	return plan, true
}

type reportAttempt struct{ intent, result *model.PanelLine }

// reportAttempts folds report lines by attempt ID in first-seen order. Lines without
// an ID are treated as attempts of their own.
func reportAttempts(lines []model.PanelLine) []reportAttempt {
	var order []string
	byID := map[string]*reportAttempt{}
	for i := range lines {
		l := &lines[i]
		id := l.ID
		if id == "" {
			id = fmt.Sprint("#", i)
		}
		a := byID[id]
		if a == nil {
			a = &reportAttempt{}
			byID[id] = a
			order = append(order, id)
		}
		if l.Delivery == model.DeliveryQueued {
			if a.intent == nil {
				a.intent = l
			}
		} else {
			a.result = l
		}
	}
	out := make([]reportAttempt, 0, len(order))
	for _, id := range order {
		if a := byID[id]; a.intent != nil || a.result != nil {
			if a.intent == nil {
				a.intent = a.result
			}
			out = append(out, *a)
		}
	}
	return out
}

func newAttemptID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "rp-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// reports runs one scheduler pass. It shares the delivery worker, so a report and an
// answer are never prompted concurrently.
func (s *Service) reports(ctx context.Context) {
	if !s.opts.Reports.Enabled {
		return
	}
	s.mu.Lock()
	var due []string
	now := s.deps.Now()
	for name, pd := range s.projects {
		if now.Before(s.due[reportKey(name)]) {
			continue
		}
		if plan, ok := s.reportPlan(pd); ok && (len(s.reportAudit[name]) > 0 || !now.Before(plan.NextDueAt)) {
			due = append(due, name)
		}
	}
	s.mu.Unlock()
	slices.Sort(due)
	for _, name := range due {
		if ctx.Err() != nil {
			return
		}
		s.reportOne(ctx, name)
	}
}

// reportReady reloads the project and decides whether a report may be typed now.
// It returns the reason to wait otherwise. Callers hold s.mu.
func (s *Service) reportReady(ctx context.Context, project string) (*model.ProjectData, model.LeaderPresence, string) {
	pd, err := s.loadLocked(ctx, project)
	if err != nil {
		return nil, model.LeaderPresence{}, "not_identified"
	}
	s.projects[project] = pd
	plan, ok := s.reportPlan(pd)
	if !ok || s.deps.Now().Before(plan.NextDueAt) {
		return pd, model.LeaderPresence{}, "not_due"
	}
	presence, _ := s.deps.Reconcile(clone(pd), append([]herdr.Agent(nil), s.agents...), s.health.OK, s.deps.Now())
	switch {
	case !presence.Identified || !targetPattern.MatchString(presence.Target) || s.deps.Herdr == nil:
		return pd, presence, "not_identified"
	case presence.AgentStatus == "blocked":
		return pd, presence, "leader_blocked"
	case !acceptsInput(presence.AgentStatus):
		return pd, presence, "leader_busy"
	}
	return pd, presence, ""
}

func (s *Service) waitReport(project, reason string) {
	if reason == "not_due" {
		reason = ""
	} else {
		s.deferDelivery(reportKey(project))
	}
	if s.reportWait[project] != reason {
		s.reportWait[project] = reason
		s.publishLocked()
	}
}

// reportGate is one complete check right before acting: a live `herdr agent get`,
// then under s.mu a fresh leader.json, the shared identity rule and the input state.
// snap pins the registration and heartbeat_at seen when the attempt was decided; the
// gate fails if either moved (leader switched, or its own cron handled a report).
// withDue also requires the report to be due; after the intent is written that check
// is replaced by the pinned heartbeat, because the intent itself consumes the cycle.
// It returns "" when the report may be typed now, otherwise the reason to wait.
func (s *Service) reportGate(ctx context.Context, project string, snap *model.LeaderInfo, target string, withDue bool) string {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	agent, err := s.deps.Herdr.Get(callCtx, target)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err != nil || !identityMatches(snap, target, agent):
		return "not_identified"
	case agent.AgentStatus == "blocked":
		return "leader_blocked"
	case !acceptsInput(agent.AgentStatus):
		return "leader_busy"
	}
	pd, err := s.loadLocked(ctx, project)
	if err != nil {
		return "not_identified"
	}
	s.projects[project] = pd
	if !protocolAvailable(pd) || !s.reportsFor(project) || pd.Leader.Leader != snap.Leader || pd.Leader.Cwd != snap.Cwd {
		return "not_identified"
	}
	if !pd.Leader.HeartbeatAt.Equal(snap.HeartbeatAt) {
		return "not_due"
	}
	if withDue {
		if plan, _ := s.reportPlan(pd); s.deps.Now().Before(plan.NextDueAt) {
			return "not_due"
		}
	}
	presence, _ := s.deps.Reconcile(clone(pd), append([]herdr.Agent(nil), s.agents...), s.health.OK, s.deps.Now())
	switch {
	case !presence.Identified || presence.Target != target || !identityMatches(pd.Leader, target, agent):
		return "not_identified"
	case presence.AgentStatus == "blocked":
		return "leader_blocked"
	case !acceptsInput(presence.AgentStatus):
		return "leader_busy"
	}
	return ""
}

func (s *Service) reportOne(ctx context.Context, project string) {
	s.mu.Lock()
	if len(s.reportAudit[project]) > 0 {
		// Fail closed: while an attempt's outcome is not durable, only retry the audit.
		s.mu.Unlock()
		s.flushReportAudit(ctx, project)
		return
	}
	pd, p, wait := s.reportReady(ctx, project)
	if wait != "" {
		s.waitReport(project, wait)
		s.mu.Unlock()
		return
	}
	snap, target := clone(pd.Leader), p.Target
	s.mu.Unlock()
	if wait = s.reportGate(ctx, project, snap, target, true); wait != "" {
		s.mu.Lock()
		s.waitReport(project, wait)
		s.mu.Unlock()
		return
	}
	// The intent is durable before anything is typed; if it cannot be written the
	// panel cannot guarantee one report per cycle, so it does not prompt at all.
	id := newAttemptID(s.deps.Now())
	intent := model.PanelLine{Type: model.PanelReport, ID: id, At: s.deps.Now(), Delivery: model.DeliveryQueued, Target: target}
	if err := s.appendPanel(ctx, project, intent); err != nil {
		s.mu.Lock()
		if ctx.Err() == nil {
			s.reportFailure(project, "audit_failed", err)
		}
		s.mu.Unlock()
		return
	}
	result := model.PanelLine{Type: model.PanelReport, ID: id, Delivery: model.DeliveryDelivered, Target: target}
	// The append may have waited seconds for the file lock: check everything again.
	wait = s.reportGate(ctx, project, snap, target, false)
	if wait == "" && ctx.Err() != nil {
		wait = "not_identified"
	}
	if wait != "" {
		// Nothing was typed: settle this intent as not sent so it does not use the cycle.
		result.Delivery, result.Error = model.DeliveryRetry, "not sent: "+wait
	} else {
		promptCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		err := s.deps.Herdr.Prompt(promptCtx, target, reportText(project))
		stop()
		var he *herdr.Error
		switch {
		case errors.As(err, &he) && he.Code == herdr.CodeAgentBlocked:
			result.Delivery, wait = model.DeliveryRetry, "leader_blocked"
		case errors.As(err, &he) && he.Code == herdr.CodeAgentNotFound:
			result.Delivery, wait = model.DeliveryRetry, "not_identified"
		case err != nil:
			// Timeouts and stalls may have typed the text: the outcome is unknown, so the
			// cycle counts as used.
			result.Delivery = model.DeliveryFailed
		}
		if err != nil {
			result.Error = s.deps.Redact(cleanTerminal(err.Error()))
		}
	}
	result.At = s.deps.Now()
	// Remember the outcome in memory before writing it (even if ctx is cancelled).
	s.mu.Lock()
	s.reportAudit[project] = append(s.reportAudit[project], result)
	if wait != "" {
		s.waitReport(project, wait)
	}
	s.mu.Unlock()
	s.flushReportAudit(ctx, project)
}

// flushReportAudit persists result lines kept in memory. It never prompts.
func (s *Service) flushReportAudit(ctx context.Context, project string) {
	s.mu.Lock()
	pending := append([]model.PanelLine(nil), s.reportAudit[project]...)
	persisted := map[string]bool{}
	if pd, err := s.loadLocked(ctx, project); err == nil {
		for _, l := range pd.Panel.Reports {
			persisted[l.ID+"/"+string(l.Delivery)] = l.ID != ""
		}
	}
	s.mu.Unlock()
	written := 0
	var err error
	for _, line := range pending {
		// An earlier append may have landed although it reported an error.
		if !persisted[line.ID+"/"+string(line.Delivery)] {
			if err = s.appendPanel(ctx, project, line); err != nil {
				break
			}
		}
		written++
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reportAudit[project] = s.reportAudit[project][written:]
	if len(s.reportAudit[project]) == 0 {
		delete(s.reportAudit, project)
	}
	if latest, loadErr := s.loadLocked(ctx, project); loadErr == nil {
		s.projects[project] = latest
	}
	if err != nil {
		if ctx.Err() == nil {
			s.reportFailure(project, "audit_pending", err)
		}
		return
	}
	if s.reportWait[project] == "audit_pending" || s.reportWait[project] == "audit_failed" {
		s.reportWait[project] = ""
	}
	if w := s.reportWait[project]; w == "" {
		delete(s.due, reportKey(project))
	}
	s.publishLocked()
}

// reportFailure records a storage problem as the waiting reason. Callers hold s.mu.
func (s *Service) reportFailure(project, reason string, err error) {
	s.reportError[project] = s.deps.Redact(cleanTerminal(err.Error()))
	s.waitReport(project, reason)
	s.publishLocked()
}

// appendPanel bounds a panel.jsonl append; it is always called without s.mu so a
// contended file lock can never stall the service.
func (s *Service) appendPanel(ctx context.Context, project string, line model.PanelLine) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := s.deps.Store.AppendPanel(ctx, project, line)
	if err == nil {
		s.mu.Lock()
		delete(s.reportError, project)
		s.mu.Unlock()
	}
	return err
}

// leaderObservation returns the panel.jsonl leader line to append when leader.json
// describes an identity the panel has not recorded yet (first sight, restart, rename
// or CLI switch).
func (s *Service) leaderObservation(pd *model.ProjectData) (model.PanelLine, bool) {
	if !protocolAvailable(pd) {
		return model.PanelLine{}, false
	}
	if n := len(pd.Panel.Leaders); n > 0 && model.SameLeader(pd.Panel.Leaders[n-1], pd.Leader) {
		return model.PanelLine{}, false
	}
	agent := pd.Leader.Leader
	line := model.PanelLine{Type: model.PanelLeader, At: s.deps.Now(), Leader: &agent}
	if !pd.Leader.StartedAt.IsZero() {
		started := pd.Leader.StartedAt
		line.StartedAt = &started
	}
	return line, true
}

// observeLeaders appends the pending leader observations without holding s.mu, then
// republishes the projects it touched.
func (s *Service) observeLeaders(ctx context.Context, lines map[string]model.PanelLine) {
	if len(lines) == 0 {
		return
	}
	failed := map[string]string{}
	for project, line := range lines {
		if err := s.appendPanel(ctx, project, line); err != nil {
			failed[project] = s.deps.Redact("panel.jsonl: " + err.Error())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for project := range lines {
		pd := s.projects[project]
		if pd == nil {
			continue
		}
		if msg, bad := failed[project]; bad {
			pd = clone(pd)
			pd.Warnings = appendWarning(pd.Warnings, msg)
		} else if latest, err := s.loadLocked(ctx, project); err == nil {
			pd = latest
		}
		s.projects[project] = pd
	}
	s.publishLocked()
}

// leaderChange returns the most recent recorded identity change, if any.
func leaderChange(pd *model.ProjectData) *model.LeaderChange {
	n := len(pd.Panel.Leaders)
	if n < 2 {
		return nil
	}
	from, to := pd.Panel.Leaders[n-2], pd.Panel.Leaders[n-1]
	return &model.LeaderChange{At: to.At, From: *from.Leader, To: *to.Leader, Switched: from.Leader.Agent != to.Leader.Agent}
}

// ReportScheduler describes the scheduler configuration for overview and healthz.
func (s *Service) ReportScheduler() model.ReportSchedulerInfo {
	o := s.opts.Reports
	return model.ReportSchedulerInfo{Enabled: o.Enabled, Projects: append([]string(nil), o.Projects...), GraceSec: int64(o.Grace / time.Second)}
}
