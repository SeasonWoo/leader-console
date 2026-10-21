// 骨架：只探测后端存活。页面逻辑由任务 C 实现（见 docs/tasks.md）。
const res = await fetch("/healthz");
const body = await res.json();
document.querySelector("#app").append(`后端状态：${body.status}`);
