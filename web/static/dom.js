// DOM 与格式化工具。渲染规则（docs/design.md §9）：
//   - 一律用 createElement / textContent 构造节点；
//   - 唯一允许的 innerHTML 是 serverHTML()，且只接收服务端渲染的 *_html 字段（context_html、报告 html）。

// h 创建元素。attrs 支持 class、text、dataset、on<事件>，其余按 attribute 设置（值为 false/null 时跳过）。
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "text") el.textContent = String(v);
    else if (k === "dataset") Object.assign(el.dataset, v);
    else if (k.startsWith("on") && typeof v === "function") el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, String(v));
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === undefined || c === null || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else if (c instanceof Node) el.append(c);
    else el.append(document.createTextNode(String(c)));
  }
}

// serverHTML 插入服务端已脱敏、已按白名单渲染的 HTML。不要传入任何其他来源的字符串。
export function serverHTML(el, html) {
  el.innerHTML = html || "";
  for (const a of el.querySelectorAll("a[href]")) {
    a.target = "_blank";
    a.rel = "noopener noreferrer";
  }
  return el;
}

// fill 替换 el 的全部子节点；与 replaceChildren 不同，null/false 会被跳过而不是变成 "null" 文本。
export function fill(el, ...children) {
  el.replaceChildren();
  append(el, children);
  return el;
}

// extLink 只为 http(s) 链接生成 <a>，在新窗口打开；其他协议或空 URL 只显示文字。
export function extLink(url, text, cls) {
  if (typeof url === "string" && /^https?:\/\//i.test(url)) {
    return h("a", { class: cls, href: url, target: "_blank", rel: "noopener noreferrer", onclick: (e) => e.stopPropagation() }, text, " ↗");
  }
  return h("span", { class: cls, text });
}

// ---- 时间 ----

const pad = (n) => String(n).padStart(2, "0");

export function parseTime(s) {
  if (!s) return null;
  const t = Date.parse(s);
  if (Number.isNaN(t) || t <= 0) return null; // Go 零值 0001-01-01 也视为空
  return t;
}

// fmtClock：同一天显示 HH:MM(:SS)，否则加 MM-DD。
export function fmtClock(ts, now, withSec) {
  const t = typeof ts === "number" ? ts : parseTime(ts);
  if (t === null) return "—";
  const d = new Date(t);
  const n = new Date(now);
  let s = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (withSec) s += `:${pad(d.getSeconds())}`;
  if (d.toDateString() !== n.toDateString()) s = `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${s}`;
  if (d.getFullYear() !== n.getFullYear()) s = `${d.getFullYear()}-${s}`;
  return s;
}

// fmtDuration 把秒数格式化为 1h15m / 3d2h / 45s。
export function fmtDuration(sec) {
  sec = Math.max(0, Math.floor(sec));
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m`;
  const hrs = Math.floor(m / 60);
  if (hrs < 48) return `${hrs}h${pad(m % 60)}m`;
  return `${Math.floor(hrs / 24)}d${hrs % 24}h`;
}

// relTime 生成会随时钟刷新的相对时间节点。kind：ago（x 前）| countdown（还剩 x / 已超时 x）。
export function relTime(ts, kind, now) {
  const el = h("span", { class: "rel", dataset: { ts: String(ts), rel: kind } });
  updateRel(el, now);
  return el;
}

function updateRel(el, now) {
  const t = Number(el.dataset.ts);
  const diff = Math.round((t - now) / 1000);
  if (el.dataset.rel === "countdown") {
    el.textContent = diff >= 0 ? `还剩 ${fmtDuration(diff)}` : `已超时 ${fmtDuration(-diff)}`;
    el.classList.toggle("overdue", diff < 0);
  } else {
    el.textContent = -diff < 60 ? "刚刚" : `${fmtDuration(-diff)} 前`;
  }
}

export function updateRelTimes(root, now) {
  for (const el of root.querySelectorAll(".rel[data-ts]")) updateRel(el, now);
}

// ---- 提示 ----

let toastTimer = 0;
export function toast(msg, kind) {
  const box = document.getElementById("toast");
  if (!box) return;
  box.textContent = msg;
  box.className = `toast show ${kind || ""}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (box.className = "toast"), 5000);
}

export function errorMessage(err) {
  if (!err) return "未知错误";
  if (err.code === "network") return err.message;
  if (err.status === 403) return "鉴权失败（token / Origin / Host），请刷新页面后重试";
  const msg = err.message || err.code || "请求失败";
  return err.status && !msg.includes(String(err.status)) ? `${msg}（HTTP ${err.status}）` : msg;
}
