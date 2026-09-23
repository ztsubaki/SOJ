import { redirect } from "next/navigation";

export default async function ProblemDetailPage({ params }: { params: Promise<{ id: string }> }) {
  await params;
  redirect("/contests");
}
