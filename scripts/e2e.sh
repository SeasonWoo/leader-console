#!/bin/sh
# 端到端：夹具副本 + 假 herdr 启动真实二进制，用 curl 走一遍
# overview → 取 token → 作答 → answers.jsonl 记 delivered → 假 herdr 收到【决策回传】→ 追加 decision_ack → acked；
# 再走定时【定期汇报】：忙/身份不符/离线不投 → 空闲投一次 → 同周期不重复 → 重启不补发 → claude→codex 切换被识别
# → 无 session 的 codex 名字丢失时不按 pane 兜底。
# 用法：make e2e（或 scripts/e2e.sh [二进制路径]）。不读真实 ~/.leader，不调用真实 herdr。
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=${1:-$ROOT/bin/leader-console}
ADDR=${E2E_ADDR:-127.0.0.1:17799}
BASE=http://$ADDR
PROJECT=demo
DECISION=d-20261021-001

for tool in curl jq; do
	command -v "$tool" >/dev/null || { echo "e2e: 需要 $tool" >&2; exit 1; }
done
[ -x "$BIN" ] || { echo "e2e: 找不到可执行文件 ${BIN}（先 make build）" >&2; exit 1; }

tmpbase=${TMPDIR:-/tmp}
TMP=$(mktemp -d "${tmpbase%/}/lc-e2e.XXXXXX")
PID=
cleanup() {
	[ -n "$PID" ] && kill "$PID" 2>/dev/null && wait "$PID" 2>/dev/null || true
	rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

step() { printf '==> %s\n' "$*"; }
fail() {
	printf 'e2e FAIL: %s\n' "$*" >&2
	printf -- '--- server log\n' >&2
	cat "$TMP/server.log" >&2 || true
	exit 1
}
# wait_for <秒> <描述> <命令...>：每 0.2s 重试命令直到成功。
wait_for() {
	secs=$1 desc=$2
	shift 2
	n=$((secs * 5))
	while [ "$n" -gt 0 ]; do
		"$@" >/dev/null 2>&1 && return 0
		n=$((n - 1))
		sleep 0.2
	done
	fail "等待超时（${secs}s）：$desc"
}
overview() { curl -fsS "$BASE/api/v1/overview"; }
decision_state() {
	overview | jq -r --arg p "$PROJECT" --arg d "$DECISION" \
		'.decisions[] | select(.project == $p and .decision.id == $d) | .state'
}
state_is() { [ "$(decision_state)" = "$1" ]; }
post_answer() { # post_answer <token> <json> → 输出 "<http_code> <body>"
	curl -sS -o "$TMP/resp.json" -w '%{http_code}' -X POST \
		"$BASE/api/v1/decisions/$PROJECT/$DECISION/answer" \
		-H "X-LC-Token: $1" -H "Origin: $BASE" -H 'Content-Type: application/json' \
		--data "$2"
}

step "准备夹具副本 $TMP/root"
cp -R "$ROOT/testdata/fixtures/leader-root" "$TMP/root"
# 夹具心跳是固定时间，刷新为当前时间，leader 才判定在线、回答才会投递。
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
leader_json=$TMP/root/$PROJECT/leader.json
sed "s/\"heartbeat_at\": *\"[^\"]*\"/\"heartbeat_at\": \"$now\"/" "$leader_json" >"$leader_json.new"
mv "$leader_json.new" "$leader_json"
# Intentionally unordered completed tasks exercise the real store/service/API path.
state_json=$TMP/root/$PROJECT/state.json
jq --arg now "$now" '.tasks += [
  {id:"sort-b", name:"sort-b", status:"done", updated_at:"2020-01-01T00:00:00Z"},
  {id:"sort-new", name:"sort-new", status:"done", updated_at:$now},
  {id:"sort-old", name:"sort-old", status:"done", updated_at:"2019-01-01T00:00:00Z", created_at:$now},
  {id:"sort-fallback", name:"sort-fallback", status:"done", created_at:"2021-01-01T00:00:00Z"},
  {id:"sort-a", name:"sort-a", status:"done", updated_at:"2020-01-01T08:00:00+08:00"}
]' "$state_json" >"$state_json.new"
mv "$state_json.new" "$state_json"

HS=$TMP/herdr-state # 假 herdr 的 leader 现场开关，见 testdata/fake-herdr.sh
mkdir -p "$HS"
start_server() {
	XDG_STATE_HOME=$TMP/state FAKE_HERDR_LOG=$TMP/herdr.log FAKE_HERDR_STATE=$HS \
		"$BIN" -addr "$ADDR" -leader-root "$TMP/root" -herdr-cmd "$ROOT/testdata/fake-herdr.sh" \
		-scan-interval 300ms -herdr-interval 1s -report-grace 0s -report-startup-delay 2s >>"$TMP/server.log" 2>&1 &
	PID=$!
	wait_for 15 "healthz" curl -fsS "$BASE/healthz"
}
step "启动 ${BIN}（${ADDR}，假 herdr）"
start_server

step "overview：项目、leader 在线、herdr 可用、决策 open"
ov=$(overview)
[ "$(printf '%s' "$ov" | jq -r '[.projects[].name] | join(",")')" = "demo,legacy-demo" ] || fail "项目列表不符：$ov"
[ "$(printf '%s' "$ov" | jq -r '.herdr.ok')" = "true" ] || fail "herdr 不可用"
[ "$(printf '%s' "$ov" | jq -r '.projects[] | select(.name=="demo") | .leader.online')" = "true" ] ||
	fail "demo leader 不在线：$(printf '%s' "$ov" | jq -c '.projects[] | select(.name=="demo") | .leader')"
[ "$(printf '%s' "$ov" | jq -r '.projects[] | select(.name=="legacy-demo") | .source')" = "ledger.md" ] || fail "legacy-demo 不是 ledger.md 来源"
state_is open || fail "$DECISION 初始状态不是 open：$(decision_state)"

step "看板排序：更新时间倒序、创建时间回退、同时间按 ID（含筛选）"
for query in '' '?project=demo' '?column=done' '?project=demo&column=done'; do
	order=$(curl -fsS "$BASE/api/v1/tasks$query" | jq -r \
		'[.tasks[] | select(.task.id | startswith("sort-")) | .task.id] | join(",")')
	[ "$order" = 'sort-new,sort-fallback,sort-a,sort-b,sort-old' ] || fail "排序不符（$query）：$order"
done

step "详情 / 报告 / 终端：渲染与脱敏已接线"
detail=$(curl -fsS "$BASE/api/v1/tasks/$PROJECT/uih-copilot")
report_id=$(printf '%s' "$detail" | jq -r '.reports[0].id')
[ -n "$report_id" ] && [ "$report_id" != null ] || fail "uih-copilot 没有报告"
curl -fsS "$BASE/api/v1/tasks/$PROJECT/uih-copilot/reports/$report_id" | jq -e '.html | test("<(h[1-4]|p|table|ul)>")' >/dev/null ||
	fail "报告没有渲染为 HTML"
term=$(curl -fsS "$BASE/api/v1/tasks/$PROJECT/uih-fe/terminal" | jq -r '.text')
case $term in *sk-FAKEFAKE*) fail "终端输出未脱敏：$term" ;; esac
case $term in *正在跑单测*) ;; *) fail "终端输出不完整：$term" ;; esac

step "SSE 首帧"
curl -sS -N --max-time 2 "$BASE/api/v1/stream" >"$TMP/sse.txt" 2>/dev/null || true
grep -q '^data:' "$TMP/sse.txt" || fail "SSE 无首帧：$(cat "$TMP/sse.txt")"

step "从首页 meta 取 token"
curl -fsS "$BASE/" >"$TMP/index.html"
token=$(sed -n 's/.*name="lc-token" content="\([0-9a-f]*\)".*/\1/p' "$TMP/index.html")
[ "${#token}" -eq 64 ] || fail "首页 token 未注入：$(grep lc-token "$TMP/index.html")"

step "鉴权：缺 token 写请求 403"
code=$(post_answer "" '{"option":"A","client_request_id":"c-e2e-noauth"}')
[ "$code" = 403 ] || fail "缺 token 返回 $code"

step "作答 $PROJECT/$DECISION → 202"
body='{"option":"A","text":"e2e：提交并提 MR","client_request_id":"c-e2e-1"}'
code=$(post_answer "$token" "$body")
[ "$code" = 202 ] || fail "作答返回 ${code}：$(cat "$TMP/resp.json")"
answer_id=$(jq -r '.answer.answer_id' "$TMP/resp.json")
case $answer_id in a-*) ;; *) fail "响应缺 answer_id：$(cat "$TMP/resp.json")" ;; esac

step "幂等重放同一 client_request_id → 同一 answer_id；换请求 → 409"
code=$(post_answer "$token" "$body")
[ "$code" = 202 ] && [ "$(jq -r '.answer.answer_id' "$TMP/resp.json")" = "$answer_id" ] || fail "幂等重放返回 ${code}：$(cat "$TMP/resp.json")"
code=$(post_answer "$token" '{"option":"B","client_request_id":"c-e2e-2"}')
[ "$code" = 409 ] || fail "重复作答返回 $code"

answers=$TMP/root/$PROJECT/answers.jsonl
step "answers.jsonl 落盘 answer 行"
grep -q "\"answer_id\":\"$answer_id\"" "$answers" || fail "answers.jsonl 无 $answer_id"
delivered() {
	jq -e --arg a "$answer_id" 'select(.type=="delivery" and .answer_id==$a and .delivery=="delivered")' "$answers" | grep -q .
}
wait_for 20 "answers.jsonl 出现 delivered" delivered

step "假 herdr 收到【决策回传】prompt"
grep -q "^leader-demo	【决策回传】project=$PROJECT decision=$DECISION answer=$answer_id " "$TMP/herdr.log" ||
	fail "FAKE_HERDR_LOG 无对应 prompt：$(cat "$TMP/herdr.log" 2>/dev/null)"
[ "$(grep -c "answer=$answer_id" "$TMP/herdr.log")" -eq 1 ] || fail "prompt 投递不止一次"
wait_for 10 "overview 状态 delivered" state_is delivered

step "模拟 leader 追加 decision_ack → acked"
printf '{"ts":"%s","type":"decision_ack","actor":"leader","task_id":"uih-copilot","decision_id":"%s","answer_id":"%s","summary":"收到：A 提交并提 MR"}\n' \
	"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$DECISION" "$answer_id" >>"$TMP/root/$PROJECT/events.jsonl"
wait_for 10 "overview 状态 acked" state_is acked

leader_json_set() { # leader_json_set <jq 表达式> [--arg k v ...]：整文件替换 leader.json，模拟 leader 写入
	expr=$1
	shift
	jq "$@" "$expr" "$leader_json" >"$leader_json.tmp" && mv "$leader_json.tmp" "$leader_json"
}
leader_field() { overview | jq -r --arg p "$PROJECT" ".projects[] | select(.name == \$p) | .leader | $1"; }
field_is() { [ "$(leader_field "$1")" = "$2" ]; }
reports_sent() { grep -c "【定期汇报】project=$PROJECT source=leader-console" "$TMP/herdr.log" || true; }
sent_is() { [ "$(reports_sent)" = "$1" ]; }
panel=$TMP/root/$PROJECT/panel.jsonl
ago=$(date -u -v-11M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '11 minutes ago' +%Y-%m-%dT%H:%M:%SZ)

step "定期汇报：心跳已过一个周期，但 leader 正忙（working）→ 暂缓"
leader_json_set '.heartbeat_at = $t' --arg t "$ago"
wait_for 10 "report.waiting=leader_busy" field_is '.report.waiting // ""' leader_busy
sent_is 0 || fail "忙时投了定期汇报：$(cat "$TMP/herdr.log")"

step "同 pane 换成 codex、leader.json 仍是 claude → 身份不符，不投"
printf idle >"$HS/leader_status"
printf codex >"$HS/leader_kind"
wait_for 10 "reason=agent_kind_mismatch" field_is .reason agent_kind_mismatch
wait_for 20 "report.waiting=not_identified" field_is '.report.waiting // ""' not_identified
sent_is 0 || fail "身份不符时投了定期汇报"

step "leader 已退出 → 离线，不投"
rm -f "$HS/leader_kind"
touch "$HS/leader_gone"
wait_for 10 "reason=agent_not_found" field_is .reason agent_not_found
sleep 3
sent_is 0 || fail "离线时投了定期汇报"

step "leader 回到空闲且身份核验通过 → 投递一次"
rm -f "$HS/leader_gone"
wait_for 25 "收到【定期汇报】" sent_is 1
grep -q "^leader-demo	【定期汇报】project=$PROJECT source=leader-console$" "$TMP/herdr.log" || fail "汇报 target/文本不符：$(cat "$TMP/herdr.log")"
jq -e 'select(.type == "report" and .delivery == "delivered" and .target == "leader-demo")' "$panel" | grep -q . || fail "panel.jsonl 无投递记录"
[ "$(jq -r '.heartbeat_at' "$leader_json")" = "$ago" ] || fail "面板改写了 heartbeat"

step "同一周期不重复（leader 未刷新心跳也不叠加）"
sleep 3
sent_is 1 || fail "同周期重复投递：$(reports_sent)"
field_is .report.unacked 1 || fail "unacked 不符：$(leader_field .report)"

step "重启面板不补发"
kill "$PID" && wait "$PID" 2>/dev/null || true
PID=
start_server
sleep 4
sent_is 1 || fail "重启后补发了定期汇报"
[ "$(jq -c 'select(.type == "leader")' "$panel" | wc -l | tr -d ' ')" = 1 ] || fail "重启后重复记录 leader 身份：$(cat "$panel")"

step "leader 切换 claude→codex：新 leader 写 leader.json 后面板识别并标注"
printf codex >"$HS/leader_kind"
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
leader_json_set '.leader.agent = "codex" | del(.leader.session_id) | .started_at = $t | .heartbeat_at = $t' --arg t "$now"
wait_for 10 "leader.change.switched" field_is '[.online, .agent, .change.switched, .change.from.agent, .change.to.agent] | join(",")' 'true,codex,true,claude,codex'
sent_is 1 || fail "切换后立刻补发了定期汇报"

step "codex leader 名字丢失（无 session）→ 不按 pane 兜底，判离线；恢复名字后重新在线"
: >"$HS/leader_name"
wait_for 10 "reason=agent_not_found" field_is '[.reason, .identified] | join(",")' 'agent_not_found,false'
rm -f "$HS/leader_name"
wait_for 10 "名字恢复后在线" field_is '[.online, .via] | join(",")' 'true,name'

step "PASS"
