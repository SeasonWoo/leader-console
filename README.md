# leader-console

本机 leader 工作流管控面：汇总 `~/.leader/*/` 下所有 leader 项目的任务、待决策事项和结果，在浏览器里查看，并可以直接作答、把决策回传给 leader。

- 只监听 127.0.0.1，数据不出本机；Go 单二进制，零第三方依赖，前端无构建链（`embed` 打包）。
- 当前状态：**骨架**。只实现了 `/healthz` 和占位首页；业务功能按 [docs/tasks.md](docs/tasks.md) 并行开发。

## 文档

| 文档 | 内容 |
|---|---|
| [docs/PRD.md](docs/PRD.md) | 需求、MVP 范围、页面原型 |
| [docs/design.md](docs/design.md) | 架构、模块、数据契约、API、状态机、并发、安全、测试 |
| [docs/leader-protocol.md](docs/leader-protocol.md) | leader 规则改造草案（待用户审批） |
| [docs/tasks.md](docs/tasks.md) | MVP 并行任务拆分 |

## 环境

- Go 1.24+（`go version`）
- macOS / Linux
- 运行时可选：`herdr`（读取 agent 实时状态、回传决策）

## 构建与运行

```bash
make build          # 产出 bin/leader-console
make run            # 读取真实 ~/.leader，监听 127.0.0.1:7788
make run-fixture    # 读取 testdata 夹具 + 假 herdr，开发前端/联调用
make test           # 单元测试
make check          # 合并前门禁：gofmt + vet + race 测试 + build
```

启动后用浏览器访问 <http://127.0.0.1:7788/>；存活检查：

```bash
curl -s http://127.0.0.1:7788/healthz
# {"status":"ok","uptime_sec":3,"version":"dev"}
```

## 配置

| flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `LC_ADDR` | `127.0.0.1:7788` | 监听地址，只允许回环地址 |
| `-leader-root` | `LC_LEADER_ROOT` | `~/.leader` | leader 数据根目录 |
| `-herdr-cmd` | `LC_HERDR_CMD` | `herdr` | herdr 命令前缀（按空格切分，如 `herdr --session work`） |
| `-scan-interval` | — | `2s` | 扫描数据目录的间隔 |
| `-herdr-interval` | — | `5s` | 刷新 `herdr agent list` 的间隔 |

## 目录

```
cmd/leader-console/   入口
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
```
