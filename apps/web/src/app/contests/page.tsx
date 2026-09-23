import { ContestList } from "@/components/contest-list";

export const dynamic = "force-dynamic";

export default async function ContestsPage() {
  return (
    <div className="min-h-screen bg-base-200 text-base-content">
      <section className="hero min-h-40 bg-base-300 md:min-h-48">
        <div className="hero-content w-full max-w-6xl justify-start px-4 text-left md:px-6">
          <div>
            <h1 className="text-3xl font-bold">比赛</h1>
            <p className="py-2 text-base-content/80">参加 HPC 比赛</p>
          </div>
        </div>
      </section>
      <ContestList />
    </div>
  );
}
