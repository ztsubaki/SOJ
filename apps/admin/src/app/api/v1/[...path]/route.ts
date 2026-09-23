import type { ProblemInput, ProblemPatch } from "@soj/bff/generated/soj/v1/problems";
import type { ContestInput, CreateUserRequest } from "@soj/bff/generated/soj/v1/admin";
type ProblemBody = Omit<ProblemInput, "difficulty" | "tags"> & {
  difficulty?: string;
  tags?: string[] | null;
};
type ContestBody = Omit<
  ContestInput,
  "startsAt" | "endsAt" | "scoringPolicy"
> & { startsAt?: string; endsAt?: string; scoringPolicy?: string };
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

export const runtime = "nodejs";
type Params = { params: Promise<{ path: string[] }> };
import { authRoute } from "@soj/bff/server/auth";
import {
  session,
  json,
  error,
  grpcError,
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
  sameOrigin,
  optional as optionalBody,
  pagination,
  query,
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
const params = (request: NextRequest, fields: string[] = []) =>
  query(request, ["page", "pageSize", ...fields]);
const page = (request: NextRequest, fields: string[] = []) => pagination(params(request, fields));
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

// Keep route existence and its accepted query parameters together. This is
// also the source for the 404/405 distinction at the end of handle.
function adminRoute(path: string[]) {
  const key = path.join("/");
  const collections: Record<string, string[]> = {
    "admin/stats": [],
    "admin/problems": ["page", "pageSize", "query", "enabled"],
    "admin/contests": ["page", "pageSize", "query", "status"],
    "admin/submissions": ["page", "pageSize", "query", "user", "problem", "verdict", "kind"],
    "admin/users": ["page", "pageSize", "query", "status", "role"],
    "admin/system/status": [],
    "admin/system/agents": ["page", "pageSize", "partition", "state"],
  };
  if (key in collections) return collections[key];
  if (path[0] !== "admin") return undefined;
  if (path[1] === "problems" && (path.length === 3 || (path.length === 4 && ["package", "test-submissions"].includes(path[3])))) return [];
  if (path[1] === "contests" && (path.length === 3 || (path.length === 4 && path[3] === "rankings") || (path.length === 6 && path[3] === "rankings" && path[5] === "disqualify"))) return path[3] === "rankings" ? ["page", "pageSize"] : [];
  if (path[1] === "submissions" && (path.length === 3 || (path.length === 4 && ["source", "rejudge", "cancel", "jobs"].includes(path[3])) || (path.length === 6 && path[3] === "jobs" && ["log", "result"].includes(path[5])))) return path[3] === "jobs" ? ["page", "pageSize"] : [];
  if (path[1] === "users" && path.length === 3) return [];
  if (path[1] === "system" && path.length === 5 && path[2] === "agents" && path[4] === "drain") return [];
  return undefined;
}

async function handle(request: NextRequest, path: string[]) {
  const key = path.join("/");
  const method = request.method === "HEAD" ? "GET" : request.method;
  const route = adminRoute(path);
  if (
    method !== "GET" &&
    key !== "auth/logout" &&
    !sameOrigin(request)
  )
    return error(4030, "origin mismatch");
  try {
    const authResponse = await authRoute(request, key, rpc, false);
    if (authResponse) return authResponse;
    if (method === "GET") query(request, route ?? []);
    if (key === "admin/stats" && method === "GET") {
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
    }
    if (key === "admin/problems" && method === "GET") {
      const x = await rpc(
        "AdminProblemService",
        "listProblems",
        {
          page: page(request, ["query", "enabled"]),
          query: optional(request, "query"),
          enabled: booleanInput(optional(request, "enabled")),
        },
        session(request),
      );
      return json(paged(x, problem));
    }
    if (key === "admin/problems" && method === "POST") {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "problems" &&
      path.length === 3 &&
      method === "GET"
    )
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
    if (
      path[0] === "admin" &&
      path[1] === "problems" &&
      path.length === 3 &&
      method === "PUT"
    ) {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "problems" &&
      path.length === 3 &&
      method === "DELETE"
    ) {
      await rpc(
        "AdminProblemService",
        "deleteProblem",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    }
    if (
      path[0] === "admin" &&
      path[1] === "problems" &&
      path[3] === "package" &&
      path.length === 4 &&
      method === "POST"
    )
      return proxyUpload(
        request,
        `/internal/v1/problems/${encodeURIComponent(path[2])}/package`,
        "PUT",
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
        megabytes(process.env.MAX_JUDGE_PROGRAM_SIZE_MB, 10 * 1024 ** 3),
      );
    if (
      path[0] === "admin" &&
      path[1] === "problems" &&
      path[3] === "test-submissions" &&
      path.length === 4 &&
      method === "POST"
    )
      return proxyUpload(
        request,
        `/internal/v1/problems/${encodeURIComponent(path[2])}/test-submissions`,
        "POST",
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
        megabytes(process.env.MAX_SOURCE_SIZE_MB, 10 << 20),
      );
    if (key === "admin/contests" && method === "GET") {
      const status = optional(request, "status");
      const x = await rpc(
        "AdminContestService",
        "listContests",
        {
          page: page(request, ["query", "status"]),
          query: optional(request, "query"),
          status: status
            ? enumInput(ContestStatus, "CONTEST_STATUS_", status)
            : undefined,
        },
        session(request),
      );
      return json(paged(x, contest));
    }
    if (key === "admin/contests" && method === "POST") {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "contests" &&
      path.length === 3 &&
      method === "GET"
    )
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
    if (
      path[0] === "admin" &&
      path[1] === "contests" &&
      path.length === 3 &&
      method === "PUT"
    ) {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "contests" &&
      path.length === 3 &&
      method === "DELETE"
    ) {
      await rpc(
        "AdminContestService",
        "deleteContest",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    }
    if (
      path[0] === "admin" &&
      path[1] === "contests" &&
      path[3] === "rankings" &&
      path.length === 4 &&
      method === "GET"
    ) {
      const x = await rpc(
        "AdminContestService",
        "listRankings",
        { contestId: numericID(path[2]), page: page(request) },
        session(request),
      );
      return json(paged(x, leaderboard));
    }
    if (
      path[0] === "admin" &&
      path[1] === "contests" &&
      path[3] === "rankings" &&
      path.length === 6 &&
      (method === "POST" || method === "DELETE") &&
      path[5] === "disqualify"
    ) {
      await rpc(
        "AdminContestService",
        method === "POST"
          ? "disqualifyParticipant"
          : "restoreParticipant",
        { contestId: numericID(path[2]), userId: numericID(path[4]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    }
    if (key === "admin/submissions" && method === "GET") {
      const x = await rpc(
        "AdminSubmissionService",
        "listSubmissions",
        {
          page: page(request, ["query", "user", "problem", "verdict", "kind"]),
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path.length === 3 &&
      method === "GET"
    ) {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path[3] === "source" &&
      path.length === 4 &&
      method === "GET"
    )
      return proxyDownload(
        request,
        `/internal/v1/admin/submissions/${encodeURIComponent(path[2])}/source`,
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
      );
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path[3] === "jobs" &&
      path.length === 4 &&
      method === "GET"
    ) {
      const x = await rpc(
        "AdminSubmissionService",
        "listSubmissionJobs",
        { submissionId: numericID(path[2]), page: page(request) },
        session(request),
      );
      return json(paged(x, job));
    }
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path[3] === "jobs" &&
      path.length === 6 &&
      method === "GET" &&
      (path[5] === "log" || path[5] === "result")
    )
      return proxyDownload(
        request,
        `/internal/v1/admin/submissions/${encodeURIComponent(path[2])}/jobs/${encodeURIComponent(path[4])}/${path[5]}`,
        process.env.ADMIN_BFF_SERVICE_TOKEN ?? "",
        path[5] === "result"
          ? { contentType: "application/json; charset=utf-8", inline: true }
          : { contentType: "text/plain; charset=utf-8", inline: true },
      );
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path[3] === "rejudge" &&
      path.length === 4 &&
      method === "POST"
    ) {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "submissions" &&
      path[3] === "cancel" &&
      path.length === 4 &&
      method === "POST"
    ) {
      const x = await rpc(
        "AdminSubmissionService",
        "cancelSubmission",
        { submissionId: numericID(path[2]) },
        session(request),
      );
      return json(submission(x.submission));
    }
    if (key === "admin/users" && method === "GET") {
      const x = await rpc(
        "AdminUserService",
        "listUsers",
        {
          page: page(request, ["query", "status", "role"]),
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
    }
    if (key === "admin/users" && method === "POST") {
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "users" &&
      path.length === 3 &&
      method === "GET"
    ) {
      const x = await rpc(
        "AdminUserService",
        "getUser",
        { id: numericID(path[2]) },
        session(request),
      );
      return json({ ...authUser(x), createdAt: timestamp(x?.createdAt) });
    }
    if (
      path[0] === "admin" &&
      path[1] === "users" &&
      path.length === 3 &&
      method === "DELETE"
    ) {
      await rpc(
        "AdminUserService",
        "deleteUser",
        { id: numericID(path[2]) },
        session(request),
      );
      return new NextResponse(null, { status: 204 });
    }
    if (
      path[0] === "admin" &&
      path[1] === "users" &&
      path[3] === "reset-password" &&
      path.length === 4 &&
      method === "POST"
    ) {
      const x = await rpc(
        "AdminUserService",
        "resetUserPassword",
        { id: numericID(path[2]) },
        session(request),
      );
      return json({ password: x.password });
    }
    if (
      path[0] === "admin" &&
      path[1] === "users" &&
      path.length === 3 &&
      method === "PUT"
    ) {
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
    }
    if (key === "admin/system/status" && method === "GET") {
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
    }
    if (key === "admin/system/agents" && method === "GET") {
      const x = await rpc(
        "AdminSystemService",
        "listAgents",
        {
          page: page(request, ["partition", "state"]),
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
    }
    if (
      path[0] === "admin" &&
      path[1] === "system" &&
      path[2] === "agents" &&
      path[4] === "drain" &&
      path.length === 5 &&
      method === "POST"
    )
      return json(
        agent(await rpc(
          "AdminSystemService",
          "drainAgent",
          { id: path[3] },
          session(request),
        )),
      );
    const known = route !== undefined || key.startsWith("auth/");
    return error(known ? 4050 : 4040, known ? "method not allowed" : "resource not found");
  } catch (reason) {
    console.error(
      "Admin BFF request failed",
      reason instanceof Error ? reason.message : "unknown error",
    );
    return grpcError(reason);
  }
}
export async function GET(request: NextRequest, context: Params) {
  const response = await handle(request, (await context.params).path);
  response.headers.set("Cache-Control", "no-store");
  return response;
}
export const POST = GET;
export const PUT = GET;
export const DELETE = GET;

export const HEAD = GET;
