import "server-only";
import { cookies } from "next/headers";
import { rpc } from "@/lib/server/control-plane";

import {
  contest,
  problem,
  webSubmission as submission,
  leaderboard,
} from "@soj/bff/server/mapping";
import { numericID } from "@soj/bff/server/http";
export type Contest = ReturnType<typeof contest>;
export type Problem = ReturnType<typeof problem>;
export type Submission = ReturnType<typeof submission>;
export type LeaderboardEntry = ReturnType<typeof leaderboard>;
export type RegistrationState = "registered" | "unregistered" | "unavailable";
const items = <T>(value: { items?: T[] }) => value.items ?? [];
const num = (value?: number) => value ?? 0;

export async function listContests() {
  return items(
    await rpc("ContestService", "listContests", {
      page: { page: 1, pageSize: 20 },
    }),
  ).map(contest);
}
export async function getHomeData() {
  const [contests, cluster] = await Promise.all([
    listContests(),
    rpc("StatsService", "getClusterStatus", {}),
  ]);
  return {
    contests,
    cluster: {
      totalAgents: num(cluster.totalAgents),
      emptyAgents: num(cluster.emptyAgents),
      noQueueAgents: num(cluster.noQueueAgents),
      queuedAgents: num(cluster.queuedAgents),
    },
  };
}
export async function getContestDetail(id: string) {
  const contestID = numericID(id);
  const session = (await cookies()).get("soj_session")?.value;
  const baseResponse = await rpc("ContestService", "getContest", { id: contestID }, session);
  const current = contest(baseResponse.contest);
  const canReadProblems =
    current.status === "ended" ||
    (current.status === "running" && baseResponse.registered);
  const problems = await (
    canReadProblems
      ? rpc(
          "ContestService",
          "listContestProblems",
          { contestId: contestID },
          session,
        )
    : Promise.resolve({ items: [] })
  );
  return {
    ...current,
    problems: items(problems).map((item) => problem(item)),
    registrationState: (baseResponse.registered && session
      ? "registered"
      : "unregistered") as RegistrationState,
  };
}
