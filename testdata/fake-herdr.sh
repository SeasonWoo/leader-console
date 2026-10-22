#!/bin/sh
# 测试/本地开发用的假 herdr：只模拟 leader-console 用到的子命令，输出格式对齐 herdr 0.9.3。
# 用法：LC_HERDR_CMD="$PWD/testdata/fake-herdr.sh" make run-fixture
# 环境变量 FAKE_HERDR_FAIL=1 时所有命令返回 agent_not_found。
# FAKE_HERDR_STATE=<目录> 时按其中的文件改写 leader（w1:pA）的现场，用于 e2e 模拟忙闲与切换：
#   leader_status（idle/working/blocked，默认 working）、leader_kind（claude/codex，codex 无 session）、
#   leader_name（默认 leader-demo，空文件表示名字丢失）、leader_gone（存在即 leader 已退出）。
set -eu
err() { printf '{"error":{"code":"%s","message":"%s"},"id":"cli:agent:%s"}\n' "$1" "$2" "$3"; exit 1; }
[ "${FAKE_HERDR_FAIL:-}" = "1" ] && err agent_not_found "fake failure" "${2:-x}"
[ "${1:-}" = "agent" ] || err exec_failed "unsupported" "x"

st=${FAKE_HERDR_STATE:-}
opt() { if [ -n "$st" ] && [ -f "$st/$1" ]; then cat "$st/$1"; else printf '%s' "$2"; fi; }
status=$(opt leader_status working)
kind=$(opt leader_kind claude)
lname=$(opt leader_name leader-demo)
gone=
[ -n "$st" ] && [ -f "$st/leader_gone" ] && gone=1
session=
[ "$kind" = claude ] && session=',"agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"e4916e66-03ee-408d-88c3-ff6c201bf695"}'
name=
[ -n "$lname" ] && name=',"name":"'"$lname"'"'
leader_list='{"agent":"'"$kind"'"'"$session"',"agent_status":"'"$status"'","cwd":"/Users/me/work/dev/tasks/demo","focused":true,"foreground_cwd":"/Users/me/work/dev/tasks/demo"'"$name"',"pane_id":"w1:pA","revision":3,"state_change_seq":177,"tab_id":"w1:t9","terminal_id":"term_1","terminal_title":"◐ leader","terminal_title_stripped":"leader","workspace_id":"w1"},'
[ -n "$gone" ] && leader_list=
case "${2:-}" in
list)
  printf '%s' '{"id":"cli:agent:list","result":{"agents":['"$leader_list"
  cat <<'JSON'
{"agent":"codex","agent_status":"done","completion_seq":5,"cwd":"/Users/me/.herdr/worktrees/copilot_marscode/feat-uih","focused":false,"foreground_cwd":"/Users/me/.herdr/worktrees/copilot_marscode/feat-uih","interactive_ready":true,"name":"uih-copilot","pane_id":"w1:pP","revision":3,"state_change_seq":20,"tab_id":"w1:tP","terminal_id":"term_2","terminal_title":"✳ copilot header","terminal_title_stripped":"copilot header","workspace_id":"w1"},{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"609d3872-d636-41a8-bcfe-646956294f44"},"agent_status":"working","cwd":"/Users/me/work/fe","focused":false,"foreground_cwd":"/Users/me/work/fe","interactive_ready":true,"name":"uih-fe","pane_id":"w1:pR","revision":3,"state_change_seq":30,"tab_id":"w1:tR","terminal_id":"term_3","terminal_title":"◐ 补保存期间禁用复选框的单测","terminal_title_stripped":"补保存期间禁用复选框的单测","workspace_id":"w1"}]}}
JSON
  ;;
get)
  case "${3:-}" in
  "$lname"|w1:pA)
    [ -z "$gone" ] || err agent_not_found "agent target ${3:-} not found" get
    [ "${3:-}" = w1:pA ] || [ -n "$lname" ] || err agent_not_found "agent target ${3:-} not found" get
    echo '{"id":"cli:agent:get","result":{"agent":{"agent":"'"$kind"'"'"$session"',"agent_status":"'"$status"'","cwd":"/Users/me/work/dev/tasks/demo"'"$name"',"pane_id":"w1:pA","tab_id":"w1:t9","terminal_title_stripped":"leader","workspace_id":"w1"},"type":"agent_info"}}' ;;
  *) err agent_not_found "agent target ${3:-} not found" get ;;
  esac
  ;;
read)
  printf '\033[1m● 正在跑单测\033[0m\n  token=sk-FAKEFAKEFAKEFAKEFAKEFAKE\n> \n'
  ;;
prompt)
  # 记录收到的 prompt，便于测试断言
  [ -n "${FAKE_HERDR_LOG:-}" ] && printf '%s\t%s\n' "${3:-}" "${4:-}" >> "$FAKE_HERDR_LOG"
  # 注意：真实 herdr prompt 成功时的 stdout 未实测（本任务禁止发送 prompt），适配层只依赖退出码 0。
  echo '{"id":"cli:agent:prompt","result":{"type":"prompt_submitted"}}'
  ;;
*) err exec_failed "unsupported subcommand ${2:-}" x ;;
esac
