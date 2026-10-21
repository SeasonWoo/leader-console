import { ArrowUpRight } from "lucide-react";
import { cn } from "@/lib/cn";

/** 只为 http(s) 链接生成 <a>（新窗口打开）；其他协议或空 URL 只显示文字。 */
export function ExtLink({ url, children, className }: { url?: string; children: React.ReactNode; className?: string }) {
  if (url && /^https?:\/\//i.test(url)) {
    return (
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        className={cn("relative z-10 inline-flex items-center gap-0.5 text-accent hover:underline", className)}
      >
        {children}
        <ArrowUpRight className="size-3" aria-hidden />
      </a>
    );
  }
  return <span className={className}>{children}</span>;
}
