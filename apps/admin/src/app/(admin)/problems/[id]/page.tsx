"use client";
import Panel from "@/components/ui/Panel";
import { request } from "@soj/bff/client";
import { uploadHeaders } from "@soj/bff/upload";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import Button from "@/components/ui/button/Button";
import Input from "@/components/form/input/InputField";
import Label from "@/components/form/Label";
import Select from "@/components/form/Select";
import Switch from "@/components/form/switch/Switch";
import FileInput from "@/components/form/input/FileInput";
import MarkdownEditor from "@/components/form/MarkdownEditor";
import type { ProblemDetail, ProblemForm } from "@/lib/admin-types";
import {
  problemPackageAccept,
  problemPackageHint,
  problemPackageUploadError,
} from "@/lib/problem-package";
import { ToastMessage } from "@/context/ToastContext";
const options = ["Easy", "Medium", "Hard"].map((value) => ({
  value,
  label: value,
}));
export default function EditProblemPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [form, setForm] = useState<ProblemForm | null>(null);
  const [problemPackage, setProblemPackage] = useState<ProblemDetail["package"]>(null);
  const [file, setFile] = useState<File | null>(null);
  const [testFile, setTestFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  useEffect(() => {
    void request<ProblemDetail>(`/admin/problems/${encodeURIComponent(id)}`)
      .then((p) => {
        setProblemPackage(p.package);
        setForm({
          title: p.title,
          difficulty: p.difficulty,
          tags: p.tags,
          partitions: p.partitions,
          enabled: p.enabled,
          content: p.content,
        });
      })
      .catch((e) => setError(e instanceof Error ? e.message : "无法读取题目"));
  }, [id]);
  const update = <K extends keyof ProblemForm>(
    key: K,
    value: ProblemForm[K],
  ) => setForm((x) => (x ? { ...x, [key]: value } : x));
  if (!form)
    return (
      <Panel className="p-8 text-center">
        <ToastMessage message={error} />
        {error ? "无法加载题目" : "正在加载题目..."}
      </Panel>
    );
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      const r = await fetch(
        `/api/v1/admin/problems/${encodeURIComponent(id)}`,
        {
          method: "PUT",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(form),
        },
      );
      if (!r.ok) throw new Error("保存题目失败");
      if (file) {
        const u = await fetch(
          `/api/v1/admin/problems/${encodeURIComponent(id)}/package`,
          {
            method: "POST",
            credentials: "include",
            headers: uploadHeaders(file),
            body: file,
          },
        );
        if (!u.ok) throw await problemPackageUploadError(u, "题目已保存", id);
      }
      router.push("/problems");
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存题目失败");
    } finally {
      setSaving(false);
    }
  };
  const submitTest = async () => {
    if (!testFile) return;
    setTesting(true);
    setError("");
    try {
      const response = await fetch(
        `/api/v1/admin/problems/${encodeURIComponent(id)}/test-submissions`,
        { method: "POST", credentials: "include", headers: uploadHeaders(testFile), body: testFile },
      );
      if (!response.ok) throw new Error("创建管理员测试失败");
      router.push("/submissions");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建管理员测试失败");
    } finally {
      setTesting(false);
    }
  };
  const deleteProblem = async () => {
    if (!window.confirm("删除题目会永久删除其管理员测试、判题记录和无引用文件。已被比赛使用的题目不能删除。是否继续？")) return;
    setDeleting(true);
    setError("");
    try {
      const response = await fetch(`/api/v1/admin/problems/${encodeURIComponent(id)}`, {
        method: "DELETE",
        credentials: "include",
      });
      if (!response.ok) {
        if (response.status === 409) throw new Error("题目已被比赛使用，不能删除。");
        throw new Error("删除题目失败");
      }
      router.push("/problems");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "删除题目失败");
    } finally {
      setDeleting(false);
    }
  };
  return (
    <form onSubmit={submit} className="grid grid-cols-12 gap-4 md:gap-6">
      <div className="col-span-12 flex justify-between">
        <div>
          <h2 className="text-2xl font-bold">编辑题目</h2>
          <p>{id}</p>
        </div>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => router.push("/problems")}
          >
            取消
          </Button>
          <Button type="button" variant="danger" disabled={deleting} onClick={() => void deleteProblem()}>
            {deleting ? "删除中..." : "删除题目"}
          </Button>
          <Button disabled={saving}>{saving ? "保存中..." : "保存"}</Button>
        </div>
      </div>
      <ToastMessage message={error} />
      <div className="col-span-12 lg:col-span-8 space-y-6">
        <Panel className="p-5 space-y-5">
          <div>
            <Label htmlFor="title">题目标题</Label>
            <Input
              id="title"
              value={form.title}
              onChange={(e) => update("title", e.target.value)}
            />
          </div>
          <div className="grid gap-5 sm:grid-cols-2">
            <div>
              <Label htmlFor="difficulty">难度</Label>
              <Select
                options={options}
                value={form.difficulty}
                onChange={(v) =>
                  update("difficulty", v as ProblemForm["difficulty"])
                }
              />
            </div>
            <div>
              <Label htmlFor="tags">标签</Label>
              <Input
                id="tags"
                value={form.tags.join(", ")}
                onChange={(e) =>
                  update(
                    "tags",
                    e.target.value
                      .split(",")
                      .map((x) => x.trim())
                      .filter(Boolean),
                  )
                }
              />
            </div>
          </div>
          <div>
            <Label htmlFor="partitions">运行分区</Label>
            <Input
              id="partitions"
              value={form.partitions.join(", ")}
              onChange={(e) =>
                update(
                  "partitions",
                  e.target.value
                    .split(",")
                    .map((x) => x.trim())
                    .filter(Boolean),
                )
              }
            />
          </div>
        </Panel>
        <Panel className="p-5">
          <MarkdownEditor
            id="content"
            value={form.content}
            onChange={(v) => update("content", v)}
            rows={20}
          />
        </Panel>
      </div>
      <div className="col-span-12 lg:col-span-4 space-y-6">
        <Panel className="p-5">
          <Switch
            label="启用题目"
            defaultChecked={form.enabled}
            onChange={(v) => update("enabled", v)}
          />
        </Panel>
        <Panel className="p-5">
          <Label htmlFor="package">Package</Label>
          {problemPackage ? (
            <dl className="mt-3 space-y-1 text-sm">
              <div className="font-medium text-success-600 dark:text-success-400">测评包已就绪</div>
              <div>Artifact ID：{problemPackage.artifactId}</div>
              <div>大小：{problemPackage.sizeBytes.toLocaleString()} bytes</div>
              <div>上传时间：{new Date(problemPackage.uploadedAt).toLocaleString("zh-CN")}</div>
              <div className="break-all font-mono text-xs">SHA-256：{problemPackage.sha256}</div>
            </dl>
          ) : (
            <p className="mt-3 text-sm text-warning-600 dark:text-warning-400">尚未上传测评包，不能创建可评测的提交。</p>
          )}
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {problemPackageHint(id)}
          </p>
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
            可领取分区：{form.partitions.length ? form.partitions.join(", ") : "未设置"}。Judge Agent 的 agent_partition 必须匹配其中一项。
          </p>
          <FileInput
            id="package"
            accept={problemPackageAccept}
            className="mt-3"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
          {file && <p className="mt-3 text-sm">{file.name}</p>}
        </Panel>
        <Panel className="p-5">
          <Label htmlFor="test-source">管理员测试</Label>
          <p className="mb-3 text-sm text-gray-500">上传源码，创建不受比赛公开状态影响的测试提交。</p>
          <FileInput id="test-source" onChange={(e) => setTestFile(e.target.files?.[0] ?? null)} />
          {testFile && <p className="mt-3 text-sm">{testFile.name}</p>}
          <Button type="button" className="mt-4" disabled={!testFile || testing} onClick={() => void submitTest()}>
            {testing ? "创建中..." : "提交测试"}
          </Button>
        </Panel>
      </div>
    </form>
  );
}
