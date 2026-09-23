import Link from "next/link";
import { contestSummary } from "@/lib/contest-summary";
import { getHomeData } from "@/lib/server/bff";

export const dynamic = "force-dynamic";

const toneClasses = {
  success: "text-success",
  warning: "text-warning",
  info: "text-info",
};
const contestStatusLabels: Record<string, string> = {
  running: "进行中",
  upcoming: "即将开始",
  ended: "已结束",
};

export default async function Home() {
  const { contests, cluster } = await getHomeData();
  const featuredContests = contests.filter(
    (contest) => contest.status !== "ended",
  );
  const taskMetrics = [
    {
      value: String(cluster.totalAgents),
      label: "有效 Agent",
      description: "在线或排空中的会话",
      tone: "info",
    },
    {
      value: String(cluster.emptyAgents),
      label: "空闲 Agent",
      description: "没有活动 Job",
      tone: "success",
    },
    {
      value: String(cluster.queuedAgents),
      label: "有排队任务",
      description: "拥有已领取或已提交 Job",
      tone: "warning",
    },
  ];

  return (
    <div className="min-h-screen bg-base-200 text-base-content">
      <section className="hero min-h-48 bg-base-300 md:min-h-64">
        <div className="hero-content w-full max-w-7xl justify-start px-4 text-left md:px-6 lg:px-8">
          <div>
            <h1 className="text-4xl font-bold sm:text-5xl">SOJ</h1>
            <p className="mt-4 text-base-content/80">
              面向 HPC 应用的在线测评平台
            </p>
          </div>
        </div>
      </section>
      <div className="mx-auto max-w-7xl px-4 pb-10 md:px-6 lg:px-8">
        <div
          id="metrics"
          className="stats stats-vertical mt-4 w-full bg-base-100 shadow-sm sm:stats-horizontal"
        >
          {taskMetrics.map(({ value, label, description, tone }) => (
            <div className="stat" key={label}>
              <div className="stat-title">{label}</div>
              <div
                className={`stat-value ${toneClasses[tone as keyof typeof toneClasses]}`}
              >
                {value}
              </div>
              <div className="stat-desc">{description}</div>
            </div>
          ))}
        </div>
        <div className="mt-6">
          <section className="card bg-base-100 shadow-sm">
            <div className="card-body p-5 sm:p-6">
              <div className="mb-3 flex items-center justify-between">
                <h2 className="card-title text-lg">进行中的比赛</h2>
                <Link
                  href="/contests"
                  className="text-base text-base-content/70 transition-colors hover:text-primary"
                >
                  全部比赛 →
                </Link>
              </div>
              <ul className="list">
                {featuredContests.map((contest, index) => (
                  <li
                    key={contest.id}
                    className="list-row rounded-box transition-colors hover:bg-base-200"
                  >
                    <div className="text-2xl font-thin tabular-nums text-base-content/60">
                      {String(index + 1).padStart(2, "0")}
                    </div>
                    <div className="list-col-grow">
                      <Link
                        className="font-medium hover:text-primary focus-visible:outline-2 focus-visible:outline-primary"
                        href={`/contests/${contest.id}`}
                      >
                        {contest.title}
                      </Link>
                      {contestSummary(contest.description) && (
                        <p className="mt-1 line-clamp-2 text-base leading-7 text-base-content/70">
                          {contestSummary(contest.description)}
                        </p>
                      )}
                    </div>
                    <div className="text-right">
                      <span
                        className={`badge badge-md px-3 ${contest.status === "running" ? "badge-success" : "badge-info"}`}
                      >
                        {contestStatusLabels[contest.status]}
                      </span>
                      <p className="mt-1 text-sm text-base-content/70">
                        {contest.participantCount} 位选手
                      </p>
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
