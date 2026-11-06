"use client";
import Panel from "@/components/ui/Panel";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useState } from "react";
import Badge from "@/components/ui/badge/Badge";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Select from "@/components/form/Select";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SubmissionDetail, SubmissionStatus } from "@/lib/admin-types";
import { verdictColor } from "@/lib/presentation";
import { rejudgeSubmission } from "@/lib/submissions-api";
import { usePaginatedQuery } from "@/lib/pagination";
import Pagination from "@/components/ui/Pagination";
import { useToast, ToastMessage } from "@/context/ToastContext";

const options = [
  { value: "", label: "全部结果" },
  { value: "accepted", label: "accepted" },
  { value: "wrong_answer", label: "wrong_answer" },
  { value: "compile_error", label: "compile_error" },
  { value: "runtime_error", label: "runtime_error" },
  { value: "time_limit", label: "time_limit" },
  { value: "memory_limit", label: "memory_limit" },
  { value: "pending", label: "pending" },
  { value: "judging", label: "judging" },
] as const;

function canRejudge(status: SubmissionStatus) {
  return status !== "pending" && status !== "judging";
}

export default function SubmissionsPage() {
  const searchParams = useSearchParams();
  const user = searchParams.get("user") ?? "";
  const [search, setSearch] = useState("");
  const [verdict, setVerdict] = useState("");
  const [error, setError] = useState("");
  const [rejudging, setRejudging] = useState<number | null>(null);
  const { show } = useToast();

  const query = new URLSearchParams();
  if (search) query.set("query", search);
  if (verdict) query.set("verdict", verdict);
  if (user) query.set("user", user);
  const { items, page, pageSize, total, loading, error: listError, setPage } = usePaginatedQuery<SubmissionDetail>(`/admin/submissions?${query}`, undefined, 250);

  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
          提交记录
        </h2>
        <p className="text-gray-500 text-theme-sm dark:text-gray-400">
          查看并重新评测用户提交。
        </p>
      </div>
      {user && (
        <div className="col-span-12 flex items-center gap-3 text-theme-sm text-gray-500 dark:text-gray-400">
          仅显示用户{" "}
          <code className="font-mono text-gray-900 dark:text-white">
            {user}
          </code>{" "}
          的提交。
          <Link className="text-brand-500" href="/submissions">
            清除筛选
          </Link>
        </div>
      )}
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row">
        <div className="w-full sm:w-72">
          <Input
            placeholder="搜索题目或提交 ID"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <div className="w-full sm:w-48">
          <Select
            options={[...options]}
            value={verdict}
            onChange={setVerdict}
          />
        </div>
      </div>
      <Panel className="col-span-12 p-5">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableCell isHeader>提交 ID</TableCell>
                <TableCell isHeader>题目</TableCell>
                <TableCell isHeader>类型</TableCell>
                <TableCell isHeader>结果</TableCell>
                <TableCell isHeader>得分</TableCell>
                <TableCell isHeader>提交时间</TableCell>
                <TableCell isHeader>操作</TableCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((submission) => (
                <TableRow key={submission.id}>
                  <TableCell>{submission.id}</TableCell>
                  <TableCell>{submission.problem.title}</TableCell>
                  <TableCell>{submission.kind}</TableCell>
                  <TableCell>
                    <Badge
                      size="sm"
                      color={
                        verdictColor[submission.status as SubmissionStatus] ??
                        "light"
                      }
                    >
                      {submission.status}
                    </Badge>
                  </TableCell>
                  <TableCell>{submission.score ?? "-"}</TableCell>
                  <TableCell>
                    {new Date(submission.createdAt).toLocaleString("zh-CN")}
                  </TableCell>
                  <TableCell className="min-w-40">
                    <div className="flex flex-wrap items-center gap-3">
                      <Link
                        href={`/submissions/${encodeURIComponent(submission.id)}`}
                        className="inline-flex h-9 items-center whitespace-nowrap text-sm font-medium text-brand-500"
                      >
                        详情
                      </Link>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={
                          rejudging === submission.id ||
                          !canRejudge(submission.status as SubmissionStatus)
                        }
                        onClick={() => {
                          setRejudging(submission.id);
                          void rejudgeSubmission(submission.id)
                            .then(() =>
                              show(`已为 ${submission.id} 创建重测任务`, "success"),
                            )
                            .catch((reason) =>
                              setError(
                                reason instanceof Error
                                  ? reason.message
                                  : "重测失败",
                              ),
                            )
                            .finally(() => setRejudging(null));
                        }}
                      >
                        {rejudging === submission.id
                          ? "处理中..."
                          : canRejudge(submission.status as SubmissionStatus)
                            ? "重测"
                            : "评测中"}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {!items.length && !loading && !error && !listError && (
          <p className="py-10 text-center text-gray-500">未找到提交记录</p>
        )}
        <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} />
        <ToastMessage message={error || listError} />
      </Panel>
    </div>
  );
}
