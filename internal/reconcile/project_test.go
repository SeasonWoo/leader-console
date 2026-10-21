package reconcile

import (
	"reflect"
	"testing"
	"time"

	"leader-console/internal/herdr"
	"leader-console/internal/model"
)

func TestLeaderPresence(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	base := model.LeaderInfo{Cwd: "/work", HeartbeatAt: now, Leader: model.LeaderAgent{HerdrName: "leader", PaneID: "w1:pA", SessionID: "session"}}
	agent := herdr.Agent{Name: "leader", PaneID: "w1:pA", Cwd: "/work", SessionID: "session", AgentStatus: "working"}
	for _, tc := range []struct {
		name   string
		change func(*model.LeaderInfo, *herdr.Agent)
		absent bool
		ok     bool
		noList bool
		reason string
		target string
	}{
		{name: "online", ok: true, reason: "ok", target: "leader"},
		{name: "no leader", absent: true, ok: true, reason: "no_leader_json"},
		{name: "herdr unavailable", reason: "herdr_unavailable"},
		{name: "unavailable without leader", absent: true, reason: "herdr_unavailable"},
		{name: "empty list", ok: true, noList: true, reason: "agent_not_found"},
		{name: "default TTL boundary", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) { li.HeartbeatAt = now.Add(-25 * time.Minute) }},
		{name: "default TTL stale", ok: true, reason: "heartbeat_stale", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
			li.HeartbeatAt = now.Add(-25*time.Minute - time.Nanosecond)
		}},
		{name: "custom TTL boundary", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
			li.ReportIntervalMin = 2
			li.HeartbeatAt = now.Add(-9 * time.Minute)
		}},
		{name: "custom TTL stale", ok: true, reason: "heartbeat_stale", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
			li.ReportIntervalMin = 2
			li.HeartbeatAt = now.Add(-9*time.Minute - time.Nanosecond)
		}},
		{name: "negative interval defaults", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) { li.ReportIntervalMin = -1 }},
		{name: "cwd mismatch pane reused", ok: true, reason: "identity_mismatch", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Cwd = "/other"; a.ForegroundCwd = "/work" }},
		{name: "session mismatch pane reused", ok: true, reason: "identity_mismatch", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.SessionID = "new-session" }},
		{name: "codex no session", ok: true, reason: "ok", target: "leader", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Agent = "codex"; a.SessionID = "" }},
		{name: "leader no session", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) { li.Leader.SessionID = "" }},
		{name: "unnamed leader", ok: true, reason: "ok", target: "w1:pA", change: func(li *model.LeaderInfo, a *herdr.Agent) { li.Leader.HerdrName = ""; a.Name = "" }},
		{name: "stale name cannot fall back to reused pane", ok: true, reason: "agent_not_found", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Name = "other" }},
		{name: "missing identifiers", ok: true, reason: "agent_not_found", change: func(li *model.LeaderInfo, a *herdr.Agent) {
			li.Leader = model.LeaderAgent{}
			a.Name = ""
			a.PaneID = ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			li, a := base, agent
			if tc.change != nil {
				tc.change(&li, &a)
			}
			pd := &model.ProjectData{Leader: &li}
			if tc.absent {
				pd.Leader = nil
			}
			agents := []herdr.Agent{a}
			if tc.noList || !tc.ok {
				agents = nil
			}
			got := Project(pd, agents, tc.ok, now).Leader
			if got.Reason != tc.reason || got.Online != (tc.reason == "ok") || got.Target != tc.target {
				t.Fatalf("presence = %+v", got)
			}
			if pd.Leader != nil {
				if got.HeartbeatAt == nil || !got.HeartbeatAt.Equal(li.HeartbeatAt) {
					t.Fatal("heartbeat lost")
				}
				*got.HeartbeatAt = time.Time{}
				if li.HeartbeatAt.IsZero() {
					t.Fatal("output aliases input")
				}
			}
		})
	}
	if got := Project(nil, nil, true, now); got.Leader.Reason != "no_leader_json" || got.Tasks == nil {
		t.Fatalf("nil project = %+v", got)
	}
}

func TestTaskProjection(t *testing.T) {
	statuses := []model.TaskStatus{model.TaskWorking, model.TaskBlocked, model.TaskNeedsUser, model.TaskReviewing, model.TaskQueued, model.TaskReadyToMerge, model.TaskDone, model.TaskFailed, model.TaskLost}
	pd := &model.ProjectData{}
	for _, status := range statuses {
		pd.Tasks = append(pd.Tasks, model.Task{ID: string(status), Status: status})
	}
	pd.Tasks = append(pd.Tasks,
		model.Task{ID: "name-priority", AgentName: "worker", PaneID: "w1:pB", Status: model.TaskWorking},
		model.Task{ID: "pane-fallback", AgentName: "old-name", PaneID: "w1:pB", Status: model.TaskReviewing},
	)
	agents := []herdr.Agent{{Name: "worker", PaneID: "w1:pA", AgentStatus: "working", TerminalTitle: "tests"}, {PaneID: "w1:pB", AgentStatus: "done", TerminalTitle: "finished"}}
	before := append([]model.Task(nil), pd.Tasks...)
	result := Project(pd, agents, true, time.Now())
	for _, status := range statuses {
		if live := result.Tasks[string(status)]; live.Lost != status.Active() || live.AgentFound {
			t.Fatalf("%s = %+v", status, live)
		}
	}
	if got := result.Tasks["name-priority"]; !got.AgentFound || got.AgentStatus != "working" || got.Activity != "tests" || got.Lost {
		t.Fatalf("name priority = %+v", got)
	}
	if got := result.Tasks["pane-fallback"]; !got.AgentFound || got.AgentStatus != "done" || got.Activity != "finished" || got.Lost {
		t.Fatalf("pane fallback = %+v", got)
	}
	// Even a stale nonnil list must be ignored when the refresh failed.
	for _, live := range Project(pd, agents, false, time.Now()).Tasks {
		if live != (model.TaskLive{}) {
			t.Fatalf("unavailable = %+v", live)
		}
	}
	if !reflect.DeepEqual(before, pd.Tasks) {
		t.Fatal("input tasks changed")
	}
}
