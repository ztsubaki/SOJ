import { uploadHeaders } from "@soj/bff/upload";
import { request } from "@soj/bff/client";
import type { Problem } from "@/lib/server/bff";
import type { Submission } from "@/lib/server/bff";

type Registration = { contestId: number; registeredAt: string };
type CreatedSubmission = {
  id: number;
  status: string;
  createdAt: string;
};
export type SubmissionDetail = CreatedSubmission & {
  problem: { id: number; title: string };
  score: string | null;
  message: string | null;
  sourceMetadata: { filename: string; contentType: string; sizeBytes: number; sha256: string };
  log: { href: string } | null;
};

export async function registerForContest(
  contestId: number,
): Promise<Registration> {
  return request(
    `/contests/${encodeURIComponent(contestId)}/registration`,
    {
      method: "POST",
    },
  );
}

export async function getContestProblem(
  contestId: number,
  problemId: number,
): Promise<Problem> {
  return request(
    `/contests/${encodeURIComponent(contestId)}/problems/${encodeURIComponent(problemId)}`,
  );
}

export async function createSubmission(
  contestId: number,
  problem: { id: number; title: string },
  file: File,
): Promise<CreatedSubmission> {
  return request(
    `/contests/${encodeURIComponent(contestId)}/problems/${encodeURIComponent(problem.id)}/submissions`,
    {
      method: "POST",
      headers: uploadHeaders(file),
      body: file,
    },
  );
}

export function getSubmission(id: number) {
  return request<SubmissionDetail>(`/submissions/${encodeURIComponent(id)}`);
}

export type Page<T> = { items: T[]; page: number; pageSize: number; total: number };
export type Participant = { user: { id: number; username: string }; registeredAt: string };

export function listParticipants(contestId: number, page: number) {
  return request<Page<Participant>>(
    `/contests/${encodeURIComponent(contestId)}/participants?page=${page}&pageSize=20`,
  );
}

export function listContestSubmissions(contestId: number, page: number) {
  return request<Page<Submission>>(
    `/submissions?contestId=${encodeURIComponent(contestId)}&page=${page}&pageSize=20`,
  );
}
