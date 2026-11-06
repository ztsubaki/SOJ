import type { Timestamp } from "../generated/google/protobuf/timestamp";
import type {
  AgentExecutor,
  AgentSession,
  Contest,
  Job,
  LeaderboardEntry,
  PageMeta,
  Problem,
  Submission,
  User,
} from "../generated/soj/v1/common";

export function timestamp(value?: Timestamp) {
  return value?.seconds === undefined
    ? ""
    : new Date(
        Number(value.seconds) * 1_000 + (value.nanos ?? 0) / 1_000_000,
      ).toISOString();
}
const timeOrNull = (value?: Timestamp) =>
  value ? timestamp(value) : null;

export function authUser(value: User = {}) {
  return {
    id: value.id ?? 0,
    username: value.username ?? "",
    email: value.email ?? "",
    role: value.role === "ROLE_ADMIN" ? ("admin" as const) : ("user" as const),
    status:
      value.status === "USER_STATUS_DISABLED"
        ? ("disabled" as const)
        : ("active" as const),
  };
}
export function contest(value: Contest = {}) {
  const status =
    value.status === "CONTEST_STATUS_RUNNING"
      ? ("running" as const)
      : value.status === "CONTEST_STATUS_ENDED"
        ? ("ended" as const)
        : ("upcoming" as const);
  return {
    id: value.id ?? 0,
    title: value.title ?? "",
    description: value.description ?? "",
    status,
    startsAt: timestamp(value.startsAt),
    endsAt: timestamp(value.endsAt),
    participantCount: Number(value.participantCount ?? 0),
  };
}
export function problem(value: Problem = {}, full = false) {
  const difficulty =
    value.difficulty === "DIFFICULTY_EASY"
      ? ("Easy" as const)
      : value.difficulty === "DIFFICULTY_HARD"
        ? ("Hard" as const)
        : ("Medium" as const);
  return {
    id: value.id ?? 0,
    title: value.title ?? "",
    difficulty,
    tags: value.tags ?? [],
    runnable: value.runnable ?? false,
    ...(full ? { content: value.content ?? "" } : {}),
  };
}
export function submission(value: Submission = {}) {
  return {
    id: value.id ?? 0,
    kind:
      value.kind === "SUBMISSION_KIND_ADMIN_TEST"
        ? ("admin_test" as const)
        : ("contest" as const),
    contestId: value.contestId ?? null,
    problem: { id: value.problem?.id ?? 0, title: value.problem?.title ?? "" },
    status: (value.status ?? "")
      .replace("SUBMISSION_STATUS_", "")
      .toLowerCase(),
    score: value.score ?? null,
    message: value.message ?? null,
    createdAt: timestamp(value.createdAt),
    updatedAt: timestamp(value.updatedAt),
  };
}
export function webSubmission(value: Submission = {}) {
  return {
    ...submission(value),
    kind: "contest" as const,
    contestId: value.contestId ?? 0,
  };
}
export function leaderboard(value: LeaderboardEntry = {}) {
  return {
    rank: value.rank ?? 0,
    user: { id: value.user?.id ?? 0, username: value.user?.username ?? "" },
    score: value.score ?? "",
    problemScores: (value.problemScores ?? []).map((item) => ({
      problemId: item.problemId ?? 0,
      score: item.score ?? "0",
    })),
    solved: value.solved ?? 0,
    submissions: value.submissions ?? 0,
    disqualified: value.disqualified ?? false,
    lastSubmission: timeOrNull(value.lastSubmission),
  };
}
export function paged<T, R>(
  value: { items?: T[]; page?: PageMeta },
  map: (item: T) => R,
) {
  return {
    items: (value.items ?? []).map(map),
    page: value.page?.page ?? 0,
    pageSize: value.page?.pageSize ?? 0,
    total: Number(value.page?.total ?? 0),
  };
}
export function adminProblem(value: Problem = {}) {
  const pkg = value.package;
  return {
    ...problem(value, true),
    enabled: value.enabled ?? false,
    partitions: value.partitions ?? [],
    createdAt: timestamp(value.createdAt),
    updatedAt: timestamp(value.updatedAt),
    package: pkg
      ? {
          artifactId: pkg.artifactId ?? 0,
          sha256: pkg.sha256 ?? "",
          sizeBytes: Number(pkg.sizeBytes ?? 0),
          manifest: pkg.manifest ?? {},
          uploadedAt: timestamp(pkg.uploadedAt),
        }
      : null,
  };
}
export function adminContest(value: Contest = {}) {
  return {
    ...contest(value),
    visible: value.visible ?? false,
    scoringPolicy: (value.scoringPolicy ?? "")
      .replace("SCORING_POLICY_", "")
      .toLowerCase(),
    problems: (value.problems ?? []).map((item) => problem(item)),
  };
}
export function job(value: Job = {}) {
  const snapshot = value.executionSnapshot;
  return {
    id: value.id ?? 0,
    submissionId: value.submissionId ?? 0,
    state: (value.state ?? "").replace("JOB_STATE_", "").toLowerCase(),
    agentSessionId: value.agentSessionId ?? null,
    executionId: value.executionId ?? null,
    executionSnapshot: {
      packageArtifactId: snapshot?.packageArtifactId ?? 0,
      packageSha256: snapshot?.packageSha256 ?? "",
      manifest: snapshot?.manifest ?? {},
      eligiblePartitions: snapshot?.eligiblePartitions ?? [],
      assignedPartition: snapshot?.assignedPartition ?? null,
      executor: executor(snapshot?.executor),
    },
    verdict: value.verdict
      ? value.verdict.replace("VERDICT_", "").toLowerCase()
      : null,
    score: value.score ?? null,
    statusMessage: value.statusMessage ?? null,
    result: value.result ?? null,
    log: value.log ?? null,
    createdAt: timeOrNull(value.createdAt),
    claimedAt: timeOrNull(value.claimedAt),
    submittedAt: timeOrNull(value.submittedAt),
    startedAt: timeOrNull(value.startedAt),
    finishedAt: timeOrNull(value.finishedAt),
    cancelRequestedAt: timeOrNull(value.cancelRequestedAt),
  };
}

export function workload(value?: { empty?: number; noQueue?: number; queued?: number }) {
  return { empty: Number(value?.empty ?? 0), noQueue: Number(value?.noQueue ?? 0), queued: Number(value?.queued ?? 0) };
}

function executor(value?: AgentExecutor): "slurm" | "docker" {
  switch (value) {
    case "AGENT_EXECUTOR_SLURM":
      return "slurm";
    case "AGENT_EXECUTOR_DOCKER":
      return "docker";
    default:
      throw new Error("Invalid agent executor in Control Plane response");
  }
}

export function agent(item: AgentSession = {}) {
  return {
    id: item.id ?? "",
    partition: item.partition ?? "",
    executor: executor(item.executor),
    state: (item.state ?? "").replace("AGENT_STATE_", "").toLowerCase(),
    workload: (item.workload ?? "").replace("AGENT_WORKLOAD_", "").toLowerCase(),
    activeJobCount: Number(item.activeJobCount ?? 0),
    waitingJobCount: Number(item.waitingJobCount ?? 0),
    runningJobCount: Number(item.runningJobCount ?? 0),
    createdAt: timestamp(item.createdAt),
    lastHeartbeatAt: timestamp(item.lastHeartbeatAt),
    drainRequestedAt: timeOrNull(item.drainRequestedAt),
    endedAt: timeOrNull(item.endedAt),
  };
}
