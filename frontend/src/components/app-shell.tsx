"use client";

// 布局：毛玻璃侧栏（导航、项目、系统状态、主题）+ 窄屏毛玻璃顶栏 + 主内容区 + toast。

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";
import { Columns3, Inbox, Monitor, Moon, Sun } from "lucide-react";
import { useConsole } from "./console-provider";
import { Dot, Segmented, Status } from "./ui";
import { boardHref, decisionsHref } from "@/lib/routes";
import { fmtClock } from "@/lib/format";
import { LEADER_REASON } from "@/lib/labels";
import { cn } from "@/lib/cn";

type Page = "decisions" | "board" | "task";

function usePage(): { page: Page; project: string } {
  const path = usePathname();
  const sp = useSearchParams();
  const page: Page = path.startsWith("/board") ? "board" : path.startsWith("/task") ? "task" : "decisions";
  return { page, project: sp.get("project") || "" };
}

export function AppShell({ children }: { children: ReactNode }) {
  const { error, overview } = useConsole();
  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded-full focus:bg-surface focus:px-4 focus:py-2">
        跳到主要内容
      </a>
      <Sidebar />
      <div className="min-w-0">
        <MobileBar />
        <main id="main" className="mx-auto w-full max-w-[1480px] px-5 pt-8 pb-20 sm:px-8 lg:px-12 lg:pt-12">
          {error && <Banner tone="red">{error}</Banner>}
          {overview && !overview.herdr.ok && (
            <Banner tone="orange">
              herdr 不可用{overview.herdr.error ? `（${overview.herdr.error}）` : ""}：任务不判定 lost，新回答会排队，恢复后自动投递。
            </Banner>
          )}
          {children}
        </main>
      </div>
      <Toast />
    </div>
  );
}

function Banner({ tone, children }: { tone: "red" | "orange"; children: ReactNode }) {
  return (
    <div role="status" className="mb-6 flex items-start gap-2.5 rounded-xl border border-line bg-surface px-4 py-3 text-[13px] text-fg-2">
      <Dot tone={tone} className="mt-1.5" />
      <span>{children}</span>
    </div>
  );
}

function NavItem({ href, active, icon, children, count }: { href: string; active: boolean; icon: ReactNode; children: ReactNode; count?: number }) {
  return (
    <Link
      href={href}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13.5px] transition-colors duration-150",
        active ? "bg-accent-soft font-medium text-accent" : "text-fg-2 hover:bg-surface-3/60",
      )}
    >
      <span className="[&_svg]:size-4 [&_svg]:stroke-[1.75]">{icon}</span>
      <span className="flex-1">{children}</span>
      {!!count && (
        <span className={cn("min-w-5 rounded-full px-1.5 text-center text-[11.5px] leading-5 font-semibold tabular-nums", active ? "bg-accent text-accent-fg" : "bg-surface-3 text-fg-2")}>
          {count}
        </span>
      )}
    </Link>
  );
}

function Sidebar() {
  const { overview, now, live, scenario } = useConsole();
  const { page, project } = usePage();
  const open = overview?.decisions.filter((d) => d.state === "open").length ?? 0;
  const projectHref = (p?: string) => (page === "decisions" ? decisionsHref(p) : boardHref(p));

  return (
    <aside className="sticky top-0 hidden h-dvh flex-col border-r border-line bg-glass backdrop-blur-xl backdrop-saturate-150 lg:flex">
      <div className="flex h-16 items-center gap-2.5 px-5">
        <span className="grid size-7 place-items-center rounded-[8px] bg-fg text-[13px] font-semibold text-bg">L</span>
        <span className="text-[15px] font-semibold tracking-tight">Leader Console</span>
      </div>

      <nav aria-label="页面" className="space-y-0.5 px-3">
        <NavItem href={decisionsHref(project)} active={page === "decisions"} icon={<Inbox />} count={open}>
          待我决定
        </NavItem>
        <NavItem href={boardHref(project)} active={page !== "decisions"} icon={<Columns3 />}>
          任务看板
        </NavItem>
      </nav>

      <div className="mt-7 min-h-0 flex-1 overflow-y-auto px-3">
        <h2 className="mb-1.5 px-2.5 text-[11.5px] font-semibold tracking-wide text-faint">项目</h2>
        <ul className="space-y-0.5">
          <li>
            <Link
              href={projectHref()}
              aria-current={!project ? "true" : undefined}
              className={cn("flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px]", !project ? "bg-surface-3/70 font-medium text-fg" : "text-fg-2 hover:bg-surface-3/60")}
            >
              <span className="size-2" />
              全部项目
            </Link>
          </li>
          {overview?.projects.map((p) => (
            <li key={p.name}>
              <Link
                href={projectHref(p.name)}
                aria-current={project === p.name ? "true" : undefined}
                title={`${p.title || p.name} · leader ${p.leader.online ? "在线" : `离线（${LEADER_REASON[p.leader.reason] || p.leader.reason}）`}`}
                className={cn(
                  "flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px]",
                  project === p.name ? "bg-surface-3/70 font-medium text-fg" : "text-fg-2 hover:bg-surface-3/60",
                )}
              >
                <Dot tone={p.leader.online ? "green" : "neutral"} />
                <span className="min-w-0 flex-1 truncate">{p.name}</span>
                {p.open_decisions > 0 && <span className="text-[12px] font-medium text-accent tabular-nums">{p.open_decisions}</span>}
              </Link>
            </li>
          ))}
        </ul>
      </div>

      <div className="space-y-3 border-t border-line px-5 py-4 text-[12px] text-muted">
        {overview && (
          <div className="space-y-1.5">
            <Status tone={overview.herdr.ok ? "green" : "red"} className="text-[12px]">
              <span className="text-fg-2">herdr</span>
              <span className="text-muted">{overview.herdr.ok ? `正常 · ${overview.herdr.agents} agents` : "不可用"}</span>
            </Status>
            <Status tone={live === "offline" ? "red" : "accent"} className="text-[12px]">
              <span className="text-fg-2">实时</span>
              <span className="text-muted">
                {{ sse: "SSE", polling: "轮询 5s", mock: scenario === "1" ? "mock 推送" : `mock · ${scenario}`, offline: "连接断开" }[live]}
              </span>
              <span className="ml-auto pl-2 tabular-nums text-faint">{fmtClock(now, now)}</span>
            </Status>
          </div>
        )}
        <ThemeSwitch />
      </div>
    </aside>
  );
}

function MobileBar() {
  const { overview } = useConsole();
  const { page, project } = usePage();
  const router = useRouter();
  const open = overview?.decisions.filter((d) => d.state === "open").length ?? 0;
  return (
    <div className="sticky top-0 z-20 border-b border-line bg-glass backdrop-blur-xl backdrop-saturate-150 lg:hidden">
      <div className="flex h-13 items-center gap-3 px-5">
        <span className="text-[15px] font-semibold tracking-tight">Leader Console</span>
        <nav aria-label="页面" className="ml-auto flex gap-1 text-[13px]">
          <Link href={decisionsHref(project)} className={cn("rounded-full px-3 py-1", page === "decisions" ? "bg-accent-soft text-accent" : "text-fg-2")}>
            待我决定{open ? ` ${open}` : ""}
          </Link>
          <Link href={boardHref(project)} className={cn("rounded-full px-3 py-1", page !== "decisions" ? "bg-accent-soft text-accent" : "text-fg-2")}>
            看板
          </Link>
        </nav>
        <select
          aria-label="项目筛选"
          value={project}
          onChange={(e) => router.push(page === "decisions" ? decisionsHref(e.target.value) : boardHref(e.target.value))}
          className="h-7 max-w-36 rounded-full border border-line bg-surface px-2 text-[12.5px]"
        >
          <option value="">全部项目</option>
          {overview?.projects.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
    </div>
  );
}

type Theme = "system" | "light" | "dark";
function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>("system");
  useEffect(() => {
    const saved = localStorage.getItem("lc-theme");
    if (saved === "light" || saved === "dark") setTheme(saved);
  }, []);
  const apply = (t: Theme) => {
    setTheme(t);
    if (t === "system") {
      localStorage.removeItem("lc-theme");
      delete document.documentElement.dataset.theme;
    } else {
      localStorage.setItem("lc-theme", t);
      document.documentElement.dataset.theme = t;
    }
  };
  return (
    <Segmented<Theme>
      label="外观"
      size="sm"
      value={theme}
      onChange={apply}
      options={[
        { value: "system", label: <Monitor className="size-3.5" aria-label="跟随系统" />, title: "跟随系统" },
        { value: "light", label: <Sun className="size-3.5" aria-label="浅色" />, title: "浅色" },
        { value: "dark", label: <Moon className="size-3.5" aria-label="深色" />, title: "深色" },
      ]}
    />
  );
}

function Toast() {
  const { toastState } = useConsole();
  return (
    <div aria-live="polite" role="status" className="pointer-events-none fixed inset-x-0 bottom-6 z-50 flex justify-center px-4">
      {toastState && (
        <div key={toastState.id} className="lc-fade-in flex max-w-md items-center gap-2.5 rounded-full border border-line bg-glass px-4 py-2.5 text-[13px] text-fg shadow-pop backdrop-blur-xl">
          <Dot tone={{ ok: "green", warn: "orange", error: "red", info: "accent" }[toastState.kind] as "green"} />
          {toastState.msg}
        </div>
      )}
    </div>
  );
}
