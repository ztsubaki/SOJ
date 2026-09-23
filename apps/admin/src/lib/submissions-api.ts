import { request } from "@soj/bff/client";

export type RejudgeResult = { submissionId: number; status: "pending"; jobId: number; createdAt: string };
export function rejudgeSubmission(id: number): Promise<RejudgeResult> {
  return request(`/admin/submissions/${encodeURIComponent(id)}/rejudge`, { method: "POST" });
}
export function cancelSubmission(id: number) {
  return request(`/admin/submissions/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
  });
}
