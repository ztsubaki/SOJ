"use client";
import {
  observeSession,
  clearSession,
  refreshSessionIfNeeded,
} from "./session";
export type AuthUser = {
	id: number;
  username: string;
  email: string;
  role: "user" | "admin";
  status: "active" | "disabled";
};

import { ApiError, request } from "./client";

export function parseAuthUser(value: unknown): AuthUser {
  if (!value || typeof value !== "object")
    throw new ApiError(5000, "认证响应格式无效");
  const user = (value as { user: Partial<AuthUser> }).user;
  if (!user) throw new Error("认证响应格式无效");
  if (
    typeof user.id !== "number" ||
    !Number.isSafeInteger(user.id) ||
    user.id <= 0 ||
    typeof user.username !== "string" ||
    typeof user.email !== "string" ||
    (user.role !== "user" && user.role !== "admin") ||
    (user.status !== "active" && user.status !== "disabled")
  )
    throw new ApiError(5000, "认证响应格式无效");
  observeSession(value);
  return user as AuthUser;
}

export async function getCurrentUser() {
  await refreshSessionIfNeeded();
  return parseAuthUser(await request<unknown>("/auth/me"));
}

export async function login(username: string, password: string) {
  return parseAuthUser(
    await request<unknown>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  );
}

export async function register(
  username: string,
  email: string,
  password: string,
  emailVerificationCode: string,
) {
  return parseAuthUser(
    await request<unknown>("/auth/register", {
      method: "POST",
      body: JSON.stringify({ username, email, password, emailVerificationCode }),
    }),
  );
}

export type RegistrationEmailVerification = {
  usernameAvailable: boolean;
  emailAvailable: boolean;
  resendAfterSeconds: number;
};

export async function requestRegistrationEmailVerification(username: string, email: string): Promise<RegistrationEmailVerification> {
  const response = await request<unknown>("/auth/email-verification", {
    method: "POST",
    body: JSON.stringify({ username, email }),
  });
  if (!response || typeof response !== "object" ||
    typeof (response as { usernameAvailable?: unknown }).usernameAvailable !== "boolean" ||
    typeof (response as { emailAvailable?: unknown }).emailAvailable !== "boolean" ||
    !Number.isSafeInteger((response as { resendAfterSeconds?: unknown }).resendAfterSeconds))
    throw new ApiError(5000, "验证码响应格式无效");
  return response as RegistrationEmailVerification;
}

export async function logout() {
  await request<void>("/auth/logout", { method: "POST" });
  clearSession();
}

export async function changePassword(
  currentPassword: string,
  newPassword: string,
) {
  await request<void>("/auth/change-password", {
    method: "POST",
    body: JSON.stringify({ currentPassword, newPassword }),
  });
  clearSession();
}
