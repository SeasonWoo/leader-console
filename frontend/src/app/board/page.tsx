import { Suspense } from "react";
import { PageSkeleton } from "@/components/shell-fallback";
import { BoardView } from "@/features/board/board-view";

export default function BoardPage() {
  return (
    <Suspense fallback={<PageSkeleton />}>
      <BoardView />
    </Suspense>
  );
}
