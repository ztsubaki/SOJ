"use client";
import Panel from "@/components/ui/Panel";
import { useRouter } from "next/navigation";
import { useState } from "react";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import Select from "@/components/form/Select";
import Switch from "@/components/form/switch/Switch";
import MarkdownEditor from "@/components/form/MarkdownEditor";
import ProblemMultiSelect from "@/components/form/ProblemMultiSelect";
import type { ContestForm } from "@/lib/admin-types";
import { fromLocalDateTime, toLocalDateTime } from "@/lib/local-datetime";
import { ToastMessage } from "@/context/ToastContext";

const scoringPolicyOptions = [
  { value: "best", label: "每题最高分" },
  { value: "latest", label: "每题最新提交" },
];

const now = new Date();
const tomorrow = new Date(now.getTime() + 24 * 60 * 60 * 1000);
const dayAfterTomorrow = new Date(tomorrow.getTime() + 24 * 60 * 60 * 1000);
const formatIso = (date: Date) => date.toISOString();

const emptyContest: ContestForm = {
  title: "",
  startsAt: formatIso(tomorrow),
  endsAt: formatIso(dayAfterTomorrow),
  description: "## 比赛介绍\n\n## 规则\n",
  problems: [],
  visible: true,
  scoringPolicy: "best",
};

export default function NewContestPage() {
  const router = useRouter();
  const [form, setForm] = useState<ContestForm>(emptyContest);
  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);


  const updateField = <K extends keyof ContestForm>(
    field: K,
    value: ContestForm[K],
  ) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setIsSubmitting(true);
    try {
      const response = await fetch("/api/v1/admin/contests", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(form),
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => null)) as {
          error?: unknown;
        } | null;
        throw new Error(
          typeof body?.error === "string" ? body.error : "创建比赛失败",
        );
      }
      router.push("/contests");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建比赛失败");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
              新建比赛
            </h2>
            <p className="text-gray-500 text-theme-sm dark:text-gray-400">
              创建后可在比赛开始前继续添加题目和调整评分策略
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => router.push("/contests")}
            >
              取消
            </Button>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? "创建中..." : "创建"}
            </Button>
          </div>
        </div>
        <ToastMessage message={error} />
      </div>

      <div className="col-span-12 lg:col-span-8">
        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            基本信息
          </h3>
          <div className="mt-5 grid gap-5">
            <div>
              <Label htmlFor="title">比赛名称</Label>
              <Input
                id="title"
                value={form.title}
                onChange={(e) => updateField("title", e.target.value)}
                placeholder="输入比赛名称"
                required
              />
            </div>
            <div className="grid gap-5 sm:grid-cols-2">
              <div>
                <Label htmlFor="startsAt">开始时间</Label>
                <Input
                  id="startsAt"
                  type="datetime-local"
                  value={toLocalDateTime(form.startsAt)}
                  onChange={(e) =>
                    updateField(
                      "startsAt",
                      fromLocalDateTime(e.target.value),
                    )
                  }
                  required
                />
              </div>
              <div>
                <Label htmlFor="endsAt">结束时间</Label>
                <Input
                  id="endsAt"
                  type="datetime-local"
                  value={toLocalDateTime(form.endsAt)}
                  onChange={(e) =>
                    updateField(
                      "endsAt",
                      fromLocalDateTime(e.target.value),
                    )
                  }
                  required
                />
              </div>
            </div>
          </div>
        </Panel>

        <Panel className="mt-6 p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            比赛介绍
          </h3>
          <p className="mt-1 text-gray-500 text-theme-sm dark:text-gray-400">
            支持 Markdown 语法与 $...$ / $$...$$ LaTeX 公式
          </p>
          <div className="mt-5">
            <MarkdownEditor
              id="description"
              value={form.description}
              onChange={(value) => updateField("description", value)}
              placeholder="输入比赛介绍"
              rows={16}
            />
          </div>
        </Panel>
      </div>

      <div className="col-span-12 lg:col-span-4 space-y-6">
        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            设置
          </h3>
          <div className="mt-5 space-y-5">
            <Switch
              label="比赛可见"
              defaultChecked={form.visible}
              onChange={(checked) => updateField("visible", checked)}
            />
            <div>
              <Label htmlFor="scoringPolicy">评分策略</Label>
              <Select
                options={scoringPolicyOptions}
                value={form.scoringPolicy}
                onChange={(value) =>
                  updateField(
                    "scoringPolicy",
                    value as ContestForm["scoringPolicy"],
                  )
                }
              />
            </div>
          </div>
        </Panel>

        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            题目
          </h3>
          <p className="mt-1 text-gray-500 text-theme-sm dark:text-gray-400">
            选择本场比赛包含的题目
          </p>
          <div className="mt-4">
            <ProblemMultiSelect
              selected={form.problems}
              onChange={(problems) => updateField("problems", problems)}
            />
          </div>
        </Panel>
      </div>
    </form>
  );
}
