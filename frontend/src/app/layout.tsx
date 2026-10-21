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

// demo：在首帧前应用手动选择的外观，避免闪烁。完整迁移时改为外链脚本或去掉手动切换，以满足 script-src 'self'。
const themeInit = `try{var t=localStorage.getItem("lc-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch(e){}`;

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeInit }} />
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
