"use client";

// 待我决定（/?project=&focus=）。PRD §7.1，状态推导见 design §7.1。

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useReducer, useRef, useState } from "react";
import { ChevronRight, CircleCheck, History, Inbox } from "lucide-react";
import { useConsole } from "@/components/console-provider";
import { PageSkeleton } from "@/components/shell-fallback";
import { Card, EmptyState, ErrorState, PageHeader, SectionTitle, Status } from "@/components/ui";
import { CLOSED_STATES, DECISION_STATE } from "@/lib/labels";
import { taskHref } from "@/lib/routes";
import type { DecisionView } from "@/lib/types";
import { cn } from "@/lib/cn";
import { DecisionCard } from "./decision-card";
import { keyOf, withOptimistic } from "./store";

export function DecisionsView() {
  const sp = useSearchParams();
  const project = sp.get("project") || "";
  const focusParam = sp.get("focus") || "";
  const { overview, error, refresh } = useConsole();
  const [, rerender] = useReducer((x: number) => x + 1, 0);
  const [closedOpen, setClosedOpen] = useState(false);
  const [focused, setFocused] = useState("");
  const focusHandled = useRef("");

  const all = overview ? withOptimistic(overview.decisions.filter((v) => !project || v.project === project)) : [];
  const active = all.filter((v) => !CLOSED_STATES.has(v.state));
  const closed = all.filter((v) => CLOSED_STATES.has(v.state));
  const open = active.filter((v) => v.state === "open").length;

  // ?focus=<id>：定位并高亮一次；已关闭的决策自动展开折叠区。
  useEffect(() => {
    if (!overview || !focusParam || focusHandled.current === focusParam) return;
    const v = overview.decisions.find((d) => d.decision.id === focusParam && (!project || d.project === project));
    if (!v) return;
    focusHandled.current = focusParam;
    const key = keyOf(v);
    if (CLOSED_STATES.has(v.state)) setClosedOpen(true);
    setFocused(key);
    requestAnimationFrame(() =>
      document.querySelector(`[data-key="${CSS.escape(key)}"]`)?.scrollIntoView({ block: "center", behavior: "smooth" }),
    );
  }, [overview, focusParam, project]);

  if (!overview) return error ? <ErrorState message={error} onRetry={() => void refresh()} /> : <PageSkeleton />;

  const hints = overview.legacy_hints.filter((x) => !project || x.project === project);

  return (
    <div className="lc-fade-in mx-auto max-w-[880px]">
      <PageHeader
        title="待我决定"
        subtitle={
          <>
            {project ? <span className="text-fg-2">{project} · </span> : null}
            {open} 条待回答{active.length - open > 0 ? ` · ${active.length - open} 条回传中` : ""}
          </>
        }
        actions={<span className="hidden text-[12px] text-faint sm:inline">待回答优先 · 紧急度 · 截止时间</span>}
      />

      {active.length ? (
        <div className="space-y-4">
          {active.map((v) => (
            <DecisionCard key={keyOf(v)} v={v} onChanged={rerender} focused={focused === keyOf(v)} />
          ))}
        </div>
      ) : (
        <Card>
          <EmptyState
            icon={<CircleCheck />}
            title={project ? `${project} 没有待你决定的事项` : "没有待你决定的事项"}
            hint="leader 提出新的决策时会实时出现在这里。"
          />
        </Card>
      )}

      <ClosedSection list={closed} open={closedOpen} onToggle={setClosedOpen} focused={focused} rerender={rerender} />

      {hints.length > 0 && (
        <section className="mt-12">
          <SectionTitle>疑似待确认 · 来自 ledger.md</SectionTitle>
          <Card className="divide-y divide-line">
            <p className="px-5 py-3 text-[12.5px] text-muted">这些项目尚未启用 leader 协议，不能在面板作答，请在 leader 对话中回答。</p>
            {hints.map((x) => (
              <Link
                key={`${x.project}/${x.task_id}`}
                prefetch={false}
                href={taskHref(x.project, x.task_id)}
                className="group flex items-start gap-3 px-5 py-3 text-[13.5px] hover:bg-surface-2/60"
              >
                <Status tone="orange" className="shrink-0 font-medium text-fg-2">
                  {x.project} / {x.task_id}
                </Status>
                <span className="min-w-0 flex-1 text-muted">{x.text}</span>
                <ChevronRight className="mt-0.5 size-4 text-faint group-hover:text-accent" aria-hidden />
              </Link>
            ))}
          </Card>
        </section>
      )}
    </div>
  );
}

function ClosedSection({ list, open, onToggle, focused, rerender }: { list: DecisionView[]; open: boolean; onToggle: (v: boolean) => void; focused: string; rerender: () => void }) {
  const [expanded, setExpanded] = useState<string>("");
  useEffect(() => {
    if (focused && list.some((v) => keyOf(v) === focused)) setExpanded(focused);
  }, [focused, list]);

  return (
    <section className="mt-12">
      <button
        type="button"
        onClick={() => onToggle(!open)}
        aria-expanded={open}
        className="mb-3 flex w-full items-center gap-2 text-left text-[13px] font-semibold tracking-wide text-muted hover:text-fg"
      >
        <ChevronRight className={cn("size-4 transition-transform duration-150", open && "rotate-90")} aria-hidden />
        <History className="size-4" aria-hidden />
        已处理 · 最近 24 小时
        <span className="font-normal text-faint tabular-nums">{list.length}</span>
      </button>
      {open &&
        (list.length ? (
          <Card className="lc-fade-in divide-y divide-line overflow-hidden">
            {list.map((v) => {
              const key = keyOf(v);
              const isOpen = expanded === key;
              return (
                <div key={key}>
                  <button
                    type="button"
                    aria-expanded={isOpen}
                    onClick={() => setExpanded(isOpen ? "" : key)}
                    className="flex w-full items-center gap-3 px-5 py-3 text-left text-[13.5px] hover:bg-surface-2/60"
                  >
                    <ChevronRight className={cn("size-3.5 shrink-0 text-faint transition-transform duration-150", isOpen && "rotate-90")} aria-hidden />
                    <span className="w-28 shrink-0 text-[12.5px] text-muted">{DECISION_STATE[v.state]}</span>
                    <span className="min-w-0 flex-1 truncate text-fg-2">{v.decision.question}</span>
                    <span className="hidden shrink-0 text-[12px] text-faint sm:inline">{v.project}</span>
                  </button>
                  {isOpen && (
                    <div className="bg-surface-2/40 px-5 pt-1 pb-5">
                      <DecisionCard v={v} readonly onChanged={rerender} focused={focused === key} />
                    </div>
                  )}
                </div>
              );
            })}
          </Card>
        ) : (
          <Card>
            <EmptyState icon={<Inbox />} title="最近 24 小时没有已处理的决策" />
          </Card>
        ))}
    </section>
  );
}
