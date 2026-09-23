import { ContestStatus, SubmissionStatus } from "@soj/bff/generated/soj/v1/common";
import { megabytes, proxyUpload, proxyDownload } from "@soj/bff/server/artifacts";
import { NextRequest } from "next/server";
import { rpc } from "@/lib/server/control-plane";

export const runtime = "nodejs";
type Params = { params: Promise<{ path: string[] }> };
import { authRoute } from "@soj/bff/server/auth";
import {
  session,
  json,
  error,
  grpcError,
  enumInput,
  pagination,
  query,
  optionalQuery,
  numericID,
  optionalNumericQuery,
  sameOrigin,
} from "@soj/bff/server/http";
import {
  contest,
  problem,
  webSubmission as submission,
  leaderboard,
  paged,
  timestamp,
} from "@soj/bff/server/mapping";
const page = (r: NextRequest, fields: string[] = []) =>
  pagination(query(r, ["page", "pageSize", ...fields]));
const optional = (r: NextRequest, name: string) =>
  optionalQuery(r.nextUrl.searchParams, name);
async function handle(r: NextRequest, path: string[]) {
  const key = path.join("/");
  const method = r.method === "HEAD" ? "GET" : r.method;
  if (
    method !== "GET" &&
    key !== "auth/logout" &&
    !sameOrigin(r)
  )
    return error(4030, "origin mismatch");
  try {
    const authResponse = await authRoute(r, key, rpc, true);
    if (authResponse) return authResponse;
    if (key === "contests") query(r, ["page", "pageSize", "query", "status"]);
    else if (key === "submissions") query(r, ["page", "pageSize", "contestId", "problemId", "status"]);
    else if (path[0] === "contests" && path[2] === "participants") query(r, ["page", "pageSize"]);
    else if (method === "GET") query(r, []);
    if (key === "contests" && method === "GET") {
      const status = optional(r, "status");
      const x = await rpc("ContestService", "listContests", {
        page: page(r, ["query", "status"]),
        query: optional(r, "query"),
        status: status
          ? enumInput(ContestStatus, "CONTEST_STATUS_", status)
          : undefined,
      });
      return json(paged(x, contest));
    }
    if (path[0] === "contests" && path.length === 2 && method === "GET") {
      const x = await rpc(
        "ContestService",
        "getContest",
        { id: numericID(path[1]) },
        session(r),
      );
      return json(contest(x.contest));
    }
    if (
      path[0] === "contests" &&
      path[2] === "problems" &&
      path.length === 3 &&
      method === "GET"
    ) {
      const x = await rpc(
        "ContestService",
        "listContestProblems",
        { contestId: numericID(path[1]) },
        session(r),
      );
      return json((x?.items ?? []).map((item) => problem(item)));
    }
    if (
      path[0] === "contests" &&
      path[2] === "problems" &&
      path.length === 4 &&
      method === "GET"
    ) {
      const x = await rpc(
        "ContestService",
        "getContestProblem",
        { contestId: numericID(path[1]), problemId: numericID(path[3]) },
        session(r),
      );
      return json(problem(x.problem, true));
    }
    if (
      path[0] === "contests" &&
      path[2] === "leaderboard" &&
      path.length === 3 &&
      method === "GET"
    ) {
      const x = await rpc(
        "ContestService",
        "getLeaderboard",
        { contestId: numericID(path[1]) },
        session(r),
      );
      return json({ items: (x?.items ?? []).map(leaderboard) });
    }
    if (
      path[0] === "contests" &&
      path[2] === "registration" &&
      path.length === 3 &&
      method === "POST"
    ) {
      const x = await rpc(
        "ContestService",
        "registerForContest",
        { contestId: numericID(path[1]) },
        session(r),
      );
      return json(
        {
          contestId: x?.contestId ?? 0,
          registeredAt: timestamp(x?.registeredAt),
        },
        201,
      );
    }
    if (
      path[0] === "contests" &&
      path[2] === "participants" &&
      path.length === 3 &&
      method === "GET"
    ) {
      const x = await rpc(
        "ContestService",
        "listParticipants",
        { contestId: numericID(path[1]), page: page(r) },
        session(r),
      );
      return json(paged(x, item => ({ user: { id: item.user?.id ?? 0, username: item.user?.username ?? "" }, registeredAt: timestamp(item.registeredAt) })));
    }
    if (
      path[0] === "contests" &&
      path[2] === "problems" &&
      path[4] === "submissions" &&
      path.length === 5 &&
      method === "POST"
    )
      return proxyUpload(
        r,
        `/internal/v1/contests/${encodeURIComponent(path[1])}/problems/${encodeURIComponent(path[3])}/submissions`,
        "POST",
        process.env.WEB_BFF_SERVICE_TOKEN ?? "",
        megabytes(process.env.MAX_SOURCE_SIZE_MB, 10 << 20),
      );
    if (
      path[0] === "submissions" &&
      path[2] === "log" &&
      path.length === 3 &&
      method === "GET"
    )
      return proxyDownload(
        r,
        `/internal/v1/submissions/${encodeURIComponent(path[1])}/log`,
        process.env.WEB_BFF_SERVICE_TOKEN ?? "",
      );
    if (path[0] === "submissions" && path.length === 2 && method === "GET") {
      const x = await rpc("SubmissionService", "getMySubmission", { id: numericID(path[1]) }, session(r));
      return json({ ...submission(x.submission), sourceMetadata: { filename: x.sourceMetadata?.filename ?? "", contentType: x.sourceMetadata?.contentType ?? "", sizeBytes: Number(x.sourceMetadata?.sizeBytes ?? 0), sha256: x.sourceMetadata?.sha256 ?? "" }, log: x.hasCurrentLog ? { href: `/api/v1/submissions/${encodeURIComponent(path[1])}/log` } : null });
    }
    if (key === "submissions" && method === "GET") {
      const x = await rpc(
        "SubmissionService",
        "listMySubmissions",
        {
          contestId: optionalNumericQuery(r.nextUrl.searchParams, "contestId"),
          problemId: optionalNumericQuery(r.nextUrl.searchParams, "problemId"),
          page: page(r, ["contestId", "problemId", "status"]),
          status: enumInput(SubmissionStatus, "SUBMISSION_STATUS_", optional(r, "status")),
        },
        session(r),
      );
      return json(paged(x, submission));
    }
    if (key === "me/stats" && method === "GET") {
      const x = await rpc("StatsService", "getMyStats", {}, session(r));
      return json({
        rank: x?.rank ?? null,
        totalUsers: Number(x?.totalUsers ?? 0),
        submissionCount: Number(x?.submissionCount ?? 0),
        acceptedCount: Number(x?.acceptedCount ?? 0),
        acceptanceRate: Number(x?.acceptanceRate ?? 0),
      });
    }
    if (key === "platform/cluster-status" && method === "GET") {
      const x = await rpc("StatsService", "getClusterStatus", {});
      return json({
        totalAgents: Number(x?.totalAgents ?? 0),
        emptyAgents: Number(x?.emptyAgents ?? 0),
        noQueueAgents: Number(x?.noQueueAgents ?? 0),
        queuedAgents: Number(x?.queuedAgents ?? 0),
        updatedAt: x?.updatedAt ? timestamp(x?.updatedAt) : null,
      });
    }
    const known =
      ["contests", "submissions", "me/stats", "platform/cluster-status"].includes(key) ||
      (path[0] === "contests" && (path.length === 2 ||
        (path.length === 3 && ["problems", "leaderboard", "registration", "participants"].includes(path[2])) ||
        (path.length === 4 && path[2] === "problems") ||
        (path.length === 5 && path[2] === "problems" && path[4] === "submissions"))) ||
      (path[0] === "submissions" && (path.length === 2 || (path.length === 3 && path[2] === "log"))) ||
      key.startsWith("auth/");
    return error(known ? 4050 : 4040, known ? "method not allowed" : "resource not found");
  } catch (reason) {
    console.error(
      "Web BFF request failed",
      reason instanceof Error ? reason.message : "unknown error",
    );
    return grpcError(reason);
  }
}
export async function GET(r: NextRequest, c: Params) {
  const response = await handle(r, (await c.params).path);
  response.headers.set("Cache-Control", "no-store");
  return response;
}
export const POST = GET;

export const HEAD = GET;
