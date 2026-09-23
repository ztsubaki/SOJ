"use client";
import Panel from "@/components/ui/Panel";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import Select from "@/components/form/Select";
import type { UserDetail } from "@/lib/admin-types";
import { useAuth } from "@/context/AuthContext";
import { ToastMessage } from "@/context/ToastContext";
import { deleteUser, getUser, resetUserPassword, updateUser } from "@/lib/users-api";

const statusOptions = [
  { value: "active", label: "正常" },
  { value: "disabled", label: "已禁用" },
];

export default function EditUserPage() {
  const params = useParams();
  const router = useRouter();
  const { user: currentUser } = useAuth();
  const id = typeof params.id === "string" ? params.id : "";
  const userID = Number(id);

  const [user, setUser] = useState<UserDetail | null>(null);
  const [form, setForm] = useState<UserDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [resettingPassword, setResettingPassword] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [generatedPassword, setGeneratedPassword] = useState("");
  const [resetError, setResetError] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    getUser(userID)
      .then((loadedUser) => {
        if (!active) return;
        setUser(loadedUser);
        setForm(loadedUser);
      })
      .catch((reason) => {
        if (active)
          setError(reason instanceof Error ? reason.message : "读取用户失败");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [userID]);

  if (loading) {
    return (
      <Panel className="p-8 text-center text-gray-500 dark:text-gray-400">
        正在加载用户...
      </Panel>
    );
  }

  if (!user || !form) {
    return (
      <Panel className="p-8 text-center">
        <ToastMessage message={error} />
        <h2 className="text-xl font-bold text-gray-900 dark:text-white">
          用户未找到
        </h2>
        <p className="mt-2 text-gray-500 dark:text-gray-400">
          无法找到 ID 为 <span className="font-mono">{id}</span> 的用户。
        </p>
        <Button className="mt-5" onClick={() => router.push("/users")}>
          返回用户列表
        </Button>
      </Panel>
    );
  }

  const updateField = <K extends keyof UserDetail>(
    field: K,
    value: UserDetail[K],
  ) => {
    setForm((prev) => (prev ? { ...prev, [field]: value } : prev));
  };

  const administratorTarget = user.role === "admin";

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const updated = await updateUser(user.id, {
        email: form.email,
        status: administratorTarget ? undefined : form.status,
      });
      setUser(updated);
      setForm(updated);
      router.push("/users");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "保存失败");
    } finally {
      setSaving(false);
    }
  };

  const handleResetPassword = async () => {
    if (!window.confirm(`重置 ${user.username} 的密码会撤销其全部会话。是否继续？`))
      return;
    setResettingPassword(true);
    setGeneratedPassword("");
    setResetError("");
    try {
      const result = await resetUserPassword(user.id);
      setGeneratedPassword(result.password);
    } catch (reason) {
      setResetError(reason instanceof Error ? reason.message : "重置密码失败");
    } finally {
      setResettingPassword(false);
    }
  };
  const handleDelete = async () => {
    if (!window.confirm(`删除 ${user.username} 会永久删除其会话、报名、提交、判题记录和无引用文件，无法恢复。是否继续？`)) return;
    setDeleting(true);
    setError("");
    try {
      await deleteUser(user.id);
      router.push("/users");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除用户失败");
    } finally {
      setDeleting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            编辑用户
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            {user.id}
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
          {currentUser?.id !== user.id && !administratorTarget && (
            <Button type="button" variant="danger" disabled={deleting} onClick={() => void handleDelete()}>
              {deleting ? "删除中..." : "删除用户"}
            </Button>
          )}
          <Button type="submit" disabled={saving}>
            {saving ? "保存中..." : "保存"}
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
              <Input id="username" value={form.username} disabled />
            </div>
            <div>
              <Label htmlFor="email">邮箱</Label>
              <Input
                id="email"
                type="email"
                value={form.email}
                onChange={(e) => updateField("email", e.target.value)}
                placeholder="输入邮箱"
                maxLength={254}
                pattern="[\\x21-\\x7E]+"
                title="不支持特殊字符"
              />
            </div>
            <div className="grid gap-5 sm:grid-cols-2">
              <div>
                <Label htmlFor="status">状态</Label>
                <Select
                  options={
                    !administratorTarget && currentUser?.id === user.id
                      ? statusOptions.filter(
                          (option) => option.value === "active",
                        )
                      : statusOptions
                  }
                  value={form.status}
                  disabled={administratorTarget}
                  onChange={(value) =>
                    updateField("status", value as UserDetail["status"])
                  }
                  placeholder="选择状态"
                />
                {administratorTarget && (
                  <p className="mt-2 text-theme-sm text-gray-500 dark:text-gray-400">
                    管理员账号状态仅可由运维修改。
                  </p>
                )}
              </div>
            </div>
          </div>
        </Panel>
      </div>

      <div className="col-span-12 lg:col-span-4 space-y-6">
        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            设置
          </h3>
          <div className="mt-5 space-y-5">
            <ToastMessage message={error} />
            <div className="rounded-xl bg-gray-50 p-4 dark:bg-white/[0.03]">
              <p className="text-sm text-gray-700 dark:text-gray-300">
                可用信息
              </p>
              <div className="mt-3 space-y-2 text-theme-sm text-gray-500 dark:text-gray-400">
                <p>每用户提交统计当前不可用。</p>
                <p>
                  创建时间：
                  {form.createdAt
                    ? new Date(form.createdAt).toLocaleString("zh-CN")
                    : "不可用"}
                </p>
              </div>
            </div>
            <div className="rounded-xl border border-gray-200 p-4 dark:border-gray-800">
              <p className="text-sm font-medium text-gray-700 dark:text-gray-300">
                重置密码
              </p>
              <p className="mt-2 text-theme-sm text-gray-500 dark:text-gray-400">
                系统会生成新密码并撤销该用户的全部会话。密码只在本页显示一次，请立即安全保存。
              </p>
              {administratorTarget ? (
                <p className="mt-3 text-theme-sm text-gray-500 dark:text-gray-400">
                  管理员密码仅可由其本人修改或由运维重置。
                </p>
              ) : (
                <Button
                  type="button"
                  className="mt-4"
                  variant="outline"
                  disabled={resettingPassword}
                  onClick={() => void handleResetPassword()}
                >
                  {resettingPassword ? "重置中..." : "重置密码"}
                </Button>
              )}
              <ToastMessage message={resetError} />
              {generatedPassword && (
                <div className="mt-3 rounded-lg bg-gray-50 p-3 dark:bg-white/[0.03]" role="alert">
                  <p className="text-theme-sm text-gray-700 dark:text-gray-300">
                    请立即记录；关闭或刷新页面后无法再次查看。
                  </p>
                  <code className="mt-2 block break-all rounded bg-white px-2 py-1 text-sm text-gray-900 dark:bg-gray-900 dark:text-white">
                    {generatedPassword}
                  </code>
                </div>
              )}
            </div>
          </div>
        </Panel>
      </div>
    </form>
  );
}
