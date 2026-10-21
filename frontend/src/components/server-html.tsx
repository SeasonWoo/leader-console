"use client";

// 插入服务端已脱敏、已按白名单渲染的 HTML（context_html、报告 html）。这是前端唯一插入 HTML 字符串的地方，
// 不要传入任何其他来源的字符串。插入后把链接改为新窗口打开。

import { useEffect, useRef } from "react";
import { cn } from "@/lib/cn";

export function ServerHTML({ html, className }: { html: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    for (const a of ref.current?.querySelectorAll("a[href]") ?? []) {
      a.setAttribute("target", "_blank");
      a.setAttribute("rel", "noopener noreferrer");
    }
  }, [html]);
  return <div ref={ref} className={cn("prose-lc", className)} dangerouslySetInnerHTML={{ __html: html }} />;
}
