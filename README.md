# leader-console

本机 leader 工作流管控面：汇总 `~/.leader/*/` 下所有 leader 项目的任务、待决策事项和结果，在浏览器里查看，并可以直接作答、把决策回传给 leader。

- 只监听 127.0.0.1，数据不出本机；Go 单二进制，零第三方依赖，前端无构建链（`embed` 打包）。
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
- macOS / Linux
- 运行时可选：`herdr`（读取 agent 实时状态、回传决策）

## 构建与运行

```bash
make build          # 产出 bin/leader-console（前端静态资源已嵌入二进制）
make run            # 读取真实 ~/.leader + 真实 herdr，监听 127.0.0.1:7788
make run-fixture    # 读取 testdata 夹具 + 假 herdr，开发前端/联调用
make test           # 单元测试
make check          # 合并前门禁：gofmt + vet + race 测试 + build
make e2e            # 端到端：夹具副本 + 假 herdr 启动二进制，curl 走作答回传全链路（需 curl、jq）
```

启动后用浏览器访问 <http://127.0.0.1:7788/>（`#/decisions` 待我决定、`#/board` 看板、`#/task/<project>/<id>` 详情）；存活检查：

```bash
curl -s http://127.0.0.1:7788/healthz
# {"status":"ok","uptime_sec":3,"version":"dev"}
curl -s http://127.0.0.1:7788/api/v1/overview   # 项目、leader 在线状态、未关闭决策
```

## 使用说明

- **数据来源**：每 `-scan-interval` 扫描一次 `<leader-root>/*/`。有 `state.json` 的项目按 leader 协议读取（`leader.json`、`decisions.jsonl`、`events.jsonl`、`answers.jsonl`、`reports/`）；只有旧格式 `ledger.md` 的项目为**过渡期只读**：任务表照常显示在看板，台账里像“等用户确认”的行显示在“疑似待确认”区，不能在面板作答，请在 leader 对话里回答。
- **任务摘要**：看板分别展示台账中的任务目标（`goal`）和进展（`result_summary`）；worker 终端标题可能被多个任务共用或沿用旧任务，只在任务详情中以“终端标题”展示，不作为任务摘要。
- **唯一的写操作**：在“待我决定”作答时追加一行到 `<leader-root>/<project>/answers.jsonl`。其他文件只读。
- **决策回传**：作答后后台通过 `herdr agent prompt <leader> "【决策回传】…"` 投递给该项目的 leader，投递结果也记在 `answers.jsonl`。leader 离线时回答排队，上线后自动投递；连续失败 5 次标记失败，可在页面上“重试投递”。leader 写入 `decision_ack` / `decision_executing` / `decision_done` 事件后，页面进度条随之推进。
- **安全**：只接受回环地址和 `Host` 为 127.0.0.1 / localhost / [::1] 的请求；写接口需要进程启动时随机生成的 token（只注入首页 `<meta name="lc-token">`，不打印到日志），并校验 `Origin`。所有展示文本先脱敏，Markdown 渲染为白名单 HTML。
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
web/                  嵌入的前端（任务 C）
testdata/             共享夹具与假 herdr
scripts/e2e.sh        端到端脚本（make e2e）
```
