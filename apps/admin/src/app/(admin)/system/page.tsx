"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";
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
import type { AgentSession } from "@/lib/admin-types";
import { usePaginatedQuery } from "@/lib/pagination";
import Pagination from "@/components/ui/Pagination";
import { ToastMessage } from "@/context/ToastContext";
type Status = {
  activeAgents: number;
  pendingJobs: number;
  runningJobs: number;
  updatedAt: string;
};
export default function SystemPage() {
  const [status, setStatus] = useState<Status | null>(null);
  const { items: agents, page, pageSize, total, loading, error: listError, setPage, refresh } = usePaginatedQuery<AgentSession>("/admin/system/agents");
  const [revision, setRevision] = useState(0);
  const [error, setError] = useState("");
  const [draining, setDraining] = useState<string | null>(null);
  const reload = () => {
    setRevision((value) => value + 1);
    refresh();
  };
  useEffect(() => {
    const controller = new AbortController();
    void request<Status>("/admin/system/status", { signal: controller.signal })
      .then((next) => { if (!controller.signal.aborted) setStatus(next); })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "无法读取系统状态");
      });
    return () => controller.abort();
  }, [revision]);
  const metrics = status
    ? [
        ["有效 Agent", status.activeAgents],
        ["待领取 Job", status.pendingJobs],
        ["活动 Job", status.runningJobs],
        ["更新时间", new Date(status.updatedAt).toLocaleString("zh-CN")],
      ]
    : [];
  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <header className="col-span-12">
        <h2 className="text-2xl font-bold">系统状态</h2>
        <p className="text-gray-500">
          仅展示 Control Plane 可从权威业务状态计算的数据。
        </p>
      </header>
      {metrics.map(([label, value]) => (
        <div
          key={String(label)}
          className="col-span-12 sm:col-span-6 xl:col-span-3"
        >
          <Panel className="p-5">
            <p className="text-gray-500">{label}</p>
            <p className="mt-3 text-2xl font-bold">{value}</p>
          </Panel>
        </div>
      ))}
      <ToastMessage message={error || listError} />
      <Panel as="section" className="col-span-12 p-5">
        <h3 className="text-lg font-semibold">Agent 会话</h3>
        <div className="mt-5 overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableCell isHeader>会话</TableCell>
                <TableCell isHeader>分区</TableCell>
                <TableCell isHeader>执行类型</TableCell>
                <TableCell isHeader>状态</TableCell>
                <TableCell isHeader>工作负载</TableCell>
                <TableCell isHeader>活动 Job</TableCell>
                <TableCell isHeader>最近心跳</TableCell>
                <TableCell isHeader>操作</TableCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {agents.map((x) => (
                <TableRow key={x.id}>
                  <TableCell className="font-mono">{x.id}</TableCell>
                  <TableCell>{x.partition}</TableCell>
                  <TableCell>{x.executor === "docker" ? "Docker" : "Slurm"}</TableCell>
                  <TableCell>
                    <Badge
                      size="sm"
                      color={x.state === "offline" ? "light" : "success"}
                    >
                      {x.state}
                    </Badge>
                  </TableCell>
                  <TableCell>{x.workload}</TableCell>
                  <TableCell>{x.activeJobCount}</TableCell>
                  <TableCell>
                    {new Date(x.lastHeartbeatAt).toLocaleString("zh-CN")}
                  </TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={x.state !== "online" || draining === x.id}
                      onClick={() => {
                        setDraining(x.id);
                        void request(
                          `/admin/system/agents/${encodeURIComponent(x.id)}/drain`,
                          { method: "POST" },
                        )
                          .then(() => reload())
                          .catch((e) =>
                            setError(
                              e instanceof Error
                                ? e.message
                                : "无法 drain Agent",
                            ),
                          )
                          .finally(() => setDraining(null));
                      }}
                    >
                      {draining === x.id ? "处理中..." : "Drain"}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} />
      </Panel>
    </div>
  );
}
