"use client";

// worker 终端最近输出（只读，已脱敏）：按需拉取，手动刷新最多 5s 一次。

import { useCallback, useEffect, useRef, useState } from "react";
import { RotateCw } from "lucide-react";
import { Button, Card, Dot, Segmented, SectionTitle } from "@/components/ui";
import { api, ApiError, errorMessage } from "@/lib/api";
import { fmtClock } from "@/lib/format";
import { AGENT_STATUS } from "@/lib/labels";
import { cn } from "@/lib/cn";

const THROTTLE_MS = 5000;
type Source = "recent" | "visible";

export function TerminalPanel({ project, id, agentFound }: { project: string; id: string; agentFound: boolean }) {
  const [source, setSource] = useState<Source>("recent");
  const [text, setText] = useState(agentFound ? "" : "herdr 中没有找到该任务的 worker，无法读取终端。");
  const [dim, setDim] = useState(!agentFound);
  const [meta, setMeta] = useState("");
  const [cooling, setCooling] = useState(false);
  const last = useRef(0);
  const deferred = useRef<ReturnType<typeof setTimeout>>(undefined);
  const srcRef = useRef(source);

  const fetchNow = useCallback(async () => {
    const wait = last.current + THROTTLE_MS - Date.now();
    if (wait > 0) {
      // 节流：5s 内的再次请求推迟到窗口结束后执行一次
      setMeta(`${Math.ceil(wait / 1000)}s 后刷新…`);
      clearTimeout(deferred.current);
      deferred.current = setTimeout(fetchNow, wait);
      return;
    }
    last.current = Date.now();
    setCooling(true);
    setTimeout(() => setCooling(false), THROTTLE_MS);
    setMeta("读取中…");
    try {
      const r = await api.terminal(project, id, srcRef.current, 80);
      setDim(false);
      setText(r.text || "（无输出）");
      setMeta(`${r.agent_status ? `${AGENT_STATUS[r.agent_status] || r.agent_status} · ` : ""}${fmtClock(r.fetched_at, Date.now(), true)} 读取`);
    } catch (e) {
      setDim(true);
      setText(e instanceof ApiError && e.status === 404 ? "该任务没有可读取的 worker 终端。" : `读取失败：${errorMessage(e)}`);
      setMeta("");
    }
  }, [project, id]);

  useEffect(() => {
    void fetchNow();
    return () => clearTimeout(deferred.current);
  }, [fetchNow]);

  return (
    <Card className="p-6">
      <SectionTitle
        aside={
          <div className="flex items-center gap-3">
            <span className="hidden text-[12px] text-faint tabular-nums sm:inline">{meta}</span>
            <Segmented<Source>
              label="终端来源"
              size="sm"
              value={source}
              onChange={(s) => {
                if (s === source) return;
                srcRef.current = s;
                setSource(s);
                void fetchNow();
              }}
              options={[
                { value: "recent", label: "最近输出" },
                { value: "visible", label: "当前屏幕" },
              ]}
            />
            <Button size="sm" onClick={() => void fetchNow()} disabled={cooling} aria-label="刷新终端输出">
              <RotateCw className="size-3.5" aria-hidden />
              刷新
            </Button>
          </div>
        }
      >
        终端输出 · 只读 · 已脱敏
      </SectionTitle>
      <div className="overflow-hidden rounded-xl bg-term-bg">
        <div className="flex items-center gap-1.5 border-b border-white/10 px-4 py-2.5" aria-hidden>
          <Dot className="opacity-40" />
          <Dot className="opacity-40" />
          <Dot className="opacity-40" />
        </div>
        <pre
          className={cn(
            "max-h-[420px] overflow-auto px-4 py-3 font-mono text-[12px] leading-[1.6] break-all whitespace-pre-wrap",
            dim ? "text-term-fg/50" : "text-term-fg",
          )}
        >
          {text || " "}
        </pre>
      </div>
    </Card>
  );
}
