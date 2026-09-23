"use client";
import Panel from "@/components/ui/Panel";

import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import { useAuth } from "@/context/AuthContext";
import { ToastMessage } from "@/context/ToastContext";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useState } from "react";

export default function LoginPage() {
  const { status, user, signIn, signOut } = useAuth();
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  useEffect(() => {
    if (status === "authenticated" && user?.role === "admin") {
      router.replace("/");
    }
  }, [router, status, user]);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setIsSubmitting(true);

    try {
      const signedInUser = await signIn(username, password);
      if (signedInUser.role !== "admin") {
        await signOut();
        setError("该账号没有管理员权限，请使用管理员账号登录。");
        return;
      }
      router.replace("/");
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "登录失败，请稍后重试。",
      );
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center bg-gray-50 p-4 dark:bg-gray-900">
      <Panel as="section"
        className="w-full max-w-md p-6 shadow-theme-md sm:p-8"
        aria-labelledby="login-title"
      >
        <p className="text-theme-sm font-semibold text-brand-500 dark:text-brand-400">
          SOJ 管理后台
        </p>
        <h1
          id="login-title"
          className="mt-2 text-2xl font-bold text-gray-900 dark:text-white"
        >
          管理员登录
        </h1>
        <p className="mt-2 text-theme-sm text-gray-500 dark:text-gray-400">
          使用管理员账号继续访问平台管理功能。
        </p>

        <form className="mt-6 space-y-5" onSubmit={handleSubmit}>
          <div>
            <Label htmlFor="username">用户名</Label>
            <Input
              id="username"
              name="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              autoComplete="username"
              disabled={isSubmitting}
              required
            />
          </div>
          <div>
            <Label htmlFor="password">密码</Label>
            <Input
              id="password"
              name="password"
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="current-password"
              disabled={isSubmitting}
              required
            />
          </div>
          <ToastMessage message={error} />
          <Button type="submit" className="w-full" disabled={isSubmitting}>
            {isSubmitting ? "正在登录..." : "登录"}
          </Button>
        </form>
      </Panel>
    </main>
  );
}
