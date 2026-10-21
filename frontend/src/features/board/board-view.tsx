"use client";

// 任务看板（/board/?project=）。PRD §7.2、§8。每列按更新时间倒序（服务端排序）：最新更新 / 最新完成的任务在最上面。

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useState } from "react";
import { AlertTriangle, ChevronRight, Clock, LayoutGrid, RotateCcw } from "lucide-react";
import { useConsole, useResource } from "@/components/console-provider";
import { ExtLink } from "@/components/ext-link";
import { Card, Dot, EmptyState, ErrorState, PageHeader, Pill, Skeleton, StaleNotice, type Tone } from "@/components/ui";
import { api, errorMessage } from "@/lib/api";
import { fmtAgo, fmtDuration, parseTime } from "@/lib/format";
import { AGENT_STATUS, COLUMNS, LEADER_REASON, TASK_STATUS } from "@/lib/labels";
import { boardHref, taskHref } from "@/lib/routes";
import type { BoardColumn, ProjectSummary, TaskView } from "@/lib/types";
import { cn } from "@/lib/cn";

const DAY = 24 * 3600 * 1000;
let showAllDone = false; // 跨刷新、跨页面保留“已完成”列的展开状态

/** 最近变化时间：updated_at，缺失时退回 created_at（与 service 层排序一致，用于“已完成”列的 24h 筛选）。 */
function changedAt(tv: TaskView) {
  return parseTime(tv.task.updated_at) ?? parseTime(tv.task.created_at) ?? 0;
}

export function BoardView() {
  const sp = useSearchParams();
  const project = sp.get("project") || "";
  const { overview, now } = useConsole();
  const res = useResource(() => api.tasks(project), [project]);
  const [allDone, setAllDone] = useState(showAllDone);

  const projects = (overview?.projects || []).filter((p) => !project || p.name === project);
  const warnings = projects.flatMap((p) => (p.warnings || []).map((w) => `${p.name}：${w}`));

  return (
    <div className="lc-fade-in">
      <PageHeader title="任务看板" subtitle={project ? `${project}${projects[0]?.title ? ` · ${projects[0].title}` : ""}` : "全部项目"} />
      <LeaderStrip projects={projects} now={now} />

      {res.error && !res.data ? (
        <Card>
          <ErrorState message={errorMessage(res.error)} onRetry={res.reload} />
        </Card>
      ) : !res.data ? (
        <BoardSkeleton />
      ) : (
        <>
          {res.error ? <StaleNotice className="mb-5" message={errorMessage(res.error)} onRetry={res.reload} retrying={res.loading} /> : null}
          <Board
          tasks={res.data.tasks}
          showProject={!project}
          now={now}
          allDone={allDone}
          onToggleDone={() => setAllDone((showAllDone = !allDone))}
          />
        </>
      )}

      {warnings.length > 0 && (
        <details className="group mt-10 text-[12.5px]">
          <summary className="inline-flex cursor-pointer list-none items-center gap-1.5 text-muted hover:text-fg [&::-webkit-details-marker]:hidden">
            <ChevronRight className="size-3.5 transition-transform duration-150 group-open:rotate-90" aria-hidden />
            <AlertTriangle className="size-3.5 text-orange" aria-hidden />
            解析警告 {warnings.length}
          </summary>
          <ul className="mt-2 space-y-1 pl-6 font-mono text-[12px] text-muted">
            {warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function LeaderStrip({ projects, now }: { projects: ProjectSummary[]; now: number }) {
  if (!projects.length) return null;
  return (
    <div className="-mt-3 mb-8 flex flex-wrap gap-2" aria-label="leader 状态">
      {projects.map((p) => {
        const l = p.leader;
        const hb = parseTime(l.heartbeat_at);
        return (
          <Link
            key={p.name}
            href={boardHref(p.name)}
            className="inline-flex h-8 items-center gap-2 rounded-full border border-line bg-surface px-3 text-[12.5px] shadow-card hover:border-line-strong"
          >
            <Dot tone={l.online ? "green" : "neutral"} pulse={l.online && l.agent_status === "working"} />
            <span className="font-medium text-fg">{p.name}</span>
            <span className="text-muted">
              {l.online ? "leader 在线" : `leader 离线 · ${LEADER_REASON[l.reason] || l.reason}`}
              {hb !== null && ` · 心跳 ${fmtAgo(hb, now)}`}
            </span>
            {p.source === "ledger.md" && (
              <span className="text-faint" title="过渡期只读解析 ledger.md，不能在面板作答">
                · ledger.md 只读
              </span>
            )}
          </Link>
        );
      })}
    </div>
  );
}

function Board({ tasks, showProject, now, allDone, onToggleDone }: { tasks: TaskView[]; showProject: boolean; now: number; allDone: boolean; onToggleDone: () => void }) {
  const byCol = Object.fromEntries(COLUMNS.map(([c]) => [c, [] as TaskView[]])) as Record<BoardColumn, TaskView[]>;
  // 服务端已按更新时间倒序（同时间按 id）排好，分列时保持 API 顺序，不再重复排序。
  for (const t of tasks) (byCol[t.column] || byCol.in_progress).push(t);

  return (
    <div className="-mx-5 overflow-x-auto px-5 pb-2 sm:-mx-8 sm:px-8 lg:-mx-12 lg:px-12">
      <div className="grid grid-cols-[repeat(5,minmax(196px,1fr))] gap-4">
        {COLUMNS.map(([col, label]) => {
          let list = byCol[col];
          const total = list.length;
          let hidden = 0;
          if (col === "done") {
            const recent = list.filter((t) => changedAt(t) >= now - DAY);
            hidden = total - recent.length;
            if (!allDone) list = recent;
          }
          const alert = col === "blocked" && total > 0;
          return (
            <section key={col} aria-label={`${label} ${total}`} className="min-w-0">
              <header className="mb-3 flex h-7 items-center gap-2 px-1">
                {alert && <Dot tone="red" />}
                <h2 className="text-[13px] font-semibold text-fg-2">{label}</h2>
                <span className="text-[12.5px] text-faint tabular-nums">{total}</span>
                {col === "done" && (hidden > 0 || allDone) && (
                  <button type="button" onClick={onToggleDone} className="ml-auto text-[12px] text-accent hover:underline">
                    {allDone ? "只看 24 小时内" : `另有 ${hidden} 个`}
                  </button>
                )}
              </header>
              <div className="space-y-2.5">
                {list.length ? (
                  list.map((t) => <TaskCard key={`${t.project}/${t.task.id}`} tv={t} showProject={showProject} now={now} />)
                ) : (
                  <div className="rounded-xl border border-dashed border-line px-3 py-6 text-center text-[12.5px] text-faint">暂无</div>
                )}
              </div>
            </section>
          );
        })}
      </div>
      {!tasks.length && (
        <EmptyState icon={<LayoutGrid />} title="还没有任务" hint="leader 派发任务后会出现在看板上。" />
      )}
    </div>
  );
}

const AGENT_TONE: Record<string, Tone> = { working: "green", blocked: "orange", idle: "neutral", done: "neutral", unknown: "neutral" };

/**
 * 看板卡片只放最重要的信息：名称与异常标记、类型、目标、进展、运行时长。
 * worker 终端标题可能被多个任务共用或沿用旧任务，不作为摘要，只在详情页展示。
 */
function TaskCard({ tv, showProject, now }: { tv: TaskView; showProject: boolean; now: number }) {
  const t = tv.task;
  const live = tv.live;
  const failed = t.status === "failed";
  const lost = live.lost || t.status === "lost";
  const goal = (t.goal || "").trim();
  const summary = (t.result_summary || "").trim();
  const href = taskHref(tv.project, t.id);

  return (
    <article
      className={cn(
        "group relative rounded-xl border border-line bg-surface p-3.5 shadow-card transition-[border-color,box-shadow] duration-150 focus-within:border-accent hover:border-line-strong hover:shadow-pop",
      )}
    >
      <div className="flex items-start gap-2">
        <h3 className="min-w-0 flex-1 text-[13.5px] leading-snug font-semibold break-all text-fg">
          {/* 整卡可点：链接的 ::after 覆盖卡片，其余链接通过 z-10 浮在上面 */}
          <Link prefetch={false} href={href} className="outline-none after:absolute after:inset-0 after:rounded-xl focus-visible:after:ring-2 focus-visible:after:ring-accent">
            {t.name || t.id}
          </Link>
        </h3>
        {lost && <Pill tone="red">LOST</Pill>}
        {failed && <Pill tone="red">失败</Pill>}
        {t.status === "needs_user" && <Pill tone="accent">等你决定</Pill>}
        {t.status === "blocked" && <Pill tone="orange">阻塞</Pill>}
      </div>

      <div className="mt-1 flex items-center gap-1.5 text-[12px] text-muted">
        <span className="truncate">
          {[t.kind, t.role].filter(Boolean).join(" · ")}
          {showProject && <span className="text-faint"> · {tv.project}</span>}
        </span>
        {live.agent_found && live.agent_status && (
          <span className="ml-auto inline-flex shrink-0 items-center gap-1">
            <Dot tone={AGENT_TONE[live.agent_status] || "neutral"} />
            {AGENT_STATUS[live.agent_status] || live.agent_status}
          </span>
        )}
      </div>

      <div className="mt-2.5 space-y-1 text-[12.5px] leading-relaxed">
        {goal && (
          <p className="line-clamp-2 text-muted" title={goal}>
            <span className="text-faint">目标　</span>
            {goal}
          </p>
        )}
        {summary && (
          <p className="line-clamp-2 text-fg-2" title={summary}>
            <span className="text-faint">进展　</span>
            {summary}
          </p>
        )}
        {!goal && !summary && <p className="text-faint">暂无任务摘要</p>}
        {live.lost && <p className="font-medium text-red">herdr 中找不到 worker</p>}
      </div>

      <footer className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-faint">
        {/* 运行中显示已运行时长；已结束显示最近更新时间（看板按它排序） */}
        <span className="inline-flex items-center gap-1 tabular-nums" title={tv.running_for_sec > 0 ? "已运行" : `${TASK_STATUS[t.status] || t.status} · 最近更新`}>
          <Clock className="size-3" aria-hidden />
          {tv.running_for_sec > 0 ? fmtDuration(tv.running_for_sec) : `更新于 ${fmtAgo(changedAt(tv), now)}`}
        </span>
        {!!t.rework_count && (
          <span className="inline-flex items-center gap-1">
            <RotateCcw className="size-3" aria-hidden />
            返工 {t.rework_count}
          </span>
        )}
        {(t.links || []).map((l) => (
          <ExtLink key={`${l.type}-${l.title}`} url={l.url} className="text-[11.5px]">
            {l.title}
          </ExtLink>
        ))}
      </footer>
    </article>
  );
}

function BoardSkeleton() {
  return (
    <div className="grid grid-cols-[repeat(5,minmax(196px,1fr))] gap-4">
      {COLUMNS.map(([c]) => (
        <div key={c} className="space-y-2.5">
          <Skeleton className="h-5 w-20" />
          <Skeleton className="h-28 w-full rounded-xl" />
          <Skeleton className="h-24 w-full rounded-xl" />
        </div>
      ))}
    </div>
  );
}
