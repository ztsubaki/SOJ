"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { useAuth } from "@/components/auth-provider";
import { ToastMessage } from "@/components/toast-provider";

export default function ChangePasswordPage() {
  const router = useRouter();
  const { changePassword, isLoading, user } = useAuth();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!isLoading && !user) router.replace("/login");
  }, [isLoading, router, user]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (newPassword !== confirmPassword) {
      setError("两次输入的新密码不一致");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await changePassword(currentPassword, newPassword);
      router.replace("/login");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "修改密码失败");
    } finally {
      setSubmitting(false);
    }
  }

  if (!isLoading && !user) return null;

  return (
    <section className="flex min-h-[calc(100dvh-4rem)] items-center justify-center bg-base-200 px-4 py-8">
      <div className="card w-full max-w-md bg-base-100 shadow-xl">
        <form className="card-body" onSubmit={submit}>
          <h1 className="card-title text-2xl">修改密码</h1>
          <p className="text-sm text-base-content/70">
            修改成功后全部会话失效，请重新登录。
          </p>
          <label className="label" htmlFor="current-password">
            当前密码
          </label>
          <input
            id="current-password"
            className="input w-full"
            type="password"
            autoComplete="current-password"
            required
            value={currentPassword}
            onChange={(event) => setCurrentPassword(event.target.value)}
          />
          <label className="label" htmlFor="new-password">
            新密码
          </label>
          <input
            id="new-password"
            className="input w-full"
            type="password"
            autoComplete="new-password"
            required
            minLength={8}
            maxLength={64}
            pattern="[\\x20-\\x7E]+"
            title="不支持特殊字符"
            value={newPassword}
            onChange={(event) => setNewPassword(event.target.value)}
          />
          <label className="label" htmlFor="confirm-password">
            确认新密码
          </label>
          <input
            id="confirm-password"
            className="input w-full"
            type="password"
            autoComplete="new-password"
            required
            minLength={8}
            maxLength={64}
            pattern="[\\x20-\\x7E]+"
            title="不支持特殊字符"
            value={confirmPassword}
            onChange={(event) => setConfirmPassword(event.target.value)}
          />
          <ToastMessage message={error} />
          <div className="card-actions mt-3">
            <button
              className="btn btn-primary w-full"
              type="submit"
              disabled={submitting}
            >
              {submitting ? "处理中..." : "确认修改"}
            </button>
          </div>
        </form>
      </div>
    </section>
  );
}
