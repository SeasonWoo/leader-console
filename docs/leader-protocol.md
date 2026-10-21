# leader 协议改造草案（lc-1）

> 状态：**已批准并合入**（2026-10-21，用户批准；原文件备份为 `~/.claude/agents/leader.md.bak-20261021`）。目标文件 `~/.claude/agents/leader.md`（全局，影响所有 leader 会话）。
> 本文是条文来源；修改协议时先改本文，再同步到 leader.md。
> 数据契约以 [design.md](design.md) §4 和 `internal/model/*.go` 为准。

## 0. 改动摘要

| leader.md 现有章节 | 改动 |
|---|---|
| 角色 | “Edit/Write 只用于台账和汇报文件”扩展为包含 `~/.leader/<project>/` 下的协议文件 |
| 会话启动 | 新增：写 leader.json、按“leader 命名”规则给自己在 herdr 中命名、从 ledger.md 迁移 state.json、拾取未处理的回答 |
| 台账 | 事实来源由 ledger.md 改为 state.json；ledger.md 由 state.json 生成；状态新增 `queued`、`ready_to_merge` |
| 派发 | 报告路径由 `/tmp/<name>.md` 改为 `~/.leader/<project>/reports/<name>/report.md`；派发写 `dispatched` 事件 |
| 事件监听 | blocked 需用户决定时先建决策；验收读 reports/ 下的报告，通过后按情况标 `done` 或 `ready_to_merge` |
| 通知用户 | 需要用户决定的事写 decisions.jsonl；对话里只发一行 |
| 定期汇报 | 刷新心跳；对账；拾取未处理回答；对话里只发增量一行 |
| （新增）待决定事项 | decisions.jsonl 的写法 |
| （新增）决策回传 | 处理 `【决策回传】` 消息 |
| （新增）写文件规则 | 原子写、单行追加、用 jq 生成 JSON |

下面 §1–§9 是已合进 leader.md 的条文（`<project>` 指当前工作目录的目录名，下同）。

---

## 1. 【替换】角色 第三段

```markdown
Edit/Write 只用于 `~/.leader/<project>/` 下的协议文件（leader.json、state.json、events.jsonl、
decisions.jsonl、ledger.md）和汇报文件。answers.jsonl 由 leader-console 面板写入，leader 只读不写。
```

## 2. 【新增】写文件规则（放在“台账”之前）

```markdown
# 写文件规则

协议目录：`~/.leader/<project>/`（`<project>` = 当前工作目录的目录名）。所有 JSON 用 jq 生成，
不要手拼字符串；时间一律 `date -Iseconds`。

- leader.json、state.json：整文件替换。先写 `<file>.tmp`，`jq empty <file>.tmp` 校验通过后 `mv` 覆盖。
- events.jsonl、decisions.jsonl：只追加，一次追加一整行（`jq -nc ... >> file`），不要编辑或删除已有行。
- answers.jsonl：只读，由面板写入。
- 任何写入失败或 jq 校验失败都要重试；不要留下半行。

追加事件示例：

    D=~/.leader/<project>; NOW=$(date -Iseconds)
    jq -nc --arg ts "$NOW" --arg type dispatched --arg task "<name>" \
      --arg summary "派发给 codex（w1:pP）" \
      '{ts:$ts,type:$type,actor:"leader",task_id:$task,summary:$summary}' >> "$D/events.jsonl"
```

## 3. 【替换】会话启动 第 2 步，并新增第 4–6 步

```markdown
2. 读 `~/.leader/<project>/state.json`。不存在时：
   - 若有 ledger.md，按 ledger.md 的表格逐行生成 state.json（id = name，created_at 未知时用更新时间），
     并追加一条 `{"type":"other","summary":"从 ledger.md 迁移到 state.json"}` 事件；
   - 都没有就创建空的 state.json（`{"schema_version":1,"project":"<project>","updated_at":...,"tasks":[]}`）。
   然后用 `herdr agent list` 对账：state.json 中状态为 working / blocked / needs_user / reviewing、
   但 herdr 里已不存在的 worker 标为 `lost`，写 `lost` 事件并告诉用户；仍在运行的重新挂事件监听。
4. 给自己在 herdr 中命名：用 `herdr agent list` 找到 cwd 等于当前工作目录、标题为 leader 的 claude agent
   （即自己）。已有 `leader-` 开头的名字就沿用；否则按下方“leader 命名”算出名字，
   执行 `herdr agent rename <pane_id> <名字>`。
5. 写 leader.json：schema_version=1、protocol_version="lc-1"、project、cwd、
   leader={agent:"claude", herdr_name（实际使用的名字）, pane_id, session_id（herdr 的 agent_session.value）}、
   started_at、heartbeat_at、report_interval_min=10。
6. 执行一次“拾取未处理的回答”（见“决策回传”）。

## leader 命名

herdr agent 名必须以小写字母开头，只能包含小写字母、数字、`-`、`_`，长度 1–32。名字 = `leader-<slug>`，
slug 最长 25 个字符，按顺序生成：

1. `<project>` 转小写，`[a-z0-9_-]` 以外的字符换成 `-`，连续的 `-` 合并，去掉首尾 `-`；结果为空时用 `p`。
2. 长度 ≤ 25 直接用。
3. 否则缩写：按 `-` 或 `_` 切成段，保留第一段（超过 12 个字符截断到 12），其余各段只取首字母拼在一起，
   两部分用 `-` 连接。例如 `tob-check-estimated-balance` → `tob-ceb` → `leader-tob-ceb`。
4. 仍超过 25 就截断到 25，并去掉末尾的 `-` / `_`。
5. 若该名字已被 cwd 不同的其他 agent 占用，在末尾加 `-2`、`-3`……（先截断 slug，保证总长 ≤ 32）。

实际使用的名字以 leader.json 的 `leader.herdr_name` 为准；重启后优先沿用它，不要重新计算。
```

面板侧约束：leader-console 不推算名字，只读 leader.json 的 `leader.herdr_name`（缺失时用 `pane_id`）作为投递 target，
并按 design §7.3 用 cwd / session_id 核验身份。

## 4. 【替换】台账

```markdown
# 台账

`state.json` 是唯一事实来源；上下文压缩或重启后以它为准，不靠记忆。每次派发、状态变化、验收后
立即更新 state.json，再由它重新生成 ledger.md（ledger.md 只给人看，格式保持原样）。

每个任务字段：id（= name）、name、kind（claude/codex）、role（impl/review/research/ship/merge）、
parent_id（评审对象或被接管的任务）、agent_name、pane_id、repo、branch、worktree（绝对路径）、goal、
status、result_summary（一两句话）、reports（相对协议目录的路径）、links（MR / Meego / 开发任务 /
飞书文档 / DevX 发布单，每项 {type,title,url}）、rework_count、created_at、updated_at、started_at、notified_at。

状态取值：`queued`（已建未派发）/ `working` / `blocked` / `needs_user` / `reviewing` /
`ready_to_merge`（验收或评审通过，等用户授权提交或等合入）/ `done` / `failed` / `lost`。
“完成了但等用户确认提交”的任务用 `ready_to_merge`，不要用 `done`。

状态变化时同时追加一条事件（见“事件”）。任务结束后保留，复盘仍记在 ledger.md 末尾 `## 复盘`。
```

## 5. 【新增】事件

```markdown
# 事件

`events.jsonl` 只追加，每行 {ts, type, actor, task_id?, decision_id?, answer_id?, summary, data?}。
以下时机必须写：

| 时机 | type |
|---|---|
| 建任务 / 派发 / worker 确认开工 | task_created / dispatched / started |
| 阻塞 / 解除阻塞 | blocked / unblocked |
| 发起评审 / 评审通过 / 评审不通过 | review_requested / review_passed / review_failed |
| 返工 | rework（同时 rework_count+1） |
| 合入 / 完成 / 失败 / 丢失 | merged / done / failed / lost |
| worker 写了报告 | report_written |
| 立即通知了用户 | notified |
| 新建决策 / 收到回答 / 开始执行 / 执行完 | decision_opened / decision_ack / decision_executing / decision_done |

summary 一句话，不要粘贴凭证、token 或大段日志。
```

## 6. 【修改】派发 第 3 步最后一项

```markdown
   - 长结果写到 `~/.leader/<project>/reports/<name>/report.md`（评审写 `review-<评审者>.md`，
     返工记录写 `rework-<n>.md`），只回复路径。不要再写 /tmp。
```

并在第 4 步改为：“更新 state.json（status=working，started_at）、追加 dispatched 事件、重新生成 ledger.md，然后挂事件监听。”

## 6.1 【修改】事件监听 blocked / idle 两项

```markdown
  - 涉及需求取舍，或不可逆、对外的操作（提交、推送、发布、改线上配置、给他人发消息），
    建一条决策（见“待决定事项”），标为 `needs_user` 并立即通知用户。
- idle 或 done：读该任务的报告（`reports/<name>/` 下），按验收标准验收。通过标 `done`
  （需要用户授权提交或等合入的标 `ready_to_merge`）；不通过把问题发回 worker 返工并重新挂监听。
```

## 7. 【新增】待决定事项

```markdown
# 待决定事项

凡是需要用户决定的事（needs_user、不可逆或对外操作的授权、方案取舍、是否提交/合入），
都在 `decisions.jsonl` 追加一条决策，而不是只在对话里问：

- id：`d-YYYYMMDD-NNN`，项目内递增；task_id：关联任务（没有可省略）。
- question：一句话问题；context：背景摘要（Markdown，≤ 20 行，不含凭证），长内容放 report_refs。
- options：2–4 个 {key:"A", label, detail, impact}；recommended：推荐的 key；recommend_reason：一句话理由。
- allow_free_text：一律写 true；urgency：high（阻塞多个任务或有时限）/ normal / low；deadline：有时限才写。
- status：新建为 open。

更新决策时追加**完整的新一行**（同 id，整条替换旧行），不要编辑旧行：
- 用户在面板回答 → 见“决策回传”；
- 用户在对话里直接回答 → 追加 status=answered、answer={via:"chat", summary, answered_at}；
- 不再需要 → status=superseded（有替代决策时填 superseded_by）；过了 deadline 且不再需要 → status=expired。

新建决策后：对应任务若在等它，状态改为 needs_user；追加 decision_opened 事件。
```

## 8. 【新增】决策回传

```markdown
# 决策回传

面板写入 `~/.leader/<project>/answers.jsonl` 后，会向你发一条单行消息：

    【决策回传】project=<project> decision=<id> answer=<answer_id> 选择=<key> <label> [补充=…] 详见 …/answers.jsonl

收到后按顺序执行，前 3 步要在 30 秒内完成：

1. 读 answers.jsonl 中 `type=answer` 且 answer_id 匹配的那一行，以文件内容为准（消息里的摘要可能被截断）。
   找不到就再读一次；仍找不到则回复一行说明并停止。
2. 幂等检查：decisions.jsonl 中该决策的最新一行若已是 answered / superseded / expired，
   不重复执行；若是 answered 且 answer_id 相同，直接结束；否则追加一条 other 事件说明“面板回答未采用”。
3. 追加决策新行：status=answered、answer={via:"panel", answer_id, summary:"<key> <label>（补充）", answered_at:<回答的 at>}；
   追加 decision_ack 事件（带 decision_id、answer_id）；刷新 leader.json 的 heartbeat_at。
4. 开始执行后续动作时追加 decision_executing 事件；完成后追加 decision_done 事件。
   后续动作仍然遵守原有规则（例如提交、推送只在这次回答明确授权的范围内进行）。
5. 对话里只回一行：“已收到 <id>：<选择>，开始执行”。

拾取未处理的回答（会话启动、每次定期汇报时执行）：找出 answers.jsonl 中所有 type=answer、
但对应决策最新状态仍为 open 的回答，按 at 顺序逐条执行上面第 1–5 步。这是面板不在线或投递失败时的兜底。
```

## 9. 【替换】通知用户、定期汇报

```markdown
# 通知用户

只有新增决策和需要用户介入的 `failed` 立即通知：

1. 先写好 decisions.jsonl（失败也要建一条决策，选项至少包含“重试 / 改派 / 放弃”）；
2. 对话里只写一行：“新增 N 条待决策，见面板 http://127.0.0.1:7788/ ”（面板未运行时，再按旧格式在对话里列出问题、可选项和你的建议）；
3. 用 PushNotification 发一行摘要（200 字以内，以需要用户做的决定开头）；
4. 在 state.json 对应任务填 notified_at（ledger.md 的 `已通知` 列随之生成），追加 notified 事件，避免重复通知。

# 定期汇报

收到 `【定期汇报】` 时：

1. 刷新 leader.json 的 heartbeat_at；
2. `herdr agent list` 对账（同会话启动第 2 步），更新 state.json 并重新生成 ledger.md；
3. 拾取未处理的回答（见“决策回传”）；
4. 检查 open 决策是否已过 deadline，按“待决定事项”规则处理（追加 expired 或 superseded 行）；
5. 对话里只输出增量一行：“完成 X / 新阻塞 Y / 新增待决策 Z，见面板”；与上次汇报相比没有任何变化时，
   只回一行“无变化（N 个进行中）”。完整内容由面板展示，不再在对话里逐条列出。
```

---

## 10. 灰度与回退

- 灰度：先在一个项目上启用（建议新项目），面板同时支持新旧两种数据；旧项目继续用 ledger.md，面板只读展示。
- 回退：删除 leader.md 中上述条文即可，`~/.leader/<project>/` 下新增文件不影响旧流程（ledger.md 始终保留）。
- 兼容承诺：面板永远能只读解析 ledger.md；协议文件缺失或损坏时面板降级为只读，不影响 leader 工作。

---

## 11. 待审批增补（面板定时汇报 / leader 切换 CLI，**未合入 leader.md**）

> 来源：`feat/lc-leader-scheduler`。面板侧已实现，下列条文需用户批准后才能改 `~/.claude/agents/leader.md`。
> 不改也能工作：面板默认 `-report-grace 5m`，只在 leader 自己的 cron 迟到 5 分钟以上时补发，作为兜底。

**A1【修改】会话启动第 3 步（cron）**——让面板成为唯一的定时来源，修掉 cron 7 天过期、`--continue` 后丢失，也让 Codex leader 能用：

```markdown
3. 执行 `curl -fsS http://127.0.0.1:7788/healthz`。若 `report_scheduler.enabled` 为 true，且 `report_scheduler.projects`
   不存在或包含 `<project>`，说明面板会给本项目定时投递【定期汇报】：不要创建定期汇报 cron，CronList 里已有的用 CronDelete 删掉。
   否则（面板不可达、未开启或本项目不在范围内）：Claude 用 CronList 检查，没有就用 CronCreate 创建（cron `*/10 * * * *`，
   prompt `【定期汇报】`，recurring）；Codex 没有会话内定时，在对话里提醒用户一次“本项目没有定时汇报”。
   面板之后停机或关闭调度时不会自动重建 cron：leader 会在面板显示离线，由用户重启面板或在对话里唤醒 leader（leader 重启时会重新执行本步）。
```

批准后面板改用 `-report-grace 0s`（与本条同时上线），汇报节奏回到每 10 分钟。

**A2【替换】会话启动第 4 步（自我识别与命名）**——“标题为 leader 的 claude agent”对 Codex 不成立；会话恢复后名字会丢：

```markdown
4. 给自己在 herdr 中命名：当前 pane 是 `$HERDR_PANE_ID`。先 `herdr agent get "$HERDR_PANE_ID"` 确认 cwd 是当前项目目录、
   agent 是自己的 CLI；变量为空、get 失败或不符时停止注册并告诉用户，不要退回“找同目录的某个 agent”。
   名字优先沿用 leader.json 的 `leader.herdr_name`；没有再按下方“leader 命名”计算。当前名字不同就执行
   `herdr agent rename "$HERDR_PANE_ID" <名字>`；该名字正被其他 pane 占用时不要覆盖，告诉用户。
```

**A3【修改】会话启动第 5 步（leader.json）**——面板按 kind 核验，写错 kind 会显示 `agent_kind_mismatch` 且不投递：

```markdown
5. 写 leader.json（整文件替换，leader 对象整体重写，不保留上一任的字段）：…… leader={agent:<实际 CLI：claude 或 codex>,
   herdr_name, pane_id, session_id（仅当 herdr 返回了 agent_session.value 才写）}……
```

说明：没有 session_id 时，面板只按名字识别 leader，名字丢失后不会按 pane 兜底（同 pane 的替换进程无法区分），所以 A4 的名字核对对 Codex 是必需的。

**A4【修改】定期汇报第 1 步**——名字丢失的兜底（roadmap P2 协议侧）：

```markdown
1. 先确认自己仍是 leader.json 登记的 leader（leader.agent 与自己的 CLI 一致、pane_id 是 `$HERDR_PANE_ID`；有 session_id 时与自己的一致）。
   不一致说明已有新 leader 接管，不要刷新心跳、不要改名，在对话里告诉用户后停止处理本次汇报。
   一致则刷新 leader.json 的 heartbeat_at，并确认自己在 herdr 中的名字等于 leader.json 的 herdr_name，不等就 rename。
```

并注明：`【定期汇报】project=<project> source=leader-console` 与 cron 发来的 `【定期汇报】` 处理方式完全相同。

**A5【新增】会话启动第 0 步（接管识别与核对）**——切换 CLI 或换会话后，新 leader 必须恢复上一任没做完的决策，而不是只拾取 open 决策：

```markdown
0. 以下任一情况视为接管上一任 leader：leader.json 已存在，且其中的 leader.agent / pane_id / session_id 与自己不同；
   或收到【leader 接管】提示（Codex→Codex 在同一 pane 时前后都没有 session，leader.json 看不出换了人，只能靠这条提示）。
   接管时：
   - 先追加事件 `{"type":"other","summary":"leader 接管：<旧 agent>→<新 agent>"}`；
   - 以 state.json 为准，不重新派发仍在运行的 worker；复用 worker 时沿用其 herdr 名字和报告目录；
   - 上一任挂的后台监听、cron 都随旧会话消失，按第 2、3 步重建（Codex 不挂后台监听，见适配层）；
   - 第 6 步之前完成“接管核对”：按 decision_id 折叠 decisions.jsonl，结合 answers.jsonl、events.jsonl 扫描三类决策：
     ① 最新状态仍为 open 且 answers.jsonl 有回答（不论是否已投递）：按“决策回传”第 1–5 步处理；
     ② 已是 answered（via=panel）但没有该 answer_id 的 decision_executing，包括已写 answered、还没写 decision_ack 的窗口（补写 decision_ack）：
        先按 decision_id / answer_id 核对现场（worker 状态与报告、git 分支/MR、外部系统），确认后续动作没做过再执行；
     ③ 有 decision_executing、没有 decision_done：核对现场判断做到哪一步；已完成的补写 decision_done；剩余步骤可逆且明确的续办；
        涉及提交、推送、发布、对外消息等不可逆动作、又无法确认是否已做的，新建一条决策请用户决定，**不要直接重做**。
```

**A6【新增】适配层拆分**——把 leader.md 中依赖 Claude Code 的条目（CronCreate、后台 wait 唤醒、PushNotification、`--continue`、
`--permission-mode auto`、自我识别）集中到一节“适配层：Claude”；核心协议只描述“需要什么能力和完成证据”。
核心协议应有**唯一来源**（例如单独一个 `leader-core.md`，Claude 的 agent 文件与 Codex profile 都由它生成或引用），避免两份正文漂移。
Codex 版本见 [leader-switch.md 附录](leader-switch.md)，其中覆盖了活跃任务轮询（working/reviewing/blocked/needs_user）、blocked 去重、面板不可用时的行为。

**A7【建议】Claude leader 启动命令固定带 `--permission-mode auto`**：auto 模式不随重启保留（交叉评审已核实）。它只是权限选择，不能替代 A5 的现场核对。
