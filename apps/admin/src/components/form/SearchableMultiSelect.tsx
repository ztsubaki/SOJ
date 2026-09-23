"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import Label from "./Label";

interface Option {
  value: string;
  label: string;
}

interface SearchableMultiSelectProps {
  id?: string;
  label?: string;
  options: Option[];
  selected: string[];
  onChange: (selected: string[]) => void;
  placeholder?: string;
  searchPlaceholder?: string;
  disabled?: boolean;
}

export default function SearchableMultiSelect({
  id,
  label,
  options,
  selected,
  onChange,
  placeholder = "选择项目",
  searchPlaceholder = "搜索...",
  disabled = false,
}: SearchableMultiSelectProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState("");
  const containerRef = useRef<HTMLDivElement>(null);

  const filteredOptions = useMemo(() => {
    const term = search.trim().toLowerCase();
    return term
      ? options.filter((opt) => opt.label.toLowerCase().includes(term))
      : options;
  }, [options, search]);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  const toggleValue = (value: string) => {
    onChange(
      selected.includes(value)
        ? selected.filter((v) => v !== value)
        : [...selected, value],
    );
  };

  const removeValue = (value: string) => {
    onChange(selected.filter((v) => v !== value));
  };

  const selectedLabels = selected
    .map((value) => options.find((opt) => opt.value === value)?.label)
    .filter(Boolean) as string[];

  return (
    <div ref={containerRef}>
      {label && <Label htmlFor={id}>{label}</Label>}
      <div className="relative">
        <button
          type="button"
          id={id}
          disabled={disabled}
          onClick={() => setIsOpen((prev) => !prev)}
          className="flex min-h-[2.75rem] w-full items-center justify-between rounded-lg border border-gray-300 bg-white px-3 py-2 text-left shadow-theme-xs transition focus:border-brand-300 focus:outline-hidden focus:ring-3 focus:ring-brand-500/10 dark:border-gray-700 dark:bg-gray-900"
        >
          <div className="flex flex-wrap items-center gap-2">
            {selectedLabels.length > 0 ? (
              selectedLabels.map((text) => (
                <span
                  key={text}
                  className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-800 dark:bg-gray-800 dark:text-white/90"
                >
                  {text}
                  <span
                    role="button"
                    tabIndex={0}
                    onClick={(e) => {
                      e.stopPropagation();
                      const value = options.find(
                        (o) => o.label === text,
                      )?.value;
                      if (value) removeValue(value);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.stopPropagation();
                        const value = options.find(
                          (o) => o.label === text,
                        )?.value;
                        if (value) removeValue(value);
                      }
                    }}
                    className="cursor-pointer text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
                  >
                    ×
                  </span>
                </span>
              ))
            ) : (
              <span className="text-sm text-gray-400">{placeholder}</span>
            )}
          </div>
          <svg
            className={`ml-2 h-5 w-5 shrink-0 text-gray-500 transition-transform ${
              isOpen ? "rotate-180" : ""
            }`}
            viewBox="0 0 20 20"
            fill="none"
          >
            <path
              d="M4.79175 7.39551L10.0001 12.6038L15.2084 7.39551"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>

        {isOpen && (
          <div className="absolute z-50 mt-2 w-full rounded-xl border border-gray-200 bg-white p-2 shadow-theme-lg dark:border-gray-800 dark:bg-gray-dark">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={searchPlaceholder}
              className="h-10 w-full rounded-lg border border-gray-200 bg-transparent px-3 text-sm text-gray-800 placeholder:text-gray-400 focus:border-brand-300 focus:outline-hidden dark:border-gray-700 dark:text-white/90"
              autoFocus
            />
            <div className="mt-2 max-h-60 overflow-y-auto rounded-lg">
              {filteredOptions.length === 0 ? (
                <p className="px-3 py-2 text-sm text-gray-500 dark:text-gray-400">
                  无匹配项目
                </p>
              ) : (
                filteredOptions.map((option) => {
                  const isSelected = selected.includes(option.value);
                  return (
                    <label
                      key={option.value}
                      className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm transition ${
                        isSelected
                          ? "bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
                          : "text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-white/5"
                      }`}
                    >
                      <input
                        type="checkbox"
                        className="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500"
                        checked={isSelected}
                        onChange={() => toggleValue(option.value)}
                      />
                      <span className="flex-1">{option.label}</span>
                    </label>
                  );
                })
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
