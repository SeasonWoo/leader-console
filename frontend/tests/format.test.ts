import { test } from "node:test";
import assert from "node:assert/strict";
import { fmtCountdown, fmtDuration, fmtSize, parseTime } from "../src/lib/format.ts";

test("parseTime：空值与 Go 零值视为空", () => {
  assert.equal(parseTime(undefined), null);
  assert.equal(parseTime(""), null);
  assert.equal(parseTime("0001-01-01T00:00:00Z"), null);
  assert.equal(parseTime("2026-10-21T16:00:00+08:00"), Date.parse("2026-10-21T08:00:00Z"));
});

test("fmtDuration", () => {
  assert.equal(fmtDuration(-5), "0s");
  assert.equal(fmtDuration(45), "45s");
  assert.equal(fmtDuration(600), "10m");
  assert.equal(fmtDuration(4500), "1h15m");
  assert.equal(fmtDuration(3 * 86400 + 7200), "3d2h");
});

test("fmtCountdown 区分剩余与超时", () => {
  assert.deepEqual(fmtCountdown(120_000, 0), { text: "还剩 2m", overdue: false });
  assert.deepEqual(fmtCountdown(0, 90_000), { text: "已超时 1m", overdue: true });
});

test("fmtSize", () => {
  assert.equal(fmtSize(512), "512 B");
  assert.equal(fmtSize(1830), "1.8 KB");
  assert.equal(fmtSize(1048576), "1.0 MB");
});
