"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import Button from "@/components/ui/button/Button";
import Badge from "@/components/ui/badge/Badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SubmissionDetail, SubmissionJob } from "@/lib/admin-types";
import { verdictColor } from "@/lib/presentation";
import { cancelSubmission, rejudgeSubmission } from "@/lib/submissions-api";
import { allPages } from "@/lib/pagination";
import { ToastMessage } from "@/context/ToastContext";

function canRejudge(status: SubmissionDetail["status"]) {
  return status !== "pending" && status !== "judging";
}

export default function SubmissionDetailPage() {
  const { id: rawId } = useParams<{ id: string }>();
  const id = Number(decodeURIComponent(rawId));
  const router = useRouter();
  const [sub, setSub] = useState<SubmissionDetail | null>(null);
  const [jobs, setJobs] = useState<SubmissionJob[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const reload = useCallback(() => {
    void Promise.all([
      request<SubmissionDetail>(`/admin/submissions/${encodeURIComponent(id)}`),
      allPages<SubmissionJob>(`/admin/submissions/${encodeURIComponent(id)}/jobs`),
    ])
      .then(([detail, history]) => {
        setSub(detail);
        setJobs(history);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "无法读取提交"));
  }, [id]);
  useEffect(reload, [reload]);
  if (!sub)
    return (
      <Panel className="p-8 text-center">
        <ToastMessage message={error} />
        <h2 className="text-xl font-bold">{error ? "无法加载提交" : "正在加载提交..."}</h2>
        <Button className="mt-5" onClick={() => router.push("/submissions")}>
          返回提交记录
        </Button>
      </Panel>
    );
  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            提交详情
          </h2>
          <p className="text-gray-500">{sub.id}</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => router.push("/submissions")}>
            返回列表
          </Button>
          <Button
            disabled={busy || !canRejudge(sub.status)}
            onClick={() => {
              setBusy(true);
              void rejudgeSubmission(id)
                .then(() => reload())
                .catch((e) =>
                  setError(e instanceof Error ? e.message : "重测失败"),
                )
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "创建中..." : canRejudge(sub.status) ? "重新评测" : "评测中"}
          </Button>
          <Button
            variant="outline"
            disabled={busy || !["pending", "judging"].includes(sub.status)}
            onClick={() => {
              setBusy(true);
              void cancelSubmission(id)
                .then(() => reload())
                .catch((e) => setError(e instanceof Error ? e.message : "取消失败"))
                .finally(() => setBusy(false));
            }}
          >
            {busy ? "处理中..." : "取消提交"}
          </Button>
        </div>
      </div>
      <ToastMessage message={error} />
      <Panel className="col-span-12 lg:col-span-8 p-5">
        <h3 className="text-lg font-semibold">提交文件</h3>
        {sub.sourceMetadata ? (
          <>
            <dl className="mt-4 space-y-3">
              <div>文件名：{sub.sourceMetadata.filename}</div>
              <div>内容类型：{sub.sourceMetadata.contentType}</div>
              <div>大小：{sub.sourceMetadata.sizeBytes} bytes</div>
              <div className="break-all font-mono text-xs">
                SHA-256：{sub.sourceMetadata.sha256}
              </div>
            </dl>
            <div className="mt-5 flex gap-3">
              {sub.source && (
                <a className="text-brand-500" href={sub.source.href}>
                  下载源码
                </a>
              )}
              {sub.log && (
                <a className="text-brand-500" href={sub.log.href}>
                  下载评测日志
                </a>
              )}
            </div>
          </>
        ) : (
          <p className="mt-4 text-gray-500">源码元数据不可用。</p>
        )}
      </Panel>
      <Panel className="col-span-12 lg:col-span-4 p-5">
        <h3 className="text-lg font-semibold">基本信息</h3>
        <dl className="mt-4 space-y-3">
          <div>题目：{sub.problem.title}</div>
          <div>
            结果：
            <Badge size="sm" color={verdictColor[sub.status] ?? "light"}>
              {sub.status}
            </Badge>
          </div>
          <div>得分：{sub.score ?? "-"}</div>
          <div>比赛 ID：{sub.contestId ?? "-"}</div>
          <div>提交时间：{new Date(sub.createdAt).toLocaleString("zh-CN")}</div>
        </dl>
      </Panel>
      <Panel as="section" className="col-span-12 p-5">
        <h3 className="text-lg font-semibold">评测历史</h3>
        <div className="mt-4 overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableCell isHeader>Job</TableCell>
                <TableCell isHeader>状态</TableCell>
                <TableCell isHeader>结果</TableCell>
                <TableCell isHeader>诊断</TableCell>
                <TableCell isHeader>Slurm</TableCell>
                <TableCell isHeader>创建时间</TableCell>
                <TableCell isHeader>文件</TableCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-mono">{job.id}</TableCell>
                  <TableCell>{job.state}</TableCell>
                  <TableCell>
                    {job.verdict ?? "-"}
                    {job.score ? ` (${job.score})` : ""}
                  </TableCell>
                  <TableCell className="max-w-xl break-words">
                    {job.statusMessage ?? "-"}
                  </TableCell>
                  <TableCell>{job.slurmJobId ?? "-"}</TableCell>
                  <TableCell>
                    {job.createdAt
                      ? new Date(job.createdAt).toLocaleString("zh-CN")
                      : "-"}
                  </TableCell>
                  <TableCell className="space-x-3 whitespace-nowrap">
                    {job.log ? (
                      <a
                        className="text-brand-500"
                        href={job.log.href}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        日志
                      </a>
                    ) : (
                      <span>-</span>
                    )}
                    {job.result ? (
                      <a
                        className="text-brand-500"
                        href={job.result.href}
                        target="_blank"
                        rel="noopener noreferrer"
                      >
                        结果
                      </a>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        {jobs.length === 0 && <p className="mt-4 text-gray-500">暂无 Job 记录。</p>}
      </Panel>
    </div>
  );
}
