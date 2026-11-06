import { ContestStatus, SubmissionStatus } from "@soj/bff/generated/soj/v1/common";
import { megabytes, proxyUpload, proxyDownload } from "@soj/bff/server/artifacts";
import { authRoutes } from "@soj/bff/server/auth";
import { createRouter, type Route } from "@soj/bff/server/router";
import {
  session,
  json,
  enumInput,
  pagination,
  optionalQuery,
  numericID,
  optionalNumericQuery,
} from "@soj/bff/server/http";
import {
  contest,
  problem,
  webSubmission as submission,
  leaderboard,
  paged,
  timestamp,
} from "@soj/bff/server/mapping";
import { NextRequest } from "next/server";
import { rpc } from "@/lib/server/control-plane";

export const runtime = "nodejs";

const page = (r: NextRequest) => pagination(r.nextUrl.searchParams);
const optional = (r: NextRequest, name: string) =>
  optionalQuery(r.nextUrl.searchParams, name);

const handle = createRouter("Web", [
  ...authRoutes(rpc, true),
  {
    path: "contests",
    query: ["page", "pageSize", "query", "status"],
    GET: async (r) => {
      const status = optional(r, "status");
      const x = await rpc("ContestService", "listContests", {
        page: page(r),
        query: optional(r, "query"),
        status: status
          ? enumInput(ContestStatus, "CONTEST_STATUS_", status)
          : undefined,
      });
      return json(paged(x, contest));
    },
  },
  {
    path: "contests/:contestId",
    GET: async (r, path) => {
      const x = await rpc(
        "ContestService",
        "getContest",
        { id: numericID(path[1]) },
        session(r),
      );
      return json(contest(x.contest));
    },
  },
  {
    path: "contests/:contestId/problems",
    GET: async (r, path) => {
      const x = await rpc(
        "ContestService",
        "listContestProblems",
        { contestId: numericID(path[1]) },
        session(r),
      );
      return json((x?.items ?? []).map((item) => problem(item)));
    },
  },
  {
    path: "contests/:contestId/problems/:problemId",
    GET: async (r, path) => {
      const x = await rpc(
        "ContestService",
        "getContestProblem",
        { contestId: numericID(path[1]), problemId: numericID(path[3]) },
        session(r),
      );
      return json(problem(x.problem, true));
    },
  },
  {
    path: "contests/:contestId/leaderboard",
    query: ["page", "pageSize"],
    GET: async (r, path) => {
      const x = await rpc(
        "ContestService",
        "getLeaderboard",
        { contestId: numericID(path[1]), page: page(r) },
        session(r),
      );
      return json(paged(x, leaderboard));
    },
  },
  {
    path: "contests/:contestId/registration",
    POST: async (r, path) => {
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
    },
  },
  {
    path: "contests/:contestId/participants",
    query: ["page", "pageSize"],
    GET: async (r, path) => {
      const x = await rpc(
        "ContestService",
        "listParticipants",
        { contestId: numericID(path[1]), page: page(r) },
        session(r),
      );
      return json(paged(x, item => ({ user: { id: item.user?.id ?? 0, username: item.user?.username ?? "" }, registeredAt: timestamp(item.registeredAt) })));
    },
  },
  {
    path: "contests/:contestId/problems/:problemId/submissions",
    POST: (r, path) => proxyUpload(
      r,
      `/internal/v1/contests/${encodeURIComponent(path[1])}/problems/${encodeURIComponent(path[3])}/submissions`,
      "POST",
      process.env.WEB_BFF_SERVICE_TOKEN ?? "",
      megabytes(process.env.MAX_SOURCE_SIZE_MB, 10 << 20),
    ),
  },
  {
    path: "submissions/:submissionId/log",
    GET: (r, path) => proxyDownload(
      r,
      `/internal/v1/submissions/${encodeURIComponent(path[1])}/log`,
      process.env.WEB_BFF_SERVICE_TOKEN ?? "",
    ),
  },
  {
    path: "submissions/:submissionId",
    GET: async (r, path) => {
      const x = await rpc("SubmissionService", "getMySubmission", { id: numericID(path[1]) }, session(r));
      return json({ ...submission(x.submission), sourceMetadata: { filename: x.sourceMetadata?.filename ?? "", contentType: x.sourceMetadata?.contentType ?? "", sizeBytes: Number(x.sourceMetadata?.sizeBytes ?? 0), sha256: x.sourceMetadata?.sha256 ?? "" }, log: x.hasCurrentLog ? { href: `/api/v1/submissions/${encodeURIComponent(path[1])}/log` } : null });
    },
  },
  {
    path: "submissions",
    query: ["page", "pageSize", "contestId", "problemId", "status"],
    GET: async (r) => {
      const x = await rpc(
        "SubmissionService",
        "listMySubmissions",
        {
          contestId: optionalNumericQuery(r.nextUrl.searchParams, "contestId"),
          problemId: optionalNumericQuery(r.nextUrl.searchParams, "problemId"),
          page: page(r),
          status: enumInput(SubmissionStatus, "SUBMISSION_STATUS_", optional(r, "status")),
        },
        session(r),
      );
      return json(paged(x, submission));
    },
  },
  {
    path: "me/stats",
    GET: async (r) => {
      const x = await rpc("StatsService", "getMyStats", {}, session(r));
      return json({
        rank: x?.rank ?? null,
        totalUsers: Number(x?.totalUsers ?? 0),
        submissionCount: Number(x?.submissionCount ?? 0),
        acceptedCount: Number(x?.acceptedCount ?? 0),
        acceptanceRate: Number(x?.acceptanceRate ?? 0),
      });
    },
  },
  {
    path: "platform/cluster-status",
    GET: async () => {
      const x = await rpc("StatsService", "getClusterStatus", {});
      return json({
        totalAgents: Number(x?.totalAgents ?? 0),
        emptyAgents: Number(x?.emptyAgents ?? 0),
        noQueueAgents: Number(x?.noQueueAgents ?? 0),
        queuedAgents: Number(x?.queuedAgents ?? 0),
        updatedAt: x?.updatedAt ? timestamp(x?.updatedAt) : null,
      });
    },
  },
] satisfies Route[]);

export const GET = handle;
export const POST = handle;
export const PUT = handle;
export const PATCH = handle;
export const DELETE = handle;
export const HEAD = handle;
