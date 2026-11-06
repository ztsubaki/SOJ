"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import Select from "@/components/form/Select";
import Switch from "@/components/form/switch/Switch";
import MarkdownEditor from "@/components/form/MarkdownEditor";
import ProblemMultiSelect from "@/components/form/ProblemMultiSelect";
import type { ContestDetail, ContestForm } from "@/lib/admin-types";
import { fromLocalDateTime, toLocalDateTime } from "@/lib/local-datetime";
import { ToastMessage } from "@/context/ToastContext";
const policies = [
  { value: "best", label: "每题最高分" },
  { value: "latest", label: "每题最新提交" },
];
export default function EditContestPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [form, setForm] = useState<ContestForm | null>(null);
  const [loadedId, setLoadedId] = useState<string | null>(null);
  const [initialProblems, setInitialProblems] = useState<ContestDetail["problems"]>([]);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [currentTime, setCurrentTime] = useState(() => Date.now());
  useEffect(() => {
    const controller = new AbortController();
    void request<ContestDetail>(`/admin/contests/${encodeURIComponent(id)}`, { signal: controller.signal })
      .then((contest) => {
        if (controller.signal.aborted) return;
        setForm({
          title: contest.title,
          startsAt: contest.startsAt,
          endsAt: contest.endsAt,
          description: contest.description,
          visible: contest.visible,
          scoringPolicy: contest.scoringPolicy,
          problems: contest.problems.map((x) => x.id),
        });
        setInitialProblems(contest.problems);
        setLoadedId(id);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "无法读取比赛");
      });
    return () => controller.abort();
  }, [id]);
  useEffect(() => {
    if (!form) return;
    const transitions = [Date.parse(form.startsAt), Date.parse(form.endsAt)]
      .filter((time) => Number.isFinite(time) && time > currentTime);
    const next = Math.min(...transitions);
    if (!Number.isFinite(next)) return;
    const timer = setTimeout(
      () => setCurrentTime(Date.now()),
      Math.min(next - currentTime + 10, 2_147_000_000),
    );
    return () => clearTimeout(timer);
  }, [form, currentTime]);
  const update = <K extends keyof ContestForm>(
    k: K,
    v: ContestForm[K],
  ) => setForm((x) => (x ? { ...x, [k]: v } : x));
  if (!form || loadedId !== id)
    return (
      <Panel className="p-8 text-center">
        <ToastMessage message={error} />
        {error ? "无法加载比赛" : "正在加载比赛..."}
      </Panel>
    );
  const fixedFields = currentTime >= Date.parse(form.endsAt);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const r = await fetch(
        `/api/v1/admin/contests/${encodeURIComponent(id)}`,
        {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(form),
        },
      );
      if (!r.ok) throw new Error("保存比赛失败");
      router.push("/contests");
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存比赛失败");
    } finally {
      setSaving(false);
    }
  };
  const deleteContest = async () => {
    if (!window.confirm("删除比赛会永久删除报名、提交、判题记录和无引用文件，无法恢复。是否继续？")) return;
    setDeleting(true);
    setError("");
    try {
      const response = await fetch(`/api/v1/admin/contests/${encodeURIComponent(id)}`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!response.ok) throw new Error("删除比赛失败");
      router.push("/contests");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除比赛失败");
    } finally {
      setDeleting(false);
    }
  };
  return (
    <form onSubmit={submit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex justify-between">
        <div>
          <h2 className="text-2xl font-bold">编辑比赛</h2>
          <p>{id}</p>
        </div>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => router.push("/contests")}
          >
            取消
          </Button>
          <Button type="button" variant="danger" disabled={deleting} onClick={() => void deleteContest()}>
            {deleting ? "删除中..." : "删除比赛"}
          </Button>
          <Button disabled={saving}>{saving ? "保存中..." : "保存"}</Button>
        </div>
      </div>
      <ToastMessage message={error} />
      <div className="col-span-12 lg:col-span-8 space-y-6">
        <Panel className="p-5 space-y-5">
          <div>
            <Label htmlFor="title">比赛名称</Label>
            <Input
              id="title"
              value={form.title}
              onChange={(e) => update("title", e.target.value)}
            />
          </div>
          <div className="grid gap-5 sm:grid-cols-2">
            <div>
              <Label htmlFor="starts">开始时间</Label>
              <Input
                id="starts"
                type="datetime-local"
                value={toLocalDateTime(form.startsAt)}
                disabled={fixedFields}
                onChange={(e) =>
                  update("startsAt", fromLocalDateTime(e.target.value))
                }
              />
            </div>
            <div>
              <Label htmlFor="ends">结束时间</Label>
              <Input
                id="ends"
                type="datetime-local"
                value={toLocalDateTime(form.endsAt)}
                disabled={fixedFields}
                onChange={(e) =>
                  update("endsAt", fromLocalDateTime(e.target.value))
                }
              />
            </div>
          </div>
        </Panel>
        <Panel className="p-5">
          <MarkdownEditor
            id="description"
            value={form.description}
            onChange={(v) => update("description", v)}
            rows={16}
          />
        </Panel>
      </div>
      <div className="col-span-12 lg:col-span-4 space-y-6">
        <Panel className="p-5 space-y-5">
          <Switch
            label="比赛可见"
            defaultChecked={form.visible}
            onChange={(v) => update("visible", v)}
          />
          <Select
            options={policies}
            value={form.scoringPolicy}
            disabled={fixedFields}
            onChange={(v) =>
              update("scoringPolicy", v as ContestForm["scoringPolicy"])
            }
          />
          {fixedFields && (
            <p className="text-sm text-gray-500 dark:text-gray-400">
              比赛已结束，时间、评分策略与赛题列表已锁定。
            </p>
          )}
        </Panel>
        <Panel className="p-5">
          <ProblemMultiSelect
            key={id}
            selected={form.problems}
            initialProblems={initialProblems}
            disabled={fixedFields}
            onChange={(problems) => update("problems", problems)}
          />
        </Panel>
      </div>
    </form>
  );
}
