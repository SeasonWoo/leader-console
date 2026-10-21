// 时间与时长格式化。

const pad = (n: number) => String(n).padStart(2, "0");

export function parseTime(s?: string | null): number | null {
  if (!s) return null;
  const t = Date.parse(s);
  if (Number.isNaN(t) || t <= 0) return null; // Go 零值 0001-01-01 也视为空
  return t;
}

/** 同一天显示 HH:MM(:SS)，否则加 MM-DD；跨年再加年份。 */
export function fmtClock(ts: string | number | undefined | null, now: number, withSec = false): string {
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

/** 秒数 → 1h15m / 3d2h / 45s。 */
export function fmtDuration(sec: number): string {
  sec = Math.max(0, Math.floor(sec));
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h${pad(m % 60)}m`;
  return `${Math.floor(h / 24)}d${h % 24}h`;
}

export function fmtAgo(ts: number, now: number): string {
  const diff = Math.round((now - ts) / 1000);
  return diff < 60 ? "刚刚" : `${fmtDuration(diff)}前`;
}

export function fmtCountdown(ts: number, now: number): { text: string; overdue: boolean } {
  const diff = Math.round((ts - now) / 1000);
  return diff >= 0
    ? { text: `还剩 ${fmtDuration(diff)}`, overdue: false }
    : { text: `已超时 ${fmtDuration(-diff)}`, overdue: true };
}

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
