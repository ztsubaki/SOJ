import type { ChangePasswordRequest, Credentials } from "../generated/soj/v1/auth";
import "server-only";
import { NextRequest, NextResponse } from "next/server";
import type { RPC } from "./control-plane";
import { body, isString, json, error, query, session } from "./http";
import { authUser, timestamp } from "./mapping";

function secureSessionCookie() {
  const configured = process.env.SESSION_COOKIE_SECURE?.trim().toLowerCase();
  if (configured === undefined || configured === "")
    return process.env.NODE_ENV === "production";
  return configured !== "false" && configured !== "0";
}

function cookie(token: string, expiresAt: string) {
  const expires = new Date(expiresAt);
  return {
    name: "soj_session",
    value: token,
    httpOnly: true,
    sameSite: "lax" as const,
    path: "/",
    secure: secureSessionCookie(),
    expires,
    maxAge: Math.max(0, Math.floor((expires.getTime() - Date.now()) / 1000)),
  };
}
export async function authRoute(
  r: NextRequest,
  key: string,
  rpc: RPC,
  allowRegister: boolean,
) {
  const method = r.method === "HEAD" ? "GET" : r.method;
  if (key.startsWith("auth/")) query(r, []);
  if (key === "auth/refresh" && method === "POST") {
    const x = await rpc("AuthService", "refreshSession", {}, session(r));
    const expiresAt = timestamp(x?.expiresAt);
    const response = json({ expiresAt });
    response.cookies.set(cookie(x?.sessionToken ?? "", expiresAt));
    return response;
  }
  if (key === "auth/me" && method === "GET") {
    const x = await rpc("AuthService", "getCurrentUser", {}, session(r));
    return json({ user: authUser(x.user), expiresAt: timestamp(x?.expiresAt) });
  }
  if (allowRegister && key === "auth/email-verification" && method === "POST") {
    const input = await body<{ username: string; email: string }>(r, ["username", "email"], (value) =>
      isString(value.username) && isString(value.email));
    if (!input) return error(4000, "invalid JSON");
    const x = await rpc("AuthService", "sendRegistrationEmailVerification", input);
    return json({
      usernameAvailable: x.usernameAvailable ?? false,
      emailAvailable: x.emailAvailable ?? false,
      resendAfterSeconds: x.resendAfterSeconds ?? 0,
    });
  }
  if (
    (key === "auth/login" || (allowRegister && key === "auth/register")) &&
    method === "POST"
  ) {
    const input = await body<Credentials & { emailVerificationCode: string }>(
      r,
      key.endsWith("login") ? ["username", "password"] : ["username", "email", "password", "emailVerificationCode"],
      (value) => isString(value.username) && isString(value.password) &&
        (key.endsWith("login") || (isString(value.email) && isString(value.emailVerificationCode))),
    );
    if (!input) return error(4000, "invalid JSON");
    const x = key.endsWith("login")
      ? await rpc("AuthService", "login", {
          username: input.username,
          password: input.password,
        })
      : await rpc("AuthService", "register", {
          credentials: { username: input.username, email: input.email, password: input.password },
          emailVerificationCode: input.emailVerificationCode,
        });
    const response = json(
      { user: authUser(x.user), expiresAt: timestamp(x?.expiresAt) },
      key.endsWith("register") ? 201 : 200,
    );
    response.cookies.set(
      cookie(x?.sessionToken ?? "", timestamp(x?.expiresAt)),
    );
    return response;
  }
  if (key === "auth/change-password" && method === "POST") {
    const input = await body<ChangePasswordRequest>(r, ["currentPassword", "newPassword"], (value) =>
      isString(value.currentPassword) && isString(value.newPassword),
    );
    if (!input) return error(4000, "invalid JSON");
    await rpc(
      "AuthService",
      "changePassword",
      {
        currentPassword: input.currentPassword,
        newPassword: input.newPassword,
      },
      session(r),
    );
    const response = new NextResponse(null, { status: 204 });
    response.cookies.delete("soj_session");
    return response;
  }
  if (key === "auth/logout" && method === "POST") {
    if (session(r)) await rpc("AuthService", "logout", {}, session(r));
    const response = new NextResponse(null, { status: 204 });
    response.cookies.delete("soj_session");
    return response;
  }
  return null;
}
