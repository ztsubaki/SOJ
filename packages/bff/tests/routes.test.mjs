import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import ts from "typescript";
function compile(path, require) {
  const source = readFileSync(new URL(path, import.meta.url), "utf8");
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const context = { exports: {}, require, console, URL, process };
  vm.runInNewContext(code, context);
  return context.exports;
}
function setup(app, result = {}) {
  const calls = [], mapping = compile("../src/server/mapping.ts");
  const response = data => ({ data, headers: new Headers() });
  const route = compile(`../../../apps/${app}/src/app/api/v1/[...path]/route.ts`, name => {
    if (name.endsWith("/mapping")) return mapping;
    if (name.endsWith("/auth")) return { authRoute: async () => null };
    if (name.endsWith("/http")) return {
      session: () => "session", json: response, error: code => response({ code }), grpcError: error => response({ code: error.code === 3 ? 4001 : 5000 }), enumInput: (_, prefix, value) => prefix + value.toUpperCase(),
      sameOrigin: () => true,
      booleanInput: value => value === undefined ? undefined : value === "true" ? true : value === "false" ? false : (() => { throw { code: 3 }; })(),
      optionalQuery: (params, name) => {
        const value = params.get(name);
        if (value === null) return undefined;
        if (value === "") throw { code: 3 };
        return value;
      },
      query: (request, fields) => {
        const seen = new Set();
        for (const [key] of request.nextUrl.searchParams) {
          if (seen.has(key) || !fields.includes(key)) throw { code: 3 };
          seen.add(key);
        }
        return request.nextUrl.searchParams;
      },
      pagination: params => ({ page: Number(params.get("page") ?? 1), pageSize: Number(params.get("pageSize") ?? 20) }),
      numericID: value => {
        const id = Number(value);
        if (!Number.isSafeInteger(id) || id <= 0) throw { code: 3 };
        return id;
      },
      optionalNumericQuery: (params, name) => {
        const value = params.get(name);
        if (value === null) return undefined;
        const id = Number(value);
        if (!Number.isSafeInteger(id) || id <= 0) throw { code: 3 };
        return id;
      },
    };
    if (name.endsWith("/control-plane")) return { rpc: async (...args) => { calls.push(args); return result; } };
    if (name === "next/server") return { NextResponse: class { constructor(_, options) { this.status = options.status; this.headers = new Headers(); } } };
    return {};
  });
  return { calls, request: async (path, method = "GET", query = "") => {
    const url = new URL(`https://soj.test/api/v1/${path}${query}`);
    return route[method]({ method, url: url.href, nextUrl: url, headers: new Headers({ origin: url.origin }) }, { params: Promise.resolve({ path: path.split("/") }) });
  } };
}
function httpHelpers() {
  return compile("../src/server/http.ts", name => {
    if (name === "next/server") return { NextResponse: class {} };
    if (name === "@grpc/grpc-js") return { status: {} };
    return {};
  });
}
test("same-origin validation uses the external Host and protocol", () => {
  const { sameOrigin } = httpHelpers();
  const request = (url, headers) => ({ url, headers: new Headers(headers) });
  assert.equal(sameOrigin(request("http://localhost:3000/api/v1/auth/login", {
    host: "192.168.1.10:3000", origin: "http://192.168.1.10:3000",
  })), true);
  assert.equal(sameOrigin(request("http://localhost:3000/api/v1/auth/login", {
    host: "oj.example.com", origin: "https://oj.example.com", "x-forwarded-proto": "https",
  })), true);
  assert.equal(sameOrigin(request("http://localhost:3000/api/v1/auth/login", {
    host: "oj.example.com", origin: "https://attacker.example",
  })), false);
  assert.equal(sameOrigin(request("http://localhost:3000/api/v1/auth/login", {
    host: "oj.example.com", origin: "https://oj.example.com", "x-forwarded-proto": "https,http",
  })), false);
});
test("Web forwards submission paging and filters and removes legacy route", async () => {
  const app = setup("web", { items: [] });
  const response = await app.request("submissions", "GET", "?contestId=1&status=accepted&page=2&pageSize=5");
  assert.equal(response.headers.get("Cache-Control"), "no-store");
  assert.equal(app.calls[0][0], "SubmissionService");
  assert.equal(app.calls[0][2].page.page, 2);
  assert.equal(app.calls[0][2].page.pageSize, 5);
  assert.equal(app.calls[0][2].status, "SUBMISSION_STATUS_ACCEPTED");
  assert.equal((await app.request("contests/c/submissions")).data.code, 4040);
  assert.equal(app.calls.length, 1);
});
test("Admin rejects invalid boolean filters", async () => {
  const app = setup("admin", { items: [] });
  assert.equal((await app.request("admin/problems", "GET", "?enabled=invalid")).data.code, 4001);
  assert.equal(app.calls.length, 0);
});
test("List filters reject empty values instead of widening the query", async () => {
  const app = setup("web", { items: [] });
  assert.equal((await app.request("submissions", "GET", "?contestId=")).data.code, 4001);
  assert.equal((await app.request("contests", "GET", "?query=")).data.code, 4001);
  assert.equal(app.calls.length, 0);
});
test("Web participants omit private fields and detail maps numeric metadata", async () => {
  const app = setup("web", { items: [{ user: { id: 1, username: "name", email: "private" }, disqualified: true }] });
  const response = await app.request("contests/1/participants");
  assert.deepEqual(Object.keys(response.data.items[0].user), ["id", "username"]);
  assert.equal(response.data.items[0].disqualified, undefined);
  const detail = setup("web", { submission: { id: 1 }, sourceMetadata: { sizeBytes: "12" }, hasCurrentLog: true });
  const actual = (await detail.request("submissions/1")).data;
  assert.equal(actual.sourceMetadata.sizeBytes, 12);
  assert.equal(actual.log.href, "/api/v1/submissions/1/log");
});
test("Admin qualification uses POST and DELETE on rankings disqualify", async () => {
  const app = setup("admin");
  for (const method of ["POST", "DELETE"]) assert.equal((await app.request("admin/contests/1/rankings/1/disqualify", method)).status, 204);
  assert.deepEqual(app.calls.map(call => call[1]), ["disqualifyParticipant", "restoreParticipant"]);
  assert.equal((await app.request("admin/contests/c/participants/u/restore", "POST")).data.code, 4040);
});
test("Admin workload and drain use browser response types", async () => {
  const app = setup("admin", { agentWorkload: { empty: 1, noQueue: 2, queued: 3 } });
  for (const path of ["admin/stats", "admin/system/status"]) assert.equal((await app.request(path)).data.agentWorkload.noQueue, 2);
  const drain = setup("admin", { id: "a", state: "AGENT_STATE_DRAINING", workload: "AGENT_WORKLOAD_EMPTY" });
  const response = await drain.request("admin/system/agents/a/drain", "POST");
  assert.equal(response.data.state, "draining");
  assert.equal(response.data.workload, "empty");
});
test("Admin password reset maps the one-time password without exposing it elsewhere", async () => {
  const app = setup("admin", { password: "one-time-password" });
  const response = await app.request("admin/users/1/reset-password", "POST");
  assert.equal(response.data.password, "one-time-password");
  assert.equal(app.calls[0][0], "AdminUserService");
  assert.equal(app.calls[0][1], "resetUserPassword");
  assert.equal(app.calls[0][2].id, 1);
});
