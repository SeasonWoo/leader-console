import type { Metadata, Viewport } from "next";
import { Suspense, type ReactNode } from "react";
import { ConsoleProvider } from "@/components/console-provider";
import { AppShell } from "@/components/app-shell";
import { ShellFallback } from "@/components/shell-fallback";
import "./globals.css";

export const metadata: Metadata = {
  title: "leader-console",
  icons: { icon: "/favicon.png" },
};

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f5f5f7" },
    { media: "(prefers-color-scheme: dark)", color: "#000000" },
  ],
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        {/* 同步外链脚本：首帧前应用手动选择的外观（public/theme-init.js）。服务端会在 <head> 开头注入 lc-token meta。 */}
        <script src="/theme-init.js" />
      </head>
      <body>
        <Suspense fallback={<ShellFallback />}>
          <ConsoleProvider>
            <AppShell>{children}</AppShell>
          </ConsoleProvider>
        </Suspense>
      </body>
    </html>
  );
}
