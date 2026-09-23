"use client";
import Panel from "@/components/ui/Panel";

import Link from "next/link";
import { useEffect, useState } from "react";
import Badge from "@/components/ui/badge/Badge";
import Button from "@/components/ui/button/Button";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { UserDetail } from "@/lib/admin-types";
import { roleColor, statusColor } from "@/lib/presentation";
import { listUsers } from "@/lib/users-api";
import { ToastMessage } from "@/context/ToastContext";

export default function UsersPage() {
  const [users, setUsers] = useState<UserDetail[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    listUsers()
      .then((page) => {
        if (active) setUsers(page.items);
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
  }, []);

  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            用户管理
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            管理平台用户账号与权限。
          </p>
        </div>
        <Link href="/users/new">
          <Button size="sm">创建用户</Button>
        </Link>
      </div>

      <div className="col-span-12">
        <Panel className="px-5 pb-5 pt-5 sm:px-6 sm:pt-6">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="border-b border-gray-100 dark:border-gray-800">
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    ID
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    用户名
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    邮箱
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    角色
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    状态
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    创建时间
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-right text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    操作
                  </TableCell>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.map((user) => (
                  <TableRow
                    key={user.id}
                    className="border-b border-gray-100 dark:border-gray-800"
                  >
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm text-gray-900 dark:text-white/90">
                      {user.id}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm font-medium text-gray-900 dark:text-white/90">
                      {user.username}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm text-gray-500 dark:text-gray-400">
                      {user.email}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <Badge size="sm" color={roleColor[user.role] ?? "light"}>
                        {user.role}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <Badge
                        size="sm"
                        color={statusColor[user.status] ?? "light"}
                      >
                        {user.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm text-gray-500 dark:text-gray-400">
                      {user.createdAt
                        ? new Date(user.createdAt).toLocaleDateString("zh-CN", {
                            timeZone: "UTC",
                          })
                        : "不可用"}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-right">
                      <Link
                        href={`/users/${user.id}`}
                        className="inline-flex items-center justify-center rounded-lg border border-gray-300 bg-white px-4 py-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-white/[0.03] dark:hover:text-gray-300"
                      >
                        编辑
                      </Link>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {loading && (
              <p className="py-8 text-center text-theme-sm text-gray-500 dark:text-gray-400">
                正在加载用户...
              </p>
            )}
            <ToastMessage message={error} />
          </div>
        </Panel>
      </div>
    </div>
  );
}
