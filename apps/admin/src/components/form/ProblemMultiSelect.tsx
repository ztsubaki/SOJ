"use client";

import { useMemo, useState } from "react";
import SearchableMultiSelect, { type Option } from "./SearchableMultiSelect";
import { usePaginatedQuery } from "@/lib/pagination";

type ProblemOption = { id: number; title: string };
const NO_PROBLEMS: ProblemOption[] = [];

export default function ProblemMultiSelect({ selected, onChange, initialProblems = NO_PROBLEMS, disabled = false }: {
  selected: number[];
  onChange: (selected: number[]) => void;
  initialProblems?: ProblemOption[];
  disabled?: boolean;
}) {
  const [search, setSearch] = useState("");
  const [retained, setRetained] = useState<Option[]>([]);
  const query = new URLSearchParams();
  if (search) query.set("query", search);
  const { items, page, pageSize, total, loading, error, setPage } = usePaginatedQuery<ProblemOption>(`/admin/problems?${query}`, undefined, 250);
  const options = useMemo(() => items.map((problem) => ({
    value: String(problem.id), label: `${problem.title} (${problem.id})`,
  })), [items]);
  const metadata = useMemo(() => new Map([
    ...initialProblems.map((problem) => ({ value: String(problem.id), label: `${problem.title} (${problem.id})` })),
    ...retained,
    ...options,
  ].map((option) => [option.value, option])), [initialProblems, retained, options]);
  const selectedValues = useMemo(() => selected.map(String), [selected]);
  const selectedOptions = selectedValues.map((value) => metadata.get(value) ?? { value, label: value });

  return <SearchableMultiSelect
    id="problems"
    options={options}
    selectedOptions={selectedOptions}
    selected={selectedValues}
    disabled={disabled}
    onChange={(values) => {
      // Retain only chosen metadata, never all previously visited result pages.
      setRetained(values.map((value) => metadata.get(value) ?? { value, label: value }));
      onChange(values.map(Number));
    }}
    placeholder="选择题目"
    searchPlaceholder="搜索题目名称或 ID"
    search={search}
    onSearchChange={setSearch}
    pagination={{ page, pageSize, total, loading, onPageChange: setPage }}
    error={error}
  />;
}
