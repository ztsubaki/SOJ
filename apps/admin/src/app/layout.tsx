import type { Metadata } from "next";
import "./globals.css";
import { SidebarProvider } from "@/context/SidebarContext";
import { ThemeProvider } from "@/context/ThemeContext";
import { AuthProvider } from "@/context/AuthContext";
import { ToastProvider } from "@/context/ToastContext";
import { PlatformNameProvider } from "@/context/PlatformNameContext";
import { getPlatformName } from "@/lib/server/platform-name";

export async function generateMetadata(): Promise<Metadata> {
  const platformName = await getPlatformName();
  return {
    title: `${platformName} 管理后台`,
    description: `${platformName} 在线测评平台管理后台`,
  };
}

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const platformName = await getPlatformName();
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script
          dangerouslySetInnerHTML={{
            __html: `
              (function () {
                try {
                  var saved = localStorage.getItem("soj-admin-theme");
                  var theme = saved === "dark" || saved === "light" ? saved : null;
                  if (!theme) {
                    theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
                  }
                  document.documentElement.classList.add(theme);
                } catch (e) {}
              })();
            `,
          }}
        />
      </head>
      <body className="dark:bg-gray-900">
        <PlatformNameProvider name={platformName}>
          <ToastProvider>
            <ThemeProvider>
              <AuthProvider>
                <SidebarProvider>{children}</SidebarProvider>
              </AuthProvider>
            </ThemeProvider>
          </ToastProvider>
        </PlatformNameProvider>
      </body>
    </html>
  );
}
