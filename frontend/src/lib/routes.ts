// 路由：/（待我决定）、/board/、/task/?project=&id=&report=。静态导出不支持动态段，用查询参数，刷新与直链可用。
// mock 模式下保留 ?mock= 参数，跨页面跳转不丢。
// 任务链接只差查询参数、页面外壳相同，<Link> 一律 prefetch={false}，避免看板上每张卡片各发一次预取。

import { mockScenario } from "./api";
import { withQuery } from "./legacy-route";

const q = (params: Record<string, string | undefined>) => ({ ...params, mock: mockScenario() || undefined });

export const decisionsHref = (project?: string, focus?: string) => withQuery("/", q({ project, focus }));
export const boardHref = (project?: string) => withQuery("/board/", q({ project }));
export const taskHref = (project: string, id: string, report?: string) => withQuery("/task/", q({ project, id, report }));
