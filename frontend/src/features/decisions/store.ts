// 跨刷新、跨页面保留的前端状态（模块级，随页面生命周期存在）。

import type { DecisionView } from "@/lib/types";

export const keyOf = (v: DecisionView) => `${v.project}/${v.decision.id}`;

/** 未提交的草稿：选项、补充说明、client_request_id（重试提交时复用，保证幂等）。 */
export const drafts = new Map<string, { option: string; text: string; crid: string }>();

/**
 * 提交 / 重试成功后、快照尚未反映之前，用服务端返回的 DecisionView 覆盖展示。
 * base 是当时快照里的视图；快照一旦变化就丢弃覆盖。
 */
export const optimistic = new Map<string, { base: string; view: DecisionView }>();

export function withOptimistic(list: DecisionView[]): DecisionView[] {
  return list.map((v) => {
    const key = keyOf(v);
    const o = optimistic.get(key);
    if (o && o.base === JSON.stringify(v)) return o.view;
    optimistic.delete(key);
    return v;
  });
}
