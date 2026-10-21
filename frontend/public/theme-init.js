// 首帧前应用手动选择的外观（左下角切换，存 localStorage "lc-theme"），避免闪烁。
// 外链脚本而非内联：CSP 为 script-src 'self'（内联脚本只允许 Next 生成并经服务端计算哈希的那些）。
try {
  var t = localStorage.getItem("lc-theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
} catch (e) {}
