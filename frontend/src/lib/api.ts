// HTTP API 客户端（docs/design.md §6）。URL 带 ?mock=…（1 / herdr-down）时改用内存 mock（lib/mock.ts，按需加载），
// 不访问真实后端。写请求带 X-LC-Token（服务端注入首页 <meta name="lc-token">）与 Content-Type: application/json。

import type {
  AnswerInput,
  DecisionView,
  Overview,
  Report,
  TaskDetail,
  TasksResponse,
  Terminal,
} from "./types";
import type { MockBackend } from "./mock";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public body?: unknown,
  ) {
    super(message || code);
  }
}

let scenario: string | null | undefined;
/** null：真实后端；"1"：默认 mock；"herdr-down" 等场景名。首次调用时从 URL 读取并固定，跨页面跳转不变。 */
export function mockScenario(): string | null {
  if (typeof window === "undefined") return null;
  if (scenario === undefined) {
    const p = new URLSearchParams(window.location.search);
    scenario = p.has("mock") ? p.get("mock") || "1" : null;
  }
  return scenario;
}

let token: string | undefined;
function lcToken(): string {
  token ??= document.querySelector<HTMLMetaElement>('meta[name="lc-token"]')?.content ?? "";
  return token;
}

// 缓存 Promise：并发的首次调用必须拿到同一个 mock 后端（订阅与请求共享状态）。
let backend: Promise<MockBackend> | null = null;
function mock(): Promise<MockBackend> {
  backend ||= import("./mock").then((m) => m.createMock(mockScenario() || "1"));
  return backend;
}

function unwrap<T>(status: number, data: unknown): T {
  if (status >= 200 && status < 300) return data as T;
  const e = (data as { error?: { code?: string; message?: string } } | null)?.error || {};
  throw new ApiError(status, e.code || `http_${status}`, e.message || `HTTP ${status}`, data);
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  if (mockScenario()) {
    const res = await (await mock()).handle(method, path, body);
    return unwrap<T>(res.status, res.body);
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, headers, credentials: "same-origin", cache: "no-store" };
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    headers["X-LC-Token"] = lcToken();
    init.body = JSON.stringify(body || {});
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch {
    throw new ApiError(0, "network", "无法连接 leader-console 服务");
  }
  let data: unknown = null;
  try {
    data = await res.json();
  } catch {
    if (res.ok) throw new ApiError(res.status, "bad_response", "服务端返回了非 JSON 内容");
  }
  return unwrap<T>(res.status, data);
}

const enc = encodeURIComponent;
function qs(obj: Record<string, string | number | undefined>) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) if (v !== undefined && v !== "") p.set(k, String(v));
  const s = p.toString();
  return s ? `?${s}` : "";
}

export function newClientRequestID() {
  const b = new Uint8Array(8);
  crypto.getRandomValues(b);
  return "c-" + Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export const api = {
  overview: () => request<Overview>("GET", "/api/v1/overview"),
  /** 任务已由服务端按更新时间倒序排好（design §6），前端分列时保持该顺序。 */
  tasks: (project?: string) => request<TasksResponse>("GET", `/api/v1/tasks${qs({ project })}`),
  task: (project: string, id: string) => request<TaskDetail>("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}`),
  report: (project: string, id: string, report: string) =>
    request<Report>("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}/reports/${enc(report)}`),
  terminal: (project: string, id: string, source: string, lines: number) =>
    request<Terminal>("GET", `/api/v1/tasks/${enc(project)}/${enc(id)}/terminal${qs({ source, lines })}`),
  answer: (project: string, decision: string, input: AnswerInput) =>
    request<DecisionView>("POST", `/api/v1/decisions/${enc(project)}/${enc(decision)}/answer`, input),
  retry: (project: string, answer: string) =>
    request<DecisionView>("POST", `/api/v1/answers/${enc(project)}/${enc(answer)}/retry`, {}),
  /** 只在 mock 模式可用：模拟数据变化时回调（真实后端走 SSE，见 console-provider）。 */
  subscribeMock: async (fn: (version: string) => void) => (await mock()).subscribe(fn),
};

export function errorMessage(err: unknown): string {
  if (!err) return "未知错误";
  if (err instanceof ApiError) {
    if (err.code === "network") return err.message;
    if (err.status === 403) return "鉴权失败（token / Origin / Host），请刷新页面后重试";
    const msg = err.message || err.code || "请求失败";
    return err.status && !msg.includes(String(err.status)) ? `${msg}（HTTP ${err.status}）` : msg;
  }
  return err instanceof Error ? err.message : String(err);
}
