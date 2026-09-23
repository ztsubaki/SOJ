import { request } from "@soj/bff/client";

type Page<T> = { items: T[]; page: number; pageSize: number; total: number };

// Administrative selectors and reports must not silently stop at the first 100
// records. Requests remain uncached and each page keeps its server-side access
// check.
export async function allPages<T>(path: string) {
  const items: T[] = [];
  for (let page = 1; ; page++) {
    const separator = path.includes("?") ? "&" : "?";
    const result = await request<Page<T>>(`${path}${separator}page=${page}&pageSize=100`);
    items.push(...result.items);
    if (items.length >= result.total || result.items.length === 0) return items;
  }
}
