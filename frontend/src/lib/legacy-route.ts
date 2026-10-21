// 旧前端的 hash 链接（#/decisions?project=&focus=、#/board?project=、#/task/<project>/<id>?report=）转换成新路由，
// 书签和 leader 消息里的旧链接仍然可用。不依赖其他模块，便于 node --test 直接测试。

export function withQuery(path: string, params: Record<string, string | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `${path}?${s}` : path;
}

/**
 * hash：location.hash（含 #）；search：location.search（保留其中的 mock 等参数）。
 * 不是旧格式的 hash 返回 null。
 */
export function legacyHashToPath(hash: string, search = ""): string | null {
  if (!hash.startsWith("#/")) return null;
  const raw = hash.slice(1);
  const qi = raw.indexOf("?");
  const path = qi >= 0 ? raw.slice(0, qi) : raw;
  const params = new URLSearchParams(qi >= 0 ? raw.slice(qi + 1) : "");
  const keep = new URLSearchParams(search);
  const mock = keep.has("mock") ? keep.get("mock") || "1" : undefined;
  let parts: string[];
  try {
    parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  } catch {
    parts = [];
  }
  const get = (k: string) => params.get(k) || undefined;
  if (parts[0] === "board") return withQuery("/board/", { project: get("project"), mock });
  if (parts[0] === "task" && parts.length === 3)
    return withQuery("/task/", { project: parts[1], id: parts[2], report: get("report"), mock });
  return withQuery("/", { project: get("project"), focus: get("focus"), mock });
}
