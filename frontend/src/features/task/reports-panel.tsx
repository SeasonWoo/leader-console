"use client";

// 报告：多份时用分段控件切换（选中项写回 ?report=，刷新与直链可用）；内容为服务端渲染的安全 HTML。

import { useRouter } from "next/navigation";
import { FileText } from "lucide-react";
import { useResource } from "@/components/console-provider";
import { ServerHTML } from "@/components/server-html";
import { Card, EmptyState, ErrorState, Segmented, SectionTitle, Skeleton, StaleNotice } from "@/components/ui";
import { api, errorMessage } from "@/lib/api";
import { fmtSize } from "@/lib/format";
import { taskHref } from "@/lib/routes";
import type { ReportRef } from "@/lib/types";

export function ReportsPanel({ project, id, reports, selected }: { project: string; id: string; reports: ReportRef[]; selected: string }) {
  const router = useRouter();
  const cur = reports.find((r) => r.id === selected || r.title === selected) || reports[0];

  return (
    <Card className="p-6">
      <SectionTitle aside={cur && <span className="text-[12px] text-faint tabular-nums">{fmtSize(cur.size)}</span>}>报告</SectionTitle>
      {!cur ? (
        <EmptyState icon={<FileText />} title="暂无报告" className="py-8" />
      ) : (
        <>
          {reports.length > 1 && (
            <div className="mb-5">
              <Segmented
                label="选择报告"
                value={cur.id}
                onChange={(r) => router.replace(taskHref(project, id, r), { scroll: false })}
                options={reports.map((r) => ({ value: r.id, label: r.title, title: fmtSize(r.size) }))}
              />
            </div>
          )}
          <ReportBody project={project} id={id} report={cur.id} />
        </>
      )}
    </Card>
  );
}

function ReportBody({ project, id, report }: { project: string; id: string; report: string }) {
  const res = useResource(() => api.report(project, id, report), [project, id, report]);
  if (res.error && !res.data) return <ErrorState message={`报告加载失败：${errorMessage(res.error)}`} onRetry={res.reload} className="py-8" />;
  if (!res.data)
    return (
      <div className="space-y-3" aria-busy="true">
        <Skeleton className="h-6 w-1/3" />
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-5/6" />
      </div>
    );
  return (
    <>
      {res.error ? (
        <StaleNotice className="mb-4" message={`报告刷新失败：${errorMessage(res.error)}`} onRetry={res.reload} retrying={res.loading} />
      ) : null}
      {res.data.truncated && <p className="mb-4 rounded-lg bg-surface-2 px-3 py-2 text-[12.5px] text-muted">报告过大，已截断显示。</p>}
      <ServerHTML html={res.data.html} className="max-w-[760px]" />
    </>
  );
}
