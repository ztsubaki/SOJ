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
  error: "alert-error",
  success: "alert-success",
  info: "alert-info",
  warning: "alert-warning",
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
        className="toast toast-top toast-end z-50 w-auto max-w-[calc(100vw-2rem)] items-end pt-16 sm:pt-4"
        aria-live="polite"
        aria-atomic="true"
      >
        {toasts.map((toast) => (
          <div
            className={`alert w-fit max-w-full ${toneClasses[toast.tone]} shadow-lg`}
            key={toast.id}
            role="alert"
          >
            <span>{toast.message}</span>
            <button
              type="button"
              className="btn btn-ghost btn-xs"
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
