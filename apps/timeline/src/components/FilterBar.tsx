import { FunnelSimple, X } from "@phosphor-icons/react";
import { Input } from "@kongken/ui/input";
import { activeFilters, type FilterState } from "@/lib/entries.ts";

type FilterBarProps = {
  filters: FilterState;
  onChange: (filters: FilterState) => void;
  /** Categories the loaded records already use; the server has no category catalogue yet. */
  categories: string[];
};

/** 分类筛选, 日期范围 and the chips of what is actually in effect, with 清除筛选. */
export function FilterBar({ filters, onChange, categories }: FilterBarProps) {
  const chips = activeFilters(filters);
  const set = (patch: Partial<FilterState>) => onChange({ ...filters, ...patch });

  return (
    <div className="mt-6 flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <FunnelSimple size={12} />
            分类筛选
          </span>
          <select
            value={filters.category}
            onChange={(event) => set({ category: event.target.value })}
            aria-label="分类筛选"
            className="h-9 min-w-[140px] rounded-md border border-outline-variant bg-card px-2.5 text-sm text-card-foreground"
          >
            <option value="">全部分类</option>
            {categories.map((category) => (
              <option key={category} value={category}>
                {category}
              </option>
            ))}
          </select>
        </label>

        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          <span>日期范围 · 从</span>
          <Input
            type="date"
            value={filters.fromDate}
            onChange={(event) => set({ fromDate: event.target.value })}
            aria-label="日期范围开始"
            className="h-9 w-[150px] border-outline-variant bg-card text-sm text-card-foreground"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          <span>到</span>
          <Input
            type="date"
            value={filters.toDate}
            onChange={(event) => set({ toDate: event.target.value })}
            aria-label="日期范围结束"
            className="h-9 w-[150px] border-outline-variant bg-card text-sm text-card-foreground"
          />
        </label>
      </div>

      {chips.length > 0 && (
        <div className="flex flex-wrap items-center gap-2">
          {chips.map((chip) => (
            <span key={chip.key} className="rounded-full bg-surface-container px-2.5 py-1 text-[11px] text-primary">
              {chip.label}
            </span>
          ))}
          <button
            type="button"
            onClick={() => onChange({ query: "", category: "", fromDate: "", toDate: "" })}
            className="inline-flex items-center gap-1 rounded-full px-1.5 py-1 text-[11px] text-muted-foreground transition-colors hover:text-destructive"
          >
            <X size={11} />
            清除筛选
          </button>
        </div>
      )}
    </div>
  );
}
