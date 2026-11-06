import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import vm from "node:vm";
import ts from "typescript";
import { NextRequest } from "next/server.js";

const nodeRequire = createRequire(import.meta.url);
const compiled = new Map();
function setup(app, rpc = async () => { throw new Error("unexpected upstream call"); }) {
  const modules = new Map();
  function load(file) {
    if (modules.has(file.href)) return modules.get(file.href);
    if (!compiled.has(file.href)) {
      compiled.set(file.href, ts.transpileModule(readFileSync(file, "utf8"), {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
      }).outputText);
    }
    const exports = {};
    modules.set(file.href, exports);
    vm.runInNewContext(compiled.get(file.href), {
      exports, console: { error() {} }, process, URL, TextDecoder,
      require(name) {
        if (name === "server-only") return {};
        if (name === "@/lib/server/control-plane") return { rpc };
        if (name.startsWith("@soj/bff/")) return load(new URL(`../src/${name.slice(9)}.ts`, import.meta.url));
        if (name.startsWith(".")) return load(new URL(`${name}.ts`, file));
        return nodeRequire(name);
      },
    });
    return exports;
  }
  const route = load(new URL(`../../../apps/${app}/src/app/api/v1/[...path]/route.ts`, import.meta.url));
  return async (target, method = "GET", init = {}) => {
    const url = new URL(`https://soj.test/api/v1/${target}`);
    const request = new NextRequest(url, {
      ...init,
      method,
      headers: { host: url.host, origin: url.origin, ...init.headers },
    });
    return route[method](request, {
      params: Promise.resolve({ path: url.pathname.slice("/api/v1/".length).split("/") }),
    });
  };
}
async function failure(response, code) {
  assert.equal(response.status, Math.floor(code / 10));
  assert.equal(response.headers.get("Cache-Control"), "no-store");
  assert.equal((await response.json()).code, code);
}

test("existing action paths reject unsupported methods rather than appearing absent", async () => {
  const admin = setup("admin");
  for (const method of ["GET", "HEAD", "PUT", "PATCH", "DELETE"])
    await failure(await admin("admin/users/1/reset-password", method), 4050);
  for (const path of ["admin/users/1/reset-password/extra", "admin/users/1/unknown-action", "auth/unknown"])
    await failure(await admin(path), 4040);
  await failure(await admin("admin/users/not-a-number"), 4001);
  await failure(await admin("admin/users/not-a-number", "POST"), 4050);
});

test("Web and Admin use the same origin, path, method and query error precedence", async () => {
  for (const app of ["web", "admin"]) {
    const request = setup(app);
    const collection = app === "web" ? "contests" : "admin/problems";
    const readOnly = app === "web" ? "platform/cluster-status" : "admin/stats";
    await failure(await request("missing?unexpected=1"), 4040);
    await failure(await request(`${readOnly}?unexpected=1`, "POST"), 4050);
    await failure(await request("missing", "POST", { headers: { origin: "https://attacker.test" } }), 4030);
    for (const method of ["GET", "HEAD"]) {
      await failure(await request(`${collection}?unexpected=1`, method), 4001);
      await failure(await request(`${collection}?page=1&page=2`, method), 4001);
      await failure(await request(`${collection}?page=0`, method), 4001);
      await failure(await request(`${collection}?query=`, method), 4001);
    }
    await failure(await request("auth/login?state=unconsumed", "POST"), 4001);
    await failure(await request("auth/login?state=1&state=2", "POST"), 4001);
  }
  await failure(await setup("admin")("admin/problems?enabled=invalid"), 4001);
  await failure(await setup("admin")("admin/users?query=ignored", "POST"), 4001);
});

test("registration routes exist only on Web, without a catch-all auth namespace", async () => {
  const web = setup("web"), admin = setup("admin");
  for (const path of ["auth/register", "auth/email-verification"]) {
    await failure(await admin(path, "POST"), 4040);
    await failure(await web(path), 4050);
    await failure(await web(path, "POST", { body: "{}", headers: { "content-type": "application/json" } }), 4000);
  }
  await failure(await web("auth/unknown"), 4040);
});

test("same-origin checks use public Host and forwarded protocol, not the listening URL", async () => {
  for (const app of ["web", "admin"]) {
    const request = setup(app);
    await failure(await request("auth/login", "POST", {
      headers: { host: "oj.example.com", origin: "https://oj.example.com", "x-forwarded-proto": "https", "content-type": "application/json" },
      body: "{}",
    }), 4000);
    await failure(await request("auth/login", "POST", {
      headers: { host: "oj.example.com", origin: "https://oj.example.com", "x-forwarded-proto": "https,http" },
    }), 4030);
    await failure(await request("auth/login", "POST", {
      headers: { host: "oj.example.com", origin: "https://attacker.example", "x-forwarded-proto": "https" },
    }), 4030);
  }
});

test("authentication responses keep opaque credentials in HttpOnly cookies only", async () => {
  const expiresAt = { seconds: Math.floor(Date.now() / 1000) + 7200, nanos: 0 };
  for (const app of ["web", "admin"]) {
    const request = setup(app, async () => ({
      user: { id: 1, username: "runner", email: "runner@example.test", role: "ROLE_USER", status: "USER_STATUS_ACTIVE" },
      sessionToken: "opaque-private-session", expiresAt,
    }));
    const response = await request("auth/login", "POST", {
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ username: "runner", password: "password" }),
    });
    assert.equal(response.status, 200);
    assert.equal(response.headers.get("Cache-Control"), "no-store");
    const cookie = response.headers.get("Set-Cookie");
    assert.match(cookie, /soj_session=opaque-private-session/);
    assert.match(cookie, /HttpOnly/i);
    assert.match(cookie, /SameSite=lax/i);
    const body = await response.json();
    assert.equal(body.user.username, "runner");
    assert.equal(body.user.role, "user");
    assert.equal(body.expiresAt, new Date(expiresAt.seconds * 1000).toISOString());
    assert.equal(JSON.stringify(body).includes("opaque-private-session"), false);
  }
});

test("logout remains origin-exempt but query errors cannot clear a session cookie", async () => {
  for (const app of ["web", "admin"]) {
    const request = setup(app);
    const response = await request("auth/logout", "POST", { headers: { origin: "https://other.test" } });
    assert.equal(response.status, 204);
    assert.equal(response.headers.get("Cache-Control"), "no-store");
    assert.match(response.headers.get("Set-Cookie"), /soj_session=;/);
    const rejected = await request("auth/logout?unexpected=1", "POST");
    assert.equal(rejected.headers.has("Set-Cookie"), false);
    await failure(rejected, 4001);
  }
});

test("upstream permission and availability errors remain private and uncached", async () => {
  for (const app of ["web", "admin"]) {
    for (const [upstream, expected] of [[7, 4030], [16, 4010], [14, 5030], [13, 5000]]) {
      const request = setup(app, async () => { throw Object.assign(new Error("private backend diagnostic"), { code: upstream }); });
      const response = await request("auth/me");
      const copy = response.clone();
      await failure(response, expected);
      assert.equal((await copy.text()).includes("private backend diagnostic"), false);
      assert.equal(copy.headers.has("Set-Cookie"), false);
    }
  }
});

test("public participant responses exclude private data and submission details retain metadata", async () => {
  const participants = setup("web", async () => ({
    items: [{ user: { id: 1, username: "runner", email: "private@example.test" }, disqualified: true }],
  }));
  const response = await participants("contests/1/participants");
  const body = await response.json();
  assert.deepEqual(Object.keys(body.items[0].user), ["id", "username"]);
  assert.equal(body.items[0].disqualified, undefined);
  const detail = setup("web", async () => ({ submission: { id: 1 }, sourceMetadata: { sizeBytes: 12 }, hasCurrentLog: true }));
  const value = await (await detail("submissions/1")).json();
  assert.equal(value.sourceMetadata.sizeBytes, 12);
  assert.equal(value.log.href, "/api/v1/submissions/1/log");
});
