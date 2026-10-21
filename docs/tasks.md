# MVP 并行开发任务

> 前置：骨架提交已在 `main`（契约 `internal/model`、端口接口 `internal/store/store.go`、`internal/herdr/herdr.go`、夹具 `testdata/`、假 herdr `testdata/fake-herdr.sh`）。
> 设计依据：[design.md](design.md)。每个任务在自己的 git worktree / 分支上开发，互不修改对方目录。

## 0. 总览

| 任务 | 分支 | 拥有的目录（只改这些） | 依赖（编译期） | 建议 worker |
|---|---|---|---|---|
| A 数据读取与解析层 | `feat/a-store` | `internal/jsonl` `internal/ledgercompat` `internal/store`（`store.go` 接口除外） | model | Codex |
| B HTTP API 与决策回传 | `feat/b-api` | `internal/service` `internal/server` | model、store 接口、herdr 接口（不依赖 A/D 的实现） | Codex |
| C 前端页面 | `feat/c-web` | `web/`（`web.go` 可加测试，不改 embed 方式） | 无（用 mock JSON） | Claude 或 Codex |
| D herdr 适配、对账、脱敏、渲染 | `feat/d-herdr` | `internal/herdr`（`herdr.go` 接口除外） `internal/reconcile` `internal/redact` `internal/render` | model | Codex |
| E 集成联调（A–D 合入后） | `feat/e-integrate` | `cmd/leader-console` `Makefile` `README.md` | 全部 | 由 B 的 worker 继续 |

并行方式：A、B、C、D 同时开工。
- **契约冻结**：`internal/model/*`、`internal/store/store.go`、`internal/herdr/herdr.go` 以及下文 §1 的函数签名视为冻结。各任务不得修改；确需修改时停下来写进报告，由 leader 统一改 main 后通知所有任务 rebase。
- **靠接口解耦**：B 只依赖 `store.Store`、`herdr.Client` 两个接口和 §1 中以函数值注入的 D 能力，测试中使用自己写的 fake；C 只依赖 design §6 的 HTTP 契约和 `model/view.go`，用 mock JSON 开发。
- **共享文件**：`go.mod`（禁止添加依赖）、`Makefile`、`docs/`、`testdata/fixtures/`、`cmd/` 在 A–D 阶段不改；需要新夹具时放到自己包下的 `testdata/`。
- **Codex 并发**：台账经验显示同时活跃的 Codex 不宜超过 2–3 个（429）。建议 A、B、D 用 Codex，C 用 Claude；或先启动 A、D、C，B 稍后。

通用要求（每个任务都适用）：
- 只用 Go 标准库，不加第三方依赖；Go 1.24。
- 测试不得读取真实 `~/.leader`、不得调用真实 herdr（D 的手工核验除外，见 D）；不得 push、不得配置 remote。
- 完成标准：在 worktree 根目录 `make check` 通过（gofmt、vet、`go test -race ./...`、build）；提交到自己的分支（可多次 commit）；报告写到 leader 指定路径，包含：做了什么、对外接口、测试覆盖率（`go test -cover`）、偏离设计之处、遗留问题。

## 1. 冻结的跨任务接口

```go
// ---- A 提供 ----
package store
func NewFS(root string, opts FSOptions) (*FS, error) // *FS 实现 store.Store
type FSOptions struct {
    Location *time.Location // ledger.md 时间的时区，nil = time.Local
    MaxReportBytes int64    // 0 = 1 MiB
}

package jsonl
// Append 以 flock(lockPath) + O_APPEND 单次 write 追加一行（自动补 '\n'），并 fsync。
func Append(path, lockPath string, line []byte) error
// ReadLines 返回所有完整行（忽略末尾不以 '\n' 结尾的残行）及其行号。
func ReadLines(path string) (lines [][]byte, err error)

package ledgercompat
func Parse(r io.Reader, loc *time.Location) (tasks []model.Task, hints []model.LegacyHint, warnings []string)
// LikelyToMerge 实现 design §5 第 5 步的“待合入”推断，供 B 推导看板列。
func LikelyToMerge(t model.Task) bool

// ---- D 提供 ----
package herdr
func NewExec(cmd []string, timeout time.Duration) Client // cmd 如 []string{"herdr"}
type Fake struct{ /* 可设置 Agents、Errors、Reads；记录 Prompts */ } // 实现 Client，供 E 和其他测试使用

package reconcile
type Result struct {
    Leader model.LeaderPresence
    Tasks  map[string]model.TaskLive // key = Task.ID
}
// agents==nil 且 herdrOK==false 表示 herdr 不可用：Leader.Reason="herdr_unavailable"，任务不判 lost。
func Project(pd *model.ProjectData, agents []herdr.Agent, herdrOK bool, now time.Time) Result

package redact
func String(s string) string

package render
func Markdown(src string) string // 输入应已脱敏；输出安全 HTML

// ---- B 提供 ----
package service
type Deps struct {
    Store     store.Store
    Herdr     herdr.Client
    Reconcile func(pd *model.ProjectData, agents []herdr.Agent, herdrOK bool, now time.Time) (model.LeaderPresence, map[string]model.TaskLive)
    Redact    func(string) string
    Render    func(string) string
    ToMerge   func(model.Task) bool // ledger 过渡期的“待合入”推断
    Now       func() time.Time
}
func New(deps Deps, opts Options) *Service
func (s *Service) Run(ctx context.Context) error // 后台扫描、herdr 刷新、投递队列

package server
func New(svc *service.Service, static fs.FS, opts Options) http.Handler // 替换骨架里的 New(static)
```

B 以函数值注入 D 和 A 的能力（`Reconcile`、`Redact`、`Render`、`ToMerge`），因此 B 在 A、D 合入前即可编译、测试；E 阶段在 `main.go` 里把真实实现接上（`reconcile.Project` 需要一个两行的适配闭包）。

---

## 任务 A：数据读取与解析层

**目标**：把 `~/.leader/*/` 读成 `model.ProjectData`，包括 ledger.md 过渡期解析；实现面板唯一的写操作 `AppendAnswer`。

**范围**：`internal/jsonl`、`internal/ledgercompat`、`internal/store`（新增 `fs.go` 等，不改 `store.go`）。

**要点**（详见 design §4、§5、§8、§9 路径部分）：
1. `jsonl`：`Append`（flock 旁路锁文件 + `O_WRONLY|O_APPEND|O_CREATE` + 单次 write + fsync）、`ReadLines`（残行忽略）、按 key 折叠的泛型辅助（可选）。
2. `ledgercompat.Parse`：design §5 全部规则；`LikelyToMerge`。
3. `store.FS`：
   - `ListProjects`：root 下满足 id 正则、含 leader.json / state.json / ledger.md 任一的目录，排序；root 不存在返回空列表而非错误。
   - `LoadProject`：stat 指纹（各源文件 size+mtime_ns，含 `reports/` 目录 mtime）不变则返回缓存；state.json 优先，否则 ledger.md；decisions 按 id 折叠（后写整条覆盖）、answers 按 answer_id 折叠成 `model.Answer`（无 delivery 行时 `Delivery=queued`）；坏行写入 Warnings；`schema_version > 1` 写 warning 并跳过该文件。
   - `ListReports` / `ReadReport`：`reports/<task>/*.md` 按 mtime 倒序；过渡期 `/tmp/<x>.md` 仅当被该任务的 Reports 引用；报告 ID 不暴露绝对路径（建议 `r-<sha1(path)[:10]>`）；`Lstat` 拒绝符号链接；超过上限截断读取。
   - `AppendAnswer`：校验 project 存在；锁文件 `answers.jsonl.lock`；进程内也要有互斥。
   - 所有 project / task / report 参数做 id 正则校验，失败返回 `ErrProjectNotFound` / `ErrReportNotFound`。

**测试要求**：
- `jsonl`：**多进程**并发追加测试（测试二进制以子进程方式重新执行，≥ 4 进程 × 200 行），校验行数、每行可解析、无交错；残行与坏行用例。
- `ledgercompat`：golden 测试——把 `testdata/fixtures/leader-root/legacy-demo/ledger.md` 和自造的边界样本（转义竖线、列顺序打乱、缺列、多余列、空表、无表、重复 name、非法状态）解析结果与 `*.golden.json` 对比（提供 `-update` flag 重新生成）。
- `store`：用 `t.TempDir()` 复制 `testdata/fixtures/leader-root` 构造；覆盖 state.json 优先、ledger 回退、缓存命中/失效、折叠规则、路径穿越（`../`、绝对路径、符号链接）、`AppendAnswer` 后 `LoadProject` 可见。
- 覆盖率 ≥ 80%。

**验收**：
- `make check` 通过。
- 附一段一次性的手工核验结果（不写进测试、不提交数据）：对真实 `~/.leader/tob-check-estimated-balance/ledger.md` 只读运行 Parse，输出任务数（应等于表格行数）与 warnings。
- 不写任何 `~/.leader` 下的文件。

---

## 任务 B：HTTP API 与决策回传

**目标**：实现 service（快照、视图推导、决策提交与投递状态机）和 server（API、鉴权、SSE）。

**范围**：`internal/service`、`internal/server`。不改 `cmd/`（E 阶段再接线）。

**要点**：
1. `service`：
   - `Run`：ScanInterval 扫描（调用 `Store.ListProjects/LoadProject`）、HerdrInterval 调 `Herdr.List`；对每个项目调用 `Deps.Reconcile`；组装不可变快照（原子指针替换）；快照版本变化时通知订阅者（供 SSE）。
   - 视图推导：看板列（PRD §8，含 `ToMerge` 与 lost）、`RunningFor`、决策 `DecisionState` 推导（design §7.1 优先级）、决策排序（design §6）、对所有展示文本调用 `Redact`，对 `Context` 和报告调用 `Render`。
   - 查询方法：`Overview()`、`Tasks(project, column)`、`TaskDetail(project, id)`、`Report(...)`、`Terminal(...)`（调 `Herdr.Read`，去控制字符后 `Redact`，带 5s 节流）、`Decisions(...)`。
   - `Answer(ctx, project, decisionID, AnswerInput)`：design §7.2（幂等、409、400 规则）。
   - 投递队列：design §7.3 全部规则（离线/blocked 不计次数、退避 5/15/30/60s、第 5 次失败写 failed、启动恢复、`Retry`）；prompt 文本模板、单行化、去控制字符、≤ 300 字符。
   - 进程单实例锁（design §8，锁文件路径可配，测试用 TempDir）。
2. `server`：
   - 路由按 design §6；`server.New(svc, static, opts)`；保留 `/healthz`。
   - 中间件：Host 白名单、写接口 token（`crypto/subtle`）+ Origin + Content-Type + 16 KiB Body 上限；安全响应头（CSP 等，design §9）。
   - 首页：读取嵌入的 `index.html`，把 `__LC_TOKEN__` 替换为 token 后返回（`Cache-Control: no-store`）。
   - SSE `/api/v1/stream`：连上立即发一次当前 version，之后变化时推送，15s 注释保活，连接数上限 16。
   - 错误格式 `{"error":{"code","message"}}`。

**测试要求**（全部用 fake，不依赖 A/D 实现）：
- service：手写内存 `fakeStore`（实现 `store.Store`，AppendAnswer 记录行并反映到下次 LoadProject）、`fakeHerdr`（实现 `herdr.Client`，可编排每次 Prompt 的返回）、可控时钟。逐条覆盖 design §7.1 状态机的每条边、幂等提交、并发提交同一决策只有一个成功（`-race`）、离线排队后上线投递、`agent_blocked` 不计次数、5 次失败后 failed + Retry、重启恢复、prompt 文本注入（换行、控制字符、超长）被清洗。
- server：`httptest` 覆盖每个路由、403（缺 token / 错 token / 错 Origin / 错 Host）、400/404/409、Body 超限、CSP 头、首页 token 注入、SSE 首帧。
- 覆盖率 ≥ 75%。

**验收**：`make check` 通过；报告中附 `curl` 走一遍作答流程的示例（用 fake 组装的临时 main 或测试日志即可）。

---

## 任务 C：前端页面

**目标**：无构建链的三页面前端：待我决定、任务看板、任务详情（PRD §7 原型）。

**范围**：`web/static/`（可新增 `web/web_test.go`）。

**要点**：
1. 结构：`index.html`（保留 `<meta name="lc-token" content="__LC_TOKEN__">`）、`app.js`（入口 + hash 路由 `#/decisions` `#/board?project=` `#/task/<project>/<id>`）、`api.js`（fetch 封装：写请求带 `X-LC-Token` 与 `Content-Type: application/json`；错误统一处理）、`views/*.js`、`style.css`。只用 ES module，不引入任何外部 CDN 资源。
2. 渲染安全：只用 `textContent` / `createElement`；唯一允许的 `innerHTML` 是服务端返回的 `*_html` 字段（`context_html`、报告 `html`）。
3. 实时：`EventSource('/api/v1/stream')`，收到 `version` 后重拉当前页；断开后改为每 5s 轮询 `/api/v1/overview` 的 version；页面标题显示 `(N) leader-console`（N = 未关闭决策数）。
4. 待我决定：卡片（紧急度、项目·任务、截止倒计时、问题、背景折叠、选项单选 + 影响 + 推荐标记 + 推荐理由、补充说明、相关报告/任务链接）、提交二次确认、提交后按 `DecisionState` 显示进度条（已记录 → 已回传 → leader 已确认 → 执行中 → 完成）、`delivery_failed` 显示“重试投递”、leader 离线提示“回答将排队”、409 时刷新并提示；已处理折叠区；legacy 提示区（只读）。
5. 看板：项目筛选、leader 在线状态、5 列、卡片字段（PRD §7.2）、lost/failed 红色徽标、已完成列默认只显示最近 24h。
6. 详情：基本信息、外部链接（新窗口打开）、事件时间线、相关决策、报告标签页（`html` 字段）、终端输出（`<pre>` + textContent，visible/recent 切换、手动刷新）。
7. Mock：`web/static/mock/*.json` 按 `internal/model/view.go` 与 design §6 的返回结构手写（数据可参考 `testdata/fixtures/leader-root`），URL 带 `?mock=1` 时 `api.js` 改读 mock（写请求返回模拟的 202/409）。所有状态（open/queued/delivered/acked/executing/done/delivery_failed/answered_in_chat/expired/superseded、leader 离线、herdr 不可用、lost 任务、legacy 来源项目）都要有 mock 数据覆盖。

**测试要求**：
- `web/web_test.go`：校验嵌入文件齐全、`index.html` 含 `__LC_TOKEN__` 占位、所有 `<script>` / `<link>` 均为同源相对路径、`static/` 中无 `http://` / `https://` 外链资源引用（mock 数据中的外部链接字段除外）、mock JSON 均能解码到对应 `model` 视图类型（严格解码）。
- 手工走查：`go run ./cmd/leader-console` 后打开 `http://127.0.0.1:7788/?mock=1#/decisions`，按 PRD §7 逐项截图或文字记录到报告中（骨架已能服务 `web/static`）。

**验收**：`make check` 通过；Chrome/Safari 最新版无控制台错误；三页面与 PRD 原型的信息一致；mock 覆盖全部状态。

---

## 任务 D：herdr 适配、对账、脱敏、渲染

**目标**：实现 `herdr.Client` 的 exec 版本与 fake、reconcile 纯函数、redact、render。

**范围**：`internal/herdr`（不改 `herdr.go`）、`internal/reconcile`、`internal/redact`、`internal/render`。

**要点**：
1. `herdr.NewExec(cmd, timeout)`：
   - `List`：`<cmd> agent list` → 解析 `{"result":{"agents":[...]}}`，`terminal_title_stripped` → `TerminalTitle`，`agent_session.value` → `SessionID`。
   - `Get`：`<cmd> agent get <target>` → `result.agent`。
   - `Read`：`<cmd> agent read <target> --source <s> --lines <n> --format text`；再剥离 ANSI/OSC 与除 `\n\t` 外的控制字符；lines 默认 80、上限 400。
   - `Prompt`：`<cmd> agent prompt <target> <text>`；只以退出码判断成功（真实成功输出未实测）。
   - 错误：非 0 退出时尝试解析 stdout/stderr 中的 `{"error":{"code","message"}}` → `*herdr.Error`；无法解析 → `CodeExec`；超时 → `CodeTimeout`；JSON 解析失败 → `CodeDecode`。
   - 一律 `exec.CommandContext` 参数数组，不经 shell；target 必须匹配 `^[A-Za-z0-9:_.-]{1,64}$`，否则直接返回错误。
2. `herdr.Fake`：可预置 agents、按 target 预置错误与 read 内容，记录所有 Prompt 调用；并发安全。
3. `reconcile.Project`：design §7.3 的目标解析与身份核验（从 List 结果中按 herdr_name 或 pane_id 找 agent，校验 cwd、session_id）、心跳 TTL（`2×interval+5min`，interval 缺省 10）；任务对账：按 `AgentName` 优先、`PaneID` 次之匹配 agent，`Status.Active()` 且找不到 → `Lost=true`；herdr 不可用时不判 lost。
4. `redact.String`：design §9 的模式集合；保留前 4 位 + `****`；多行 PEM 整块替换为 `[REDACTED PRIVATE KEY]`。
5. `render.Markdown`：先整体 HTML 转义再生成白名单标签（h1–h4、p、ul/ol/li、pre/code、table/thead/tbody/tr/th/td、strong、em、a、blockquote、hr）；链接仅 `http(s)://`，加 `rel="noopener noreferrer" target="_blank"`；输入超过 1 MiB 截断。

**测试要求**：
- herdr：exec 实现指向 `testdata/fake-herdr.sh`（`../../testdata/fake-herdr.sh`），覆盖 list/get/read/prompt、`agent_not_found`、`FAKE_HERDR_FAIL=1`、超时（用 `sleep` 包一层的临时脚本）、含 `;`、空格、换行、`$()` 的 prompt 文本原样到达（用 `FAKE_HERDR_LOG` 断言）、非法 target 拒绝。
- reconcile：表驱动覆盖 Reason 的 6 种取值、TTL 边界、pane 复用导致的 identity_mismatch、无名 leader 用 pane_id、Codex（无 session）、lost 判定、herdr 不可用。
- redact：正例 ≥ 15（每类模式至少一个）、反例 ≥ 5（普通 hex commit 哈希、UUID、`token 数量` 这类中文文本不应被误杀）。
- render：XSS 向量（`<script>`、`<img onerror>`、`javascript:`/`data:` 链接、属性引号逃逸、嵌套反引号）、GFM 表格、代码块内 Markdown 不解析。
- 覆盖率 ≥ 85%。

**验收**：`make check` 通过。手工核验（只读，可选）：对真实 herdr 运行 `List`、`Get`、`Read` 各一次，确认解析正常，把结果摘要写进报告（不要提交真实输出，不要调用 `Prompt`）。若需实测 `Prompt` 的真实输出格式，先在报告里提出，由 leader 征得用户同意后另行安排。

---

## 任务 E：集成联调（A–D 合入后）

**合入顺序**：D → A → B（rebase 到含 A、D 的 main）→ C。各分支合入前在 worktree 内 `make check` 通过；合入采用 `git merge --no-ff`（本地 main，不 push）。

**内容**：
1. `cmd/leader-console/main.go`：`config` → `store.NewFS` → `herdr.NewExec(cfg.HerdrCmd, 5s)` → `service.New(Deps{... Reconcile: 适配 reconcile.Project, Redact: redact.String, Render: render.Markdown, ToMerge: ledgercompat.LikelyToMerge, Now: time.Now})` → `go svc.Run(ctx)` → `server.New(svc, web.Static(), ...)`；启动日志打印访问地址（不打印 token）。
2. 端到端脚本 `scripts/e2e.sh`：复制 `testdata/fixtures/leader-root` 到临时目录 → 用假 herdr 启动 → curl 走一遍：overview → 取 token（从首页 meta 解析）→ 作答 → 轮询 answers.jsonl 出现 delivered → `FAKE_HERDR_LOG` 中有【决策回传】行 → 手工追加 decision_ack 事件 → overview 中状态变为 acked。加入 `make e2e`。
3. README 补充真实使用说明；`make run` 对真实 `~/.leader` 只读验证一次（过渡期 ledger.md 项目应正常显示，作答按钮不可用）。

**验收**：`make check` + `make e2e` 通过；PRD §11 验收项 1、3、4、5 在本机核验并记录；验收项 2（端到端 ≤ 30s）需要 leader 协议上线后由 leader 实测。

## 契约变更流程

1. 发现契约（model、冻结接口、API 形状）不够用时，不要自行修改冻结文件；在自己包内用临时适配绕过，并在报告中写“契约变更请求”：动机、建议改法、影响的任务。
2. leader 汇总后在 main 上修改契约 + 文档 + 夹具，并通知所有进行中的任务 rebase。
