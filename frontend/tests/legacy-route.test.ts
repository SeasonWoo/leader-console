import { test } from "node:test";
import assert from "node:assert/strict";
import { legacyHashToPath, withQuery } from "../src/lib/legacy-route.ts";

test("旧 hash 链接转换为新路由", () => {
  const cases: [string, string, string | null][] = [
    ["", "", null],
    ["#section", "", null],
    ["#/", "", "/"],
    ["#/decisions", "", "/"],
    ["#/decisions?project=demo&focus=d-1", "", "/?project=demo&focus=d-1"],
    ["#/board", "", "/board/"],
    ["#/board?project=tob-x", "", "/board/?project=tob-x"],
    ["#/task/demo/uih-copilot", "", "/task/?project=demo&id=uih-copilot"],
    ["#/task/demo/uih-copilot?report=report.md", "", "/task/?project=demo&id=uih-copilot&report=report.md"],
    ["#/task/a%2Fb/x%20y", "", "/task/?project=a%2Fb&id=x+y"],
    ["#/task/demo", "", "/"],
    ["#/board", "?mock=1", "/board/?mock=1"],
    ["#/board", "?mock=herdr-down", "/board/?mock=herdr-down"],
    ["#/task/%E0%A4%A/x", "", "/"],
  ];
  for (const [hash, search, want] of cases) assert.equal(legacyHashToPath(hash, search), want, `${hash} ${search}`);
});

test("withQuery 跳过空参数", () => {
  assert.equal(withQuery("/board/", { project: "", mock: undefined }), "/board/");
  assert.equal(withQuery("/", { project: "p q", focus: "d" }), "/?project=p+q&focus=d");
});
