# leader 切换 CLI 操作手册（草稿）

> 场景：Claude 额度用完，需要把某个项目的 leader 从 Claude Code 临时换成 Codex，额度恢复后再切回。
> 状态：**草稿**。leader 提示词修改（lc-1 §11 A1–A7）已批准（d-20261022-025）并写入 `~/.claude/agents/leader.md`；
> 第 0.1 步的 Codex profile **尚未创建**（权限方案待用户选择），切换到 Codex 前需要先完成。
> 面板侧能力（定时汇报、按 kind 核验、切换识别）已合入 main，7788 已上线（`-report-grace 0s`）。
> 真实 CLI 行为（`herdr agent start` 复用名字、Codex 从 idle 被 prompt 开回合、退出命令、所选权限下 herdr socket 可达）
> 尚未在隔离项目中实测，第一次切换请在场观察。

下文用 `P` 表示项目名（`~/.leader/<P>/` 的目录名），`D=~/.leader/$P`。

## 原理与边界

面板只认 `leader.json` 描述的那个 agent：herdr 名字 + cwd + agent kind（+ 双方都有时的 session）都对上才投递；
名字丢失时，只有 leader.json 带 session_id（Claude）才按 pane + 相同 session 兜底，Codex 没有 session 就不兜底。
所以切换的关键是：**旧 leader 先退出 → 新 leader 在同一目录启动并取回原名字 → 新 leader 整体改写 leader.json → 新 leader 做接管核对**。

有条件的保证：
- 旧 leader **已退出**时，中间这段时间面板显示 leader 离线，决策回传和定期汇报排队，不会投给别的 agent；新 leader 写好 leader.json 后自动恢复，
  leader 状态里出现 `change.switched=true`（claude→codex）。
- 旧 leader **没退出**、还挂着名字时，在 leader.json 被改写之前它仍是合法目标，回答可能被投给它（例如额度耗尽、无法处理的 Claude）。
  面板无法区分“额度耗尽”和“正常空闲”，只能靠第 1.2 步先退出。
- “已投递”（delivered）只表示文字送进了终端，不表示 leader 已处理；已确认（ack）的决策也可能还没执行完。
  这些都要靠第 1.5 步的接管核对补上，面板不会重投 delivered/acked 的回答。

## 0. 一次性准备（需要用户审批，本手册不创建任何文件）

### 0.1 Codex leader profile

`~/.codex/leader.config.toml`，通过 `codex -p leader` 叠加在基础配置 `~/.codex/config.toml` 上，worker 不受影响。
需要 Codex CLI ≥ 0.134.0（从该版本起 profile 是独立的 `<name>.config.toml`，不是旧式 `[profiles.leader]`；本机 0.162.1）。
权限二选一，由用户决定：

方案 A（最省事，没有任何审批兜底，继承基础配置的 `danger-full-access` + `never`）：

```toml
# ~/.codex/leader.config.toml（示例，未创建）
model = "gpt-6.1-sol"
developer_instructions = """
<~/.claude/agents/leader.md 中 frontmatter 之后、“# 适配层：Claude”之前的核心协议全文>
<下方“附录：Codex 适配层”全文>
"""
```

方案 B（沙箱 + 自动复核；herdr socket 在沙箱内能否访问**未实测**，选它要先在隔离项目验证）：

```toml
# ~/.codex/leader.config.toml（示例，未创建）
model = "gpt-6.1-sol"
sandbox_mode = "workspace-write"
approval_policy = "on-request"
approvals_reviewer = "auto_review"
developer_instructions = """
<同方案 A>
"""
```

方案 B 启动时还要加 `--add-dir ~/.leader/<P>`，让协议目录可写。

A6 已落地：`~/.claude/agents/leader.md` 分为核心协议和末尾的“# 适配层：Claude”，核心协议是唯一来源。
Codex profile 只取核心协议部分（不要带上“适配层：Claude”），再接本手册附录的 Codex 适配层。
`developer_instructions` 只能内嵌文本，所以核心协议仍需复制进 profile；leader.md 的核心协议有改动时，要重新生成 profile。
不要用 `model_instructions_file`（它替换 Codex 内置基础指令）；不要把 leader 角色写进全局 `~/.codex/AGENTS.md`（会污染所有 worker）。

### 0.2 leader 提示词的 agent 无关化

已完成：[leader-protocol.md §11](leader-protocol.md) 的 A1–A7 已批准（d-20261022-025）并写入 `~/.claude/agents/leader.md`——
自我识别改用 `$HERDR_PANE_ID`、leader.json 写真实 kind 并整体替换、汇报时核对身份并恢复名字、启动时做接管核对、定时来源先看面板。

### 0.3 面板

用 main 构建的面板运行在 7788（已上线），并确认**本项目**确实会被定时投递（Codex 没有 cron，面板不投就没有任何定时唤醒）：

```bash
curl -s http://127.0.0.1:7788/healthz | jq .report_scheduler
# 需要 enabled=true，且 projects 不存在（=全部项目）或包含 <P>
curl -s http://127.0.0.1:7788/api/v1/overview | jq --arg p "$P" '.projects[] | select(.name==$p) | .leader.report'
# 必须非 null（null 表示本项目不在调度范围，或不是 lc-1 项目）
test -w "$D" && echo writable   # 只说明目录权限，不代表已有的 panel.jsonl / 锁文件可写、磁盘有空间
```

面板运行起来后，以 `leader.report.waiting` / `leader.report.last_error` 为准：出现 `audit_failed` / `audit_pending` 说明 panel.jsonl 写不进去，
面板此时不会投递（fail closed），要先处理权限或磁盘。

## 1. Claude → Codex

1. **记下现状**

   ```bash
   jq '{cwd, leader, heartbeat_at}' "$D/leader.json"
   ```

   记住 `cwd`、`leader.herdr_name`（如 `leader-trae-tob-dev`）、`leader.pane_id`（如 `w1:pA`）。

2. **退出旧 Claude leader**：在 leader 所在 pane 输入 `/exit`（卡在额度提示上也一样退出），直到 pane 回到 shell 提示符。
   - 为什么必须退出：旧 Claude 不退出就还占着 herdr 名字，新 leader 改不了名；在 leader.json 被改写前它也仍是合法投递目标（见“原理与边界”）。
   - 此时面板：leader 离线（`agent_not_found`），回答显示排队，定期汇报暂缓。**这是预期的。**

3. **在同一个 pane、同一个目录启动 Codex，并直接沿用原来的名字**（在任意其他 pane 执行）：

   ```bash
   herdr agent start <herdr_name> --kind codex --pane <pane_id> -- -p leader
   ```

   - pane 必须在 shell 提示符下，且当前目录就是 `leader.json` 的 `cwd`（面板要比对 cwd）。不确定时先在该 pane `cd <cwd>`。
   - 若 0.1 选了方案 B，在 `-p leader` 后追加 `--add-dir ~/.leader/<P>`。
   - 不要启动第二个 leader：同一项目同时只能有一个 leader 写 state.json。

4. **让 Codex 按启动流程接管**（Codex 需要一条用户消息才开始第一个回合）：

   ```bash
   herdr agent prompt <herdr_name> "【leader 接管】project=<P> 上一任 leader 是 claude。按会话启动流程执行（含接管核对），写 leader.json（agent=codex），完成后汇报一行。"
   ```

   新 leader 应当完成：确认名字（`herdr agent rename "$HERDR_PANE_ID" <herdr_name>`，已是该名字则跳过）→ 以 state.json 为准对账 →
   **整体改写** `leader.json` 的 leader 对象（`agent="codex"`、herdr_name、pane_id，**不带 session_id**），started_at/heartbeat_at=现在、report_interval_min=10 →
   追加事件 `leader 接管：claude→codex` → 第 5 步的接管核对。

5. **接管核对（决策恢复）**——宣布接管完成前必须做完。新 leader 按 decision_id 折叠 decisions.jsonl，结合 answers.jsonl 与 events.jsonl，扫描三类：

   | 类别 | 判定 | 处理 |
   |---|---|---|
   | ① 有回答未确认 | 决策最新状态仍为 `open`，answers.jsonl 有该决策的 `type=answer`（不论 delivery 是 queued 还是 delivered） | 按“决策回传”第 1–5 步正常处理（原拾取规则） |
   | ② 已确认未开始 | 最新状态 `answered`（via=panel），但 events 里没有该 answer_id 的 `decision_executing`；包括“已写 answered、还没写 decision_ack”的窗口（此时补写 decision_ack） | 先按 decision_id / answer_id 核对现场：相关 worker 状态和报告、git 分支/MR、外部系统，确认动作确实没做过，再开始执行并写 `decision_executing` |
   | ③ 执行中未完成 | 有 `decision_executing`，没有 `decision_done` | 核对现场判断做到哪一步：已完成的补写 `decision_done`；能确定剩余步骤且可逆的续办；涉及提交、推送、发布、对外消息等**不可逆动作且无法确认是否已做**的，新建一条决策请用户决定，不要直接重做 |

   查找命令示意（新 leader 执行）：

   ```bash
   jq -s 'group_by(.id) | map(last) | map(select(.status=="open" or .status=="answered")) | map({id, status, answer})' "$D/decisions.jsonl"
   jq -c 'select(.type|startswith("decision_")) | {type, decision_id, answer_id, ts}' "$D/events.jsonl"
   ```

   以上只是查找示例；核对必须覆盖 events.jsonl 全量（较早的 executing/done 同样要按 decision_id/answer_id 匹配），不能只看最近几十行。

6. **验证面板已识别**

   ```bash
   curl -s http://127.0.0.1:7788/api/v1/overview |
     jq --arg p "$P" '.projects[] | select(.name==$p) | .leader | {online, reason, agent, observed_agent, via, target, change, report}'
   ```

   期望：`online=true`、`reason="ok"`、`agent="codex"`、`observed_agent="codex"`、`via="name"`、`change.switched=true`、
   `change.from.agent="claude"`、`change.to.agent="codex"`、`report` 非 null 且 `next_due_at` 有值。
   排队中的回答一般在 15 秒左右投出（多个项目同时有积压、herdr 调用很慢时会更久）：`tail -n 3 "$D/answers.jsonl"` 应出现
   `"delivery":"delivered","target":"<herdr_name>"`。面板自己的记录：`tail -n 4 "$D/panel.jsonl"`。

   常见异常：
   - `reason=agent_kind_mismatch`：现场 agent 是 codex，但 leader.json 还写着 claude → 新 leader 还没完成第 4 步。
   - `reason=agent_not_found`：herdr 里没有叫 `herdr_name` 的 agent。Codex 没有 session，名字丢了面板不会按 pane 兜底，
     **也就送不进定期汇报**，所以不能指望 leader 在“下一次汇报”里自己改回名字（A4 对此失效）。需要用户恢复：
     在任意 pane 执行 `herdr agent rename <pane_id> <herdr_name>`，或直接在 leader 的 pane 里对它说“按 leader.json 恢复 herdr 名字”。
     恢复后约 5 秒内面板重新识别，排队的回答随之投出。
   - `reason=identity_mismatch`：cwd 不一致（Codex 不在项目目录启动）。按名字识别时，只有双方都带 session 且不同才会因 session 判不符；
     现场 Codex 没有 session，所以 leader.json 残留旧 session_id 不一定报错，但它会让名字丢失后的兜底和后续诊断出错——仍要求新 leader 整体替换 leader 对象。
   - `report.waiting=audit_failed` / `audit_pending`：面板写不进 `$D/panel.jsonl`，汇报暂停（不会重复输入），检查目录权限和磁盘。

7. **切换后的差异**（Codex 没有会话内定时和后台唤醒）：
   - 定期汇报完全由面板投递。默认 `-report-grace 5m`，即约每 15 分钟一次（心跳 TTL 25 分钟，不会误判离线）；
     想恢复 10 分钟节奏，重启面板时加 `-report-grace 0s`。
   - worker 状态变化不会自动叫醒 Codex leader：它在每次定期汇报时检查所有活跃任务（见附录），最长延迟一个汇报周期。急的时候直接在对话里让它“检查 <task>”。
   - 面板不可用时 Codex leader 只在你说话时工作；它启动时的接管核对仍然有效。

不需要改的：`state.json`、`events.jsonl`、`decisions.jsonl`、`answers.jsonl`、`panel.jsonl`、所有 worker、`~/.codex/config.toml` 基础配置、
`~/.claude/` 下任何文件。**不要手改 leader.json**——由新 leader 写；手改容易和 leader 的整文件替换互相覆盖。

## 2. Codex → Claude（额度恢复后切回）

1. 等 Codex leader 当前回合结束（看板上 leader 状态为空闲），在它的 pane 里退出（`/quit`，未实测；不行就连按两次 Ctrl+C），回到 shell 提示符。
2. 同一 pane 启动 Claude leader：

   ```bash
   herdr agent start <herdr_name> --kind claude --pane <pane_id> -- --agent leader --permission-mode auto
   ```

   - 建议开新会话，不加 `--continue`：`--continue` 会恢复切换前那个 Claude 会话，它的记忆已经过时。
   - `--permission-mode auto` 要显式写上：auto 模式不会随重启保留。
3. 发接管提示（同 1.4，把 claude/codex 对调），并做 1.5 的接管核对。Claude leader 写 `leader.agent="claude"` 和 `session_id`，
   按 leader.md 会话启动第 3 步处理定时来源：本项目在面板调度范围内就不建 CronCreate（已有的删掉），否则建 cron 兜底。
4. 验证（同 1.6）：期望 `agent="claude"`、`change.switched=true`、`change.from.agent="codex"`、`change.to.agent="claude"`。

同 CLI 换会话（Codex→Codex 或 Claude 新开会话）也走同样的步骤：Codex 前后都没有 session、pane 也可能不变，
leader.json 里看不出“换了人”，所以接管核对必须由显式的【leader 接管】提示触发（协议 A5）。

## 3. 回退与应急

- 新 leader 起不来：回答和汇报一直排队，不会投给别的 agent；在原 pane 重新启动原来的 leader，同样做接管核对。
- 想彻底关掉面板的定时汇报：重启面板加 `-leader-reports off`（leader 自己的 cron 不受影响；面板仍会记录 leader 身份变化）。
- 只对部分项目开：`-leader-reports projA,projB`（列表为空或项目名不合法会启动失败，不会退化成全部项目；拼错但合法的项目名不会报错，只是匹配不到——用 0.3 的 `report` 非 null 检查确认）。

## 附录：Codex 适配层（建议文本，放进 profile 的 developer_instructions）

```markdown
# 适配层：Codex

以下是核心协议中“由适配层提供”的能力在 Codex 上的实现，替代 leader.md 末尾的“# 适配层：Claude”一节；核心协议本身不改。

- 启动与恢复：由用户用 `herdr agent start <herdr_name> --kind codex --pane <pane_id> -- -p leader` 启动，并发一条【leader 接管】提示。
  启动和恢复后都从核心协议“会话启动”第 0 步开始执行。
- 自己的 CLI 取值：会话启动第 4 步 `herdr agent get "$HERDR_PANE_ID"` 返回的 agent 应为 codex，不符就按第 4 步停止注册。
- leader.json：整体替换 leader 对象，leader.agent 写 "codex"；herdr 拿不到 Codex 的 session，**不写 session_id**。
  名字一旦丢失面板就找不到你（没有 session 不做 pane 兜底），也送不进定期汇报；每次定期汇报都核对名字，
  用户在对话里提到“面板显示 leader 离线”时第一件事也是核对并恢复名字。
- 定时兜底：Codex 没有会话内定时能力，不要尝试创建定时任务。【定期汇报】由 leader-console 面板投递。会话启动第 3 步检查
  `curl -fsS http://127.0.0.1:7788/healthz` 的 report_scheduler：enabled 为 true，且 projects 不存在、为空或包含 <project>；
  再检查 `curl -fsS http://127.0.0.1:7788/api/v1/overview` 中本项目的 leader.report 非 null、report.waiting 不是 audit_failed / audit_pending。
  任何一项不满足（面板不可达、未开启、本项目不在范围内、panel.jsonl 写不进去）就在对话里提醒用户一次：本项目将没有定时汇报。
- 事件监听：不要挂后台 `herdr agent wait`（回合结束后后台命令完成不会唤醒你）。派发时用
  `herdr agent prompt ... --wait --until working --timeout 30000` 确认开工即可。每次定期汇报对账时检查 state.json 中所有活跃任务
  （working / reviewing / blocked / needs_user）：
  - state 为 working / reviewing、herdr 状态为 idle / done：按“事件监听”的 idle/done 规则验收；
  - state 为 blocked、herdr 状态仍是 blocked：按 blocked 规则处理；已为它建过 open 决策的不要重复建；
  - state 为 blocked、herdr 状态已是 idle / done（用户直接在 worker 里解了阻塞，worker 已做完）：读 worker 输出和报告，
    按 idle/done 规则验收，并追加 unblocked 事件；不要再为它建阻塞决策；
  - state 为 blocked、herdr 状态是 working：已被解阻在跑，改回 working 并追加 unblocked 事件；
  - state 为 needs_user：对应决策仍 open 且 answers.jsonl 有回答 → 按决策回传处理；决策已 answered 但没有 decision_executing，
    或 executing 未 done → 按接管核对的类别 ②/③ 核对现场后续办（不能因为“决策回传幂等检查”认为已处理就跳过）；
    没有回答 → 保持等待，不重复通知；
  - 接管时已经处于 blocked / needs_user 的任务同样按上面规则核对一次。
- 通知：没有 PushNotification。“通知用户”第 3 步改为 `herdr notification show "<一行摘要>" --sound request`，其余步骤不变。
- 接管：按核心协议会话启动第 0 步执行（三类决策的接管核对）。Codex 前后都没有 session，同一 pane 换会话时 leader.json 看不出换了人，
  收到【leader 接管】提示时一律按接管处理。
```
