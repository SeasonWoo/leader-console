// hash 路由与列表复用工具：#/decisions、#/board?project=、#/task/<project>/<id>。

const enc = encodeURIComponent;

export function parseRoute(hash) {
  const raw = (hash || "").replace(/^#/, "") || "/decisions";
  const qi = raw.indexOf("?");
  const path = qi >= 0 ? raw.slice(0, qi) : raw;
  const params = new URLSearchParams(qi >= 0 ? raw.slice(qi + 1) : "");
  let parts;
  try {
    parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  } catch {
    parts = [];
  }
  if (parts[0] === "board") return { name: "board", params };
  if (parts[0] === "task" && parts.length === 3) return { name: "task", project: parts[1], id: parts[2], params };
  return { name: "decisions", params };
}

const withQuery = (path, q) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(q)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `${path}?${s}` : path;
};

export const decisionsHref = (project, focus) => withQuery("#/decisions", { project, focus });
export const boardHref = (project) => withQuery("#/board", { project });
export const taskHref = (project, id, report) => withQuery(`#/task/${enc(project)}/${enc(id)}`, { report });

// syncChildren 让 container 的子节点与 items 一致：key 相同且 sig 未变的节点原样复用（保留焦点与输入），
// 其余重新 build。cache 由调用方持有（key → {sig, el}）。
export function syncChildren(container, items, cache) {
  const next = new Map();
  const nodes = items.map((it) => {
    let c = cache.get(it.key);
    if (!c || c.sig !== it.sig) c = { sig: it.sig, el: it.build() };
    next.set(it.key, c);
    return c.el;
  });
  nodes.forEach((n, i) => {
    if (container.children[i] !== n) container.insertBefore(n, container.children[i] || null);
  });
  while (container.children.length > nodes.length) container.lastElementChild.remove();
  cache.clear();
  for (const [k, v] of next) cache.set(k, v);
}
