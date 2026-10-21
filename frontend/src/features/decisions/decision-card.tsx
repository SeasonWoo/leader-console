"use client";

// 单张决策卡片：头部（紧急度 · 项目 · 任务 · 状态）、问题、背景、作答表单或回传进度、相关链接。

import Link from "next/link";
import { useLayoutEffect, useRef, useState } from "react";
import { ArrowRight, Check, FileText, Sparkles, X } from "lucide-react";
import { useConsole } from "@/components/console-provider";
import { ServerHTML } from "@/components/server-html";
import { Button, Dot, Pill, Status, type Tone } from "@/components/ui";
import { api, ApiError, errorMessage, newClientRequestID } from "@/lib/api";
import { fmtClock, fmtCountdown, parseTime } from "@/lib/format";
import { DECISION_STATE, LEADER_REASON, PROGRESS_STEPS } from "@/lib/labels";
import { boardHref, decisionsHref, taskHref } from "@/lib/routes";
import type { Decision, DecisionView, LeaderPresence } from "@/lib/types";
import { cn } from "@/lib/cn";
import { drafts, keyOf, optimistic } from "./store";

export function deliveredStale(v: DecisionView, now: number) {
  if (v.state !== "delivered" || !v.answer) return false;
  const t = parseTime(v.answer.updated_at);
  return t !== null && now - t > 60_000;
}

function optionLabel(d: Decision, key: string) {
  const o = (d.options || []).find((x) => x.key === key);
  return o ? `${o.key} · ${o.label}` : key;
}

const STATE_TONE: Record<string, Tone> = {
  open: "accent",
  queued: "orange",
  delivered: "green",
  acked: "green",
  executing: "green",
  done: "green",
  delivery_failed: "red",
  answered_in_chat: "neutral",
  expired: "neutral",
  superseded: "neutral",
};

export function DecisionCard({ v, readonly, onChanged, focused }: { v: DecisionView; readonly?: boolean; onChanged: () => void; focused?: boolean }) {
  const { projectSummary, now } = useConsole();
  const d = v.decision;
  const leader: LeaderPresence = projectSummary(v.project)?.leader ?? { online: false, reason: "" };
  const answerable = !readonly && v.state === "open" && v.can_answer;
  const urgent = d.urgency === "high" && v.state === "open";

  return (
    <article
      data-key={keyOf(v)}
      className={cn(
        "relative scroll-mt-24 rounded-2xl border bg-surface shadow-card transition-shadow duration-200",
        focused ? "border-accent ring-4 ring-accent-soft" : "border-line",
        readonly ? "p-5" : "p-6",
      )}
    >
      {urgent && <span aria-hidden className="absolute top-6 bottom-6 left-0 w-[3px] rounded-r-full bg-red" />}
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5 text-[12.5px] text-muted">
        {urgent && (
          <Pill tone="red" dot>
            紧急
          </Pill>
        )}
        <Link href={boardHref(v.project)} className="font-medium text-fg-2 hover:text-accent">
          {v.project}
        </Link>
        {d.task_id && (
          <>
            <span className="text-faint">/</span>
            <Link prefetch={false} href={taskHref(v.project, d.task_id)} className="hover:text-accent">
              {d.task_id}
            </Link>
          </>
        )}
        <span className="font-mono text-[11.5px] text-faint">{d.id}</span>
        <span className="ml-auto">
          <StatusLine v={v} leader={leader} now={now} />
        </span>
      </header>

      <h3 className={cn("mt-3 font-semibold tracking-[-0.01em] text-fg", readonly ? "text-[15px]" : "text-[17px] leading-snug")}>{d.question}</h3>

      {(v.context_html || d.context) && <Context v={v} />}

      {answerable ? (
        <AnswerForm v={v} leader={leader} onChanged={onChanged} />
      ) : v.state === "open" ? (
        <>
          <OptionList d={d} disabled />
          {!readonly && <Note tone="orange">当前不可在面板作答（leader 协议不可用），请在 leader 对话中回答。</Note>}
        </>
      ) : (
        <AnsweredBlock v={v} leader={leader} now={now} readonly={readonly} onChanged={onChanged} />
      )}

      <Related v={v} />
    </article>
  );
}

function StatusLine({ v, leader, now }: { v: DecisionView; leader: LeaderPresence; now: number }) {
  const d = v.decision;
  const a = v.answer;
  switch (v.state) {
    case "open": {
      const dl = parseTime(d.deadline);
      if (dl === null) return v.overdue ? <Pill tone="red">已超时，仍可作答</Pill> : <span className="text-faint">无截止时间</span>;
      const cd = fmtCountdown(dl, now);
      return (
        <span className={cn("inline-flex items-center gap-2 tabular-nums", cd.overdue && "text-red")}>
          截止 {fmtClock(dl, now)} · {cd.text}
          {v.overdue && <Pill tone="red">仍可作答</Pill>}
        </span>
      );
    }
    case "queued":
      return (
        <Status tone="orange">
          {leader.online ? "已记录，等待投递" : `leader 离线（${LEADER_REASON[leader.reason] || leader.reason}），已排队`}
        </Status>
      );
    case "delivered":
      return <Status tone="green">已回传 {fmtClock(a?.updated_at, now, true)}</Status>;
    case "delivery_failed":
      return <Status tone="red">投递失败（{a?.attempts ?? 0} 次）</Status>;
    case "superseded":
      return (
        <Status tone="neutral">
          已被
          {d.superseded_by ? (
            <Link href={decisionsHref(v.project, d.superseded_by)} className="font-mono text-accent hover:underline">
              {d.superseded_by}
            </Link>
          ) : (
            "新决策"
          )}
          取代
        </Status>
      );
    default:
      return <Status tone={STATE_TONE[v.state]}>{DECISION_STATE[v.state] || v.state}</Status>;
  }
}

/** 背景：默认收起为 3 行，内容超出时才显示“展开”。 */
function Context({ v }: { v: DecisionView }) {
  const [open, setOpen] = useState(false);
  const [overflow, setOverflow] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (el && !open) setOverflow(el.scrollHeight > el.clientHeight + 2);
  }, [v.context_html, v.decision.context, open]);
  return (
    <div className="mt-2.5">
      <div ref={ref} className={cn("relative", !open && "max-h-[4.9em] overflow-hidden")}>
        {v.context_html ? (
          <ServerHTML html={v.context_html} className="text-[13.5px]" />
        ) : (
          <p className="prose-lc text-[13.5px] whitespace-pre-wrap">{v.decision.context}</p>
        )}
        {!open && overflow && <div aria-hidden className="absolute inset-x-0 bottom-0 h-6 bg-gradient-to-t from-surface" />}
      </div>
      {(overflow || open) && (
        <button type="button" onClick={() => setOpen(!open)} className="mt-1 text-[12.5px] font-medium text-accent hover:underline" aria-expanded={open}>
          {open ? "收起" : "展开背景"}
        </button>
      )}
    </div>
  );
}

function OptionList({ d, selected, disabled, onPick, chosen }: { d: Decision; selected?: string; disabled?: boolean; onPick?: (k: string) => void; chosen?: string }) {
  const opts = d.options || [];
  if (!opts.length) return null;
  return (
    <div role="radiogroup" aria-label="选项" className="mt-4 grid gap-2">
      {opts.map((o) => {
        const on = selected === o.key || chosen === o.key;
        const rec = d.recommended === o.key;
        return (
          <label
            key={o.key}
            className={cn(
              "group relative flex items-start gap-3 rounded-xl border px-4 py-3 transition-colors duration-150",
              on ? "border-accent bg-accent-soft" : "border-line",
              disabled ? "cursor-default" : "cursor-pointer hover:border-line-strong hover:bg-surface-2/60",
              disabled && !on && "opacity-70",
            )}
          >
            <input
              type="radio"
              name={`opt-${d.id}`}
              value={o.key}
              checked={on}
              disabled={disabled}
              onChange={() => onPick?.(o.key)}
              className="peer sr-only"
            />
            <span
              aria-hidden
              className={cn(
                "mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border text-[11px] font-semibold transition-colors duration-150 peer-focus-visible:ring-2 peer-focus-visible:ring-accent peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-surface",
                on ? "border-accent bg-accent text-accent-fg" : "border-line-strong text-muted",
              )}
            >
              {on ? <Check className="size-3 stroke-[3]" /> : o.key}
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex flex-wrap items-center gap-2">
                <span className="text-[14px] font-medium text-fg">{o.label}</span>
                {rec && (
                  <span className="inline-flex items-center gap-1 text-[12px] font-medium text-accent">
                    <Sparkles className="size-3" aria-hidden />
                    推荐
                  </span>
                )}
              </span>
              {o.detail && <span className="mt-0.5 block text-[13px] text-muted">{o.detail}</span>}
              {o.impact && <span className="mt-0.5 block text-[12px] text-faint">影响：{o.impact}</span>}
            </span>
          </label>
        );
      })}
    </div>
  );
}

function AnswerForm({ v, leader, onChanged }: { v: DecisionView; leader: LeaderPresence; onChanged: () => void }) {
  const { toast, refresh } = useConsole();
  const d = v.decision;
  const key = keyOf(v);
  const [draft, setDraftState] = useState(() => drafts.get(key) ?? { option: "", text: "", crid: "" });
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const confirmRef = useRef<HTMLButtonElement>(null);

  const setDraft = (next: typeof draft) => {
    drafts.set(key, next);
    setDraftState(next);
  };
  const valid = !!draft.option || (d.allow_free_text && draft.text.trim() !== "");
  const hasOptions = (d.options || []).length > 0;
  const summary = [draft.option ? optionLabel(d, draft.option) : "", draft.text.trim() ? `补充：${draft.text.trim()}` : ""].filter(Boolean).join("；");

  useLayoutEffect(() => {
    if (confirming) confirmRef.current?.focus();
  }, [confirming]);

  async function submit() {
    setBusy(true);
    const crid = draft.crid || newClientRequestID();
    setDraft({ ...draft, crid });
    const base = JSON.stringify(v);
    try {
      const res = await api.answer(v.project, d.id, { option: draft.option || undefined, text: draft.text.trim() || undefined, client_request_id: crid });
      optimistic.set(key, { base, view: res });
      drafts.delete(key);
      toast(leader.online ? "已记录，正在回传 leader" : "已记录，leader 离线，回答已排队", "ok");
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        drafts.delete(key);
        // 409 响应体的 current 是服务端当前的 DecisionView（internal/server writeError）。
        const cur = (e.body as { current?: DecisionView })?.current;
        if (cur) optimistic.set(key, { base, view: cur });
        toast(e.code === "protocol_unavailable" ? "该项目尚未启用 leader 协议，无法在面板作答" : "该决策已被处理，已刷新为最新状态", "warn");
      } else {
        toast(errorMessage(e), "error");
        setBusy(false);
        return;
      }
    }
    onChanged();
    void refresh();
  }

  return (
    <div>
      <OptionList d={d} selected={draft.option} disabled={busy || confirming} onPick={(k) => setDraft({ ...draft, option: k })} />
      {d.recommend_reason && (
        <p className="mt-3 flex gap-1.5 text-[13px] text-muted">
          <Sparkles className="mt-[3px] size-3.5 shrink-0 text-accent" aria-hidden />
          <span>
            <span className="text-fg-2">推荐理由　</span>
            {d.recommend_reason}
          </span>
        </p>
      )}
      {d.allow_free_text && (
        <label className="mt-4 block">
          <span className="sr-only">{hasOptions ? "补充说明" : "你的回答"}</span>
          <textarea
            rows={2}
            maxLength={2000}
            disabled={busy || confirming}
            value={draft.text}
            placeholder={hasOptions ? "补充说明（可选）" : "写下你的回答"}
            onChange={(e) => setDraft({ ...draft, text: e.target.value })}
            className="block w-full resize-y rounded-xl border border-transparent bg-surface-2 px-4 py-2.5 text-[14px] text-fg placeholder:text-faint transition-colors duration-150 focus:border-accent focus:bg-surface focus:outline-none disabled:opacity-60"
          />
        </label>
      )}
      {!leader.online && (
        <Note tone="orange">leader 离线（{LEADER_REASON[leader.reason] || leader.reason}），回答会先排队，leader 上线后自动投递。</Note>
      )}

      <div className="mt-5 flex min-h-8 items-center justify-end gap-2">
        {confirming ? (
          <div className="lc-fade-in flex w-full flex-wrap items-center justify-end gap-2" role="group" aria-label="确认提交">
            <p className="mr-auto min-w-0 text-[13px] text-fg-2">
              提交后不可修改。确认提交 <span className="font-medium text-fg">「{summary}」</span>？
            </p>
            <Button onClick={() => setConfirming(false)} disabled={busy}>
              取消
            </Button>
            <Button ref={confirmRef} variant="primary" onClick={submit} disabled={busy}>
              {busy ? "提交中…" : "确认提交"}
            </Button>
          </div>
        ) : (
          <Button variant="primary" disabled={!valid} onClick={() => setConfirming(true)}>
            提交决定
          </Button>
        )}
      </div>
    </div>
  );
}

function AnsweredBlock({ v, leader, now, readonly, onChanged }: { v: DecisionView; leader: LeaderPresence; now: number; readonly?: boolean; onChanged: () => void }) {
  const { toast, refresh } = useConsole();
  const [busy, setBusy] = useState(false);
  const d = v.decision;
  const a = v.answer;

  async function retry() {
    if (!a) return;
    setBusy(true);
    const base = JSON.stringify(v);
    try {
      const res = await api.retry(v.project, a.answer_id);
      optimistic.set(keyOf(v), { base, view: res });
      toast("已重新加入投递队列（答案不变）", "ok");
    } catch (e) {
      const conflict = e instanceof ApiError && e.status === 409;
      toast(conflict ? "该回答当前不需要重试，已刷新" : errorMessage(e), conflict ? "warn" : "error");
    }
    setBusy(false);
    onChanged();
    void refresh();
  }

  return (
    <div className="mt-4 space-y-3">
      {a && (
        <div className="rounded-xl bg-surface-2 px-4 py-3 text-[13.5px]">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="text-muted">你的选择</span>
            <span className="font-medium text-fg">{a.option ? optionLabel(d, a.option) : "（未选选项）"}</span>
            <span className="ml-auto text-[12px] text-faint tabular-nums">{fmtClock(a.answered_at, now, true)} 提交</span>
          </div>
          {a.text && <p className="mt-1 text-fg-2">{a.text}</p>}
        </div>
      )}
      {v.state === "answered_in_chat" && (
        <>
          {d.answer?.summary && (
            <p className="text-[13.5px]">
              <span className="text-muted">对话中的回答　</span>
              <span className="text-fg">{d.answer.summary}</span>
            </p>
          )}
          {a && <Note tone="orange">对话中已处理，面板回答未被采用。</Note>}
        </>
      )}
      {v.state === "expired" && <Note>leader 已将该决策标记为过期，无需再回答。</Note>}
      <Progress v={v} />
      {v.state === "queued" && !leader.online && <Note tone="orange">leader 上线后会自动投递。</Note>}
      {deliveredStale(v, now) && <Note>已送达，leader 可能正忙，等待确认。</Note>}
      {v.state === "delivery_failed" && a && (
        <>
          {a.last_error && (
            <Note tone="red">
              最后一次错误：<code className="font-mono text-[12px]">{a.last_error}</code>
            </Note>
          )}
          {!readonly && (
            <div className="flex flex-wrap items-center justify-end gap-3">
              <span className="text-[12.5px] text-muted">只重发同一条回答，不能修改答案。</span>
              <Button variant="primary" onClick={retry} disabled={busy}>
                {busy ? "重试中…" : "重试投递"}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

/** 回传进度：已记录 → 已回传 → leader 已确认 → 执行中 → 完成；投递失败时只显示两步。 */
function Progress({ v }: { v: DecisionView }) {
  const failed = v.state === "delivery_failed";
  const idx = PROGRESS_STEPS.findIndex(([s]) => s === v.state);
  if (idx < 0 && !failed) return null;
  const steps: [string, string][] = failed ? [PROGRESS_STEPS[0], ["delivery_failed", "投递失败"]] : PROGRESS_STEPS;
  const cur = failed ? 1 : idx;
  return (
    <ol className="flex flex-wrap items-center gap-y-2 text-[12.5px]" aria-label="回传进度">
      {steps.map(([s, label], i) => {
        const done = i <= cur;
        const fail = failed && i === 1;
        return (
          <li key={s} className="flex items-center" aria-current={i === cur ? "step" : undefined}>
            {i > 0 && <span aria-hidden className={cn("mx-2 h-px w-6 sm:w-10", done ? (fail ? "bg-red/50" : "bg-green/50") : "bg-line-strong")} />}
            <span className={cn("inline-flex items-center gap-1.5", fail ? "text-red" : done ? "text-fg-2" : "text-faint", i === cur && "font-medium text-fg")}>
              {fail ? (
                <X className="size-3.5 text-red" aria-hidden />
              ) : (
                <span aria-hidden className={cn("grid size-3.5 place-items-center rounded-full", done ? "bg-green" : "border border-line-strong")}>
                  {done && <Check className="size-2.5 stroke-[3.5] text-white" />}
                </span>
              )}
              {label}
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function Note({ tone = "neutral", children }: { tone?: Tone; children: React.ReactNode }) {
  return (
    <p className="mt-3 flex items-start gap-2 text-[13px] text-fg-2">
      <Dot tone={tone} className="mt-[7px]" />
      <span className="min-w-0">{children}</span>
    </p>
  );
}

function Related({ v }: { v: DecisionView }) {
  const d = v.decision;
  const refs = d.report_refs || [];
  if (!refs.length && !d.task_id) return null;
  return (
    <footer className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-line pt-3.5 text-[12.5px]">
      {refs.map((p) => {
        const base = p.split("/").pop() || p;
        return d.task_id ? (
          <Link key={p} prefetch={false} href={taskHref(v.project, d.task_id, base)} className="inline-flex items-center gap-1 text-muted hover:text-accent">
            <FileText className="size-3.5" aria-hidden />
            {base}
          </Link>
        ) : (
          <span key={p} className="inline-flex items-center gap-1 text-muted">
            <FileText className="size-3.5" aria-hidden />
            {base}
          </span>
        );
      })}
      {d.task_id && (
        <Link prefetch={false} href={taskHref(v.project, d.task_id)} className="ml-auto inline-flex items-center gap-1 font-medium text-accent hover:underline">
          任务详情
          <ArrowRight className="size-3.5" aria-hidden />
        </Link>
      )}
    </footer>
  );
}
