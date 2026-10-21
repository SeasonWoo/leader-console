// HTTP API 封装（docs/design.md §6）。URL 带 ?mock=… 时改读 web/static/mock/*.json（见 mock.js）。

const tokenMeta = document.querySelector('meta[name="lc-token"]');
const token = tokenMeta ? tokenMeta.content : "";

const params = new URLSearchParams(location.search);
// mockScenario：null（真实后端）| "1"（默认 mock）| "herdr-down" 等场景名。
export const mockScenario = params.has("mock") ? params.get("mock") || "1" : null;

export class ApiError extends Error {
  constructor(status, code, message, body) {
    super(message || code);
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

// 缓存 Promise 而不是实例：并发的首次调用必须拿到同一个 mock 后端（订阅与请求共享状态）。
let mockBackend = null;
function mock() {
  mockBackend ||= import("./mock.js").then((m) => m.createMock(mockScenario));
  return mockBackend;
}

async function request(method, path, body) {
  if (mockScenario) {
    const res = await (await mock()).handle(method, path, body);
    return unwrap(res.status, res.body);
  }
  const headers = { Accept: "application/json" };
  const init = { method, headers, credentials: "same-origin", cache: "no-store" };
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-LC-Token"] = token;
    init.body = JSON.stringify(body || {});
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, "network", "无法连接 leader-console 服务");
  }
  let data = null;
  try {
    data = await res.json();
  } catch {
    if (res.ok) throw new ApiError(res.status, "bad_response", "服务端返回了非 JSON 内容");
  }
  return unwrap(res.status, data);
}

function unwrap(status, data) {
  if (status >= 200 && status < 300) return data;
  const e = (data && data.error) || {};
  throw new ApiError(status, e.code || `http_${status}`, e.message || `HTTP ${status}`, data);
}

const enc = encodeURIComponent;
function qs(obj) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) if (v !== undefined && v !== null && v !== "") p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : "";
}

export function newClientRequestID() {
  const b = new Uint8Array(8);
  crypto.getRandomValues(b);
  return "c-" + Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export const api = {
  overview: () => request("GET", "/api/v1/overview"),
  tasks: (project, column) => request("GET", `/api/v1/tasks${qs({ project, column })}`),
  task: (project, id) => request("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}`),
  report: (project, id, report) => request("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}/reports/${enc(report)}`),
  terminal: (project, id, source, lines) =>
    request("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}/terminal${qs({ source, lines })}`),
  decisions: (project, state) => request("GET", `/api/v1/decisions${qs({ project, state })}`),
  // input: {option, text, client_request_id}
  answer: (project, decision, input) => request("POST", `/api/v1/decisions/${enc(project)}/${enc(decision)}/answer`, input),
  retry: (project, answer) => request("POST", `/api/v1/answers/${enc(project)}/${enc(answer)}/retry`, {}),
  // subscribe 只在 mock 模式可用：模拟数据变化时回调（真实后端走 SSE，见 app.js）。
  subscribe: async (fn) => (mockScenario ? (await mock()).subscribe(fn) : null),
};
