import type { ChangePasswordRequest, Credentials } from "../generated/soj/v1/auth";
import "server-only";
import { NextRequest, NextResponse } from "next/server";
import type { RPC } from "./control-plane";
import { body, isString, json, error, session } from "./http";
import { authUser, timestamp } from "./mapping";
import type { Route } from "./router";

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
export function authRoutes(rpc: RPC, allowRegister: boolean): Route[] {
  async function authenticate(r: NextRequest, register: boolean) {
    const input = await body<Credentials & { emailVerificationCode: string }>(
      r,
      register ? ["username", "email", "password", "emailVerificationCode"] : ["username", "password"],
      (value) => isString(value.username) && isString(value.password) &&
        (!register || (isString(value.email) && isString(value.emailVerificationCode))),
    );
    if (!input) return error(4000, "invalid JSON");
    const x = register
      ? await rpc("AuthService", "register", {
          credentials: { username: input.username, email: input.email, password: input.password },
          emailVerificationCode: input.emailVerificationCode,
        })
      : await rpc("AuthService", "login", {
          username: input.username,
          password: input.password,
        });
    const response = json(
      { user: authUser(x.user), expiresAt: timestamp(x.expiresAt) },
      register ? 201 : 200,
    );
    response.cookies.set(cookie(x.sessionToken ?? "", timestamp(x.expiresAt)));
    return response;
  }

  const routes: Route[] = [
    {
      path: "auth/refresh",
      POST: async (r) => {
        const x = await rpc("AuthService", "refreshSession", {}, session(r));
        const expiresAt = timestamp(x.expiresAt);
        const response = json({ expiresAt });
        response.cookies.set(cookie(x.sessionToken ?? "", expiresAt));
        return response;
      },
    },
    {
      path: "auth/me",
      GET: async (r) => {
        const x = await rpc("AuthService", "getCurrentUser", {}, session(r));
        return json({ user: authUser(x.user), expiresAt: timestamp(x.expiresAt) });
      },
    },
    { path: "auth/login", POST: (r) => authenticate(r, false) },
    {
      path: "auth/change-password",
      POST: async (r) => {
        const input = await body<ChangePasswordRequest>(r, ["currentPassword", "newPassword"], (value) =>
          isString(value.currentPassword) && isString(value.newPassword));
        if (!input) return error(4000, "invalid JSON");
        await rpc("AuthService", "changePassword", input, session(r));
        const response = new NextResponse(null, { status: 204 });
        response.cookies.delete("soj_session");
        return response;
      },
    },
    {
      path: "auth/logout",
      POST: async (r) => {
        if (session(r)) await rpc("AuthService", "logout", {}, session(r));
        const response = new NextResponse(null, { status: 204 });
        response.cookies.delete("soj_session");
        return response;
      },
    },
  ];
  if (allowRegister) routes.push(
    { path: "auth/register", POST: (r) => authenticate(r, true) },
    {
      path: "auth/email-verification",
      POST: async (r) => {
        const input = await body<{ username: string; email: string }>(r, ["username", "email"], (value) =>
          isString(value.username) && isString(value.email));
        if (!input) return error(4000, "invalid JSON");
        const x = await rpc("AuthService", "sendRegistrationEmailVerification", input);
        return json({
          usernameAvailable: x.usernameAvailable ?? false,
          emailAvailable: x.emailAvailable ?? false,
          resendAfterSeconds: x.resendAfterSeconds ?? 0,
        });
      },
    },
  );
  return routes;
}
