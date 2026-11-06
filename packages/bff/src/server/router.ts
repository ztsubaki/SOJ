import "server-only";
import type { NextRequest } from "next/server";
import { error, grpcError, query, sameOrigin } from "./http";

type Method = "GET" | "POST" | "PUT" | "DELETE";
export type RouteHandler = (
  request: NextRequest,
  path: string[],
) => Response | Promise<Response>;
export type Route = {
  path: string;
  query?: readonly string[];
} & Partial<Record<Method, RouteHandler>>;
type Context = { params: Promise<{ path: string[] }> };

export function createRouter(name: string, routes: readonly Route[]) {
  const entries = routes.map((route) => ({ route, segments: route.path.split("/") }));

  async function dispatch(request: NextRequest, path: string[]): Promise<Response> {
    const method = request.method === "HEAD" ? "GET" : request.method;
    if (method !== "GET" && path.join("/") !== "auth/logout" && !sameOrigin(request))
      return error(4030, "origin mismatch");

    const entry = entries.find(({ segments }) =>
      segments.length === path.length &&
      segments.every((segment, index) => segment.startsWith(":") || segment === path[index]),
    );
    if (!entry) return error(4040, "resource not found");

    const handler = Object.hasOwn(entry.route, method)
      ? entry.route[method as Method]
      : undefined;
    if (!handler) return error(4050, "method not allowed");

    query(request, method === "GET" ? entry.route.query ?? [] : []);
    return handler(request, path);
  }

  return async function handle(request: NextRequest, context: Context): Promise<Response> {
    let response: Response;
    try {
      response = await dispatch(request, (await context.params).path);
    } catch (reason) {
      console.error(
        `${name} BFF request failed`,
        reason instanceof Error ? reason.message : "unknown error",
      );
      response = grpcError(reason);
    }
    response.headers.set("Cache-Control", "no-store");
    return response;
  };
}
