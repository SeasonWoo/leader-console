// 页面截图（浅色 + 深色，按 prefers-color-scheme 仿真，验证的是“跟随系统”路径）。
// 用法：node scripts/screenshots.mjs <base-url> <输出目录> <名称=路径>...
//   名称以 -answering 结尾时，在第一张可作答的决策卡片上选中第一个选项并点“提交决定”（只展示二次确认，不提交）。
// 例：node scripts/screenshots.mjs http://127.0.0.1:17822 /tmp/shots decisions=/?mock=1 board=/board/?mock=1

import { writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { findChrome, launch, sleep } from "./cdp.mjs";

const [, , BASE, OUT, ...pairs] = process.argv;
const shots = pairs.map((p) => {
  const i = p.indexOf("=");
  return [p.slice(0, i), p.slice(i + 1)];
});
const chrome = findChrome();
if (!BASE || !OUT || !shots.length || !chrome) {
  console.error("用法：node scripts/screenshots.mjs <base-url> <输出目录> <名称=路径>...（需要本机 Chrome）");
  process.exit(2);
}
mkdirSync(OUT, { recursive: true });

const W = 1440;
const H = 900;
const errors = [];
const b = await launch(chrome);
b.on("Runtime.exceptionThrown", (p) => errors.push(p.exceptionDetails.exception?.description || p.exceptionDetails.text));
b.on("Log.entryAdded", ({ entry }) => entry.level === "error" && errors.push(`${entry.source}: ${entry.text}`));
await Promise.all([b.send("Runtime.enable"), b.send("Page.enable"), b.send("Log.enable")]);

const answer = `(() => {
  const card = [...document.querySelectorAll("article[data-key]")].find((a) => a.querySelector("input[type=radio]:not(:disabled)"));
  card.querySelector("input[type=radio]").click();
  return new Promise((r) => setTimeout(() => {
    [...card.querySelectorAll("button")].find((x) => x.textContent.trim() === "提交决定").click();
    setTimeout(r, 300);
  }, 200));
})()`;

for (const scheme of ["light", "dark"]) {
  await b.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }] });
  for (const [name, path] of shots) {
    await b.send("Emulation.setDeviceMetricsOverride", { width: W, height: H, deviceScaleFactor: 2, mobile: false });
    await b.send("Page.navigate", { url: BASE + path });
    await b.waitFor(`加载 ${path}`, `document.readyState === "complete" && !document.querySelector('[aria-busy="true"]')`, 15_000);
    await sleep(800);
    if (name.endsWith("-answering")) await b.evaluate(answer);
    const h = await b.evaluate("document.documentElement.scrollHeight");
    await b.send("Emulation.setDeviceMetricsOverride", { width: W, height: Math.min(Math.max(h, H), 5000), deviceScaleFactor: 2, mobile: false });
    await sleep(400);
    const shot = await b.send("Page.captureScreenshot", { format: "png" });
    const file = join(OUT, `${scheme}-${name}.png`);
    writeFileSync(file, Buffer.from(shot.data, "base64"));
    console.log(file);
  }
}
await b.close();
if (errors.length) {
  console.error(`页面错误：\n  ${errors.join("\n  ")}`);
  process.exit(1);
}
