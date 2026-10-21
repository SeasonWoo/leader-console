import { Suspense } from "react";
import { PageSkeleton } from "@/components/shell-fallback";
import { DecisionsView } from "@/features/decisions/decisions-view";

export default function DecisionsPage() {
  return (
    <Suspense fallback={<PageSkeleton />}>
      <DecisionsView />
    </Suspense>
  );
}
