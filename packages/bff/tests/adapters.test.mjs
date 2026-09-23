import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
import { createRequire } from "node:module";
const nodeRequire = createRequire(import.meta.url);
function load(name, globals = {}) {
  const source = readFileSync(
    new URL(`../src/${name}.ts`, import.meta.url),
    "utf8",
  );
  const code = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
  const context = { exports: {}, TextDecoder, ...globals };
  vm.runInNewContext(code, context);
  return context.exports;
}
test("page-load renewal uses 24 hours, does not retry and stores only expiry", async () => {
  const now = Date.now(),
    day = 24 * 60 * 60 * 1_000,
    storage = new Map();
  let calls = 0,
    status = 200;
  const session = load("session", {
    Date: class extends Date {
      static now() {
        return now;
      }
    },
    localStorage: {
      getItem: (k) => storage.get(k) ?? null,
      setItem: (k, v) => storage.set(k, v),
      removeItem: (k) => storage.delete(k),
    },
    fetch: async () => {
      calls++;
      return {
        ok: status === 200,
        status,
        json: async () => ({
          expiresAt: new Date(now + 3 * day).toISOString(),
          sessionToken: "private",
        }),
      };
    },
  });
  await session.refreshSessionIfNeeded();
  for (const offset of [0, -1, day, 3 * day]) {
    session.observeSession({ expiresAt: new Date(now + offset).toISOString() });
    await session.refreshSessionIfNeeded();
  }
  assert.equal(calls, 0);
  session.observeSession({ expiresAt: new Date(now + day - 1).toISOString() });
  await session.refreshSessionIfNeeded();
  assert.equal(calls, 1);
  assert.deepEqual(
    [...storage],
    [["soj-session-expires-at", String(now + 3 * day)]],
  );
  await session.refreshSessionIfNeeded();
  assert.equal(calls, 1);
  status = 401;
  session.observeSession({ expiresAt: new Date(now + 1).toISOString() });
  await session.refreshSessionIfNeeded();
  assert.equal(calls, 2);
  assert.equal(storage.size, 0);
});
test("mappings exclude credentials and preserve false, zero and null", () => {
  const map = load("server/mapping");
  const user = map.authUser({
    id: "u",
    role: "ROLE_ADMIN",
    sessionToken: "private",
  });
  assert.equal(user.role, "admin");
  assert.equal("sessionToken" in user, false);
  const submission = map.submission({
    kind: "SUBMISSION_KIND_ADMIN_TEST",
    score: "0",
    message: "",
    status: "SUBMISSION_STATUS_PENDING",
  });
  assert.equal(submission.score, "0");
  assert.equal(submission.message, "");
  assert.equal(submission.contestId, null);
  assert.equal(submission.kind, "admin_test");
  assert.equal(map.problem({ runnable: true }).runnable, true);
  assert.equal(map.problem({}).runnable, false);
  const leaderboard = map.leaderboard({
    score: "12.5",
    problemScores: [
      { problemId: 3, score: "10" },
      { problemId: 7, score: "2.5" },
    ],
  });
  assert.deepEqual(JSON.parse(JSON.stringify(leaderboard.problemScores)), [
    { problemId: 3, score: "10" },
    { problemId: 7, score: "2.5" },
  ]);
  const values = map.structValue({
    fields: {
      enabled: { boolValue: false },
      count: { numberValue: 0 },
      empty: { nullValue: "NULL_VALUE" },
      tags: { listValue: { values: [{ stringValue: "HPC" }] } },
    },
  });
  assert.deepEqual(JSON.parse(JSON.stringify(values)), {
    enabled: false,
    count: 0,
    empty: null,
    tags: ["HPC"],
  });
  assert.equal(
    map.timestamp({ seconds: "1", nanos: 500_000_000 }),
    "1970-01-01T00:00:01.500Z",
  );
  assert.equal(
    map.paged(
      { items: [], page: { total: "0", page: 1, pageSize: 20 } },
      map.submission,
    ).total,
    0,
  );
});

test("shared client preserves raw uploads, disables caching and handles BFF errors", async () => {
  let cleared = 0;
  let response = new Response(null, { status: 204 });
  const calls = [];
  const client = load("client", {
    Headers,
    require: (name) => {
      assert.equal(name, "./session");
      return { clearSession: () => cleared++ };
    },
    fetch: async (url, init) => {
      calls.push({ url, init });
      return response;
    },
  });
  const file = new Blob(["source"]);
  assert.equal(await client.request("/upload", {
    method: "POST", body: file, headers: { "Content-Type": "application/octet-stream" },
  }), undefined);
  assert.equal(calls[0].url, "/api/v1/upload");
  assert.equal(calls[0].init.body, file);
  assert.equal(calls[0].init.headers.get("Content-Type"), "application/octet-stream");
  assert.equal(calls[0].init.cache, "no-store");
  assert.equal(calls[0].init.credentials, "include");
  await client.request("/users", { method: "POST", body: "{}" });
  assert.equal(calls[1].init.headers.get("Content-Type"), "application/json");
  response = Response.json({ code: 4010, error: "authentication required" }, { status: 401 });
  await assert.rejects(client.request("/auth/me"), (error) => error instanceof client.ApiError && error.code === 4010);
  assert.equal(cleared, 1);
  response = new Response("unavailable", { status: 503 });
  await assert.rejects(client.request("/users"), (error) => error.code === 5030);
  assert.equal(calls.length, 4);
});

test("JSON bodies preserve their declared scalar types", async () => {
  const http = load("server/http", {
    require: (name) => {
      if (name === "server-only") return {};
      if (name === "next/server") return { NextResponse: { json: () => ({}) } };
      return nodeRequire(name);
    },
  });
  const request = (value) => new Request("https://soj.test", {
    method: "POST",
    headers: new Headers({ "content-type": "application/json" }),
    body: JSON.stringify(value),
  });
  const validate = (value) =>
    http.isBoolean(value.visible) && http.isString(value.title);
  assert.deepEqual(
    JSON.parse(JSON.stringify(await http.body(request({ visible: false, title: "private" }), ["visible", "title"], validate))),
    { visible: false, title: "private" },
  );
  assert.equal(
    await http.body(request({ visible: "false", title: "private" }), ["visible", "title"], validate),
    null,
  );
  assert.equal(
    await http.body(request({ visible: false, title: 12 }), ["visible", "title"], validate),
    null,
  );
});

test("JSON body limit counts streamed bytes and maps overflow to 413", async () => {
  const http = load("server/http", {
    require: (name) => {
      if (name === "server-only") return {};
      if (name === "next/server") return { NextResponse: { json: (body, options) => ({ body, status: options.status }) } };
      return nodeRequire(name);
    },
  });
  const limit = http.MAX_JSON_BODY_BYTES;
  const request = (body, length) => new Request("https://soj.test", {
    method: "POST", body, duplex: "half",
    headers: { "content-type": "application/json", ...(length === undefined ? {} : { "content-length": String(length) }) },
  });
  const exact = '{"text":"' + "a".repeat(limit - 11) + '"}';
  assert.equal(new TextEncoder().encode(exact).length, limit);
  assert.equal((await http.body(request(exact))).text.length, limit - 11);
  for (const length of [undefined, 1]) {
    let cancelled = false;
    const stream = new ReadableStream({
      pull(controller) { controller.enqueue(new Uint8Array(65536).fill(32)); },
      cancel() { cancelled = true; },
    });
    await assert.rejects(http.body(request(stream, length)), error => {
      assert.equal(error.code, 4130);
      assert.equal(http.grpcError(error).status, 413);
      return true;
    });
    assert.equal(cancelled, true);
  }
  await assert.rejects(http.body(request("{}", limit + 1)), error => error.code === 4130);
  await assert.rejects(http.body(request(JSON.stringify({ text: "界".repeat(limit / 3) }))), error => error.code === 4130);
  assert.equal(await http.body(request('{"a":1,"a":2}')), null);
});

test("BFF exposes registration email-domain rejection as a distinct error", () => {
  const http = load("server/http", {
    require: (name) => {
      if (name === "server-only") return {};
      if (name === "next/server") return { NextResponse: { json: (body, options) => ({ body, status: options.status }) } };
      return nodeRequire(name);
    },
  });
  const response = http.grpcError({
    code: 3,
    details: "registration email domain is not allowed",
  });
  assert.equal(response.status, 400);
  assert.equal(response.body.code, 4002);
});

test("BFF upload limits use a valid shared MiB setting", () => {
  const artifacts = load("server/artifacts", {
    require: (name) => {
      if (name === "server-only") return {};
      if (name === "./control-plane") return { controlPlaneHTTPURL: () => "http://control-plane.test" };
      return nodeRequire(name);
    },
  });
  assert.equal(artifacts.megabytes("25", 10), 25 * 1024 * 1024);
  assert.equal(artifacts.megabytes("0", 10), 10);
  assert.equal(artifacts.megabytes("3.5", 10), 10);
});
