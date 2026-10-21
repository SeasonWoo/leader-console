"use client";

// 任务详情（/task?project=&id=&report=）。PRD §7.3。

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import type { ReactNode } from "react";
import { ChevronLeft, SearchX } from "lucide-react";
import { useConsole, useResource } from "@/components/console-provider";
import { ExtLink } from "@/components/ext-link";
import { Card, EmptyState, ErrorState, Pill, SectionTitle, Skeleton, Status, type Tone } from "@/components/ui";
import { api, ApiError, errorMessage } from "@/lib/api";
import { fmtClock, fmtDuration, parseTime } from "@/lib/format";
import { AGENT_STATUS, COLUMN_LABEL, DECISION_STATE, EVENT_TYPE, TASK_STATUS } from "@/lib/labels";
import { boardHref, decisionsHref, taskHref } from "@/lib/routes";
import type { Event, TaskDetail } from "@/lib/types";
import { cn } from "@/lib/cn";
import { ReportsPanel } from "./reports-panel";
import { TerminalPanel } from "./terminal-panel";

export function TaskView() {
  const sp = useSearchParams();
  const project = sp.get("project") || "";
  const id = sp.get("id") || "";
  const report = sp.get("report") || "";
  const res = useResource(() => api.task(project, id), [project, id]);

  const back = (
    <Link href={boardHref(project)} className="mb-6 inline-flex items-center gap-0.5 text-[13px] text-accent hover:underline">
      <ChevronLeft className="size-4" aria-hidden />
      任务看板
    </Link>
  );

  if (!project || !id || (res.error instanceof ApiError && res.error.status === 404)) {
    return (
      <div>
        {back}
        <Card>
          <EmptyState icon={<SearchX />} title={`任务 ${project}/${id} 不存在`} hint="它可能已被 leader 从台账中移除。" />
        </Card>
      </div>
    );
  }
  if (res.error && !res.data) {
    return (
      <div>
        {back}
        <Card>
          <ErrorState message={errorMessage(res.error)} onRetry={res.reload} />
        </Card>
      </div>
    );
  }
  if (!res.data) return <DetailSkeleton back={back} />;

  const d = res.data;
  return (
    <div className="lc-fade-in">
      {back}
      <Header d={d} />
      <div className="mt-8 grid gap-5 xl:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
        <Info d={d} />
        <Timeline d={d} />
      </div>
      <div className="mt-5 space-y-5">
        <ReportsPanel project={project} id={id} reports={d.reports} selected={report} />
        <TerminalPanel key={`${project}/${id}`} project={project} id={id} agentFound={d.live.agent_found} />
      </div>
    </div>
  );
}

const COLUMN_TONE: Record<string, Tone> = { in_progress: "accent", blocked: "red", reviewing: "accent", to_merge: "green", done: "green" };
const AGENT_TONE: Record<string, Tone> = { working: "green", blocked: "orange" };

function Header({ d }: { d: TaskDetail }) {
  const t = d.task;
  const live = d.live;
  return (
    <header>
      <p className="text-[13px] text-muted">
        <Link href={boardHref(d.project)} className="hover:text-accent">
          {d.project}
        </Link>
      </p>
      <div className="mt-1 flex flex-wrap items-center gap-3">
        <h1 className="min-w-0 text-[28px] leading-tight font-semibold tracking-[-0.02em] break-all">{t.name || t.id}</h1>
        <Pill tone={COLUMN_TONE[d.column] || "neutral"} dot>
          {COLUMN_LABEL[d.column] || d.column}
        </Pill>
        {(live.lost || t.status === "lost") && <Pill tone="red">LOST</Pill>}
        {t.status === "failed" && <Pill tone="red">失败</Pill>}
      </div>
      <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-muted">
        <span>{[t.kind, t.role, t.pane_id ? `pane ${t.pane_id}` : ""].filter(Boolean).join(" · ")}</span>
        {live.agent_found ? (
          <Status tone={AGENT_TONE[live.agent_status || ""] || "neutral"}>worker {AGENT_STATUS[live.agent_status || ""] || live.agent_status || "未知"}</Status>
        ) : (
          <Status tone="neutral">herdr 中无对应 worker</Status>
        )}
      </p>
    </header>
  );
}

function Info({ d }: { d: TaskDetail }) {
  const { now } = useConsole();
  const t = d.task;
  const live = d.live;
  const times = [
    ["创建", t.created_at],
    ["开始", t.started_at],
    ["更新", t.updated_at],
    ["通知", t.notified_at],
  ]
    .filter(([, v]) => parseTime(v) !== null)
    .map(([k, v]) => `${k} ${fmtClock(v, now)}`)
    .join(" · ");

  const rows: [string, ReactNode][] = [
    ["目标", t.goal],
    ["状态", `${TASK_STATUS[t.status] || t.status}${d.running_for_sec > 0 ? ` · 已运行 ${fmtDuration(d.running_for_sec)}` : ""}`],
    ["进展 / 结果", t.result_summary],
    ["worker 状态", live.lost ? <span className="text-red">herdr 中找不到 worker（只展示，不回写台账）</span> : ""],
    [
      "终端标题",
      live.activity ? (
        <>
          <span className="font-mono text-[12.5px]">{live.activity}</span>
          <span className="mt-0.5 block text-[12px] text-faint">worker 会话标题，可能沿用旧任务</span>
        </>
      ) : (
        ""
      ),
    ],
    ["仓库 / 分支", [t.repo, t.branch].filter(Boolean).join(" @ ")],
    ["worktree", t.worktree ? <span className="font-mono text-[12.5px] break-all">{t.worktree}</span> : ""],
    ["agent", t.agent_name],
    ["父任务", t.parent_id ? <Link href={taskHref(d.project, t.parent_id)} className="text-accent hover:underline">{t.parent_id}</Link> : ""],
    ["返工", t.rework_count ? `${t.rework_count} 次` : ""],
    ["时间", times],
    [
      "链接",
      (t.links || []).length ? (
        <span className="flex flex-wrap gap-x-3">
          {(t.links || []).map((l) => (
            <ExtLink key={`${l.type}-${l.title}`} url={l.url}>
              {l.title}
            </ExtLink>
          ))}
        </span>
      ) : (
        ""
      ),
    ],
  ];

  return (
    <Card className="p-6">
      <SectionTitle>概况</SectionTitle>
      <dl className="divide-y divide-line text-[13.5px]">
        {rows
          .filter(([, v]) => v)
          .map(([k, v]) => (
            <div key={k} className="grid grid-cols-[92px_minmax(0,1fr)] gap-4 py-2.5 first:pt-0">
              <dt className="text-muted">{k}</dt>
              <dd className="text-fg">{v}</dd>
            </div>
          ))}
      </dl>

      <div className="mt-7">
        <SectionTitle>相关决策</SectionTitle>
        {d.decisions.length ? (
          <ul className="space-y-2">
            {d.decisions.map((v) => (
              <li key={v.decision.id}>
                <Link
                  href={decisionsHref(d.project, v.decision.id)}
                  className="flex items-start gap-3 rounded-xl bg-surface-2 px-4 py-3 text-[13.5px] transition-colors duration-150 hover:bg-surface-3"
                >
                  <span className="min-w-0 flex-1 text-fg">{v.decision.question}</span>
                  <span className="shrink-0 text-[12px] text-muted">{DECISION_STATE[v.state]}</span>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-[13px] text-faint">无</p>
        )}
      </div>
    </Card>
  );
}

const EVENT_TONE: Record<string, Tone> = {
  decision_opened: "accent",
  blocked: "orange",
  failed: "red",
  lost: "red",
  review_failed: "orange",
  review_passed: "green",
  done: "green",
  merged: "green",
  decision_ack: "green",
  decision_done: "green",
};
const DOT_BG: Record<Tone, string> = { neutral: "bg-line-strong", accent: "bg-accent", green: "bg-green", orange: "bg-orange", red: "bg-red" };

function Timeline({ d }: { d: TaskDetail }) {
  const { now } = useConsole();
  const evs = d.events || [];
  return (
    <Card className="p-6">
      <SectionTitle aside={<span className="text-[12px] text-faint tabular-nums">{evs.length} 条</span>}>时间线</SectionTitle>
      {evs.length ? (
        <ol className="relative max-h-[520px] overflow-y-auto pr-1">
          {evs.map((e: Event, i) => {
            const tone = EVENT_TONE[e.type] || "neutral";
            return (
              <li key={`${e.ts}-${i}`} className="relative grid grid-cols-[44px_16px_minmax(0,1fr)] gap-x-2 pb-4 last:pb-0">
                <time dateTime={e.ts} className="pt-px text-right text-[12px] text-faint tabular-nums">
                  {fmtClock(e.ts, now)}
                </time>
                <span className="relative flex justify-center">
                  {i < evs.length - 1 && <span aria-hidden className="absolute top-3 -bottom-4 w-px bg-line" />}
                  <span aria-hidden className={cn("relative mt-[5px] size-2 rounded-full ring-4 ring-surface", DOT_BG[tone])} />
                </span>
                <div className="min-w-0 text-[13.5px]">
                  <p className="flex flex-wrap items-baseline gap-x-2">
                    <span className={cn("font-medium", tone === "neutral" ? "text-fg" : { accent: "text-accent", green: "text-green", orange: "text-orange", red: "text-red" }[tone])}>
                      {EVENT_TYPE[e.type] || e.type}
                    </span>
                    {e.actor && <span className="text-[12px] text-faint">{e.actor}</span>}
                  </p>
                  <p className="mt-0.5 text-fg-2">
                    {e.summary}
                    {e.decision_id && (
                      <Link href={decisionsHref(d.project, e.decision_id)} className="ml-2 font-mono text-[12px] text-accent hover:underline">
                        {e.decision_id}
                      </Link>
                    )}
                  </p>
                </div>
              </li>
            );
          })}
        </ol>
      ) : (
        <p className="py-6 text-center text-[13px] text-faint">暂无事件（过渡期 ledger.md 项目没有事件流）</p>
      )}
    </Card>
  );
}

function DetailSkeleton({ back }: { back: ReactNode }) {
  return (
    <div aria-busy="true">
      {back}
      <Skeleton className="h-4 w-24" />
      <Skeleton className="mt-2 h-8 w-64" />
      <div className="mt-8 grid gap-5 xl:grid-cols-2">
        <Skeleton className="h-80 rounded-2xl" />
        <Skeleton className="h-80 rounded-2xl" />
      </div>
    </div>
  );
}
