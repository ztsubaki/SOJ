import Button from "@/components/ui/button/Button";

export type PaginationProps = {
  page: number;
  pageSize: number;
  total: number;
  loading: boolean;
  onPageChange: (page: number) => void;
};

export default function Pagination({ page, pageSize, total, loading, onPageChange }: PaginationProps) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  return (
    <nav aria-label="列表分页" aria-busy={loading} className="mt-4 flex flex-wrap items-center justify-between gap-3">
      <p role="status" aria-live="polite" className="text-sm text-gray-500 dark:text-gray-400">
        {loading ? `正在加载第 ${page} 页...` : `共 ${total} 条 · 第 ${page} / ${pages} 页`}
      </p>
      <div className="flex gap-2">
        <Button type="button" size="sm" variant="outline" disabled={loading || page <= 1} onClick={() => onPageChange(page - 1)}>上一页</Button>
        <Button type="button" size="sm" variant="outline" disabled={loading || page * pageSize >= total} onClick={() => onPageChange(page + 1)}>下一页</Button>
      </div>
    </nav>
  );
}
