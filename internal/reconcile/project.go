package reconcile

import (
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
)

type Result struct {
	Leader model.LeaderPresence
	Tasks  map[string]model.TaskLive
}

// Project projects a snapshot without changing either input or writing the ledger.
// An unavailable herdr never implies that a worker was lost.
func Project(pd *model.ProjectData, agents []herdr.Agent, herdrOK bool, now time.Time) Result {
	r := Result{Leader: model.LeaderPresence{Reason: "no_leader_json"}, Tasks: make(map[string]model.TaskLive)}
	if !herdrOK {
		r.Leader.Reason = "herdr_unavailable"
	}
	if pd == nil {
		return r
	}
	byName, byPane := make(map[string]herdr.Agent), make(map[string]herdr.Agent)
	for _, a := range agents {
		if a.Name != "" {
			byName[a.Name] = a
		}
		if a.PaneID != "" {
			byPane[a.PaneID] = a
		}
	}
	for _, task := range pd.Tasks {
		live := model.TaskLive{}
		if herdrOK {
			a, found := byName[task.AgentName]
			if !found {
				a, found = byPane[task.PaneID]
			}
			live.AgentFound = found
			live.Lost = task.Status.Active() && !found
			if found {
				live.AgentStatus, live.Activity = a.AgentStatus, a.TerminalTitle
			}
		}
		r.Tasks[task.ID] = live
	}
	if pd.Leader == nil {
		return r
	}
	li := pd.Leader
	heartbeat := li.HeartbeatAt // Don't expose a pointer into the input snapshot.
	r.Leader.HeartbeatAt = &heartbeat
	if !herdrOK {
		return r
	}
	interval := li.ReportIntervalMin
	if interval <= 0 {
		interval = 10
	}
	// Compare in minutes to avoid overflowing time.Duration on malformed intervals.
	if now.Sub(li.HeartbeatAt).Minutes() > 2*float64(interval)+5 {
		r.Leader.Reason = "heartbeat_stale"
		return r
	}
	target := li.Leader.HerdrName
	a, found := byName[target]
	if target == "" {
		target = li.Leader.PaneID
		a, found = byPane[target]
	}
	if !found {
		r.Leader.Reason = "agent_not_found"
		return r
	}
	if a.Cwd != li.Cwd || (li.Leader.SessionID != "" && a.SessionID != "" && li.Leader.SessionID != a.SessionID) {
		r.Leader.Reason = "identity_mismatch"
		return r
	}
	r.Leader.Online, r.Leader.Reason = true, "ok"
	r.Leader.Target, r.Leader.AgentStatus = target, a.AgentStatus
	return r
}
