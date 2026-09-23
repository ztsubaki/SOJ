"use client";
import Panel from "@/components/ui/Panel";

import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import { useAuth } from "@/context/AuthContext";
import { ToastMessage } from "@/context/ToastContext";
import { usePlatformName } from "@/context/PlatformNameContext";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useState } from "react";

export default function ChangePasswordPage() {
  const platformName = usePlatformName();
  const { changePassword, status, user } = useAuth();
  const router = useRouter();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (status === "unauthenticated") router.replace("/login");
  }, [router, status]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (newPassword !== confirmation) {
      setError("两次输入的新密码不一致");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      await changePassword(currentPassword, newPassword);
      router.replace("/login");
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "修改密码失败，请稍后重试。",
      );
    } finally {
      setSubmitting(false);
    }
  }

  if (status === "loading" || !user) return null;

  return (
    <main className="flex min-h-screen items-center justify-center bg-gray-50 p-4 dark:bg-gray-900">
      <Panel as="section"
        className="w-full max-w-md p-6 shadow-theme-md sm:p-8"
        aria-labelledby="change-password-title"
      >
        <p className="text-theme-sm font-semibold text-brand-500 dark:text-brand-400">
          {platformName} 管理后台
        </p>
        <h1
          id="change-password-title"
          className="mt-2 text-2xl font-bold text-gray-900 dark:text-white"
        >
          修改密码
        </h1>
        <p className="mt-2 text-theme-sm text-gray-500 dark:text-gray-400">
          修改成功后全部会话失效，请重新登录。
        </p>
        <form className="mt-6 space-y-5" onSubmit={submit}>
          <div>
            <Label htmlFor="current-password">当前密码</Label>
            <Input
              id="current-password"
              type="password"
              autoComplete="current-password"
              required
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </div>
          <div>
            <Label htmlFor="new-password">新密码</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              minLength={8}
              maxLength={64}
              pattern="[\\x20-\\x7E]+"
              title="不支持特殊字符"
              required
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </div>
          <div>
            <Label htmlFor="confirmation">确认新密码</Label>
            <Input
              id="confirmation"
              type="password"
              autoComplete="new-password"
              minLength={8}
              maxLength={64}
              pattern="[\\x20-\\x7E]+"
              title="不支持特殊字符"
              required
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </div>
          <ToastMessage message={error} />
          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting ? "处理中..." : "确认修改"}
          </Button>
        </form>
      </Panel>
    </main>
  );
}
