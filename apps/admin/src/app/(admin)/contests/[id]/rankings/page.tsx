"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
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
import type { ContestDetail, RankItem } from "@/lib/admin-types";
import { statusColor } from "@/lib/presentation";
import { allPages } from "@/lib/pagination";
import { ToastMessage } from "@/context/ToastContext";

export default function ContestRankingsPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [contest, setContest] = useState<ContestDetail | null>(null);
  const [items, setItems] = useState<RankItem[]>([]);
  const [error, setError] = useState("");
  const [changingUser, setChangingUser] = useState<number | null>(null);

  useEffect(() => {
    void Promise.all([
      request<ContestDetail>(`/admin/contests/${encodeURIComponent(id)}`),
      allPages<RankItem>(`/admin/contests/${encodeURIComponent(id)}/rankings`),
    ])
      .then(([contest, rankings]) => {
        setContest(contest);
        setItems(rankings);
      })
      .catch((reason) =>
        setError(reason instanceof Error ? reason.message : "无法读取比赛排名"),
      );
  }, [id]);

  async function changeParticipant(item: RankItem) {
    setChangingUser(item.user.id);
    setError("");
    try {
      const method = item.disqualified ? "DELETE" : "POST";
      const response = await fetch(
        `/api/v1/admin/contests/${encodeURIComponent(id)}/rankings/${encodeURIComponent(item.user.id)}/disqualify`,
        { method, credentials: "include" },
      );
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: string;
        } | null;
        throw new Error(body?.error ?? "更新参赛者状态失败");
      }
      setItems((current) =>
        current.map((entry) =>
          entry.user.id === item.user.id
            ? { ...entry, disqualified: !entry.disqualified }
            : entry,
        ),
      );
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "更新参赛者状态失败");
    } finally {
      setChangingUser(null);
    }
  }

  if (!contest)
    return (
      <Panel className="p-8 text-center">
        <ToastMessage message={error} />
        {error ? "无法加载排名" : "正在加载排名..."}
      </Panel>
    );

  return (
    <div className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex justify-between">
        <div>
          <h2 className="text-2xl font-bold">{contest.title}</h2>
          <Badge size="sm" color={statusColor[contest.status] ?? "light"}>
            {contest.status}
          </Badge>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => router.push("/contests")}>
            返回比赛列表
          </Button>
          <Button onClick={() => router.push(`/contests/${id}`)}>
            编辑比赛
          </Button>
        </div>
      </div>
      <Panel className="col-span-12 p-5">
        <ToastMessage message={error} />
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableCell isHeader>排名</TableCell>
                <TableCell isHeader>用户</TableCell>
                <TableCell isHeader>得分</TableCell>
                <TableCell isHeader>解决题数</TableCell>
                <TableCell isHeader>提交</TableCell>
                <TableCell isHeader>最后提交</TableCell>
                <TableCell isHeader>状态</TableCell>
                <TableCell isHeader>操作</TableCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={`${item.rank}-${item.user.id}`}>
                  <TableCell>{item.rank}</TableCell>
                  <TableCell>{item.user.username}</TableCell>
                  <TableCell>{item.score}</TableCell>
                  <TableCell>{item.solved}</TableCell>
                  <TableCell>
                    <Link
                      className="text-brand-500"
                      href={`/submissions?user=${encodeURIComponent(item.user.id)}`}
                    >
                      {item.submissions}
                    </Link>
                  </TableCell>
                  <TableCell>
                    {item.lastSubmission
                      ? new Date(item.lastSubmission).toLocaleString("zh-CN")
                      : "-"}
                  </TableCell>
                  <TableCell>
                    <Badge
                      size="sm"
                      color={item.disqualified ? "error" : "success"}
                    >
                      {item.disqualified ? "已取消资格" : "正常"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={changingUser === item.user.id}
                      onClick={() => void changeParticipant(item)}
                    >
                      {changingUser === item.user.id
                        ? "处理中..."
                        : item.disqualified
                          ? "恢复资格"
                          : "取消资格"}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Panel>
    </div>
  );
}
