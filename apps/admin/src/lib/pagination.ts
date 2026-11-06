"use client";

import { useCallback, useEffect, useState } from "react";
import { request } from "@soj/bff/client";

export const PAGE_SIZE = 20;
export type PageRequest = { page: number; pageSize: number };
export type PageResult<T> = PageRequest & { items: T[]; total: number };
type PageLoader<T> = (page: PageRequest, signal: AbortSignal) => Promise<PageResult<T>>;

export function requestPage<T>(path: string, pagination: PageRequest, signal?: AbortSignal) {
  const [pathname, search = ""] = path.split("?");
  const query = new URLSearchParams(search);
  query.set("page", String(pagination.page));
  query.set("pageSize", String(pagination.pageSize));
  return request<PageResult<T>>(`${pathname}?${query}`, { signal });
}

// The query identity resets pagination immediately, before an effect can fetch an
// old page with new filters. Aborting and guarding completion also handles clients
// that finish parsing a response after navigation.
export function usePaginatedQuery<T>(path: string, load?: PageLoader<T>, debounceMs = 0) {
  const [position, setPosition] = useState({ path, page: 1 });
  const page = position.path === path ? position.page : 1;
  if (position.path !== path) setPosition({ path, page: 1 });
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{
    position: { path: string; page: number };
    revision: number;
    data?: PageResult<T>;
    error: string;
  } | null>(null);
  const current = result?.position === position && position.path === path && result.revision === revision
    ? result : null;

  useEffect(() => {
    const controller = new AbortController();
    const pagination = { page, pageSize: PAGE_SIZE };
    const start = () => {
      const pending = load
        ? load(pagination, controller.signal)
        : requestPage<T>(position.path, pagination, controller.signal);
      void pending.then((data) => {
        if (!controller.signal.aborted) setResult({ position, revision, data, error: "" });
      }).catch((reason: unknown) => {
        if (!controller.signal.aborted) setResult({
          position, revision,
          error: reason instanceof Error ? reason.message : "无法读取列表",
        });
      });
    };
    const timer = debounceMs > 0 ? setTimeout(start, debounceMs) : undefined;
    if (timer === undefined) start();
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [position, page, revision, load, debounceMs]);

  const setPage = useCallback((next: number) => {
    setPosition({ path, page: next });
  }, [path]);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  return {
    items: current?.data?.items ?? [],
    total: current?.data?.total ?? 0,
    page,
    pageSize: PAGE_SIZE,
    loading: current === null,
    error: current?.error ?? "",
    setPage,
    refresh,
  };
}
