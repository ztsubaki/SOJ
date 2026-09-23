import "server-only";
import { createHash } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import { controlPlaneHTTPURL } from "./control-plane";

const failure = (status: number, message: string) =>
  NextResponse.json(
    {
      code:
        status === 413
          ? 4130
          : status === 415
            ? 4150
            : status === 400
              ? 4001
              : status * 10,
      error: message,
    },
    { status, headers: { "Cache-Control": "no-store" } },
  );
export function megabytes(value: string | undefined, fallback: number) {
  if (!value || !/^[1-9]\d*$/.test(value)) return fallback;
  const bytes = Number(value) * (1 << 20);
  return Number.isSafeInteger(bytes) ? bytes : fallback;
}
function credentials(request: NextRequest, serviceToken: string) {
  const token = request.cookies.get("soj_session")?.value;
  if (!token || !serviceToken) return null;
  return { authorization: `Bearer ${serviceToken}`, "x-soj-session": token };
}
function metadata(request: NextRequest) {
  const filename = request.headers.get("x-soj-filename") ?? "";
  const type = request.headers.get("x-soj-content-type") ?? "";
  let decoded: string;
  try {
    decoded = decodeURIComponent(filename);
  } catch {
    return null;
  }
  if (
    !filename ||
    filename.length > 765 ||
    !/^(?:[A-Za-z0-9._~-]|%[A-Fa-f0-9]{2})+$/.test(filename) ||
    !decoded ||
    Buffer.byteLength(decoded) > 255 ||
    /[\\/\u0000-\u001f\u007f]/.test(decoded) ||
    type.length > 127 ||
    !/^[a-zA-Z0-9!#$&^_.+-]+\/[a-zA-Z0-9!#$&^_.+-]+$/.test(type)
  )
    return null;
  return { "x-soj-filename": filename, "x-soj-content-type": type };
}
export async function proxyUpload(
  request: NextRequest,
  path: string,
  method: "POST" | "PUT",
  serviceToken: string,
  max: number,
) {
  const auth = credentials(request, serviceToken);
  if (!auth) return failure(401, "authentication required");
  if (request.headers.get("content-type") !== "application/octet-stream")
    return failure(415, "raw bytes required");
  const meta = metadata(request);
  if (!meta) return failure(400, "invalid upload metadata");
  if (!request.body) return failure(400, "file required");
  const abort = new AbortController();
  const cancel = () => abort.abort();
  request.signal.addEventListener("abort", cancel, { once: true });
  let size = 0;
  const hash = createHash("sha256");
  let tooLarge = false;
  const stream = request.body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        size += chunk.byteLength;
        if (size > max) {
          tooLarge = true;
          throw new Error("upload too large");
        }
        hash.update(chunk);
        controller.enqueue(chunk);
      },
    }),
  );
  try {
    const response = await fetch(`${controlPlaneHTTPURL()}${path}`, {
      method,
      headers: { ...auth, ...meta, "content-type": "application/octet-stream" },
      body: stream,
      signal: abort.signal,
      cache: "no-store", // @ts-expect-error Node streaming request
      duplex: "half",
    });
    if (!response.ok) {
      const value = await response.json().catch(() => null);
      return value &&
        typeof value.code === "number" &&
        typeof value.error === "string"
        ? NextResponse.json(
            { code: value.code, error: value.error },
            { status: response.status },
          )
        : failure(503, "upload unavailable");
    }
    if (
      Number(response.headers.get("x-soj-size-bytes")) !== size ||
      response.headers.get("x-soj-sha256") !== hash.digest("hex")
    )
      return failure(
        503,
        "upload integrity check failed; verify resource before retrying",
      );
    return NextResponse.json(await response.json(), {
      status: response.status,
    });
  } catch {
    return failure(
      tooLarge ? 413 : 503,
      tooLarge ? "upload exceeds limit" : "upload unavailable",
    );
  } finally {
    request.signal.removeEventListener("abort", cancel);
  }
}
export async function proxyDownload(
  request: NextRequest,
  path: string,
  serviceToken: string,
  options?: { contentType?: string; inline?: boolean },
) {
  const auth = credentials(request, serviceToken);
  if (!auth) return failure(401, "authentication required");
  const abort = new AbortController();
  const cancel = () => abort.abort();
  request.signal.addEventListener("abort", cancel, { once: true });
  try {
    const upstream = await fetch(`${controlPlaneHTTPURL()}${path}`, {
      method: request.method === "HEAD" ? "HEAD" : "GET",
      headers: auth,
      signal: abort.signal,
      cache: "no-store",
    });
    if (!upstream.ok) {
      request.signal.removeEventListener("abort", cancel);
      return failure(
        upstream.status === 404
          ? 404
          : upstream.status === 401
            ? 401
            : upstream.status === 403
              ? 403
              : 503,
        "artifact unavailable",
      );
    }
    const expected = Number(upstream.headers.get("content-length"));
    const digest = upstream.headers.get("x-soj-sha256");
    if (
      !Number.isSafeInteger(expected) ||
      expected < 0 ||
      !digest ||
      !/^[a-f0-9]{64}$/.test(digest)
    )
      throw new Error("invalid metadata");
    const upstreamDisposition = upstream.headers.get("content-disposition");
    const disposition = options?.inline
      ? upstreamDisposition?.replace(/^\s*attachment\b/i, "inline") || "inline"
      : upstreamDisposition || "attachment";
    const headers = new Headers({
      "Cache-Control": "private, no-store",
      "X-Content-Type-Options": "nosniff",
      "Content-Length": String(expected),
      "X-SOJ-SHA256": digest,
      "Content-Type":
        options?.contentType ||
        upstream.headers.get("content-type") ||
        "application/octet-stream",
      "Content-Disposition": disposition,
    });
    if (request.method === "HEAD") {
      request.signal.removeEventListener("abort", cancel);
      return new NextResponse(null, { headers });
    }
    if (!upstream.body) throw new Error("missing artifact");
    const reader = upstream.body.getReader();
    const hash = createHash("sha256");
    let size = 0;
    const stream = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          const part = await reader.read();
          if (part.done) {
            if (size !== expected || hash.digest("hex") !== digest)
              throw new Error("artifact integrity check failed");
            request.signal.removeEventListener("abort", cancel);
            controller.close();
            return;
          }
          size += part.value.byteLength;
          if (size > expected)
            throw new Error("artifact exceeds declared size");
          hash.update(part.value);
          controller.enqueue(part.value);
        } catch {
          console.error("Artifact download stream failed", { path });
          cancel();
          request.signal.removeEventListener("abort", cancel);
          controller.error(new Error("artifact transfer failed"));
        }
      },
      cancel() {
        cancel();
        request.signal.removeEventListener("abort", cancel);
        return reader.cancel();
      },
    });
    return new NextResponse(stream, { headers });
  } catch {
    cancel();
    request.signal.removeEventListener("abort", cancel);
    return failure(503, "artifact unavailable");
  }
}
