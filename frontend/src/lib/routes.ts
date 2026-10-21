// 路由：/（待我决定）、/board、/task?project=&id=&report=。静态导出不支持动态段，用查询参数，刷新与直链可用。
// demo 里保留 mock 场景参数，跨页面跳转不丢。

import { mockScenario } from "./api";

function withQuery(path: string, q: Record<string, string | undefined>) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v) p.set(k, v);
  const scenario = mockScenario();
  if (scenario !== "1") p.set("mock", scenario);
  const s = p.toString();
  return s ? `${path}?${s}` : path;
}

export const decisionsHref = (project?: string, focus?: string) => withQuery("/", { project, focus });
export const boardHref = (project?: string) => withQuery("/board/", { project });
export const taskHref = (project: string, id: string, report?: string) =>
  withQuery("/task/", { project, id, report });
