# leader 切换 CLI 操作手册（草稿）

> 场景：Claude 额度用完，需要把某个项目的 leader 从 Claude Code 临时换成 Codex，额度恢复后再切回。
> 状态：**草稿**。第 0 步里的 Codex profile 和 leader 提示词修改都还没有创建或批准，切换前需要用户先审批；
> 面板侧能力（定时汇报、按 kind 核验、切换识别）在 `feat/lc-leader-scheduler` 分支，需要用该分支构建的面板。

下文用 `P` 表示项目名（`~/.leader/<P>/` 的目录名），`D=~/.leader/$P`。

## 原理（一句话）

面板只认 `leader.json` 描述的那个 agent：名字（或名字丢失时的 pane）+ cwd + agent kind (+ session) 都对上才投递。
所以切换的关键就是：**旧 leader 先退出 → 新 leader 在同一目录启动 → 新 leader 改写 leader.json**。
中间这段时间面板显示 leader 离线，回答和定期汇报排队，不会丢、也不会投错；新 leader 写好 leader.json 后自动恢复，
项目的 leader 状态里会出现 `change.switched=true`（claude→codex）。

## 0. 一次性准备（需要用户审批，本手册不创建任何文件）

1. **Codex leader profile**：`~/.codex/leader.config.toml`，通过 `codex -p leader` 叠加在基础配置上，worker 不受影响。示例：

   ```toml
   # ~/.codex/leader.config.toml（示例，未创建）
   model = "gpt-6.1-sol"
   # 权限二选一，由用户决定：
   #  A. 不写，继承基础配置的 danger-full-access + never：最省事，但没有任何审批兜底；
   #  B. sandbox_mode = "workspace-write" + approval_policy = "on-request" + approvals_reviewer = "auto_review"，
   #     并在启动时 --add-dir ~/.leader/<P>：更安全，但 herdr socket 在沙箱内能否访问未实测。
   developer_instructions = """
   <leader 核心协议：~/.claude/agents/leader.md 去掉 frontmatter 的正文>
   <下方“附录：Codex 适配层”全文>
   """
   ```

   不要用 `model_instructions_file`（它替换 Codex 内置基础指令）；不要把 leader 角色写进全局 `~/.codex/AGENTS.md`（会污染所有 worker）。

2. **leader 提示词的 agent 无关化**：见 [leader-protocol.md §11](leader-protocol.md) 的 A2/A3/A5——自我识别改用 `$HERDR_PANE_ID`、
   leader.json 写真实 kind、启动时识别“接管”。不改的话 Claude 照常工作，但 Codex 照搬 Claude 的自我识别规则会找不到自己。

3. **面板**：用本分支构建的面板运行在 7788，并确认调度器开着：

   ```bash
   curl -s http://127.0.0.1:7788/healthz | jq .report_scheduler
   # {"enabled":true,"grace_sec":300}
   ```

## 1. Claude → Codex

1. **记下现状**

   ```bash
   jq '{cwd, leader, heartbeat_at}' "$D/leader.json"
   ```

   记住 `cwd`、`leader.herdr_name`（如 `leader-trae-tob-dev`）、`leader.pane_id`（如 `w1:pA`）。

2. **退出旧 Claude leader**：在 leader 所在 pane 输入 `/exit`（卡在额度提示上也一样退出），直到 pane 回到 shell 提示符。
   - 为什么必须退出：旧 Claude 不退出就还占着 herdr 名字，新 leader 改不了名；它也还能收到终端输入。
     即使忘了退出，新 leader 写好 `leader.json`（agent=codex）后面板也不会再投给它（kind 不符），但改名会冲突。
   - 此时面板：leader 离线（`agent_not_found`），回答显示排队，定期汇报暂缓。**这是预期的。**

3. **在同一个 pane、同一个目录启动 Codex，并直接沿用原来的名字**（在任意其他 pane 执行）：

   ```bash
   herdr agent start <herdr_name> --kind codex --pane <pane_id> -- -p leader
   ```

   - pane 必须在 shell 提示符下，且当前目录就是 `leader.json` 的 `cwd`（面板要比对 cwd）。不确定时先在该 pane `cd <cwd>`。
   - 若 0.2 选了权限方案 B，在 `-p leader` 后追加 `--add-dir ~/.leader/<P>`。
   - 不要启动第二个 leader：同一项目同时只能有一个 leader 写 state.json。

4. **让 Codex 按启动流程接管**（Codex 需要一条用户消息才开始第一个回合）：

   ```bash
   herdr agent prompt <herdr_name> "【leader 接管】project=<P> 上一任 leader 是 claude。按会话启动流程执行：读 state.json、herdr agent list 对账、写 leader.json（agent=codex）、拾取未处理回答，然后汇报一行。"
   ```

   新 leader 应当完成：确认名字（`herdr agent rename "$HERDR_PANE_ID" <herdr_name>`，已是该名字则跳过）→ 以 state.json 为准对账 →
   改写 `leader.json`（`leader.agent="codex"`、herdr_name、pane_id、**不写 session_id**、started_at/heartbeat_at=现在、report_interval_min=10）→
   追加一条事件 `leader 接管：claude→codex` → 拾取未处理回答。

5. **验证面板已识别**

   ```bash
   curl -s http://127.0.0.1:7788/api/v1/overview |
     jq --arg p "$P" '.projects[] | select(.name==$p) | .leader | {online, reason, agent, observed_agent, via, target, change, report}'
   ```

   期望：`online=true`、`reason="ok"`、`agent="codex"`、`observed_agent="codex"`、`change.switched=true`、
   `change.from.agent="claude"`、`change.to.agent="codex"`、`report.next_due_at` 有值。
   排队中的回答约 15 秒内投出：`tail -n 3 "$D/answers.jsonl"` 应出现 `"delivery":"delivered","target":"<herdr_name>"`。
   面板自己的记录：`tail -n 3 "$D/panel.jsonl"`。

   常见异常：
   - `reason=agent_kind_mismatch`：Codex 已在 pane 里，但 leader.json 还写着 claude → 新 leader 还没完成第 4 步。
   - `reason=identity_mismatch`：cwd 不一致（Codex 不在项目目录启动），或 leader.json 残留了旧的 session_id。
   - `reason=agent_not_found`：名字没改成 leader.json 里的 herdr_name，且 pane_id 也对不上。

6. **切换后的差异**（Codex 没有会话内定时和后台唤醒）：
   - 定期汇报完全由面板投递。默认 `-report-grace 5m`，即约每 15 分钟一次（心跳 TTL 25 分钟，不会误判离线）；
     想恢复 10 分钟节奏，重启面板时加 `-report-grace 0s`。
   - worker 完成不会自动叫醒 Codex leader：它在每次定期汇报时用 `herdr agent list` 检查 worker 状态再验收，
     最长延迟一个汇报周期。急的时候直接在对话里让它“检查 <task>”。
   - 面板不可用时 Codex leader 只在你说话时工作；它启动时的“拾取未处理回答”兜底仍然有效。

不需要改的：`state.json`、`events.jsonl`、`decisions.jsonl`、`answers.jsonl`、`panel.jsonl`、所有 worker、`~/.codex/config.toml` 基础配置、
`~/.claude/` 下任何文件。**不要手改 leader.json**——由新 leader 写；手改容易和 leader 的整文件替换互相覆盖。

## 2. Codex → Claude（额度恢复后切回）

1. 等 Codex leader 当前回合结束（看板上 leader 状态为空闲），在它的 pane 里 `/quit`（或连按两次 Ctrl+C），回到 shell 提示符。
2. 同一 pane 启动 Claude leader：

   ```bash
   herdr agent start <herdr_name> --kind claude --pane <pane_id> -- --agent leader --permission-mode auto
   ```

   - 建议开新会话，不加 `--continue`：`--continue` 会恢复切换前那个 Claude 会话，它的记忆已经过时（以 state.json 为准也能工作，但容易误导）。
   - `--permission-mode auto` 要显式写上：auto 模式不会随重启保留。
3. 发一条接管提示（同 1.4，把 claude/codex 对调）。Claude leader 的启动流程会写 `leader.agent="claude"` 和 `session_id`，并按 leader.md 处理 cron
   （若 §11 A1 已批准：面板调度器开着就不再建 CronCreate）。
4. 验证（同 1.5）：期望 `agent="claude"`、`change.switched=true`、`change.from.agent="codex"`、`change.to.agent="claude"`。

## 3. 回退与应急

- 新 leader 起不来：回答和汇报一直排队，什么都不会丢；在原 pane 重新启动原来的 leader 即可。
- 想彻底关掉面板的定时汇报：重启面板加 `-leader-reports off`（leader 自己的 cron 不受影响）。
- 只对部分项目开：`-leader-reports projA,projB`。

## 附录：Codex 适配层（建议文本，放进 profile 的 developer_instructions）

```markdown
# 适配层：Codex

以下条目覆盖核心协议里依赖 Claude Code 的部分。

- 自我识别：当前 pane 是 `$HERDR_PANE_ID`。名字优先沿用 leader.json 的 leader.herdr_name；
  `herdr agent get "$HERDR_PANE_ID"` 显示的名字不同就执行 `herdr agent rename "$HERDR_PANE_ID" <名字>`。
- leader.json：leader.agent 写 "codex"；herdr 拿不到 Codex 的 session，**不写 session_id**。
- 定时：不要尝试创建定时任务。【定期汇报】由 leader-console 面板投递
  （`curl -fsS http://127.0.0.1:7788/healthz` 的 report_scheduler.enabled 为 true）。面板不可达时在对话里提醒用户一次。
- 事件监听：不要挂后台 `herdr agent wait`（回合结束后后台命令完成不会唤醒你）。派发时用
  `herdr agent prompt ... --wait --until working --timeout 30000` 确认开工即可；每次定期汇报对账时，
  state.json 里 working / reviewing 的任务若 herdr 状态已是 idle 或 done，按“事件监听”的 idle/done 规则验收；blocked 同理。
- 通知用户：没有 PushNotification。改为 `herdr notification show "<一行摘要>" --sound request`，其余步骤不变。
- 启动接管：若 leader.json 的 leader.agent 不是 codex（或 pane/会话不同），先追加事件
  `{"type":"other","summary":"leader 接管：<旧 agent>→codex"}`，再按会话启动流程执行。
```
