// 基础组件：卡片、胶囊、状态圆点、按钮、分段控件、空 / 错误 / 加载状态。

import type { ComponentProps, ReactNode } from "react";
import { AlertTriangle, RotateCw } from "lucide-react";
import { cn } from "@/lib/cn";

export type Tone = "neutral" | "accent" | "green" | "orange" | "red";

const TONE_TEXT: Record<Tone, string> = {
  neutral: "text-muted",
  accent: "text-accent",
  green: "text-green",
  orange: "text-orange",
  red: "text-red",
};
const TONE_DOT: Record<Tone, string> = {
  neutral: "bg-faint",
  accent: "bg-accent",
  green: "bg-green",
  orange: "bg-orange",
  red: "bg-red",
};

export function Card({ className, children, ...rest }: { className?: string; children: ReactNode } & React.HTMLAttributes<HTMLElement>) {
  return (
    <section className={cn("rounded-2xl border border-line bg-surface shadow-card", className)} {...rest}>
      {children}
    </section>
  );
}

export function Dot({ tone = "neutral", pulse, className }: { tone?: Tone; pulse?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2 shrink-0", className)} aria-hidden>
      {pulse && <span className={cn("absolute inset-0 rounded-full opacity-60 motion-safe:animate-ping", TONE_DOT[tone])} />}
      <span className={cn("relative inline-flex size-2 rounded-full", TONE_DOT[tone])} />
    </span>
  );
}

/** 胶囊标签：细描边 + 彩色文字，不用实心色块。 */
export function Pill({ tone = "neutral", dot, children, className, title }: { tone?: Tone; dot?: boolean; children: ReactNode; className?: string; title?: string }) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex h-[22px] shrink-0 items-center gap-1.5 rounded-full border px-2 text-[12px] font-medium leading-none whitespace-nowrap",
        tone === "neutral" ? "border-line-strong" : "border-current/25",
        TONE_TEXT[tone],
        className,
      )}
    >
      {dot && <Dot tone={tone} />}
      {children}
    </span>
  );
}

/** 带圆点的状态文字。 */
export function Status({ tone = "neutral", children, className }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-[13px]", TONE_TEXT[tone], className)}>
      <Dot tone={tone} />
      {children}
    </span>
  );
}

type Variant = "primary" | "secondary" | "ghost";
export function Button({ variant = "secondary", size = "md", className, ...rest }: ComponentProps<"button"> & { variant?: Variant; size?: "sm" | "md" }) {
  return (
    <button
      type="button"
      className={cn(
        "inline-flex items-center justify-center gap-1.5 rounded-full font-medium transition-colors duration-150 disabled:cursor-default disabled:opacity-40",
        size === "md" ? "h-8 px-4 text-[13px]" : "h-7 px-3 text-[12px]",
        variant === "primary" && "bg-accent text-accent-fg hover:bg-accent-hover",
        variant === "secondary" && "bg-surface-2 text-fg hover:bg-surface-3",
        variant === "ghost" && "text-accent hover:bg-accent-soft",
        className,
      )}
      {...rest}
    />
  );
}

/** 分段控件（报告切换、终端来源、主题）。 */
export function Segmented<T extends string>({ value, options, onChange, label, size = "md" }: { value: T; options: { value: T; label: ReactNode; title?: string }[]; onChange: (v: T) => void; label: string; size?: "sm" | "md" }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex max-w-full overflow-x-auto rounded-full bg-surface-2 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          title={o.title}
          onClick={() => onChange(o.value)}
          className={cn(
            "inline-flex shrink-0 items-center gap-1 rounded-full font-medium whitespace-nowrap transition-all duration-150",
            size === "md" ? "h-7 px-3 text-[12.5px]" : "h-6 px-2 text-[12px]",
            o.value === value ? "bg-surface text-fg shadow-[0_1px_3px_rgb(0_0_0/0.12)]" : "text-muted hover:text-fg",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="mb-8 flex flex-wrap items-end gap-x-6 gap-y-2">
      <div className="min-w-0">
        <h1 className="text-[28px] leading-tight font-semibold tracking-[-0.02em] text-fg">{title}</h1>
        {subtitle && <p className="mt-1 text-[14px] text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="ml-auto flex items-center gap-3">{actions}</div>}
    </header>
  );
}

export function SectionTitle({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="mb-3 flex items-center gap-3">
      <h2 className="text-[13px] font-semibold tracking-wide text-muted">{children}</h2>
      {aside && <div className="ml-auto">{aside}</div>}
    </div>
  );
}

export function EmptyState({ icon, title, hint, className }: { icon: ReactNode; title: ReactNode; hint?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center px-6 py-14 text-center", className)}>
      <div className="mb-3 text-faint [&_svg]:size-8 [&_svg]:stroke-[1.4]">{icon}</div>
      <p className="text-[15px] font-medium text-fg-2">{title}</p>
      {hint && <p className="mt-1 max-w-sm text-[13px] text-muted">{hint}</p>}
    </div>
  );
}

export function ErrorState({ message, onRetry, className }: { message: string; onRetry?: () => void; className?: string }) {
  return (
    <div role="alert" className={cn("flex flex-col items-center justify-center px-6 py-12 text-center", className)}>
      <AlertTriangle className="mb-3 size-7 stroke-[1.5] text-orange" aria-hidden />
      <p className="text-[15px] font-medium text-fg-2">加载失败</p>
      <p className="mt-1 max-w-md text-[13px] text-muted">{message}</p>
      {onRetry && (
        <Button className="mt-4" onClick={onRetry}>
          <RotateCw className="size-3.5" aria-hidden />
          重试
        </Button>
      )}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn("rounded-lg bg-surface-2 motion-safe:animate-pulse", className)} />;
}

export function Kbd({ children }: { children: ReactNode }) {
  return <code className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11.5px] text-muted">{children}</code>;
}
