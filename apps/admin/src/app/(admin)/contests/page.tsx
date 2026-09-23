"use client";
import Panel from "@/components/ui/Panel";
import Link from "next/link";
import { useEffect, useState } from "react";
import Badge from "@/components/ui/badge/Badge";
import type { ContestDetail } from "@/lib/admin-types";
import { statusColor } from "@/lib/presentation";
import { allPages } from "@/lib/pagination";
import { ToastMessage } from "@/context/ToastContext";

const statusText: Record<string, string> = {
  running: "进行中",
  upcoming: "即将开始",
  ended: "已结束",
};

export default function ContestsPage() {
  const [contests, setContests] = useState<ContestDetail[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    void allPages<ContestDetail>("/admin/contests")
      .then(setContests)
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : "无法读取比赛列表"),
      );
  }, []);

  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            比赛管理
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            管理赛事安排与参与情况。
          </p>
        </div>
        <Link
          href="/contests/new"
          className="inline-flex items-center justify-center rounded-lg bg-brand-500 px-4 py-3 text-sm font-medium text-white shadow-theme-xs transition hover:bg-brand-600"
        >
          新建比赛
        </Link>
      </div>
      <div className="col-span-12 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {contests.map((contest) => (
          <div
            key={contest.id}
            className="col-span-12 sm:col-span-6 xl:col-span-3"
          >
            <Panel className="p-5">
              <div className="flex items-start justify-between">
                <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
                  {contest.title}
                </h3>
                <Badge size="sm" color={statusColor[contest.status] ?? "light"}>
                  {statusText[contest.status] ?? contest.status}
                </Badge>
              </div>
              <div className="mt-4 space-y-2 text-theme-sm text-gray-500 dark:text-gray-400">
                <p>参赛人数：{contest.participantCount}</p>
                <p>
                  开始：{new Date(contest.startsAt).toLocaleString("zh-CN")}
                </p>
                <p>结束：{new Date(contest.endsAt).toLocaleString("zh-CN")}</p>
              </div>
              <div className="mt-5 flex items-center gap-2">
                <Link
                  href={`/contests/${contest.id}`}
                  className="flex flex-1 items-center justify-center rounded-lg border border-gray-300 bg-white px-4 py-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-white/[0.03] dark:hover:text-gray-300"
                >
                  编辑
                </Link>
                <Link
                  href={`/contests/${contest.id}/rankings`}
                  className="flex flex-1 items-center justify-center rounded-lg bg-brand-500 px-4 py-3 text-sm font-medium text-white shadow-theme-xs transition hover:bg-brand-600"
                >
                  排名
                </Link>
              </div>
            </Panel>
          </div>
        ))}
      </div>
      <ToastMessage message={error} />
    </div>
  );
}
