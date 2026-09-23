"use client";
import Panel from "@/components/ui/Panel";

import { useSidebar } from "@/context/SidebarContext";
import { useAuth } from "@/context/AuthContext";
import AppHeader from "@/layout/AppHeader";
import AppSidebar from "@/layout/AppSidebar";
import Backdrop from "@/layout/Backdrop";
import Button from "@/components/ui/button/Button";
import { useRouter } from "next/navigation";
import React from "react";

export default function AdminLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { isExpanded, isHovered, isMobileOpen } = useSidebar();
  const { user, status, signOut } = useAuth();
  const router = useRouter();

  React.useEffect(() => {
    if (status === "unauthenticated") router.replace("/login");
  }, [router, status, user]);

  if (status === "loading") {
    return (
      <main className="flex min-h-screen items-center justify-center bg-gray-50 p-4 dark:bg-gray-900">
        <Panel className="px-6 py-5 text-center shadow-theme-xs">
          <span
            className="mx-auto mb-3 block h-8 w-8 animate-spin rounded-full border-4 border-gray-200 border-t-brand-500 dark:border-gray-700 dark:border-t-brand-400"
            aria-hidden="true"
          />
          <p className="text-theme-sm font-medium text-gray-700 dark:text-gray-300">
            正在验证管理员会话...
          </p>
        </Panel>
      </main>
    );
  }

  if (status === "unauthenticated") return null;

  if (user?.role !== "admin") {
    return (
      <main className="flex min-h-screen items-center justify-center bg-gray-50 p-4 dark:bg-gray-900">
        <Panel className="max-w-md p-6 text-center shadow-theme-xs sm:p-8">
          <p className="text-theme-sm font-semibold text-error-600 dark:text-error-400">
            403 Forbidden
          </p>
          <h1 className="mt-2 text-2xl font-bold text-gray-900 dark:text-white">
            无管理员访问权限
          </h1>
          <p className="mt-3 text-theme-sm text-gray-500 dark:text-gray-400">
            当前账号 {user?.username} 的角色为 {user?.role ?? "未知"}
            ，无法访问管理后台。
          </p>
          <div className="mt-6 flex flex-col justify-center gap-3 sm:flex-row">
            <Button onClick={() => void signOut()}>退出登录</Button>
          </div>
        </Panel>
      </main>
    );
  }

  const mainContentMargin = isMobileOpen
    ? "ml-0"
    : isExpanded || isHovered
      ? "lg:ml-[290px]"
      : "lg:ml-[90px]";

  return (
    <div className="min-h-screen xl:flex">
      <AppSidebar />
      <Backdrop />
      <div
        className={`flex-1 transition-all duration-300 ease-in-out ${mainContentMargin}`}
      >
        <AppHeader />
        <main className="p-4 mx-auto max-w-(--breakpoint-2xl) md:p-6">
          {children}
        </main>
      </div>
    </div>
  );
}
