"use client";

// 全局状态：overview 快照、实时刷新（SSE + 5s 轮询回退；mock 模式为模拟推送）、时钟、toast、旧 hash 链接跳转。
// 根布局常驻，页面间切换不会断开订阅或丢失草稿。

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { api, errorMessage, mockScenario } from "@/lib/api";
import { legacyHashToPath } from "@/lib/legacy-route";
import type { Overview, ProjectSummary } from "@/lib/types";

export type LiveMode = "connecting" | "mock" | "sse" | "polling" | "offline";
export type ToastKind = "ok" | "warn" | "error" | "info";

interface ToastState {
  id: number;
  msg: string;
  kind: ToastKind;
}

interface ConsoleCtx {
  overview: Overview | null;
  /** overview 拉取失败的提示；成功后清空 */
  error: string;
  /** 每次成功刷新 +1，页面据此重新拉自己的数据 */
  epoch: number;
  /** 当前时间（mock 模式平移到数据时间附近），每 15s 更新 */
  now: number;
  live: LiveMode;
  /** null：真实后端；否则为 mock 场景名 */
  scenario: string | null;
  refresh: () => Promise<void>;
  projectSummary: (name: string) => ProjectSummary | undefined;
  toast: (msg: string, kind?: ToastKind) => void;
  toastState: ToastState | null;
}

const POLL_MS = 5000;
const SSE_RETRY_MS = 30_000;

const Ctx = createContext<ConsoleCtx | null>(null);

export function useConsole() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useConsole must be used inside ConsoleProvider");
  return c;
}

export function ConsoleProvider({ children }: { children: ReactNode }) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const [epoch, setEpoch] = useState(0);
  const [offset, setOffset] = useState<number | null>(null);
  const [wall, setWall] = useState(() => Date.now());
  const [toastState, setToast] = useState<ToastState | null>(null);
  const scenario = mockScenario();
  const versionRef = useRef("");
  const inflight = useRef<Promise<void> | null>(null);
  const pending = useRef(false);

  const refresh = useCallback(async (): Promise<void> => {
    if (inflight.current) {
      pending.current = true;
      return inflight.current;
    }
    inflight.current = (async () => {
      try {
        const ov = await api.overview();
        versionRef.current = ov.version;
        // mock 数据时间固定在 generated_at 附近：只在 mock 模式校准一次，倒计时才有意义。
        if (mockScenario()) setOffset((o) => (o === null ? Date.parse(ov.generated_at) - Date.now() : o));
        setOverview(ov);
        setError("");
        setEpoch((e) => e + 1);
      } catch (e) {
        setError(`数据加载失败：${errorMessage(e)}`);
      }
      setWall(Date.now());
    })();
    await inflight.current;
    inflight.current = null;
    if (pending.current) {
      pending.current = false;
      void refresh();
    }
  }, []);

  // 旧前端的 #/board、#/task/<project>/<id> 等链接跳到新路由：首次打开时转换一次，页面已打开时（点旧锚点、
  // 切换旧书签、前进/后退）随 hashchange 再转换。不是 #/ 开头的 hash（如“跳到主要内容”的 #main）保持浏览器默认行为。
  const router = useRouter();
  useEffect(() => {
    const convert = () => {
      const to = legacyHashToPath(window.location.hash, window.location.search);
      if (to) router.replace(to);
    };
    convert();
    window.addEventListener("hashchange", convert);
    return () => window.removeEventListener("hashchange", convert);
  }, [router]);

  const [live, setLive] = useState<LiveMode>(() => (mockScenario() ? "mock" : "connecting"));

  // 实时：mock 模式订阅模拟推送；否则 SSE（/api/v1/stream 的 version 事件），断开时回退 5s 轮询并每 30s 重连。
  useEffect(() => {
    void refresh();
    const tick = setInterval(() => setWall(Date.now()), 15_000);
    const onVersion = (v: string) => {
      if (v && v !== versionRef.current) void refresh();
    };

    if (mockScenario()) {
      let unsub: (() => void) | undefined;
      let cancelled = false;
      void api.subscribeMock(onVersion).then((u) => (cancelled ? u() : (unsub = u)));
      return () => {
        cancelled = true;
        unsub?.();
        clearInterval(tick);
      };
    }

    let es: EventSource | null = null;
    let poll: ReturnType<typeof setInterval> | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let closed = false;

    const stopPolling = () => {
      clearInterval(poll);
      poll = undefined;
    };
    const startPolling = () => {
      setLive("polling");
      if (poll) return;
      poll = setInterval(async () => {
        try {
          const ov = await api.overview();
          setLive(es ? "sse" : "polling");
          if (ov.version !== versionRef.current) void refresh();
        } catch {
          setLive("offline");
        }
      }, POLL_MS);
    };
    const connect = () => {
      if (closed) return;
      if (typeof EventSource === "undefined") return startPolling();
      es = new EventSource("/api/v1/stream");
      es.addEventListener("open", () => {
        stopPolling();
        setLive("sse");
      });
      es.addEventListener("version", (e) => {
        try {
          onVersion(JSON.parse((e as MessageEvent<string>).data).version);
        } catch {
          /* 忽略坏帧 */
        }
      });
      es.addEventListener("error", () => {
        es?.close();
        es = null;
        startPolling();
        retry = setTimeout(connect, SSE_RETRY_MS);
      });
    };
    connect();
    return () => {
      closed = true;
      es?.close();
      stopPolling();
      clearTimeout(retry);
      clearInterval(tick);
    };
  }, [refresh]);

  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const toast = useCallback((msg: string, kind: ToastKind = "info") => {
    setToast({ id: Date.now(), msg, kind });
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast(null), 4000);
  }, []);

  useEffect(() => {
    const onRejection = (e: PromiseRejectionEvent) => toast(errorMessage(e.reason), "error");
    window.addEventListener("unhandledrejection", onRejection);
    return () => window.removeEventListener("unhandledrejection", onRejection);
  }, [toast]);

  // 浏览器标题显示待回答数。
  const openCount = overview ? overview.decisions.filter((d) => d.state === "open").length : 0;
  useEffect(() => {
    document.title = overview ? `(${openCount}) leader-console` : "leader-console";
  }, [overview, openCount]);

  const value = useMemo<ConsoleCtx>(
    () => ({
      overview,
      error,
      epoch,
      now: wall + (offset ?? 0),
      live,
      scenario,
      refresh,
      projectSummary: (name) => overview?.projects.find((p) => p.name === name),
      toast,
      toastState,
    }),
    [overview, error, epoch, wall, offset, live, scenario, refresh, toast, toastState],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/**
 * 页面级数据：首次挂载与每次全局刷新（epoch 变化）时重新拉取。
 * 刷新失败时保留上一次成功的数据（避免闪烁），同时返回 error——调用方必须在有 error 时提示“可能是旧数据”并提供 reload。
 */
export function useResource<T>(fetcher: () => Promise<T>, deps: unknown[]) {
  const { epoch } = useConsole();
  const key = JSON.stringify(deps);
  const [state, setState] = useState<{ key: string; data: T | null; error: unknown; loading: boolean }>({
    key,
    data: null,
    error: null,
    loading: true,
  });
  const [nonce, setNonce] = useState(0);
  const fn = useRef(fetcher);
  useEffect(() => {
    fn.current = fetcher;
  });

  useEffect(() => {
    let alive = true;
    fn.current().then(
      (data) => alive && setState({ key, data, error: null, loading: false }),
      (error) =>
        alive && setState((s) => ({ key, data: s.key === key ? s.data : null, error, loading: false })),
    );
    return () => {
      alive = false;
    };
  }, [epoch, nonce, key]);

  const reload = useCallback(() => {
    setState((s) => ({ ...s, loading: true }));
    setNonce((n) => n + 1);
  }, []);

  // 参数变了（例如切到另一个任务）就不再展示旧数据。
  if (state.key !== key) return { data: null, error: null, loading: true, reload };
  return { data: state.data, error: state.error, loading: state.loading, reload };
}
