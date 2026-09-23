import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";
import { AuthProvider } from "@/components/auth-provider";
import { SiteNavbar } from "@/components/site-navbar";
import { ToastProvider } from "@/components/toast-provider";
import { PlatformNameProvider } from "@/components/platform-name-provider";
import { getPlatformName } from "@/lib/server/platform-name";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export async function generateMetadata(): Promise<Metadata> {
  const platformName = await getPlatformName();
  return {
    title: `${platformName} | HPC Performance Arena`,
    description: `面向高性能计算应用的在线测评平台 · ${platformName}`,
  };
}

export default async function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const platformName = await getPlatformName();
  return (
    <html
      lang="zh-CN"
      className={`${geistSans.variable} ${geistMono.variable} h-full antialiased`}
      suppressHydrationWarning
    >
      <head>
        <script
          dangerouslySetInnerHTML={{
            __html: `
              (function () {
                try {
                  var saved = localStorage.getItem("soj-theme");
                  var theme = saved === "dark" || saved === "light" ? saved : null;
                  if (!theme) {
                    theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
                  }
                  document.documentElement.dataset.theme = theme;
                } catch (e) {}
              })();
            `,
          }}
        />
      </head>
      <body className="min-h-full flex flex-col">
        <PlatformNameProvider name={platformName}>
          <ToastProvider>
            <AuthProvider>
              <SiteNavbar />
              <main className="flex-1">{children}</main>
            </AuthProvider>
          </ToastProvider>
        </PlatformNameProvider>
      </body>
    </html>
  );
}
