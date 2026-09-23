"use client";
import { clearSession } from "./session";

export class ApiError extends Error {
  constructor(public readonly code: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

// Paths are relative to the same-origin BFF. Raw uploads retain their headers.
export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (typeof init.body === "string" && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    credentials: "include",
    cache: "no-store",
    headers,
  });
  if (!response.ok) {
    if (response.status === 401) clearSession();
    const body = (await response.json().catch(() => null)) as {
      code?: number;
      error?: string;
    } | null;
    throw new ApiError(body?.code ?? response.status * 10, body?.error ?? "请求失败，请稍后重试");
  }
  return response.status === 204 ? (undefined as T) : response.json() as Promise<T>;
}
