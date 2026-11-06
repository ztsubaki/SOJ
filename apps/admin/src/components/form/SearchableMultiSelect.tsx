"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Label from "./Label";
import Pagination, { type PaginationProps } from "@/components/ui/Pagination";

export interface Option {
  value: string;
  label: string;
}

interface SearchableMultiSelectProps {
  id?: string;
  label?: string;
  options: Option[];
  selectedOptions?: Option[];
  selected: string[];
  onChange: (selected: string[]) => void;
  placeholder?: string;
  searchPlaceholder?: string;
  disabled?: boolean;
  search?: string;
  onSearchChange?: (search: string) => void;
  pagination?: PaginationProps;
  error?: string;
}

export default function SearchableMultiSelect({
  id,
  label,
  options,
  selectedOptions = [],
  selected,
  onChange,
  placeholder = "选择项目",
  searchPlaceholder = "搜索...",
  disabled = false,
  search: remoteSearch,
  onSearchChange,
  pagination,
  error,
}: SearchableMultiSelectProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [localSearch, setLocalSearch] = useState("");
  const search = remoteSearch ?? localSearch;
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const optionsByValue = useMemo(() => new Map(
    [...selectedOptions, ...options].map((option) => [option.value, option]),
  ), [selectedOptions, options]);
  const filteredOptions = useMemo(() => {
    if (onSearchChange) return options;
    const term = search.trim().toLowerCase();
    return term ? options.filter((option) => option.label.toLowerCase().includes(term)) : options;
  }, [options, search, onSearchChange]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setIsOpen(false);
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={containerRef} onKeyDown={(event) => {
      if (event.key === "Escape") {
        setIsOpen(false);
        triggerRef.current?.focus();
      }
    }}>
      {label && <Label htmlFor={id}>{label}</Label>}
      <div className="relative">
        <div className="flex min-h-[2.75rem] w-full flex-wrap items-center gap-2 rounded-lg border border-gray-300 bg-white px-3 py-2 shadow-theme-xs focus-within:border-brand-300 focus-within:ring-3 focus-within:ring-brand-500/10 dark:border-gray-700 dark:bg-gray-900">
          {selected.map((value) => (
            <span key={value} className="inline-flex items-center gap-1 rounded-full bg-gray-100 pl-2.5 text-xs font-medium text-gray-800 dark:bg-gray-800 dark:text-white/90">
              {optionsByValue.get(value)?.label ?? value}
              <button type="button" disabled={disabled} aria-label={`移除 ${optionsByValue.get(value)?.label ?? value}`} onClick={() => onChange(selected.filter((entry) => entry !== value))} className="flex min-h-11 min-w-11 items-center justify-center rounded-full text-gray-500 hover:text-gray-700 focus-visible:outline-2 focus-visible:outline-brand-500 dark:text-gray-400 dark:hover:text-gray-200">×</button>
            </span>
          ))}
          <button
            ref={triggerRef}
            type="button"
            id={id}
            disabled={disabled}
            aria-expanded={isOpen && !disabled}
            aria-controls={id ? `${id}-options` : undefined}
            onClick={() => setIsOpen((previous) => !previous)}
            className="flex min-h-11 flex-1 items-center justify-between gap-2 rounded text-left text-sm text-gray-500 focus-visible:outline-2 focus-visible:outline-brand-500 dark:text-gray-400"
          >
            {selected.length ? "添加项目" : placeholder}
            <svg aria-hidden="true" className={`ml-2 h-5 w-5 shrink-0 transition-transform ${isOpen ? "rotate-180" : ""}`} viewBox="0 0 20 20" fill="none">
              <path d="M4.79175 7.39551L10.0001 12.6038L15.2084 7.39551" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
        </div>
        {isOpen && !disabled && (
          <div id={id ? `${id}-options` : undefined} className="absolute z-50 mt-2 w-full rounded-xl border border-gray-200 bg-white p-2 shadow-theme-lg dark:border-gray-800 dark:bg-gray-dark">
            <input type="text" value={search} onChange={(event) => {
              if (onSearchChange) onSearchChange(event.target.value);
              else setLocalSearch(event.target.value);
            }} aria-label={searchPlaceholder} placeholder={searchPlaceholder} className="h-11 w-full rounded-lg border border-gray-200 bg-transparent px-3 text-sm text-gray-800 placeholder:text-gray-400 focus:border-brand-300 focus:outline-hidden dark:border-gray-700 dark:text-white/90" autoFocus />
            {error && <p role="alert" className="px-3 py-2 text-sm text-error-500">{error}</p>}
            <div className="mt-2 max-h-60 overflow-y-auto rounded-lg" aria-busy={pagination?.loading}>
              {!pagination?.loading && !error && filteredOptions.length === 0 && <p className="px-3 py-2 text-sm text-gray-500 dark:text-gray-400">无匹配项目</p>}
              {filteredOptions.map((option) => {
                const isSelected = selectedSet.has(option.value);
                return (
                  <label key={option.value} className={`flex min-h-11 cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm transition ${isSelected ? "bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400" : "text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-white/5"}`}>
                    <input type="checkbox" className="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500" checked={isSelected} onChange={() => onChange(isSelected ? selected.filter((value) => value !== option.value) : [...selected, option.value])} />
                    <span className="flex-1">{option.label}</span>
                  </label>
                );
              })}
            </div>
            {pagination && <Pagination {...pagination} />}
          </div>
        )}
      </div>
    </div>
  );
}
