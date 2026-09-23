"use client";
import Panel from "@/components/ui/Panel";

import { useRouter } from "next/navigation";
import { useState } from "react";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import { createUser } from "@/lib/users-api";
import { ToastMessage } from "@/context/ToastContext";

export default function NewUserPage() {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setSaving(true);
    try {
      const result = await createUser({
        username,
        email,
      });
      router.push(`/users/${encodeURIComponent(result.user.id)}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建用户失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            创建用户
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            创建普通用户账号。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => router.push("/users")}
          >
            取消
          </Button>
          <Button type="submit" disabled={saving}>
            {saving ? "创建中..." : "创建"}
          </Button>
        </div>
      </div>
      <div className="col-span-12 lg:col-span-8">
        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            基本信息
          </h3>
          <div className="mt-5 grid gap-5">
            <div>
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="username"
                minLength={3}
                maxLength={32}
                pattern="[A-Za-z0-9_-]+"
                title="不支持特殊字符"
                required
              />
            </div>
            <div>
              <Label htmlFor="email">邮箱</Label>
              <Input
                id="email"
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                autoComplete="email"
                maxLength={254}
                pattern="[\\x21-\\x7E]+"
                title="不支持特殊字符"
                required
              />
            </div>
            <p className="text-theme-sm text-gray-500 dark:text-gray-400">
              创建后，请在用户详情中重置密码并一次性记录系统生成的初始密码。
            </p>
            <ToastMessage message={error} />
          </div>
        </Panel>
      </div>
    </form>
  );
}
