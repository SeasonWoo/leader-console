# Ignore unrelated and malformed tables
| item | status |
|---|---|
| not-a-task | done |

| name | 状态 |
this is not a separator
| phantom | done |

| result | status | name | goal | kind | pane | worktree | updated | notified |
|:---|---:|:---:|---|---|---|---|---|---|
| 等用户确认提交 /tmp/a.md | DONE（完成） | a | 转义 \| 竖线 | CODEX | w1:pA | branch-a | 2026-10-21 09:30 | 2026-10-21 10:00（已告知） |
| 等待用户授权 MR | ??? | a | second | Other | wS:p1（已关闭） | /work/tree | bad | bad |
| 需要用户决定 | blocked | a#2 | collision | claude | w9:pZ | 无（只读） | | |
| 待用户确认 | done | a | third | codex | w9:pX | ~/work | | |
| result | working | extra | goal | codex | w2:pA | branch | 2026-10-21 11:30 | note | extra | tail |
| short | reviewing | short |
| ignored | working | | | | | | | |

## 备注
| name | status |
|---|---|
| never | done |
