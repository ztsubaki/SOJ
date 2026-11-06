"use client";

import Link from "next/link";
import { useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Icon } from "@/components/icon";
import { useAuth } from "@/components/auth-provider";
import { ThemeControls } from "@/components/theme-controls";
import { usePlatformName } from "@/components/platform-name-provider";
import { useToast } from "@/components/toast-provider";

const navigation = [
  { href: "/", label: "首页" },
  { href: "/contests", label: "比赛" },
];

export function SiteNavbar() {
  const platformName = usePlatformName();
  const pathname = usePathname();
  const router = useRouter();
  const { isLoading, signOut, user } = useAuth();
  const { show } = useToast();
  const [isSigningOut, setIsSigningOut] = useState(false);
  const isActive = (href: string) =>
    href === "/" ? pathname === href : pathname.startsWith(href);

  async function handleSignOut() {
    setIsSigningOut(true);
    try {
      await signOut();
      router.replace("/login");
    } catch (reason) {
      show(reason instanceof Error ? `退出失败：${reason.message}` : "退出失败，请稍后重试。", "error");
    } finally {
      setIsSigningOut(false);
    }
  }

  return (
    <nav className="navbar sticky top-0 z-50 min-h-16 bg-base-100 px-4 shadow-sm sm:px-6">
      <div className="navbar-start">
        <details className="dropdown lg:hidden">
          <summary className="btn btn-ghost btn-square cursor-pointer" aria-label="打开导航">
            <Icon name="menu" />
          </summary>
          <ul className="dropdown-content menu mt-2 w-48 rounded-box border border-base-content/10 bg-base-100 p-2 shadow-lg">
            {navigation.map((item) => (
              <li key={item.href}>
                <Link
                  className={isActive(item.href) ? "font-semibold text-primary" : ""}
                  href={item.href}
                >
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </details>
        <Link href="/" className="btn btn-ghost text-xl font-bold">
          {platformName}
        </Link>
      </div>
      <div className="navbar-center hidden lg:flex">
        <ul className="menu menu-horizontal gap-1 px-1">
          {navigation.map((item) => (
            <li key={item.href}>
              <Link
                className={isActive(item.href) ? "font-semibold text-primary" : ""}
                href={item.href}
              >
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </div>
        <div className="navbar-end gap-2">
          <ThemeControls />
          {!isLoading && (user ? (
            <>
              <span className="hidden text-sm font-medium sm:inline">{user.username}</span>
              <button type="button" className="btn btn-ghost btn-sm" disabled={isSigningOut} onClick={() => void handleSignOut()}>
                {isSigningOut ? "退出中..." : "退出"}
              </button>
            </>
          ) : (
            <>
              <Link href="/login" className="btn btn-ghost btn-sm">
                登录
              </Link>
              <Link href="/register" className="btn btn-primary btn-sm">
                注册
              </Link>
            </>
          ))}
        </div>
    </nav>
  );
}
