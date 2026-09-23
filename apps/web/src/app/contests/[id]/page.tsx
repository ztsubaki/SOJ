import { notFound } from "next/navigation";
import { status } from "@grpc/grpc-js";
import { ContestDetail } from "@/components/contest-detail";
import { getContestDetail } from "@/lib/server/bff";

export default async function ContestDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  let contest;
  try {
    contest = await getContestDetail(id);
  } catch (reason) {
    if ((reason as { code?: unknown })?.code === status.NOT_FOUND) notFound();
    throw reason;
  }

  return (
    <div className="min-h-screen bg-base-200 text-base-content">
      <ContestDetail contest={contest} registrationState={contest.registrationState} />
    </div>
  );
}
