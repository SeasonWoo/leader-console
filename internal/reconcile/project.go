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
	r.Leader.Agent = li.Leader.Agent
	if !herdrOK {
		return r
	}
	m := ResolveLeader(li, agents)
	if m.Found {
		r.Leader.ObservedAgent = m.Agent.Agent
	}
	if m.Reason != "" {
		r.Leader.Reason = m.Reason
		return r
	}
	r.Leader.Identified, r.Leader.Via = true, m.Via
	r.Leader.Target, r.Leader.AgentStatus = m.Target, m.Agent.AgentStatus
	interval := li.ReportIntervalMin
	if interval <= 0 {
		interval = 10
	}
	// Compare in minutes to avoid overflowing time.Duration on malformed intervals.
	if now.Sub(li.HeartbeatAt).Minutes() > 2*float64(interval)+5 {
		r.Leader.Reason = "heartbeat_stale"
		return r
	}
	r.Leader.Online, r.Leader.Reason = true, "ok"
	return r
}

// LeaderMatch 是 leader.json 与 herdr 现场对账的结果。
type LeaderMatch struct {
	Agent  herdr.Agent
	Target string // 可用于 herdr get/prompt 的 target
	Via    string // name / pane
	// Found 表示找到了候选 agent；Reason 为空才表示身份核验通过。
	Found  bool
	Reason string // "" / agent_not_found / identity_mismatch / agent_kind_mismatch
}

// ResolveLeader 是在线判断（Project）和投递前复核（VerifyLeader）共用的唯一身份规则：
//
//  1. 有 herdr_name 时按名字找；
//  2. 名字找不到（会话恢复后名字丢失）或从未命名时，按 pane_id 找，但该 pane 上的 agent
//     若已有别的名字，说明 pane 被其他 agent 占用，不认；
//  3. 候选必须 cwd 一致、agent kind 与 leader.json 声明一致（防止投给切换前的旧 CLI 或同 pane 的新 CLI）；
//  4. session：双方都有时必须一致；按 pane 兜底一个原本有名字的 leader 时更严格，
//     leader.json 有 session_id 就要求现场 session 完全相同（Codex 没有 session 时只能靠 pane + cwd + kind）。
func ResolveLeader(li *model.LeaderInfo, agents []herdr.Agent) LeaderMatch {
	if li == nil {
		return LeaderMatch{Reason: "agent_not_found"}
	}
	name, pane := li.Leader.HerdrName, li.Leader.PaneID
	if name != "" {
		for _, a := range agents {
			if a.Name == name {
				return checkLeader(li, a, name, "name")
			}
		}
	}
	if pane != "" {
		for _, a := range agents {
			if a.PaneID != pane {
				continue
			}
			if name != "" && a.Name != "" {
				break
			}
			return checkLeader(li, a, pane, "pane")
		}
	}
	return LeaderMatch{Reason: "agent_not_found"}
}

// VerifyLeader 用同一规则复核 `herdr agent get <target>` 返回的单个 agent。
func VerifyLeader(li *model.LeaderInfo, target string, a herdr.Agent) bool {
	m := ResolveLeader(li, []herdr.Agent{a})
	return target != "" && m.Reason == "" && m.Target == target
}

func checkLeader(li *model.LeaderInfo, a herdr.Agent, target, via string) LeaderMatch {
	m := LeaderMatch{Agent: a, Target: target, Via: via, Found: true}
	strictSession := via == "pane" && li.Leader.HerdrName != ""
	switch {
	case li.Cwd == "" || a.Cwd != li.Cwd:
		m.Reason = "identity_mismatch"
	case li.Leader.Agent != "" && a.Agent != li.Leader.Agent:
		m.Reason = "agent_kind_mismatch"
	case li.Leader.SessionID != "" && a.SessionID != li.Leader.SessionID && (a.SessionID != "" || strictSession):
		m.Reason = "identity_mismatch"
	}
	return m
}
