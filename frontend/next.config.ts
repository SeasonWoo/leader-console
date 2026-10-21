import type { NextConfig } from "next";

// 静态导出：产物是纯 HTML/JS/CSS，运行时不需要 Node（最终由 Go 二进制 embed 提供）。
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
  images: { unoptimized: true },
  reactStrictMode: true,
  generateBuildId: async () => "leader-console",
};

export default nextConfig;
