"use client";

import { useEffect, useState } from "react";
import { Icon } from "@/components/icon";

const STORAGE_KEY = "soj-theme";

type Theme = "light" | "dark";

function getSystemTheme(): Theme {
  if (typeof window === "undefined") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function ThemeControls() {
  // Fixed initial state so server and client first render match (avoids hydration mismatch).
  const [theme, setTheme] = useState<Theme>("light");

  // Initialize from localStorage / system preference after hydration.
  useEffect(() => {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    const initial: Theme =
      saved === "dark" ? "dark" : saved === "light" ? "light" : getSystemTheme();
    // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing initial theme from localStorage
    setTheme(initial);
    document.documentElement.dataset.theme = initial;
  }, []);

  // Keep document data-theme in sync with state.
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  // Follow system preference when no manual preference is saved.
  useEffect(() => {
    const listener = (event: MediaQueryListEvent) => {
      const saved = window.localStorage.getItem(STORAGE_KEY);
      if (!saved) {
        const next = event.matches ? "dark" : "light";
        setTheme(next);
      }
    };

    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", listener);
    return () => media.removeEventListener("change", listener);
  }, []);

  function toggleTheme() {
    const next = theme === "light" ? "dark" : "light";
    setTheme(next);
    window.localStorage.setItem(STORAGE_KEY, next);
    document.documentElement.dataset.theme = next;
  }

  return (
    <button
      type="button"
      onClick={toggleTheme}
      className="btn btn-ghost btn-square"
      aria-label={theme === "light" ? "切换到深色模式" : "切换到浅色模式"}
      title={theme === "light" ? "切换到深色模式" : "切换到浅色模式"}
    >
      {theme === "light" ? <Icon name="moon" /> : <Icon name="sun" />}
    </button>
  );
}
