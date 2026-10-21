# leader-console

本机 leader 工作流管控面：汇总 `~/.leader/*/` 下所有 leader 项目的任务、待决策事项和结果，在浏览器里查看，并可以直接作答、把决策回传给 leader。

- 只监听 127.0.0.1，数据不出本机；Go 单二进制，Go 侧零第三方依赖。前端是 Next.js 静态导出 + Tailwind CSS（`frontend/`），构建产物 `web/dist` 入库并 `embed` 进二进制，运行时不需要 Node。
- 当前状态：**MVP 已集成**（任务 A–E 合入 main）：待我决定、任务看板、任务详情三个页面，作答写入 `answers.jsonl` 并经 herdr 回传 leader。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/PRD.md](docs/PRD.md) | 需求、MVP 范围、页面原型 |
| [docs/design.md](docs/design.md) | 架构、模块、数据契约、API、状态机、并发、安全、测试 |
| [docs/leader-protocol.md](docs/leader-protocol.md) | leader 规则改造（已批准并合入 leader.md） |
| [docs/tasks.md](docs/tasks.md) | MVP 并行任务拆分 |

## 环境

- Go 1.24+（`go version`）
- 前端构建（`make build/test/e2e`）：Node 22.18+、pnpm 10.34.7（没装 pnpm 时 Makefile 自动用 `npx pnpm@10.34.7`）。只编译 Go 用 `make go-build`，不需要 Node
- `make e2e` 另需 curl、jq；浏览器阶段需要本机 Chrome / Chromium（找不到时跳过并提示，`E2E_REQUIRE_BROWSER=1` 时改为失败）
- macOS / Linux
- 运行时可选：`herdr`（读取 agent 实时状态、回传决策）

## 构建与运行

```bash
make build          # 构建前端（frontend/ → web/dist）并产出 bin/leader-console（前端已嵌入二进制）
make go-build       # 只编译 Go，使用已入库的 web/dist（不需要 Node）
make run            # 读取真实 ~/.leader + 真实 herdr，监听 127.0.0.1:7788
make run-fixture    # 读取 testdata 夹具 + 假 herdr，开发前端/联调用
make test           # 构建前端 + 前端类型检查与单元测试 + Go 单元测试
make check          # 合并前门禁：gofmt + vet + 前端测试 + race 测试 + build + web/dist 与源码一致
make e2e            # 端到端：夹具副本 + 假 herdr 启动二进制，curl 走作答回传全链路 + 无头 Chrome 走查页面
make web-dev        # 前端开发服务器 http://127.0.0.1:17810/?mock=1（内存 mock 数据，不连后端）
```

改了 `frontend/` 后要 `make web` 并提交 `web/dist`（`make web-verify` / `make check` 会检查两者一致）。产物入库是为了干净 clone 后不装 Node 也能 `go build` / `go install` 得到完整的二进制；Next 构建是确定性的（固定 buildId、内容哈希文件名），同一源码重复构建产物逐字节相同。

启动后用浏览器访问 <http://127.0.0.1:7788/>：`/` 待我决定、`/board/?project=` 看板、`/task/?project=<project>&id=<id>&report=` 详情（旧的 `#/decisions`、`#/board`、`#/task/<project>/<id>` 链接会自动跳到新地址）。URL 加 `?mock=1`（或 `?mock=herdr-down`）使用前端内存 mock 数据，不读写真实数据。存活检查：

```bash
curl -s http://127.0.0.1:7788/healthz
# {"status":"ok","uptime_sec":3,"version":"dev"}
curl -s http://127.0.0.1:7788/api/v1/overview   # 项目、leader 在线状态、未关闭决策
```

## 使用说明

- **数据来源**：每 `-scan-interval` 扫描一次 `<leader-root>/*/`。有 `state.json` 的项目按 leader 协议读取（`leader.json`、`decisions.jsonl`、`events.jsonl`、`answers.jsonl`、`reports/`）；只有旧格式 `ledger.md` 的项目为**过渡期只读**：任务表照常显示在看板，台账里像“等用户确认”的行显示在“疑似待确认”区，不能在面板作答，请在 leader 对话里回答。
- **任务摘要**：看板分别展示台账中的任务目标（`goal`）和进展（`result_summary`）；worker 终端标题可能被多个任务共用或沿用旧任务，只在任务详情中以“终端标题”展示，不作为任务摘要。
- **看板排序**：每列按 `updated_at` 倒序，缺失时使用 `created_at`；时间相同按任务 `id` 升序，同 ID 跨项目再按项目名升序。新完成的任务显示在已完成列顶部；该列默认只看最近 24h，可展开全部。
- **唯一的写操作**：在“待我决定”作答时追加一行到 `<leader-root>/<project>/answers.jsonl`。其他文件只读。
- **决策回传**：作答后后台通过 `herdr agent prompt <leader> "【决策回传】…"` 投递给该项目的 leader，投递结果也记在 `answers.jsonl`。leader 离线时回答排队，上线后自动投递；连续失败 5 次标记失败，可在页面上“重试投递”。leader 写入 `decision_ack` / `decision_executing` / `decision_done` 事件后，页面进度条随之推进。
- **安全**：只接受回环地址和 `Host` 为 127.0.0.1 / localhost / [::1] 的请求；写接口需要进程启动时随机生成的 token（服务端注入每个页面 `<head>` 开头的 `<meta name="lc-token">`，不打印到日志），并校验 `Origin`。所有展示文本先脱敏，Markdown 渲染为白名单 HTML。CSP 为 `script-src 'self'` 加上该页面自带内联脚本（Next 引导脚本）的 sha256，不放开 `unsafe-inline`；`style-src 'self'`。
- **外观**：浅色 / 深色跟随系统，侧栏左下角可手动切换（存在浏览器 localStorage）。
- **单实例**：同一用户只能运行一个实例，锁文件为 `$XDG_STATE_HOME/leader-console/instance.lock`（未设置时为 `~/.local/state/leader-console/instance.lock`）。已有实例时启动会报“已有实例在运行”并退出。
- **herdr 不可用**时页面顶部提示，任务不判 lost，回答排队等待。

## 配置

| flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `LC_ADDR` | `127.0.0.1:7788` | 监听地址，只允许回环地址 |
| `-leader-root` | `LC_LEADER_ROOT` | `~/.leader` | leader 数据根目录 |
| `-herdr-cmd` | `LC_HERDR_CMD` | `herdr` | herdr 命令前缀（按空格切分，如 `herdr --session work`） |
| `-scan-interval` | — | `2s` | 扫描数据目录的间隔 |
| `-herdr-interval` | — | `5s` | 刷新 `herdr agent list` 的间隔 |
| — | `XDG_STATE_HOME` | `~/.local/state` | 单实例锁所在目录的上级（`leader-console/instance.lock`） |

## 目录

```
cmd/leader-console/   入口：装配 store / herdr / reconcile / redact / render / service / server
internal/model/       数据契约（磁盘文件 + API 视图），冻结
internal/config/      启动配置
internal/store/       读取 ~/.leader、追加 answers.jsonl（任务 A）
internal/jsonl/       jsonl 加锁追加与容错读取（任务 A）
internal/ledgercompat/ 过渡期 ledger.md 解析（任务 A）
internal/herdr/       herdr CLI 适配（任务 D）
internal/reconcile/   leader 在线判定与 lost 对账（任务 D）
internal/redact/      凭证脱敏（任务 D）
internal/render/      安全 Markdown 渲染（任务 D）
internal/service/     快照、视图推导、决策回传（任务 B）
internal/server/      HTTP API、鉴权、SSE（任务 B）
frontend/             前端源码：Next.js（App Router，TypeScript，静态导出）+ Tailwind CSS v4
  src/app/            页面薄壳：/（待我决定）、/board、/task；layout 与全局样式（设计令牌）
  src/features/       三个页面的实现（decisions / board / task）
  src/components/     布局、全局状态（SSE / 轮询 / toast）、基础组件、ServerHTML（唯一插入服务端 HTML 处）
  src/lib/            与 Go model 对齐的类型、API 客户端、mock 后端、格式化、路由
  src/mock/           mock 数据（web_test.go 按 Go 视图契约严格解码）
  tests/              node --test 单元测试
web/                  web.go 嵌入 dist/（frontend 构建产物，入库）
testdata/             共享夹具与假 herdr
scripts/e2e.sh        端到端脚本（make e2e）；e2e-browser.mjs 为其浏览器阶段，screenshots.mjs 用于截图（均基于 cdp.mjs，无第三方依赖）
```
