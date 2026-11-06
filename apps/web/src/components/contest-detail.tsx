"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/components/auth-provider";
import { Icon } from "@/components/icon";
import { Markdown } from "@/components/markdown";
import { ToastMessage } from "@/components/toast-provider";
import {
  createSubmission,
  getContestProblem,
  getSubmission,
  listContestSubmissions,
  listLeaderboard,
  listParticipants,
  type Page,
  type Participant,
  registerForContest,
  type SubmissionDetail,
} from "@/lib/contest-api";
import type {
  Contest,
  LeaderboardEntry,
  Problem,
  RegistrationState,
  Submission,
} from "@/lib/server/bff";
type ContestDetail = Contest & {
  problems: Problem[];
};
type ContestProblem = Problem;
type ModalTab = "description" | "submit";

const statusLabels = {
  running: "进行中",
  upcoming: "即将开始",
  ended: "已结束",
} as const;
const statusBadges = {
  running: "badge-success",
  upcoming: "badge-info",
  ended: "badge-neutral",
} as const;
const tabs = ["details", "problems", "standings", "submissions", "participants"] as const;
const tabLabels = {
  details: "详情",
  problems: "题目",
  standings: "排名",
  submissions: "提交",
  participants: "参赛者",
};

function formatStartTime(value: string) {
  return new Date(value).toLocaleString("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function getDifficultyBadge(difficulty: string) {
  if (difficulty === "Hard") return "badge-error";
  return "badge-warning";
}

export function ContestDetail({
  contest,
  registrationState,
}: {
  contest: ContestDetail;
  registrationState: RegistrationState;
}) {
  const router = useRouter();
  const { user } = useAuth();
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>("details");
  const [joined, setJoined] = useState(registrationState === "registered");
  const [selectedProblem, setSelectedProblem] = useState<ContestProblem | null>(
    null,
  );
  const [modalTab, setModalTab] = useState<ModalTab>("description");
  const [uploadedFile, setUploadedFile] = useState<File | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [isRegistering, setIsRegistering] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submissionDetail, setSubmissionDetail] = useState<SubmissionDetail | null>(null);
  const [submissions, setSubmissions] = useState<Page<Submission> | null>(null);
  const [submissionPage, setSubmissionPage] = useState(1);
  const [participants, setParticipants] = useState<Page<Participant> | null>(null);
  const [participantPage, setParticipantPage] = useState(1);
  const [leaderboardResult, setLeaderboardResult] = useState<Page<LeaderboardEntry> | null>(null);
  const [leaderboardPage, setLeaderboardPage] = useState(1);
  const [leaderboardError, setLeaderboardError] = useState("");
  const [leaderboardRevision, setLeaderboardRevision] = useState(0);
  const leaderboard = leaderboardResult?.page === leaderboardPage ? leaderboardResult : null;
  const problemDialogRef = useRef<HTMLDialogElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const status = contest.status as keyof typeof statusLabels;
  const visibleTabs =
    status === "upcoming" ? tabs.filter((tab) => tab !== "standings") : tabs;
  const canViewProblems =
    status === "ended" || (status === "running" && joined);
  const canSubmit = status === "running" && joined;
  const canRegister = status === "upcoming" || status === "running";
  const registrationUnavailable = registrationState === "unavailable";
  const leaderboardProblemIDs =
    contest.problems.length > 0
      ? contest.problems.map((problem) => problem.id)
      : (leaderboard?.items[0]?.problemScores ?? []).map(
          (item) => item.problemId,
        );

  useEffect(() => {
    if (selectedProblem) {
      problemDialogRef.current?.showModal();
    } else {
      problemDialogRef.current?.close();
    }
  }, [selectedProblem]);
  useEffect(() => {
    if (activeTab !== "submissions" || !joined) return;
    void listContestSubmissions(contest.id, submissionPage).then(setSubmissions).catch((reason) =>
      setActionError(reason instanceof Error ? reason.message : "读取提交记录失败"),
    );
  }, [activeTab, contest.id, joined, submissionPage]);
  useEffect(() => {
    if (activeTab !== "participants" || !joined) return;
    void listParticipants(contest.id, participantPage).then(setParticipants).catch((reason) =>
      setActionError(reason instanceof Error ? reason.message : "读取参赛者失败"),
    );
  }, [activeTab, contest.id, joined, participantPage]);

  useEffect(() => {
    if (activeTab !== "standings" || status === "upcoming") return;
    const controller = new AbortController();
    let active = true;
    void listLeaderboard(contest.id, leaderboardPage, controller.signal)
      .then((result) => {
        if (!active) return;
        setLeaderboardResult(result);
        setLeaderboardError("");
      })
      .catch((reason) => {
        if (active) setLeaderboardError(reason instanceof Error ? reason.message : "读取排行榜失败");
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [activeTab, contest.id, leaderboardPage, leaderboardRevision, status]);

  async function openProblem(problem: ContestProblem) {
    setActionError(null);
    try {
      const detail = await getContestProblem(contest.id, problem.id);
      setSelectedProblem(detail);
      setModalTab("description");
      setUploadedFile(null);
    } catch (reason) {
      setActionError(reason instanceof Error ? reason.message : "读取题目失败");
    }
  }

  function handleFileChange(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (file) setUploadedFile(file);
  }

  function handleFileDrop(event: React.DragEvent<HTMLButtonElement>) {
    event.preventDefault();
    const file = event.dataTransfer.files[0];
    if (file) setUploadedFile(file);
  }

  function clearFile() {
    setUploadedFile(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  }

  async function handleRegistration() {
    if (!user) {
      router.push("/login");
      return;
    }
    setIsRegistering(true);
    setActionError(null);
    try {
      await registerForContest(contest.id);
      setJoined(true);
      setActionMessage("报名成功");
      router.refresh();
    } catch (reason) {
      setActionError(reason instanceof Error ? reason.message : "报名失败");
    } finally {
      setIsRegistering(false);
    }
  }

  async function handleSubmission() {
    if (!user) {
      router.push("/login");
      return;
    }
    if (!selectedProblem || !uploadedFile) return;
    setIsSubmitting(true);
    setActionError(null);
    try {
      const created = await createSubmission(
        contest.id,
        selectedProblem,
        uploadedFile,
      );
      setActionMessage(`提交 ${created.id} 已进入队列`);
      clearFile();
      setModalTab("description");
      router.refresh();
    } catch (reason) {
      setActionError(reason instanceof Error ? reason.message : "提交失败");
    } finally {
      setIsSubmitting(false);
    }
  }
  async function openSubmission(id: number) {
    setActionError(null);
    try {
      setSubmissionDetail(await getSubmission(id));
    } catch (reason) {
      setActionError(reason instanceof Error ? reason.message : "读取提交详情失败");
    }
  }

  return (
    <>
      <section className="hero min-h-48 bg-base-300 py-8 md:min-h-56">
        <div className="hero-content w-full max-w-6xl justify-start px-4 text-left md:px-6">
          <div className="w-full">
            <div className="mb-3 flex flex-wrap items-center gap-2">
              <span className={`badge badge-md px-3 ${statusBadges[status]}`}>
                {statusLabels[status]}
              </span>
              {canRegister && (
                <button
                  type="button"
                  disabled={joined || isRegistering || registrationUnavailable}
                  className={`btn btn-sm cursor-pointer ${joined ? "btn-success" : "btn-primary"}`}
                  onClick={() => void handleRegistration()}
                >
                  {joined
                    ? "已报名"
                    : registrationUnavailable
                      ? "报名状态不可用"
                      : isRegistering
                        ? "报名中..."
                        : "报名比赛"}
                </button>
              )}
            </div>
            <h1 className="text-3xl font-bold sm:text-4xl">{contest.title}</h1>
            <div className="stats stats-vertical mt-6 w-full rounded-box bg-base-100/60 sm:stats-horizontal">
              <div className="stat">
                <div className="stat-title">开始时间</div>
                <div className="stat-value text-lg">
                  {formatStartTime(contest.startsAt)}
                </div>
              </div>
              <div className="stat">
                <div className="stat-title">结束时间</div>
                <div className="stat-value text-lg">
                  {formatStartTime(contest.endsAt)}
                </div>
              </div>
              <div className="stat">
                <div className="stat-title">题目数量</div>
                <div className="stat-value text-lg">
                  {canViewProblems ? contest.problems.length : "-"}
                </div>
              </div>
              <div className="stat">
                <div className="stat-title">参赛人数</div>
                <div className="stat-value text-lg">
                  {contest.participantCount}
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>

      <div className="mx-auto max-w-6xl px-4 py-8 md:px-6">
        <ToastMessage message={actionError} />
        <ToastMessage
          message={
            registrationUnavailable
              ? "无法确认报名状态，报名功能暂不可用，请稍后重试。"
              : null
          }
        />
        <ToastMessage message={actionMessage} tone="success" />
        <div
          role="group"
          aria-label="比赛内容"
          className="tabs tabs-boxed rounded-box mb-6 bg-base-100 p-2 shadow-md"
        >
          {visibleTabs.map((tab) => (
            <button
              key={tab}
              type="button"
              aria-pressed={activeTab === tab}
              className={`tab cursor-pointer ${activeTab === tab ? "tab-active" : ""}`}
              onClick={() => setActiveTab(tab)}
            >
              {tabLabels[tab]}
            </button>
          ))}
        </div>

        {activeTab === "details" && (
          <section className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body p-5 sm:p-6">
              <h2 className="card-title">比赛详情</h2>
              <Markdown className="max-w-none text-base text-base-content/80">
                {contest.description}
              </Markdown>
            </div>
          </section>
        )}

        {activeTab === "problems" && (
          <section className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body p-5 sm:p-6">
              <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
                <h2 className="card-title">题目列表</h2>
              </div>
              {!canViewProblems ? (
                <p className="rounded-box bg-base-200 p-5 text-base leading-7 text-base-content/70">
                  {status === "upcoming"
                    ? "赛题将在比赛开始后开放。"
                    : "报名用户可在比赛进行期间查看赛题。"}
                </p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="table table-md table-zebra">
                    <thead>
                      <tr>
                        <th>题号</th>
                        <th>标题</th>
                        <th>难度</th>
                      </tr>
                    </thead>
                    <tbody>
                      {contest.problems.map((problem, index) => (
                        <tr className="hover" key={problem.id}>
                          <td className="font-mono font-bold tabular-nums">{index + 1}</td>
                          <td>
                            <button
                              type="button"
                              className="font-medium hover:text-primary focus-visible:outline-2 focus-visible:outline-primary"
                              onClick={() => void openProblem(problem)}
                            >
                              {problem.title}
                            </button>
                          </td>
                          <td>
                            <span
                              className={`badge badge-md px-3 ${getDifficultyBadge(problem.difficulty)}`}
                            >
                              {problem.difficulty}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </section>
        )}

        {status !== "upcoming" && activeTab === "standings" && (
          <section className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body p-5 sm:p-6">
              <h2 className="card-title mb-3">排行榜</h2>
              {leaderboardError && (
                <div className="alert alert-error sm:alert-horizontal" role="alert">
                  <span>{leaderboardError}</span>
                  <button type="button" className="btn btn-sm" onClick={() => {
                    setLeaderboardError("");
                    setLeaderboardRevision((revision) => revision + 1);
                  }}>重试</button>
                </div>
              )}
              {!leaderboard && !leaderboardError && (
                <span className="loading loading-spinner loading-sm" aria-label="正在读取排行榜" />
              )}
              <div className="overflow-x-auto">
                <table className="table table-md table-zebra">
                  <thead>
                    <tr>
                      <th scope="col">排名</th>
                      <th scope="col">用户</th>
                      {leaderboardProblemIDs.map((problemID, index) => (
                        <th className="text-right" scope="col" key={problemID}>
                          题目 {index + 1}
                        </th>
                      ))}
                      <th className="text-right" scope="col">总分</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(leaderboard?.items ?? []).map((row) => {
                      const scores = new Map(
                        row.problemScores.map((item) => [item.problemId, item.score]),
                      );
                      const isCurrentUser = row.user.id === user?.id;
                      return (
                        <tr key={row.user.id}>
                          <td className="font-mono tabular-nums">{row.rank}</td>
                          <td className={isCurrentUser ? "text-primary" : undefined}>
                            {row.user.username}
                          </td>
                          {leaderboardProblemIDs.map((problemID, index) => {
                            const score = scores.get(problemID) ?? "0";
                            return (
                              <td className="text-right" key={problemID}>
                                <span
                                  aria-label={`题目 ${index + 1} 得分 ${score}`}
                                  className={`font-mono font-medium tabular-nums ${Number(score) === 0 ? "text-base-content/50" : ""}`}
                                >
                                  {score}
                                </span>
                              </td>
                            );
                          })}
                          <td className="text-right font-mono font-semibold tabular-nums">{row.score}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
              {leaderboardResult && (
                <Pagination
                  page={leaderboardPage}
                  total={leaderboardResult.total}
                  onPageChange={(page) => {
                    setLeaderboardError("");
                    setLeaderboardPage(page);
                  }}
                />
              )}
            </div>
          </section>
        )}

        {activeTab === "submissions" && (
          <section className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body p-5 sm:p-6">
              <h2 className="card-title mb-3">提交记录</h2>
              {!joined ? (
                <p className="text-base-content/70">报名后可查看自己的提交记录。</p>
              ) : !submissions ? (
                <span className="loading loading-spinner loading-sm" aria-label="正在读取提交记录" />
              ) : submissions.items.length === 0 ? (
                <p className="text-base-content/70">暂无提交记录。</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="table table-md table-zebra">
                    <thead>
                      <tr>
                        <th>提交 ID</th>
                        <th>时间</th>
                        <th>题目</th>
                        <th>结果</th>
                        <th>得分</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {submissions.items.map((submission) => (
                        <tr key={submission.id}>
                          <td className="font-mono">{submission.id}</td>
                          <td>
                            {new Date(submission.createdAt).toLocaleString(
                              "zh-CN",
                            )}
                          </td>
                          <td className="font-mono">
                            {submission.problem.title}
                          </td>
                          <td>
                            <span
                              className={`badge badge-md px-3 ${submission.status === "accepted" ? "badge-success" : "badge-error"}`}
                            >
                              {submission.status}
                            </span>
                          </td>
                          <td>{submission.score ?? "-"}</td>
                          <td><button type="button" className="btn btn-link btn-sm" onClick={() => void openSubmission(submission.id)}>详情</button></td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {submissions && (
                <Pagination page={submissionPage} total={submissions.total} onPageChange={setSubmissionPage} />
              )}
              {submissionDetail && (
                <section className="mt-4 rounded-box border border-info/30 bg-base-200 p-4 sm:p-5">
                  <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0 space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-base font-semibold">
                          提交 #{submissionDetail.id}
                        </h3>
                        <span className="badge badge-md badge-info">
                          {submissionDetail.status}
                        </span>
                      </div>
                      <p className="text-base leading-7 text-base-content/80">
                        {submissionDetail.message ?? "暂无评测消息"}
                      </p>
                      <p className="text-sm leading-6 text-base-content/70">
                        {submissionDetail.sourceMetadata.filename} · {submissionDetail.sourceMetadata.sizeBytes} 字节
                      </p>
                      {submissionDetail.log && (
                        <a
                          className="link link-primary text-sm"
                          href={submissionDetail.log.href}
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          查看当前评测日志
                        </a>
                      )}
                    </div>
                    <button
                      type="button"
                      className="btn btn-sm btn-outline btn-info shrink-0"
                      onClick={() => setSubmissionDetail(null)}
                    >
                      关闭
                    </button>
                  </div>
                </section>
              )}
            </div>
          </section>
        )}

        {activeTab === "participants" && (
          <section className="card rounded-box bg-base-100 shadow-md">
            <div className="card-body p-5 sm:p-6">
              <h2 className="card-title mb-3">参赛者</h2>
              {!joined ? <p className="text-base-content/70">报名后可查看参赛者名单。</p> : !participants ? (
                <span className="loading loading-spinner loading-sm" aria-label="正在读取参赛者" />
              ) : <>
                <div className="overflow-x-auto"><table className="table table-md table-zebra"><thead><tr><th>用户</th><th>报名时间</th></tr></thead><tbody>{participants.items.map((participant) => <tr key={participant.user.id}><td>{participant.user.username}</td><td>{new Date(participant.registeredAt).toLocaleString("zh-CN")}</td></tr>)}</tbody></table></div>
                <Pagination page={participantPage} total={participants.total} onPageChange={setParticipantPage} />
              </>}
            </div>
          </section>
        )}
      </div>

      <dialog
        ref={problemDialogRef}
        className="modal"
        onClose={() => setSelectedProblem(null)}
      >
        {selectedProblem && (
          <div className="modal-box flex w-11/12 max-w-5xl max-h-[90vh] flex-col overflow-hidden p-0">
            {/* Header */}
            <div className="shrink-0 border-b border-base-content/10 px-5 py-5 sm:px-6">
              <div className="flex items-start justify-between gap-4">
                <div>
                  <h2 className="text-2xl font-bold">
                    {Math.max(1, contest.problems.findIndex((problem) => problem.id === selectedProblem.id) + 1)}. {selectedProblem.title}
                  </h2>
                  <div className="mt-2 flex flex-wrap gap-2">
                    <span
                      className={`badge badge-md px-3 ${getDifficultyBadge(selectedProblem.difficulty)}`}
                    >
                      {selectedProblem.difficulty}
                    </span>
                  </div>
                </div>
                <form method="dialog">
                  <button
                    type="submit"
                    className="btn btn-circle btn-ghost btn-sm"
                    aria-label="关闭"
                  >
                    ✕
                  </button>
                </form>
              </div>
            </div>

            <div
              role="group"
              aria-label="题目操作"
              className="tabs tabs-boxed shrink-0 rounded-none bg-base-200 px-6 py-3"
            >
              <button
                type="button"
                aria-pressed={modalTab === "description"}
                className={`tab ${modalTab === "description" ? "tab-active" : ""}`}
                onClick={() => setModalTab("description")}
              >
                题目描述
              </button>
              <button
                type="button"
                aria-pressed={modalTab === "submit"}
                className={`tab ${modalTab === "submit" ? "tab-active" : ""}`}
                disabled={!canSubmit}
                onClick={() => setModalTab("submit")}
              >
                提交
              </button>
            </div>

            {/* Modal Content */}
            <div className="flex-1 overflow-y-auto px-5 py-5 sm:px-6">
              {modalTab === "description" && (
                <div className="space-y-4">
                  <section className="card bg-base-100 shadow-sm">
                    <div className="card-body p-5 sm:p-6">
                      <h3 className="mb-2 text-lg font-bold">题目描述</h3>
                      <Markdown className="max-w-none text-base leading-7">
                        {selectedProblem.content ?? ""}
                      </Markdown>
                    </div>
                  </section>

                  {selectedProblem.tags && selectedProblem.tags.length > 0 && (
                    <section className="card bg-base-100 shadow-sm">
                      <div className="card-body p-5 sm:p-6">
                        <h3 className="mb-2 text-lg font-bold">标签</h3>
                        <div className="flex flex-wrap gap-2">
                          {selectedProblem.tags.map((tag) => (
                            <span
                              className="badge badge-md px-3 badge-outline badge-primary"
                              key={tag}
                            >
                              {tag}
                            </span>
                          ))}
                        </div>
                      </div>
                    </section>
                  )}
                </div>
              )}

              {modalTab === "submit" && (
                <div className="space-y-4">
                  {!selectedProblem.runnable ? (
                    <div className="alert alert-warning" role="alert">
                      <span>本题尚未配置可用测评包或候选分区，暂时不能提交。请联系管理员完成题目配置。</span>
                    </div>
                  ) : (
                    <>
                      <input
                        ref={fileInputRef}
                        id="submission-file"
                        type="file"
                        className="hidden"
                        onChange={handleFileChange}
                      />
                      <button
                        type="button"
                        className="w-full cursor-pointer rounded-box border-2 border-dashed border-base-300 p-8 text-center transition-colors hover:bg-base-200 focus-visible:outline-2 focus-visible:outline-primary"
                        onClick={() => fileInputRef.current?.click()}
                        aria-label="上传代码文件"
                        onDragOver={(event) => event.preventDefault()}
                        onDrop={handleFileDrop}
                      >
                        <Icon
                          name="upload"
                          className="mx-auto size-12 text-base-content/40"
                        />
                        <span className="mt-2 block text-lg font-medium">
                          点击上传文件
                        </span>
                        <span className="block text-sm leading-6 text-base-content/60">
                          或将代码文件拖放到此处
                        </span>
                      </button>

                      {uploadedFile && (
                        <div className="alert alert-success">
                          <div>
                            <span className="font-medium">{uploadedFile.name}</span>
                            <span className="ml-2 text-sm text-base-content/70">
                              ({(uploadedFile.size / 1024).toFixed(2)} KB)
                            </span>
                          </div>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            onClick={clearFile}
                            aria-label="移除文件"
                          >
                            <Icon name="x" className="size-4" />
                          </button>
                        </div>
                      )}

                      <div className="flex gap-2">
                        <button
                          type="button"
                          className="btn btn-primary"
                          disabled={!uploadedFile || isSubmitting}
                          onClick={() => void handleSubmission()}
                        >
                          {isSubmitting ? "提交中..." : "提交"}
                        </button>
                        <button
                          type="button"
                          className="btn btn-outline"
                          onClick={clearFile}
                        >
                          清空
                        </button>
                      </div>
                    </>
                  )}
                </div>
              )}
            </div>
          </div>
        )}
        <form method="dialog" className="modal-backdrop">
          <button type="submit">关闭</button>
        </form>
      </dialog>
    </>
  );
}

function Pagination({ page, total, onPageChange }: { page: number; total: number; onPageChange: (page: number) => void }) {
  const lastPage = Math.max(1, Math.ceil(total / 20));
  if (lastPage === 1) return null;
  return (
    <div className="join mt-4" aria-label="分页">
      <button type="button" className="btn join-item btn-sm" disabled={page === 1} onClick={() => onPageChange(page - 1)}>上一页</button>
      <span className="btn join-item btn-sm" aria-current="page">{page} / {lastPage}</span>
      <button type="button" className="btn join-item btn-sm" disabled={page === lastPage} onClick={() => onPageChange(page + 1)}>下一页</button>
    </div>
  );
}
