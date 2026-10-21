// API 客户端（docs/design.md §6）。设计 demo 阶段所有请求都由内存 mock 处理，不访问真实后端；
// 完整迁移时 request() 改为 fetch(path)，写请求带 X-LC-Token / Content-Type: application/json。

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

let backend: Promise<MockBackend> | null = null;
function mock(): Promise<MockBackend> {
  backend ||= import("./mock").then((m) => m.createMock(mockScenario()));
  return backend;
}

/** demo 场景：默认 "1"，?mock=herdr-down 模拟 herdr 不可用。 */
export function mockScenario(): string {
  if (typeof window === "undefined") return "1";
  return new URLSearchParams(window.location.search).get("mock") || "1";
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await (await mock()).handle(method, path, body);
  if (res.status >= 200 && res.status < 300) return res.body as T;
  const e = ((res.body as { error?: { code?: string; message?: string } })?.error) || {};
  throw new ApiError(res.status, e.code || `http_${res.status}`, e.message || `HTTP ${res.status}`, res.body);
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
  /** 数据变化通知（真实后端为 SSE version 事件）。 */
  subscribe: async (fn: (version: string) => void) => (await mock()).subscribe(fn),
};

export function errorMessage(err: unknown): string {
  if (!err) return "未知错误";
  if (err instanceof ApiError) {
    if (err.code === "network") return err.message;
    if (err.status === 403) return "鉴权失败（token / Origin / Host），请刷新页面后重试";
    return err.message.includes(String(err.status)) ? err.message : `${err.message}（HTTP ${err.status}）`;
  }
  return err instanceof Error ? err.message : String(err);
}
