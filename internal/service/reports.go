package service

import (
	"context"
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

// reportPlan derives the schedule purely from leader.json and panel.jsonl, so a restart
// resumes the same cycle instead of replaying missed ones:
//
//   - a cycle starts at the later of heartbeat_at (the leader handled a report or an
//     answer) and the panel's last delivered/abandoned report;
//   - the next report is due one interval (+ grace) later; a report the leader has not
//     acknowledged by refreshing heartbeat_at is never stacked, and each further
//     unacknowledged report doubles the wait up to reportMaxBackoff intervals;
//   - a failed attempt within a cycle retries on the answer-delivery backoff.
func (s *Service) reportPlan(pd *model.ProjectData) (model.ReportSchedule, bool) {
	if pd == nil || pd.Leader == nil || !protocolAvailable(pd) || !s.reportsFor(pd.Name) {
		return model.ReportSchedule{}, false
	}
	li := pd.Leader
	interval := li.ReportIntervalMin
	if interval <= 0 {
		interval = 10
	}
	plan := model.ReportSchedule{IntervalMin: interval}
	base := li.HeartbeatAt
	var retry *model.PanelLine
	for i := range pd.Panel.Reports {
		l := pd.Panel.Reports[i]
		switch l.Delivery {
		case model.DeliveryDelivered:
			at := l.At
			plan.LastSentAt = &at
			if l.At.After(li.HeartbeatAt) {
				plan.Unacked++
			}
			fallthrough
		case model.DeliveryFailed:
			if l.At.After(base) {
				base = l.At
			}
			retry = nil
		case model.DeliveryRetry:
			retry = &pd.Panel.Reports[i]
		}
		plan.LastError = l.Error
	}
	mult := 1
	if plan.Unacked > 1 {
		mult = min(1<<(plan.Unacked-1), reportMaxBackoff)
	}
	due := base.Add(time.Duration(interval*mult)*time.Minute + s.opts.Reports.Grace)
	if retry != nil && retry.At.After(base) {
		due = retry.At.Add(backoff(retry.Attempt))
	}
	if earliest := s.started.Add(s.opts.Reports.StartupDelay); due.Before(earliest) {
		due = earliest
	}
	plan.NextDueAt = due
	return plan, true
}

// reportAttempt continues the attempt count of a cycle whose last attempt failed; a
// heartbeat refresh since then starts a new cycle.
func reportAttempt(pd *model.ProjectData) int {
	if n := len(pd.Panel.Reports); n > 0 {
		if last := pd.Panel.Reports[n-1]; last.Delivery == model.DeliveryRetry && last.At.After(pd.Leader.HeartbeatAt) {
			return last.Attempt
		}
	}
	return 0
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
		if plan, ok := s.reportPlan(pd); ok && !now.Before(plan.NextDueAt) && !now.Before(s.due[reportKey(name)]) {
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

func (s *Service) reportOne(ctx context.Context, project string) {
	s.mu.Lock()
	pd, p, wait := s.reportReady(ctx, project)
	if wait != "" {
		s.waitReport(project, wait)
		s.mu.Unlock()
		return
	}
	expected := clone(pd.Leader)
	s.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	agent, err := s.deps.Herdr.Get(callCtx, p.Target)
	cancel()
	s.mu.Lock()
	switch {
	case err != nil || !identityMatches(expected, p.Target, agent):
		wait = "not_identified"
	case agent.AgentStatus == "blocked":
		wait = "leader_blocked"
	case !acceptsInput(agent.AgentStatus):
		wait = "leader_busy"
	}
	if wait == "" {
		// Recheck after Get: the leader may have refreshed heartbeat_at (its own cron
		// fired) or been replaced while the call was in flight.
		var current *model.ProjectData
		var presence model.LeaderPresence
		current, presence, wait = s.reportReady(ctx, project)
		if wait == "" && (presence.Target != p.Target || !identityMatches(current.Leader, p.Target, agent)) {
			wait = "not_identified"
		}
		pd = current
	}
	if wait != "" {
		s.waitReport(project, wait)
		s.mu.Unlock()
		return
	}
	attempt := reportAttempt(pd) + 1
	s.mu.Unlock()
	promptCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	err = s.deps.Herdr.Prompt(promptCtx, p.Target, reportText(project))
	stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	var he *herdr.Error
	if errors.As(err, &he) && (he.Code == herdr.CodeAgentBlocked || he.Code == herdr.CodeAgentNotFound) {
		wait = "not_identified"
		if he.Code == herdr.CodeAgentBlocked {
			wait = "leader_blocked"
		}
		s.waitReport(project, wait)
		return
	}
	line := model.PanelLine{Type: model.PanelReport, At: s.deps.Now(), Delivery: model.DeliveryDelivered, Attempt: attempt, Target: p.Target}
	if err != nil {
		line.Delivery = model.DeliveryRetry
		if attempt%5 == 0 {
			line.Delivery = model.DeliveryFailed
		}
		line.Error = s.deps.Redact(cleanTerminal(err.Error()))
	}
	if appendErr := s.deps.Store.AppendPanel(ctx, project, line); appendErr != nil {
		// Without a durable record a retry could duplicate the report; wait a cycle step.
		s.waitReport(project, "backoff")
		return
	}
	if latest, loadErr := s.loadLocked(ctx, project); loadErr == nil {
		s.projects[project] = latest
	}
	delete(s.due, reportKey(project))
	s.reportWait[project] = ""
	if err != nil {
		s.reportWait[project] = "backoff"
	}
	s.publishLocked()
}

// observeLeader appends a panel.jsonl leader line when leader.json describes a leader
// identity the panel has not recorded yet (first sight, restart, rename or CLI switch).
// Callers hold s.mu.
func (s *Service) observeLeader(ctx context.Context, pd *model.ProjectData) *model.ProjectData {
	if pd.Leader == nil || !protocolAvailable(pd) {
		return pd
	}
	if n := len(pd.Panel.Leaders); n > 0 && model.SameLeader(pd.Panel.Leaders[n-1], pd.Leader) {
		return pd
	}
	agent := pd.Leader.Leader
	line := model.PanelLine{Type: model.PanelLeader, At: s.deps.Now(), Leader: &agent}
	if !pd.Leader.StartedAt.IsZero() {
		started := pd.Leader.StartedAt
		line.StartedAt = &started
	}
	if err := s.deps.Store.AppendPanel(ctx, pd.Name, line); err != nil {
		pd.Warnings = appendWarning(pd.Warnings, s.deps.Redact("panel.jsonl: "+err.Error()))
		return pd
	}
	if latest, err := s.loadLocked(ctx, pd.Name); err == nil {
		return latest
	}
	return pd
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
