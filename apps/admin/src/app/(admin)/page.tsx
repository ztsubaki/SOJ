"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";
import { useEffect, useState } from "react";
import Link from "next/link";
import Badge from "@/components/ui/badge/Badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SubmissionDetail } from "@/lib/admin-types";
import { verdictColor } from "@/lib/presentation";
import { ToastMessage } from "@/context/ToastContext";
type Stats = {
  totalUsers: number;
  todaySubmissions: number;
  pendingSubmissions: number;
};
export default function DashboardPage() {
  const [stats, setStats] = useState<Stats | null>(null);
  const [submissions, setSubmissions] = useState<SubmissionDetail[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    void Promise.all([
      request<Stats>("/admin/stats"),
      request<{ items: SubmissionDetail[] }>("/admin/submissions?page=1&pageSize=5"),
    ])
      .then(([stats, submissions]) => {
        setStats(stats);
        setSubmissions(submissions.items);
      })
      .catch((e) =>
        setError(e instanceof Error ? e.message : "无法读取总览数据"),
      );
  }, []);
  const cards = stats
    ? [
        { label: "总用户数", value: stats.totalUsers },
        { label: "今日提交", value: stats.todaySubmissions },
        { label: "等待评测", value: stats.pendingSubmissions },
      ]
    : [];
  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
          总览
        </h2>
        <p className="text-gray-500">欢迎使用 SOJ 管理后台。</p>
      </div>
      {cards.map((x) => (
        <div key={x.label} className="col-span-12 sm:col-span-6 xl:col-span-4">
          <Panel className="p-5">
            <p className="text-gray-500">{x.label}</p>
            <p className="mt-3 text-3xl font-bold text-gray-900 dark:text-white">
              {x.value}
            </p>
          </Panel>
        </div>
      ))}
      <ToastMessage message={error} />
      <Panel className="col-span-12 p-5">
        <div className="flex items-center justify-between gap-4">
          <h3 className="text-lg font-semibold">最近提交</h3>
          <Link className="text-brand-500" href="/submissions">
            查看全部
          </Link>
        </div>
        <div className="mt-5 overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableCell isHeader>提交 ID</TableCell>
                <TableCell isHeader>题目</TableCell>
                <TableCell isHeader>结果</TableCell>
                <TableCell isHeader>得分</TableCell>
                <TableCell isHeader>时间</TableCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {submissions.map((x) => (
                <TableRow key={x.id}>
                  <TableCell>{x.id}</TableCell>
                  <TableCell>{x.problem.title}</TableCell>
                  <TableCell>
                    <Badge size="sm" color={verdictColor[x.status] ?? "light"}>
                      {x.status}
                    </Badge>
                  </TableCell>
                  <TableCell>{x.score ?? "-"}</TableCell>
                  <TableCell>
                    {new Date(x.createdAt).toLocaleString("zh-CN")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {submissions.length === 0 && (
          <p className="py-10 text-center text-theme-sm text-gray-500 dark:text-gray-400">
            暂无提交记录
          </p>
        )}
      </Panel>
    </div>
  );
}
