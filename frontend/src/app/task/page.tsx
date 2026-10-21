import { Suspense } from "react";
import { PageSkeleton } from "@/components/shell-fallback";
import { TaskView } from "@/features/task/task-view";

export default function TaskPage() {
  return (
    <Suspense fallback={<PageSkeleton />}>
      <TaskView />
    </Suspense>
  );
}
