// 极简 Chrome DevTools Protocol 客户端（无第三方依赖，Node ≥ 22 自带 WebSocket）。
// 供 scripts/e2e-browser.mjs（make e2e 的浏览器阶段）和 scripts/screenshots.mjs 使用。

import { spawn, execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** 找本机 Chrome / Chromium / Edge；环境变量 CHROME 优先。找不到返回 null。 */
export function findChrome() {
  const candidates = [
    process.env.CHROME,
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
  ];
  for (const c of candidates) if (c && existsSync(c)) return c;
  for (const name of ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"]) {
    try {
      const p = execFileSync("sh", ["-c", `command -v ${name}`], { encoding: "utf8" }).trim();
      if (p) return p;
    } catch {
      /* 未安装 */
    }
  }
  return null;
}

/**
 * 启动无头浏览器并连接第一个页面。返回 { send, on, evaluate, waitFor, close }。
 * 临时 profile 目录，结束时删除；不影响本机浏览器。
 */
export async function launch(chromePath, { port = 19000 + Math.floor(Math.random() * 900) } = {}) {
  const profile = mkdtempSync(join(tmpdir(), "lc-chrome-"));
  const proc = spawn(
    chromePath,
    ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--hide-scrollbars", "about:blank"],
    { stdio: "ignore" },
  );
  let wsURL;
  for (let i = 0; i < 100 && !wsURL; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      wsURL = list.find((t) => t.type === "page")?.webSocketDebuggerUrl;
    } catch {
      /* 浏览器尚未就绪 */
    }
    if (!wsURL) await sleep(100);
  }
  if (!wsURL) {
    proc.kill();
    throw new Error("无法连接无头浏览器的调试端口");
  }
  const ws = new WebSocket(wsURL);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve);
    ws.addEventListener("error", reject);
  });

  let seq = 0;
  const pending = new Map();
  const listeners = new Map();
  ws.addEventListener("message", (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(`${msg.error.message}`)) : resolve(msg.result);
    } else if (msg.method) {
      for (const fn of listeners.get(msg.method) || []) fn(msg.params);
    }
  });

  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = ++seq;
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  const on = (method, fn) => listeners.set(method, [...(listeners.get(method) || []), fn]);

  /** 在页面中执行表达式（可返回 Promise），返回值按 JSON 传回；页面异常转成 Error。 */
  async function evaluate(expression) {
    const r = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  }

  /** 轮询表达式直到为真值；超时抛出带描述的错误。 */
  async function waitFor(desc, expression, timeoutMs = 10_000) {
    const end = Date.now() + timeoutMs;
    let last;
    while (Date.now() < end) {
      try {
        last = await evaluate(expression);
        if (last) return last;
      } catch (e) {
        last = e.message;
      }
      await sleep(100);
    }
    throw new Error(`等待超时（${timeoutMs / 1000}s）：${desc}（最后结果：${JSON.stringify(last)}）`);
  }

  async function close() {
    try {
      ws.close();
    } catch {
      /* 已关闭 */
    }
    proc.kill();
    await sleep(200);
    rmSync(profile, { recursive: true, force: true });
  }

  return { send, on, evaluate, waitFor, close };
}
