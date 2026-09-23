import { request } from "@soj/bff/client";
import type { UserDetail } from "@/lib/admin-types";
import { allPages } from "@/lib/pagination";

type UserPatch = { email?: string; status?: "active" | "disabled" };
type UserPage = { items: UserDetail[]; page: number; pageSize: number; total: number };
type CreateUserInput = { username: string; email: string };
type CreateUserResult = { user: UserDetail };

export function updateUser(id: number, patch: UserPatch): Promise<UserDetail> {
  return request(`/admin/users/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

export async function createUser(input: CreateUserInput): Promise<CreateUserResult> {
  return { user: await request<UserDetail>("/admin/users", {
    method: "POST",
    body: JSON.stringify(input),
  }) };
}

export function resetUserPassword(id: number): Promise<{ password: string }> {
  return request(`/admin/users/${encodeURIComponent(id)}/reset-password`, {
    method: "POST",
  });
}

export function deleteUser(id: number): Promise<void> {
	return request(`/admin/users/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function listUsers(): Promise<UserPage> {
  const items = await allPages<UserDetail>("/admin/users");
  return { items, page: 1, pageSize: items.length, total: items.length };
}

export function getUser(id: number): Promise<UserDetail> {
  return request(`/admin/users/${encodeURIComponent(id)}`);
}
