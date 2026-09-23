import type { HTMLAttributes, ReactNode } from "react";

type PanelProps = HTMLAttributes<HTMLElement> & {
  as?: "div" | "section";
  children: ReactNode;
};

// Shared Admin surface. Layout, spacing, and elevation stay with each caller
// so panels remain appropriate for both dense tables and form sections.
export default function Panel({
  as: Element = "div",
  children,
  className = "",
  ...props
}: PanelProps) {
  return (
    <Element
      className={`rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900 ${className}`}
      {...props}
    >
      {children}
    </Element>
  );
}
