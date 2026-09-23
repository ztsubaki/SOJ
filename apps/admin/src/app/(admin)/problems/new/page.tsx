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
import type { ProblemForm } from "@/lib/admin-types";
import { ToastMessage } from "@/context/ToastContext";

const difficultyOptions = [
  { value: "Easy", label: "Easy" },
  { value: "Medium", label: "Medium" },
  { value: "Hard", label: "Hard" },
];

const emptyProblem: ProblemForm = {
  title: "",
  difficulty: "Medium",
  tags: [],
  partitions: ["judge"],
  enabled: true,
  content: "## 描述\n\n## 输入格式\n\n## 输出格式\n\n## 样例\n",
};

export default function NewProblemPage() {
  const router = useRouter();
  const [form, setForm] = useState<ProblemForm>(emptyProblem);

  const updateField = <K extends keyof ProblemForm>(
    field: K,
    value: ProblemForm[K],
  ) => {
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setIsSubmitting(true);
    try {
      const response = await fetch("/api/v1/admin/problems", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(form),
      });
      if (!response.ok) throw new Error("创建题目失败");
      const problem = (await response.json()) as { id: number };
      router.push(`/problems/${encodeURIComponent(problem.id)}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建题目失败");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            新建题目
          </h2>
          <p className="text-gray-500 text-theme-sm dark:text-gray-400">
            创建新题目并单独上传 package
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => router.push("/problems")}
          >
            取消
          </Button>
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? "创建中..." : "创建"}
          </Button>
        </div>
      </div>

      <ToastMessage message={error} />

      <div className="col-span-12 lg:col-span-8">
        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            基本信息
          </h3>
          <div className="mt-5 grid gap-5">
            <div>
              <Label htmlFor="title">题目标题</Label>
              <Input
                id="title"
                value={form.title}
                onChange={(e) => updateField("title", e.target.value)}
                placeholder="输入题目标题"
              />
            </div>
            <div className="grid gap-5 sm:grid-cols-2">
              <div>
                <Label htmlFor="difficulty">难度</Label>
                <Select
                  options={difficultyOptions}
                  value={form.difficulty}
                  onChange={(value) =>
                    updateField(
                      "difficulty",
                      value as ProblemForm["difficulty"],
                    )
                  }
                  placeholder="选择难度"
                />
              </div>
              <div>
                <Label htmlFor="tags">标签</Label>
                <Input
                  id="tags"
                  value={form.tags.join(", ")}
                  onChange={(e) =>
                    updateField(
                      "tags",
                      e.target.value.split(",").map((t) => t.trim()),
                    )
                  }
                  placeholder="用逗号分隔标签"
                />
              </div>
            </div>
            <div>
              <Label htmlFor="partitions">运行分区</Label>
              <Input
                id="partitions"
                value={form.partitions.join(", ")}
                onChange={(e) =>
                  updateField(
                    "partitions",
                    e.target.value
                      .split(",")
                      .map((value) => value.trim())
                      .filter(Boolean),
                  )
                }
                placeholder="用逗号分隔分区"
              />
            </div>
          </div>
        </Panel>

        <Panel className="mt-6 p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            题目内容
          </h3>
          <p className="mt-1 text-gray-500 text-theme-sm dark:text-gray-400">
            支持 Markdown 语法与 $...$ / $$...$$ LaTeX 公式
          </p>
          <div className="mt-5">
            <MarkdownEditor
              id="content"
              value={form.content}
              onChange={(value) => updateField("content", value)}
              placeholder="输入题目内容"
              rows={20}
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
              label="启用题目"
              defaultChecked={form.enabled}
              onChange={(checked) => updateField("enabled", checked)}
            />
          </div>
        </Panel>

        <Panel className="p-5 sm:p-6">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-white">
            Package
          </h3>
          <p className="mt-1 text-gray-500 text-theme-sm dark:text-gray-400">
            题目创建后会跳转到编辑页；请在编辑页上传测评包。因为 manifest.json 的 problemId 必须等于创建后分配的题目 ID，不能在创建前可靠地准备上传包。
          </p>
        </Panel>
      </div>
    </form>
  );
}
