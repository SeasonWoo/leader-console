# Leader 台账（legacy-demo）

| name | kind | pane | worktree | 目标 | 状态 | 结果 | 更新时间 | 已通知 |
|------|------|------|----------|------|------|------|----------|--------|
| cloud-compat | claude | w1:pB | 无（只读，master） | 排查云端兼容性 | done | /tmp/cloud-compat.md：CLI 无 kube 依赖 | 2026-10-20 22:45 | 2026-10-20 22:45（汇报安全发现） |
| sec-scrub | codex | wH:p1 | ~/.herdr/worktrees/x/fix-secrets | 删除明文凭证 | failed | 429 限流，改派给 sec-scrub-c | 2026-10-20 23:15 | |
| bits-qa | codex | wP:p1 | fix-bits-qa | QA 默认不填 | done | /tmp/bits-qa.md：只改了 SKILL.md；等用户确认提交 | 2026-10-20 23:15 | |
| devx-fix | codex | wS:p1（已关闭） | fix-devx | 适配 0.1.57 | failed | Codex 卡住，改派 devx-fix-c | 2026-10-21 12:25 | |
| panic-fix | codex | wR:p1 | fix-panic | 修复 panic | lost | herdr 重启后会话没有恢复 | 2026-10-21 11:55 | |
| lc-arch | claude | w1:pW | /Users/me/leader-console | 技术方案 | working | /tmp/lc-arch.md | 2026-10-21 15:55 | |

## 备注
- 2026-10-20 首次创建台账。表格之后的段落不是任务。

## 复盘
- cloud-compat｜claude｜一次通过验收｜无返工
