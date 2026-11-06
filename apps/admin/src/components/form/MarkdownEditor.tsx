"use client";

import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import remarkGfm from "remark-gfm";
import rehypeKatex from "rehype-katex";
import "katex/dist/katex.min.css";
import Label from "./Label";

interface MarkdownEditorProps {
  id?: string;
  label?: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  hint?: string;
}

export default function MarkdownEditor({
  id,
  label,
  value,
  onChange,
  placeholder = "输入 Markdown 内容，支持 LaTeX 公式...",
  rows = 10,
  hint,
}: MarkdownEditorProps) {
  const [activeTab, setActiveTab] = useState<"edit" | "preview" | "split">(
    "split",
  );

  const showEditor = activeTab === "edit" || activeTab === "split";
  const showPreview = activeTab === "preview" || activeTab === "split";

  return (
    <div>
      {label && <Label htmlFor={id}>{label}</Label>}
      <div className="overflow-hidden rounded-lg border border-gray-300 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex items-center justify-end border-b border-gray-200 bg-gray-50 px-3 py-2 dark:border-gray-800 dark:bg-white/[0.03]">
          <div className="flex items-center gap-1">
            {(["edit", "split", "preview"] as const).map((tab) => (
              <button
                key={tab}
                type="button"
                onClick={() => setActiveTab(tab)}
                className={`rounded px-2.5 py-1 text-xs font-medium transition ${
                  activeTab === tab
                    ? "bg-white text-brand-500 shadow-theme-xs dark:bg-gray-800 dark:text-brand-400"
                    : "text-gray-500 hover:bg-gray-200 dark:text-gray-400 dark:hover:bg-gray-800"
                }`}
              >
                {tab === "edit" && "编辑"}
                {tab === "split" && "分屏"}
                {tab === "preview" && "预览"}
              </button>
            ))}
          </div>
        </div>

        <div
          className={`grid ${
            activeTab === "split" ? "grid-cols-1 lg:grid-cols-2" : "grid-cols-1"
          }`}
        >
          {showEditor && (
            <textarea
              id={id}
              value={value}
              onChange={(e) => onChange(e.target.value)}
              placeholder={placeholder}
              rows={rows}
              className="w-full resize-y border-0 bg-transparent px-4 py-3 text-sm text-gray-800 placeholder:text-gray-400 focus:outline-hidden focus:ring-0 dark:bg-gray-900 dark:text-white/90 dark:placeholder:text-white/30"
            />
          )}
          {showPreview && (
            <div className="prose prose-sm max-w-none border-t border-gray-200 bg-white px-4 py-3 dark:border-gray-800 dark:bg-gray-900 dark:prose-invert lg:border-t-0 lg:border-l">
              <ReactMarkdown
                remarkPlugins={[remarkGfm, remarkMath]}
                rehypePlugins={[rehypeKatex]}
              >
                {value || "*预览区域*"}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>
      {hint && <p className="mt-1.5 text-xs text-gray-500">{hint}</p>}
    </div>
  );
}
