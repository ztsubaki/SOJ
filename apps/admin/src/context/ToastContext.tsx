"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

type ToastTone = "error" | "success" | "info" | "warning";
type Toast = { id: number; message: string; tone: ToastTone };
type ToastContextValue = {
  show: (message: string, tone?: ToastTone) => void;
};

const ToastContext = createContext<ToastContextValue | null>(null);

const toneClasses: Record<ToastTone, string> = {
  error:
    "border-error-200 bg-error-50 text-error-800 dark:border-error-500/40 dark:bg-error-500/15 dark:text-error-200",
  success:
    "border-success-200 bg-success-50 text-success-800 dark:border-success-500/40 dark:bg-success-500/15 dark:text-success-200",
  info:
    "border-blue-light-200 bg-blue-light-50 text-blue-light-800 dark:border-blue-light-500/40 dark:bg-blue-light-500/15 dark:text-blue-light-200",
  warning:
    "border-warning-200 bg-warning-50 text-warning-800 dark:border-warning-500/40 dark:bg-warning-500/15 dark:text-warning-200",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);
  const show = useCallback((message: string, tone: ToastTone = "info") => {
    if (!message) return;
    setToasts((current) => [
      ...current,
      { id: Date.now() + Math.floor(Math.random() * 1_000), message, tone },
    ]);
  }, []);

  useEffect(() => {
    const timers = toasts.map((toast) =>
      window.setTimeout(() => dismiss(toast.id), 5_000),
    );
    return () => timers.forEach(window.clearTimeout);
  }, [dismiss, toasts]);

  const value = useMemo(() => ({ show }), [show]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        className="pointer-events-none fixed right-4 top-4 z-50 flex w-[calc(100%-2rem)] max-w-sm flex-col gap-3 pt-14 sm:pt-0"
        aria-live="polite"
        aria-atomic="true"
      >
        {toasts.map((toast) => (
          <div
            className={`pointer-events-auto flex items-start gap-3 rounded-xl border px-4 py-3 text-sm shadow-lg ${toneClasses[toast.tone]}`}
            key={toast.id}
          >
            <p className="flex-1 leading-6">{toast.message}</p>
            <button
              type="button"
              className="-mr-1 -mt-1 flex size-8 items-center justify-center rounded-md text-current/70 transition-colors hover:bg-black/5 hover:text-current focus:outline-none focus:ring-2 focus:ring-current/40 dark:hover:bg-white/10"
              onClick={() => dismiss(toast.id)}
              aria-label="关闭提示"
            >
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const context = useContext(ToastContext);
  if (!context) throw new Error("useToast must be used within ToastProvider");
  return context;
}

export function ToastMessage({
  message,
  tone = "error",
}: {
  message: string | null | undefined;
  tone?: ToastTone;
}) {
  const { show } = useToast();
  useEffect(() => {
    if (message) show(message, tone);
  }, [message, show, tone]);
  return null;
}
