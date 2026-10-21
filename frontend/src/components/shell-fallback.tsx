import { Skeleton } from "./ui";

/** 静态 HTML 首屏（JS 接管前）：与真实布局同构的骨架。 */
export function ShellFallback() {
  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
      <aside className="hidden border-r border-line lg:block">
        <div className="space-y-3 p-5">
          <Skeleton className="h-7 w-36" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      </aside>
      <PageSkeleton />
    </div>
  );
}

export function PageSkeleton() {
  return (
    <div className="mx-auto w-full max-w-[1480px] px-5 pt-8 sm:px-8 lg:px-12 lg:pt-12" aria-busy="true" aria-label="加载中">
      <Skeleton className="mb-3 h-8 w-48" />
      <Skeleton className="mb-10 h-4 w-72" />
      <div className="space-y-4">
        <Skeleton className="h-44 w-full rounded-2xl" />
        <Skeleton className="h-44 w-full rounded-2xl" />
      </div>
    </div>
  );
}
