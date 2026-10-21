# leader 协议改造草案（lc-1）

> 状态：**草案，待用户审批**。目标文件 `~/.claude/agents/leader.md`（全局，影响所有 leader 会话）。本仓库不修改该文件。
> 数据契约以 [design.md](design.md) §4 和 `internal/model/*.go` 为准。

## 0. 改动摘要

| leader.md 现有章节 | 改动 |
|---|---|
| 角色 | “Edit/Write 只用于台账和汇报文件”扩展为包含 `~/.leader/<project>/` 下的协议文件 |
| 会话启动 | 新增：写 leader.json、给自己在 herdr 中命名、从 ledger.md 迁移 state.json、拾取未处理的回答 |
| 台账 | 事实来源由 ledger.md 改为 state.json；ledger.md 由 state.json 生成；状态新增 `queued`、`ready_to_merge` |
| 派发 | 报告路径由 `/tmp/<name>.md` 改为 `~/.leader/<project>/reports/<name>/report.md`；派发写 `dispatched` 事件 |
| 事件监听 | 每次状态变化同时写 events.jsonl |
| 通知用户 | 需要用户决定的事写 decisions.jsonl；对话里只发一行 |
| 定期汇报 | 刷新心跳；对账；拾取未处理回答；对话里只发增量一行 |
| （新增）待决定事项 | decisions.jsonl 的写法 |
| （新增）决策回传 | 处理 `【决策回传】` 消息 |
| （新增）写文件规则 | 原子写、单行追加、用 jq 生成 JSON |

下面 §1–§9 是可以直接合进 leader.md 的条文（`<project>` 指当前工作目录的目录名，下同）。

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
   （即自己），若没有名字则 `herdr agent rename <pane_id> leader-<project>`。
5. 写 leader.json：schema_version=1、protocol_version="lc-1"、project、cwd、
   leader={agent:"claude", herdr_name, pane_id, session_id（herdr 的 agent_session.value）}、
   started_at、heartbeat_at、report_interval_min=10。
6. 执行一次“拾取未处理的回答”（见“决策回传”）。
```

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
旧规则里“done 但等用户确认提交”的任务一律改用 `ready_to_merge`。

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
2. 对话里只写一行：“新增 N 条待决策，见面板 http://127.0.0.1:7788/ ”（面板未运行时，再按旧格式在对话里列出问题和选项）；
3. 用 PushNotification 发一行摘要（200 字以内，以需要用户做的决定开头）；
4. 在 state.json 对应任务填 notified_at，追加 notified 事件，避免重复通知。

# 定期汇报

收到 `【定期汇报】` 时：

1. 刷新 leader.json 的 heartbeat_at；
2. `herdr agent list` 对账（同会话启动第 2 步），更新 state.json；
3. 拾取未处理的回答（见“决策回传”）；
4. 检查 open 决策是否已过 deadline，按“待决定事项”规则处理（追加 expired 或 superseded 行）；
5. 对话里只输出增量一行：“完成 X / 新阻塞 Y / 新增待决策 Z，见面板”；没有任何变化时回“无变化（N 个进行中）”。
   完整内容由面板展示，不再在对话里逐条列出。
```

---

## 10. 灰度与回退

- 灰度：先在一个项目上启用（建议新项目），面板同时支持新旧两种数据；旧项目继续用 ledger.md，面板只读展示。
- 回退：删除 leader.md 中上述条文即可，`~/.leader/<project>/` 下新增文件不影响旧流程（ledger.md 始终保留）。
- 兼容承诺：面板永远能只读解析 ledger.md；协议文件缺失或损坏时面板降级为只读，不影响 leader 工作。
