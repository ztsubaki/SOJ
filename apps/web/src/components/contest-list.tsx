"use client";

import { FormEvent, useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Icon } from "@/components/icon";
import { ToastMessage } from "@/components/toast-provider";
import type { Contest } from "@/lib/server/bff";

const statusLabels = {
  running: "进行中",
  upcoming: "即将开始",
  ended: "已结束",
} as const;
const statusBadges = {
  running: "badge-success",
  upcoming: "badge-info",
  ended: "badge-neutral",
} as const;
const pageSize = 20;
type Status = keyof typeof statusLabels;
type ContestPage = {
  items: Contest[];
  page: number;
  pageSize: number;
  total: number;
};

function statusFrom(value: string | null): Status {
  return value === "upcoming" || value === "ended" ? value : "running";
}

function pageFrom(value: string | null) {
  const page = Number(value);
  return Number.isSafeInteger(page) && page > 0 ? page : 1;
}

export function ContestList() {
  const pathname = usePathname();
  const router = useRouter();
  const searchParams = useSearchParams();
  const status = statusFrom(searchParams.get("status"));
  const query = searchParams.get("query") ?? "";
  const page = pageFrom(searchParams.get("page"));
  const requestKey = `${status}\u0000${query}\u0000${page}`;
  const [result, setResult] = useState<{
    key: string;
    data: ContestPage;
  } | null>(null);
  const [error, setError] = useState<{ key: string; message: string } | null>(
    null,
  );

  useEffect(() => {
    const controller = new AbortController();
    const params = new URLSearchParams({
      status,
      page: String(page),
      pageSize: String(pageSize),
    });
    if (query) params.set("query", query);
    fetch(`/api/v1/contests?${params}`, {
      credentials: "include",
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) {
          const body = (await response.json().catch(() => null)) as {
            error?: string;
          } | null;
          throw new Error(body?.error ?? "无法读取比赛列表，请稍后重试。");
        }
        return response.json() as Promise<ContestPage>;
      })
      .then(
        (data) =>
          !controller.signal.aborted && setResult({ key: requestKey, data }),
      )
      .catch((reason: unknown) => {
        if (!controller.signal.aborted)
          setError({
            key: requestKey,
            message:
              reason instanceof Error
                ? reason.message
                : "无法读取比赛列表，请稍后重试。",
          });
      });
    return () => controller.abort();
  }, [page, query, requestKey, status]);

  function updateFilters(next: {
    status?: Status;
    query?: string;
    page?: number;
  }) {
    const params = new URLSearchParams();
    const nextStatus = next.status ?? status;
    const nextQuery = next.query ?? query;
    const nextPage = next.page ?? page;
    params.set("status", nextStatus);
    if (nextQuery) params.set("query", nextQuery);
    if (nextPage > 1) params.set("page", String(nextPage));
    router.replace(`${pathname}?${params}`);
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const query = new FormData(event.currentTarget).get("query");
    updateFilters({
      query: typeof query === "string" ? query.trim() : "",
      page: 1,
    });
  }

  const data = result?.key === requestKey ? result.data : null;
  const requestError = error?.key === requestKey ? error.message : null;
  const totalPages = data
    ? Math.max(1, Math.ceil(data.total / data.pageSize))
    : 1;
  const canGoBack = page > 1;
  const canGoForward = data !== null && page < totalPages;

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 md:px-6">
      <div>
        <div className="card rounded-box mb-4 border border-base-content/10 bg-base-100 shadow-md">
          <div className="card-body p-5 sm:p-6">
            <form onSubmit={submitSearch} className="flex gap-2">
              <label className="input input-bordered flex flex-1 items-center gap-2">
                <Icon name="search" className="size-5 text-base-content/60" />
                <input
                  key={query}
                  name="query"
                  defaultValue={query}
                  type="search"
                  placeholder="搜索比赛..."
                  aria-label="搜索比赛"
                />
              </label>
              <button type="submit" className="btn">
                搜索
              </button>
            </form>
          </div>
        </div>
        <div
          role="tablist"
          aria-label="比赛状态筛选"
          className="tabs tabs-boxed rounded-box mb-4 bg-base-100 p-2 shadow-md"
        >
          {(Object.keys(statusLabels) as Status[]).map((item) => (
            <button
              key={item}
              type="button"
              role="tab"
              aria-selected={status === item}
              className={`tab gap-1 ${status === item ? "tab-active" : ""}`}
              onClick={() => updateFilters({ status: item, page: 1 })}
            >
              {statusLabels[item]}
            </button>
          ))}
        </div>
        <ToastMessage message={requestError} />
        {!data && !requestError ? (
          <div className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body items-center py-12">
              <span
                className="loading loading-spinner loading-md"
                aria-label="正在加载比赛"
              />
            </div>
          </div>
        ) : data?.items.length === 0 ? (
          <div className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body items-center py-12 text-center text-base-content/70">
              暂无比赛
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            {data?.items.map((contest) => (
              <Link
                href={`/contests/${contest.id}`}
                key={contest.id}
                className="card rounded-box cursor-pointer border border-base-content/10 bg-base-100 shadow-md transition-shadow hover:shadow-lg focus-visible:outline-2 focus-visible:outline-primary"
              >
                <div className="card-body p-5 sm:p-6">
                  <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
                    <div className="flex-1">
                      <div className="mb-2 flex flex-wrap items-center gap-2">
                        <h2 className="card-title text-lg">
                          {contest.title}
                        </h2>
                        <span
                          className={`badge badge-md px-3 ${statusBadges[contest.status]}`}
                        >
                          {statusLabels[contest.status]}
                        </span>
                      </div>
                      <div className="mt-3 flex flex-wrap gap-4 text-sm leading-6 text-base-content/70">
                        <span>
                          {new Date(contest.startsAt).toLocaleString("zh-CN", {
                            month: "numeric",
                            day: "numeric",
                            hour: "2-digit",
                            minute: "2-digit",
                          })}
                        </span>
                        <span>{contest.participantCount} 参赛者</span>
                      </div>
                    </div>
                    <span
                      className={`btn btn-sm ${contest.status === "running" ? "btn-primary" : "btn-ghost"}`}
                    >
                      {contest.status === "upcoming"
                        ? "报名"
                        : contest.status === "running"
                          ? "进入"
                          : "查看"}
                    </span>
                  </div>
                </div>
              </Link>
            ))}
          </div>
        )}
        {data && data.total > 0 && (
          <nav
            className="mt-6 flex items-center justify-between gap-3"
            aria-label="比赛分页"
          >
            <span className="text-sm text-base-content/70">
              第 {page} / {totalPages} 页，共 {data.total} 场比赛
            </span>
            <div className="join">
              <button
                type="button"
                className="btn join-item"
                disabled={!canGoBack}
                onClick={() => updateFilters({ page: page - 1 })}
              >
                上一页
              </button>
              <button
                type="button"
                className="btn join-item"
                disabled={!canGoForward}
                onClick={() => updateFilters({ page: page + 1 })}
              >
                下一页
              </button>
            </div>
          </nav>
        )}
      </div>
    </div>
  );
}
