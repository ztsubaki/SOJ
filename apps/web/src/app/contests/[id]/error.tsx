"use client";

import { useEffect } from "react";
import { useToast } from "@/components/toast-provider";

export default function ContestError({ reset }: { reset: () => void }) {
  const { show } = useToast();
  useEffect(() => {
    show("服务暂时不可用，请稍后重试。", "error");
  }, [show]);

  return (
    <main className="grid min-h-screen place-items-center bg-base-200 p-4">
      <section className="card w-full max-w-md bg-base-100 shadow-md">
        <div className="card-body">
          <h1 className="card-title">暂时无法读取比赛</h1>
          <div className="card-actions justify-end">
            <button type="button" className="btn" onClick={reset}>
              重试
            </button>
          </div>
        </div>
      </section>
    </main>
  );
}
