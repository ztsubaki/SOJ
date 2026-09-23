"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import "katex/dist/katex.min.css";

interface MarkdownProps {
  children: string;
  className?: string;
}

export function Markdown({ children, className = "max-w-none" }: MarkdownProps) {
  return (
    <div className={className}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex]}
        components={{
          h1: ({ children, ...props }) => (
            <h1 className="mt-8 border-b border-base-300 pb-3 text-3xl font-bold first:mt-0" {...props}>{children}</h1>
          ),
          h2: ({ children, ...props }) => (
            <h2 className="mt-7 text-2xl font-bold first:mt-0" {...props}>{children}</h2>
          ),
          h3: ({ children, ...props }) => (
            <h3 className="mt-6 text-xl font-semibold first:mt-0" {...props}>{children}</h3>
          ),
          p: ({ children, ...props }) => (
            <p className="my-4 leading-7" {...props}>{children}</p>
          ),
          a: ({ children, ...props }) => (
            <a className="link link-primary" {...props}>{children}</a>
          ),
          ul: ({ children, ...props }) => (
            <ul className="my-4 list-disc space-y-2 pl-6" {...props}>{children}</ul>
          ),
          ol: ({ children, ...props }) => (
            <ol className="my-4 list-decimal space-y-2 pl-6" {...props}>{children}</ol>
          ),
          blockquote: ({ children, ...props }) => (
            <blockquote className="my-5 border-s-4 border-primary bg-base-200 px-5 py-3 italic" {...props}>{children}</blockquote>
          ),
          pre: ({ children, ...props }) => (
            <pre className="my-5 overflow-x-auto rounded-box bg-neutral p-5 text-sm leading-6 text-neutral-content" {...props}>{children}</pre>
          ),
          code: ({ children, className, ...props }) => (
            <code className={className ? "font-mono" : "rounded bg-base-200 px-1.5 py-0.5 font-mono text-sm"} {...props}>{children}</code>
          ),
          table: ({ children, ...props }) => (
            <div className="my-4 overflow-x-auto">
              <table className="table table-md table-zebra" {...props}>{children}</table>
            </div>
          ),
          th: ({ children, ...props }) => <th {...props}>{children}</th>,
          td: ({ children, ...props }) => <td {...props}>{children}</td>,
          hr: (props) => <hr className="my-6 border-base-300" {...props} />,
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
