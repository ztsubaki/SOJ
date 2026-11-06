export type SubmissionStatus =
  | "pending"
  | "judging"
  | "accepted"
  | "wrong_answer"
  | "compile_error"
  | "runtime_error"
  | "time_limit"
  | "memory_limit"
  | "cancelled"
  | "system_error";
export type SubmissionDetail = {
  id: number;
  kind: string;
  contestId: number | null;
  problem: { id: number; title: string };
  status: SubmissionStatus;
  score: string | null;
  message: string | null;
  createdAt: string;
  updatedAt: string;
  sourceMetadata?: {
    filename: string;
    contentType: string;
    sizeBytes: number;
    sha256: string;
  };
  source?: { href: string };
  log?: { href: string } | null;
};
export type ProblemDetail = {
  id: number;
  title: string;
  difficulty: "Easy" | "Medium" | "Hard";
  tags: string[];
  content: string;
  enabled: boolean;
  partitions: string[];
  createdAt: string;
  updatedAt: string;
  package: {
    artifactId: number;
    sha256: string;
    sizeBytes: number;
    manifest: Record<string, unknown>;
    uploadedAt: string;
  } | null;
};
export type ProblemForm = Pick<
  ProblemDetail,
  "title" | "difficulty" | "tags" | "partitions" | "enabled" | "content"
>;
export type ContestDetail = {
  id: number;
  title: string;
  description: string;
  status: "running" | "upcoming" | "ended";
  startsAt: string;
  endsAt: string;
  participantCount: number;
  visible: boolean;
  scoringPolicy: "best" | "latest";
  problems: Array<{
    id: number;
    title: string;
    difficulty: "Easy" | "Medium" | "Hard";
    tags: string[];
  }>;
};
export type ContestForm = Pick<
  ContestDetail,
  | "title"
  | "startsAt"
  | "endsAt"
  | "description"
  | "visible"
  | "scoringPolicy"
> & { problems: number[] };
export type RankItem = {
  rank: number;
  user: { id: number; username: string };
  score: string;
  solved: number;
  submissions: number;
  disqualified: boolean;
  lastSubmission: string | null;
};
export type UserDetail = {
	id: number;
  username: string;
  email: string;
  role: "admin" | "user";
  status: "active" | "disabled";
  createdAt?: string;
};
export type AgentExecutor = "slurm" | "docker";
export type AgentSession = {
  id: string;
  partition: string;
  executor: AgentExecutor;
  state: "online" | "draining" | "ended" | "offline";
  workload: "empty" | "no_queue" | "queued";
  waitingJobCount: number;
  runningJobCount: number;
  activeJobCount: number;
  lastHeartbeatAt: string;
};
export type SubmissionJob = {
  id: number;
  state: string;
  agentSessionId: string | null;
  executionId: string | null;
  executionSnapshot: {
    packageArtifactId: number;
    packageSha256: string;
    manifest: Record<string, unknown>;
    eligiblePartitions: string[];
    assignedPartition: string | null;
    executor: AgentExecutor;
  };
  verdict: string | null;
  score: string | null;
  statusMessage: string | null;
  result: { href: string } | null;
  log: { href: string } | null;
  createdAt: string | null;
  finishedAt: string | null;
  cancelRequestedAt: string | null;
};
