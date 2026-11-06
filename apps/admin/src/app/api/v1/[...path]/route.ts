import type { ProblemInput, ProblemPatch } from "@soj/bff/generated/soj/v1/problems";
import type { ContestInput, CreateUserRequest } from "@soj/bff/generated/soj/v1/admin";
import {
  AgentState,
  ContestStatus,
  Difficulty,
  Role,
  ScoringPolicy,
  SubmissionKind,
  UserStatus,
  Verdict,
} from "@soj/bff/generated/soj/v1/common";
import { NullValue } from "@soj/bff/generated/google/protobuf/struct";
import { megabytes, proxyUpload, proxyDownload } from "@soj/bff/server/artifacts";
import { NextRequest, NextResponse } from "next/server";
import { rpc } from "@/lib/server/control-plane";

import { authRoutes } from "@soj/bff/server/auth";
import { createRouter, type Route, type RouteHandler } from "@soj/bff/server/router";
import {
  session,
  json,
  error,
  body,
  grpcTimestamp,
  enumInput,
  titleEnumInput,
  booleanInput,
  isBoolean,
  isString,
  isStringArray,
  isPositiveIntegerArray,
  nullable,
  numericID,
  optional as optionalBody,
  pagination,
  optionalQuery,
} from "@soj/bff/server/http";
import {
  agent,
  workload,
  authUser,
  adminProblem as problem,
  adminContest as contest,
  submission,
  paged,
  timestamp,
  job,
  leaderboard,
} from "@soj/bff/server/mapping";
type ProblemBody = Omit<ProblemInput, "difficulty" | "tags"> & {
  difficulty?: string;
  tags?: string[] | null;
};
type ContestBody = Omit<
  ContestInput,
  "startsAt" | "endsAt" | "scoringPolicy"
> & { startsAt?: string; endsAt?: string; scoringPolicy?: string };
export const runtime = "nodejs";
const page = (request: NextRequest) => pagination(request.nextUrl.searchParams);
const optional = (request: NextRequest, name: string) =>
  optionalQuery(request.nextUrl.searchParams, name);
const validProblemCreate = (value: Record<string, unknown>) =>
  isString(value.title) && isString(value.difficulty) &&
  isString(value.content) && isStringArray(value.partitions) &&
  optionalBody(value.tags, isStringArray) && optionalBody(value.enabled, isBoolean);
const validProblemPatch = (value: Record<string, unknown>) =>
  optionalBody(value.title, isString) && optionalBody(value.difficulty, isString) &&
  optionalBody(value.content, isString) && optionalBody(value.enabled, isBoolean) &&
  optionalBody(value.partitions, isStringArray) && nullable(value.tags, isStringArray);
const validContestCreate = (value: Record<string, unknown>) =>
  isString(value.title) && isString(value.startsAt) &&
  isString(value.endsAt) && isString(value.description) && isPositiveIntegerArray(value.problems) &&
  isBoolean(value.visible) && isString(value.scoringPolicy);
const validContestPatch = (value: Record<string, unknown>) =>
  optionalBody(value.title, isString) && optionalBody(value.startsAt, isString) &&
  optionalBody(value.endsAt, isString) && optionalBody(value.description, isString) &&
  optionalBody(value.problems, isPositiveIntegerArray) && optionalBody(value.visible, isBoolean) &&
  optionalBody(value.scoringPolicy, isString);

const qualification: RouteHandler = async (request, path) => {
  await rpc(
    "AdminContestService",
    request.method === "POST"
      ? "disqualifyParticipant"
      : "restoreParticipant",
    { contestId: numericID(path[2]), userId: numericID(path[4]) },
    session(request),
  );
  return new NextResponse(null, { status: 204 });
};

const downloadJobArtifact: RouteHandler = async (request, path) => {
  return proxyDownload(
    request,
    `/internal/v1/admin/submissions/${encodeURIComponent(path[2])}/jobs/${encodeURIComponent(path[4])}/${path[5]}`,
    process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
    path[5] === "result"
      ? { contentType: "application/json; charset=utf-8", inline: true }
      : { contentType: "text/plain; charset=utf-8", inline: true },
  );
};

const handle = createRouter("Admin", [
  ...authRoutes(rpc, false),
  {
    path: "admin/stats",
    GET: async (request) => {
      const x = await rpc(
        "AdminSystemService",
        "getStats",
        {},
        session(request),
      );
      return json({
        totalUsers: Number(x?.totalUsers ?? 0),
        todaySubmissions: Number(x?.todaySubmissions ?? 0),
        pendingSubmissions: Number(x?.pendingSubmissions ?? 0),
        agentWorkload: workload(x.agentWorkload),
      });
    },
  },
  {
    path: "admin/problems",
    query: ["page", "pageSize", "query", "enabled"],
    GET: async (request) => {
      const x = await rpc(
        "AdminProblemService",
        "listProblems",
        {
          page: page(request),
          query: optional(request, "query"),
          enabled: booleanInput(optional(request, "enabled")),
        },
        session(request),
      );
      return json(paged(x, problem));
    },
    POST: async (request) => {
      const input = await body<ProblemBody>(request, ["title", "difficulty", "tags", "content", "enabled", "partitions"], validProblemCreate);
      if (!input) return error(4000, "invalid JSON");
      return json(
        problem(
          await rpc(
            "AdminProblemService",
            "createProblem",
            {
              problem: {
                ...input,
                tags: input.tags ?? undefined,
                difficulty: titleEnumInput(
                  Difficulty,
                  "DIFFICULTY_",
                  input.difficulty,
                ),
              },
            },
            session(request),
          ),
        ),
        201,
      );
    },
  },
  {
    path: "admin/problems/:problemId",
    GET: async (request, path) => {
      return json(
        problem(
          await rpc(
            "AdminProblemService",
            "getProblem",
            { id: numericID(path[2]) },
            session(request),
          ),
        ),
      );
    },
    PUT: async (request, path) => {
      const input = await body<ProblemBody>(request, ["title", "difficulty", "tags", "content", "enabled", "partitions"], validProblemPatch);
      if (!input) return error(4000, "invalid JSON");
      const patch: ProblemPatch = {
        ...input,
        difficulty: input.difficulty
          ? titleEnumInput(Difficulty, "DIFFICULTY_", input.difficulty)
          : undefined,
        tags: input.tags == null ? undefined : { values: input.tags },
        clearTags: input.tags === null ? NullValue.NULL_VALUE : undefined,
        partitions:
          input.partitions === undefined
            ? undefined
            : { values: input.partitions },
      };
      return json(
        problem(
          await rpc(
            "AdminProblemService",
            "updateProblem",
            { id: numericID(path[2]), patch },
            session(request),
          ),
        ),
      );
    },
    DELETE: async (request, path) => {
      await rpc(
        "AdminProblemService",
        "deleteProblem",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    },
  },
  {
    path: "admin/problems/:problemId/package",
    POST: async (request, path) => {
      return proxyUpload(
        request,
        `/internal/v1/problems/${encodeURIComponent(path[2])}/package`,
        "PUT",
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
        megabytes(process.env.MAX_JUDGE_PROGRAM_SIZE_MB, 10 * 1024 ** 3),
      );
    },
  },
  {
    path: "admin/problems/:problemId/test-submissions",
    POST: async (request, path) => {
      return proxyUpload(
        request,
        `/internal/v1/problems/${encodeURIComponent(path[2])}/test-submissions`,
        "POST",
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
        megabytes(process.env.MAX_SOURCE_SIZE_MB, 10 << 20),
      );
    },
  },
  {
    path: "admin/contests",
    query: ["page", "pageSize", "query", "status"],
    GET: async (request) => {
      const status = optional(request, "status");
      const x = await rpc(
        "AdminContestService",
        "listContests",
        {
          page: page(request),
          query: optional(request, "query"),
          status: status
            ? enumInput(ContestStatus, "CONTEST_STATUS_", status)
            : undefined,
        },
        session(request),
      );
      return json(paged(x, contest));
    },
    POST: async (request) => {
      const input = await body<ContestBody>(request, ["title", "startsAt", "endsAt", "description", "problems", "visible", "scoringPolicy"], validContestCreate);
      if (!input) return error(4000, "invalid JSON");
      return json(
        contest(
          await rpc(
            "AdminContestService",
            "createContest",
            {
              contest: {
                ...input,
                startsAt: grpcTimestamp(input.startsAt),
                endsAt: grpcTimestamp(input.endsAt),
                scoringPolicy: enumInput(
                  ScoringPolicy,
                  "SCORING_POLICY_",
                  input.scoringPolicy,
                ),
              },
            },
            session(request),
          ),
        ),
        201,
      );
    },
  },
  {
    path: "admin/contests/:contestId",
    GET: async (request, path) => {
      return json(
        contest(
          await rpc(
            "AdminContestService",
            "getContest",
            { id: numericID(path[2]) },
            session(request),
          ),
        ),
      );
    },
    PUT: async (request, path) => {
      const input = await body<ContestBody>(request, ["title", "startsAt", "endsAt", "description", "problems", "visible", "scoringPolicy"], validContestPatch);
      if (!input) return error(4000, "invalid JSON");
      return json(
        contest(
          await rpc(
            "AdminContestService",
            "updateContest",
            {
              ...input,
              id: numericID(path[2]),
              startsAt:
                input.startsAt === undefined
                  ? undefined
                  : grpcTimestamp(input.startsAt),
              endsAt:
                input.endsAt === undefined
                  ? undefined
                  : grpcTimestamp(input.endsAt),
              scoringPolicy: input.scoringPolicy
                ? enumInput(
                    ScoringPolicy,
                    "SCORING_POLICY_",
                    input.scoringPolicy,
                  )
                : undefined,
              problems:
                input.problems === undefined
                  ? undefined
                  : { values: input.problems },
            },
            session(request),
          ),
        ),
      );
    },
    DELETE: async (request, path) => {
      await rpc(
        "AdminContestService",
        "deleteContest",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    },
  },
  {
    path: "admin/contests/:contestId/rankings",
    query: ["page", "pageSize"],
    GET: async (request, path) => {
      const x = await rpc(
        "AdminContestService",
        "listRankings",
        { contestId: numericID(path[2]), page: page(request) },
        session(request),
      );
      return json(paged(x, leaderboard));
    },
  },
  {
    path: "admin/contests/:contestId/rankings/:userId/disqualify",
    POST: qualification,
    DELETE: qualification,
  },
  {
    path: "admin/submissions",
    query: ["page", "pageSize", "query", "user", "problem", "verdict", "kind"],
    GET: async (request) => {
      const x = await rpc(
        "AdminSubmissionService",
        "listSubmissions",
        {
          page: page(request),
          query: optional(request, "query"),
          user: optional(request, "user"),
          problem: optional(request, "problem"),
          verdict: optional(request, "verdict")
            ? enumInput(Verdict, "VERDICT_", optional(request, "verdict"))
            : undefined,
          kind: optional(request, "kind")
            ? enumInput(
                SubmissionKind,
                "SUBMISSION_KIND_",
                optional(request, "kind"),
              )
            : undefined,
        },
        session(request),
      );
      return json(paged(x, submission));
    },
  },
  {
    path: "admin/submissions/:submissionId",
    GET: async (request, path) => {
      const x = await rpc(
        "AdminSubmissionService",
        "getSubmission",
        { id: numericID(path[2]) },
        session(request),
      );
      return json({
        ...submission(x.submission),
        sourceMetadata: {
          filename: x?.sourceMetadata?.filename ?? "",
          contentType: x?.sourceMetadata?.contentType ?? "",
          sizeBytes: Number(x?.sourceMetadata?.sizeBytes ?? 0),
          sha256: x?.sourceMetadata?.sha256 ?? "",
        },
        source: { href: `/api/v1/admin/submissions/${path[2]}/source` },
        log:
          x?.hasCurrentLog === true
            ? {
                href: `/api/v1/admin/submissions/${path[2]}/jobs/${x?.currentJobId ?? ""}/log`,
              }
            : null,
      });
    },
  },
  {
    path: "admin/submissions/:submissionId/source",
    GET: async (request, path) => {
      return proxyDownload(
        request,
        `/internal/v1/admin/submissions/${encodeURIComponent(path[2])}/source`,
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
      );
    },
  },
  {
    path: "admin/submissions/:submissionId/jobs",
    query: ["page", "pageSize"],
    GET: async (request, path) => {
      const x = await rpc(
        "AdminSubmissionService",
        "listSubmissionJobs",
        { submissionId: numericID(path[2]), page: page(request) },
        session(request),
      );
      return json(paged(x, job));
    },
  },
  {
    path: "admin/submissions/:submissionId/jobs/:jobId/log",
    GET: downloadJobArtifact,
  },
  {
    path: "admin/submissions/:submissionId/jobs/:jobId/result",
    GET: downloadJobArtifact,
  },
  {
    path: "admin/submissions/:submissionId/rejudge",
    POST: async (request, path) => {
      const x = await rpc(
        "AdminSubmissionService",
        "rejudgeSubmission",
        { submissionId: numericID(path[2]) },
        session(request),
      );
      return json(
        {
          submissionId: x?.submissionId ?? 0,
          status: (x?.status ?? "")
            .replace("SUBMISSION_STATUS_", "")
            .toLowerCase(),
          jobId: x?.jobId ?? 0,
          createdAt: timestamp(x?.createdAt),
        },
        201,
      );
    },
  },
  {
    path: "admin/submissions/:submissionId/cancel",
    POST: async (request, path) => {
      const x = await rpc(
        "AdminSubmissionService",
        "cancelSubmission",
        { submissionId: numericID(path[2]) },
        session(request),
      );
      return json(submission(x.submission));
    },
  },
  {
    path: "admin/users",
    query: ["page", "pageSize", "query", "status", "role"],
    GET: async (request) => {
      const x = await rpc(
        "AdminUserService",
        "listUsers",
        {
          page: page(request),
          query: optional(request, "query"),
          status: optional(request, "status")
            ? enumInput(UserStatus, "USER_STATUS_", optional(request, "status"))
            : undefined,
          role: optional(request, "role")
            ? enumInput(Role, "ROLE_", optional(request, "role"))
            : undefined,
        },
        session(request),
      );
      return json(
        paged(x, (item) => ({
          ...authUser(item),
          createdAt: timestamp(item?.createdAt),
        })),
      );
    },
    POST: async (request) => {
      const input = await body<CreateUserRequest>(request, ["username", "email"], (value) =>
        isString(value.username) && isString(value.email),
      );
      if (!input)
        return error(4000, "invalid user input");
      const x = await rpc(
        "AdminUserService",
        "createUser",
        input,
        session(request),
      );
      return json(
        { ...authUser(x.user), createdAt: timestamp(x.user?.createdAt) },
        201,
      );
    },
  },
  {
    path: "admin/users/:userId",
    GET: async (request, path) => {
      const x = await rpc(
        "AdminUserService",
        "getUser",
        { id: numericID(path[2]) },
        session(request),
      );
      return json({ ...authUser(x), createdAt: timestamp(x?.createdAt) });
    },
    PUT: async (request, path) => {
      const input = await body<{ email?: string; status?: string }>(request, ["email", "status"], (value) =>
        optionalBody(value.email, isString) && optionalBody(value.status, isString),
      );
      if (!input)
        return error(4000, "invalid user input");
      const x = await rpc(
        "AdminUserService",
        "updateUser",
        {
          id: numericID(path[2]),
          email: input.email,
          status: input.status
            ? enumInput(UserStatus, "USER_STATUS_", input.status)
            : undefined,
        },
        session(request),
      );
      return json({ ...authUser(x), createdAt: timestamp(x?.createdAt) });
    },
    DELETE: async (request, path) => {
      await rpc(
        "AdminUserService",
        "deleteUser",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    },
  },
  {
    path: "admin/users/:userId/reset-password",
    POST: async (request, path) => {
      const x = await rpc(
        "AdminUserService",
        "resetUserPassword",
        { id: numericID(path[2]) },
        session(request),
      );
      return json({ password: x.password });
    },
  },
  {
    path: "admin/system/status",
    GET: async (request) => {
      const x = await rpc(
        "AdminSystemService",
        "getStatus",
        {},
        session(request),
      );
      return json({
        activeAgents: Number(x?.activeAgents ?? 0),
        agentWorkload: workload(x.agentWorkload),
        pendingJobs: Number(x?.pendingJobs ?? 0),
        runningJobs: Number(x?.runningJobs ?? 0),
        updatedAt: timestamp(x?.updatedAt),
      });
    },
  },
  {
    path: "admin/system/agents",
    query: ["page", "pageSize", "partition", "state"],
    GET: async (request) => {
      const x = await rpc(
        "AdminSystemService",
        "listAgents",
        {
          page: page(request),
          partition: optional(request, "partition"),
          state: optional(request, "state")
            ? enumInput(AgentState, "AGENT_STATE_", optional(request, "state"))
            : undefined,
        },
        session(request),
      );
      return json(
        paged(x, agent),
      );
    },
  },
  {
    path: "admin/system/agents/:agentId/drain",
    POST: async (request, path) => {
      return json(
        agent(await rpc(
          "AdminSystemService",
          "drainAgent",
          { id: path[3] },
          session(request),
        )),
      );
    },
  },
] satisfies Route[]);

export const GET = handle;
export const POST = handle;
export const PUT = handle;
export const PATCH = handle;
export const DELETE = handle;
export const HEAD = handle;
