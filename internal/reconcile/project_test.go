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
		via    string
	}{
		{name: "online", ok: true, reason: "ok", target: "leader"},
		{name: "no leader", absent: true, ok: true, reason: "no_leader_json"},
		{name: "herdr unavailable", reason: "herdr_unavailable"},
		{name: "unavailable without leader", absent: true, reason: "herdr_unavailable"},
		{name: "empty list", ok: true, noList: true, reason: "agent_not_found"},
		{name: "default TTL boundary", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) { li.HeartbeatAt = now.Add(-25 * time.Minute) }},
		{name: "default TTL stale", ok: true, reason: "heartbeat_stale", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
			li.HeartbeatAt = now.Add(-25*time.Minute - time.Nanosecond)
		}},
		{name: "custom TTL boundary", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
			li.ReportIntervalMin = 2
			li.HeartbeatAt = now.Add(-9 * time.Minute)
		}},
		{name: "custom TTL stale", ok: true, reason: "heartbeat_stale", target: "leader", change: func(li *model.LeaderInfo, _ *herdr.Agent) {
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
		// Name lost after a session resume: same pane, cwd and session identify the same leader.
		{name: "name lost falls back to pane", ok: true, reason: "ok", target: "w1:pA", via: "pane", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Name = "" }},
		{name: "pane fallback requires session", ok: true, reason: "identity_mismatch", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Name = ""; a.SessionID = "" }},
		{name: "pane fallback rejects other session", ok: true, reason: "identity_mismatch", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Name = ""; a.SessionID = "other" }},
		{name: "pane fallback rejects other cwd", ok: true, reason: "identity_mismatch", change: func(_ *model.LeaderInfo, a *herdr.Agent) { a.Name = ""; a.Cwd = "/other" }},
		{name: "codex name lost falls back without session", ok: true, reason: "ok", target: "w1:pA", via: "pane", change: func(li *model.LeaderInfo, a *herdr.Agent) {
			li.Leader.Agent, li.Leader.SessionID = "codex", ""
			a.Agent, a.Name, a.SessionID = "codex", "", ""
		}},
		// leader.json still describes the old claude leader while codex now runs in that pane.
		{name: "switching kind on same pane", ok: true, reason: "agent_kind_mismatch", change: func(li *model.LeaderInfo, a *herdr.Agent) {
			li.Leader.Agent = "claude"
			a.Agent, a.Name, a.SessionID = "codex", "", ""
		}},
		{name: "kind mismatch by name", ok: true, reason: "agent_kind_mismatch", change: func(li *model.LeaderInfo, a *herdr.Agent) {
			li.Leader.Agent = "claude"
			a.Agent = "codex"
			a.SessionID = ""
		}},
		{name: "kind declared and matching", ok: true, reason: "ok", target: "leader", change: func(li *model.LeaderInfo, a *herdr.Agent) { li.Leader.Agent = "claude"; a.Agent = "claude" }},
		{name: "stale but identified", ok: true, reason: "heartbeat_stale", target: "w1:pA", via: "pane", change: func(li *model.LeaderInfo, a *herdr.Agent) {
			li.HeartbeatAt = now.Add(-time.Hour)
			a.Name = ""
		}},
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
			if got.Reason != tc.reason || got.Online != (tc.reason == "ok") || got.Target != tc.target || got.Identified != (tc.target != "") {
				t.Fatalf("presence = %+v", got)
			}
			if want := tc.via; got.Identified && want == "" {
				if want = "name"; tc.target == "w1:pA" {
					want = "pane"
				}
				if got.Via != want {
					t.Fatalf("via = %q, want %q", got.Via, want)
				}
			}
			if !tc.absent && got.Agent != li.Leader.Agent {
				t.Fatalf("declared agent = %q", got.Agent)
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

// TestLeaderSwitch walks a claude → codex switch: the old leader exits, codex starts in
// the same pane, and only after codex rewrites leader.json is it addressable again.
func TestLeaderSwitch(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	old := model.LeaderInfo{Cwd: "/work", HeartbeatAt: now, Leader: model.LeaderAgent{Agent: "claude", HerdrName: "leader-p", PaneID: "w1:pA", SessionID: "s1"}}
	claude := herdr.Agent{Name: "leader-p", Agent: "claude", PaneID: "w1:pA", Cwd: "/work", SessionID: "s1", AgentStatus: "idle"}
	codex := herdr.Agent{Agent: "codex", PaneID: "w1:pA", Cwd: "/work", AgentStatus: "idle"}
	worker := herdr.Agent{Name: "worker", Agent: "codex", PaneID: "w1:pB", Cwd: "/work", AgentStatus: "idle"}
	presence := func(li model.LeaderInfo, agents ...herdr.Agent) model.LeaderPresence {
		return Project(&model.ProjectData{Leader: &li}, agents, true, now).Leader
	}
	if p := presence(old, claude, worker); !p.Online || p.Target != "leader-p" {
		t.Fatalf("before = %+v", p)
	}
	if p := presence(old, worker); p.Identified || p.Reason != "agent_not_found" {
		t.Fatalf("old leader exited = %+v", p)
	}
	if p := presence(old, codex, worker); p.Identified || p.Reason != "agent_kind_mismatch" || p.ObservedAgent != "codex" || p.Agent != "claude" {
		t.Fatalf("codex started, leader.json not yet rewritten = %+v", p)
	}
	next := model.LeaderInfo{Cwd: "/work", HeartbeatAt: now, Leader: model.LeaderAgent{Agent: "codex", HerdrName: "leader-p", PaneID: "w1:pA"}}
	codex.Name = "leader-p"
	if p := presence(next, codex, worker); !p.Online || p.Target != "leader-p" || p.ObservedAgent != "codex" {
		t.Fatalf("after switch = %+v", p)
	}
	// A codex worker in the same directory is never mistaken for the leader.
	if p := presence(next, worker); p.Identified {
		t.Fatalf("worker taken as leader = %+v", p)
	}
	// The exited claude leader that kept its name cannot receive messages for codex.
	if p := presence(next, claude); p.Identified || p.Reason != "agent_kind_mismatch" {
		t.Fatalf("stale claude leader = %+v", p)
	}
	if !VerifyLeader(&next, "leader-p", codex) || VerifyLeader(&next, "leader-p", claude) || VerifyLeader(&next, "w1:pA", codex) || VerifyLeader(nil, "leader-p", codex) {
		t.Fatal("VerifyLeader disagrees with Project")
	}
	unnamed := codex
	unnamed.Name = ""
	if !VerifyLeader(&next, "w1:pA", unnamed) || VerifyLeader(&next, "leader-p", unnamed) {
		t.Fatal("pane fallback recheck")
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
