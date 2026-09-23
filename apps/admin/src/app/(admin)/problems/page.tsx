"use client";
import Panel from "@/components/ui/Panel";
import Link from "next/link";
import { useEffect, useState } from "react";
import Badge from "@/components/ui/badge/Badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ProblemDetail } from "@/lib/admin-types";
import { difficultyColor } from "@/lib/presentation";
import { allPages } from "@/lib/pagination";
import { ToastMessage } from "@/context/ToastContext";

export default function ProblemsPage() {
  const [problems, setProblems] = useState<ProblemDetail[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    void allPages<ProblemDetail>("/admin/problems")
      .then(setProblems)
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : "无法读取题目列表"),
      );
  }, []);

  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            题目管理
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            管理平台题库、启用状态与运行分区。
          </p>
        </div>
        <Link
          href="/problems/new"
          className="inline-flex items-center justify-center rounded-lg bg-brand-500 px-4 py-3 text-sm font-medium text-white shadow-theme-xs transition hover:bg-brand-600"
        >
          新建题目
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
                    标题
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    难度
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    标签
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    分区
                  </TableCell>
                  <TableCell
                    isHeader
                    className="pb-3.5 pl-4 pr-4 text-left text-theme-xs font-medium text-gray-500 dark:text-gray-400"
                  >
                    状态
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
                {problems.map((problem) => (
                  <TableRow
                    key={problem.id}
                    className="border-b border-gray-100 dark:border-gray-800"
                  >
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm text-gray-900 dark:text-white/90">
                      {problem.id}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-theme-sm font-medium text-gray-900 dark:text-white/90">
                      {problem.title}
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <Badge
                        size="sm"
                        color={difficultyColor[problem.difficulty] ?? "light"}
                      >
                        {problem.difficulty}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <div className="flex flex-wrap gap-1">
                        {problem.tags.map((tag) => (
                          <Badge key={tag} size="sm" color="light">
                            {tag}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <div className="flex flex-wrap gap-1">
                        {problem.partitions.map((partition) => (
                          <Badge key={partition} size="sm" color="light">
                            {partition}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4">
                      <Badge
                        size="sm"
                        color={problem.enabled ? "success" : "light"}
                      >
                        {problem.enabled ? "已启用" : "未启用"}
                      </Badge>
                    </TableCell>
                    <TableCell className="py-3 pl-4 pr-4 text-right">
                      <Link
                        href={`/problems/${problem.id}`}
                        className="inline-flex items-center justify-center rounded-lg border border-gray-300 bg-white px-4 py-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-white/[0.03] dark:hover:text-gray-300"
                      >
                        编辑
                      </Link>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <ToastMessage message={error} />
          </div>
        </Panel>
      </div>
    </div>
  );
}
