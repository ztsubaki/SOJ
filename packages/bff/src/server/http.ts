import "server-only";
import { NextRequest, NextResponse } from "next/server";
import * as grpc from "@grpc/grpc-js";
export const session = (r: NextRequest) => r.cookies.get("soj_session")?.value;

// Next constructs request.url from its listening hostname. It is therefore an
// internal URL (often localhost), not necessarily the origin the browser used.
// A trusted reverse proxy must overwrite X-Forwarded-Proto and preserve Host.
export function requestProtocol(r: NextRequest): "http" | "https" | null {
  const forwarded = r.headers.get("x-forwarded-proto");
  if (forwarded !== null) {
    const value = forwarded.trim().toLowerCase();
    return value === "http" || value === "https" ? value : null;
  }
  try {
    const protocol = new URL(r.url).protocol;
    return protocol === "http:" ? "http" : protocol === "https:" ? "https" : null;
  } catch {
    return null;
  }
}

export function externalOrigin(r: NextRequest) {
  const protocol = requestProtocol(r);
  const host = r.headers.get("host");
  if (!protocol || !host || host.includes(",")) return null;
  try {
    const origin = new URL(`${protocol}://${host}`);
    if (origin.username || origin.password || origin.pathname !== "/" || origin.search || origin.hash)
      return null;
    return origin.origin;
  } catch {
    return null;
  }
}

export const sameOrigin = (r: NextRequest) => {
  const origin = r.headers.get("origin");
  return origin !== null && origin === externalOrigin(r);
};

export const json = (body: unknown, status = 200) =>
  NextResponse.json(body, { status });
export const error = (code: number, message: string) =>
  json({ code, error: message }, Math.floor(code / 10));
export function grpcError(reason: unknown) {
  if ((reason as { code?: unknown })?.code === 4130)
    return error(4130, "request body too large");
  if ((reason as { code?: unknown })?.code === 4150)
    return error(4150, "unsupported media type");
  const grpcError = reason as grpc.ServiceError;
  if (
    grpcError.code === grpc.status.INVALID_ARGUMENT &&
    grpcError.details === "registration email domain is not allowed"
  )
    return error(4002, "registration email domain is not allowed");
  switch (grpcError.code) {
    case grpc.status.INVALID_ARGUMENT:
      return error(4001, "invalid request");
    case grpc.status.UNAUTHENTICATED:
      return error(4010, "authentication required");
    case grpc.status.PERMISSION_DENIED:
      return error(4030, "operation is not authorized");
    case grpc.status.NOT_FOUND:
      return error(4040, "resource not found");
    case grpc.status.ALREADY_EXISTS:
      return error(4091, "resource already exists");
    case grpc.status.FAILED_PRECONDITION:
      return error(4092, "operation is not allowed");
    case grpc.status.RESOURCE_EXHAUSTED:
      return error(4290, "request rate is limited");
    case grpc.status.UNAVAILABLE:
    case grpc.status.DEADLINE_EXCEEDED:
      return error(5030, "service temporarily unavailable");
    default:
      return error(5000, "internal server error");
  }
}

export const MAX_JSON_BODY_BYTES = 1 << 20;

async function readJSONBody(r: NextRequest): Promise<string> {
  const tooLarge = () => Object.assign(new Error("request body too large"), { code: 4130 });
  if (Number(r.headers.get("content-length")) > MAX_JSON_BODY_BYTES) {
    void r.body?.cancel().catch(() => {});
    throw tooLarge();
  }
  if (!r.body) return "";
  const reader = r.body.getReader();
  const decoder = new TextDecoder();
  let size = 0;
  let text = "";
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_JSON_BODY_BYTES) {
        void reader.cancel().catch(() => {});
        throw tooLarge();
      }
      text += decoder.decode(value, { stream: true });
    }
    return text + decoder.decode();
  } finally {
    reader.releaseLock();
  }
}

export async function body<T extends object>(
  r: NextRequest,
  fields: readonly string[] = [],
  validate?: (value: Record<string, unknown>) => boolean,
): Promise<T | null> {
  try {
    const type = r.headers.get("content-type") ?? "";
    if (!/^application\/json(?:\s*;|$)/i.test(type))
      throw Object.assign(new Error("unsupported media type"), { code: 4150 });
    const raw = await readJSONBody(r);
    if (hasDuplicateObjectKey(raw)) return null;
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    if (fields.length && Object.keys(value).some((key) => !fields.includes(key)))
      return null;
    if (validate && !validate(value as Record<string, unknown>)) return null;
    return value as T;
  } catch (reason) {
    if ([4130, 4150].includes(Number((reason as { code?: unknown })?.code))) throw reason;
    return null;
  }
}

export const isString = (value: unknown) => typeof value === "string";
export const isBoolean = (value: unknown) => typeof value === "boolean";
export const isStringArray = (value: unknown) =>
  Array.isArray(value) && value.every(isString);
export const isPositiveInteger = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value > 0;
export const isPositiveIntegerArray = (value: unknown): value is number[] =>
  Array.isArray(value) && value.every(isPositiveInteger);
export const optional = <T>(
  value: unknown,
  validate: (value: unknown) => value is T,
): value is T | undefined => value === undefined || validate(value);
export const nullable = <T>(
  value: unknown,
  validate: (value: unknown) => value is T,
): value is T | null | undefined => value === undefined || value === null || validate(value);

// JSON.parse accepts duplicate object keys. Browser requests do not: preserving
// this distinction prevents an intermediary and the BFF reading different data.
function hasDuplicateObjectKey(raw: string) {
  let i = 0;
  const space = () => { while (/\s/.test(raw[i] ?? "")) i++; };
  const string = () => {
    const start = i++;
    let escaped = false;
    while (i < raw.length) {
      const c = raw[i++];
      if (!escaped && c === '"') return JSON.parse(raw.slice(start, i)) as string;
      escaped = !escaped && c === "\\";
      if (c !== "\\") escaped = false;
    }
    throw new Error("unterminated string");
  };
  const value = (): boolean => {
    space();
    if (raw[i] === "{") {
      i++; space(); const keys = new Set<string>();
      if (raw[i] === "}") { i++; return false; }
      while (true) {
        space(); if (raw[i] !== '"') throw new Error("object key");
        const key = string(); if (keys.has(key)) return true; keys.add(key);
        space(); if (raw[i++] !== ":") throw new Error("colon");
        if (value()) return true; space();
        if (raw[i] === "}") { i++; return false; }
        if (raw[i++] !== ",") throw new Error("comma");
      }
    }
    if (raw[i] === "[") {
      i++; space(); if (raw[i] === "]") { i++; return false; }
      while (true) { if (value()) return true; space(); if (raw[i] === "]") { i++; return false; } if (raw[i++] !== ",") throw new Error("comma"); }
    }
    if (raw[i] === '"') { string(); return false; }
    const match = /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(raw.slice(i));
    if (!match) throw new Error("value");
    i += match[0].length; return false;
  };
  try { const duplicate = value(); space(); return duplicate || i !== raw.length; } catch { return true; }
}

export function query(r: NextRequest, fields: readonly string[]) {
  const entries = [...r.nextUrl.searchParams.entries()];
  const seen = new Set<string>();
  if (entries.some(([key]) => seen.has(key) || !fields.includes(key) ? true : (seen.add(key), false)))
    throw Object.assign(new Error("invalid query"), { code: grpc.status.INVALID_ARGUMENT });
  return r.nextUrl.searchParams;
}

export function pagination(params: URLSearchParams) {
  const parse = (name: "page" | "pageSize", fallback: number) => {
    const value = params.get(name);
    if (value === null) return fallback;
    if (!/^[1-9]\d*$/.test(value))
      throw Object.assign(new Error("invalid page"), { code: grpc.status.INVALID_ARGUMENT });
    const number = Number(value);
    if (!Number.isSafeInteger(number) || (name === "pageSize" && number > 100))
      throw Object.assign(new Error("invalid page"), { code: grpc.status.INVALID_ARGUMENT });
    return number;
  };
  return { page: parse("page", 1), pageSize: parse("pageSize", 20) };
}

export function optionalQuery(params: URLSearchParams, name: string) {
  const value = params.get(name);
  if (value === null) return undefined;
  if (value === "")
    throw Object.assign(new Error("empty query parameter"), {
      code: grpc.status.INVALID_ARGUMENT,
    });
  return value;
}

// Route and query parameters are strings by definition. Convert numeric
// resource IDs at the BFF boundary so internal RPCs and browser JSON remain
// numeric instead of carrying numeric strings through the application.
export function numericID(value: unknown) {
  if (typeof value !== "string" || !/^[1-9]\d*$/.test(value))
    throw Object.assign(new Error("invalid numeric id"), {
      code: grpc.status.INVALID_ARGUMENT,
    });
  const id = Number(value);
  if (!Number.isSafeInteger(id))
    throw Object.assign(new Error("invalid numeric id"), {
      code: grpc.status.INVALID_ARGUMENT,
    });
  return id;
}

export function optionalNumericQuery(params: URLSearchParams, name: string) {
  const value = optionalQuery(params, name);
  return value === undefined ? undefined : numericID(value);
}
export function grpcTimestamp(value: unknown) {
  const milliseconds = Date.parse(String(value));
  if (!Number.isFinite(milliseconds))
    throw Object.assign(new Error("invalid timestamp"), {
      code: grpc.status.INVALID_ARGUMENT,
    });
  return {
    seconds: Math.floor(milliseconds / 1_000),
    nanos: (milliseconds % 1_000) * 1_000_000,
  };
}
export function enumInput<T extends string | number>(
  values: Record<string, T>,
  prefix: string,
  value: unknown,
): T | undefined {
  if (value === undefined) return undefined;
  const raw = String(value);
  if (raw !== raw.toLowerCase())
    throw Object.assign(new Error("invalid enum"), { code: grpc.status.INVALID_ARGUMENT });
  const key = `${prefix}${raw.toUpperCase()}`;
  if (!Object.hasOwn(values, key))
    throw Object.assign(new Error("invalid enum"), {
      code: grpc.status.INVALID_ARGUMENT,
    });
  return values[key];
}

// Problem difficulty is deliberately title-cased in the browser contract.
export function titleEnumInput<T extends string | number>(
  values: Record<string, T>,
  prefix: string,
  value: unknown,
): T | undefined {
  if (value === undefined) return undefined;
  if (typeof value !== "string" || !/^[A-Z][a-z]*$/.test(value))
    throw Object.assign(new Error("invalid enum"), { code: grpc.status.INVALID_ARGUMENT });
  const key = `${prefix}${value.toUpperCase()}`;
  if (!Object.hasOwn(values, key))
    throw Object.assign(new Error("invalid enum"), { code: grpc.status.INVALID_ARGUMENT });
  return values[key];
}

export function booleanInput(value: unknown): boolean | undefined {
  if (value === undefined) return undefined;
  if (value === "true") return true;
  if (value === "false") return false;
  throw Object.assign(new Error("invalid boolean"), {
    code: grpc.status.INVALID_ARGUMENT,
  });
}
