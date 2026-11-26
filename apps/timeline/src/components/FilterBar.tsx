import { useEffect, useRef, useState } from "react";
import { CalendarBlank, CaretDown, Funnel, MagnifyingGlass, SquaresFour, X } from "@phosphor-icons/react";
import { activeFilters, EMPTY_FILTERS, type ActiveFilter, type FilterState } from "@/lib/entries.ts";

type FilterBarProps = {
  filters: FilterState;
  onChange: (filters: FilterState) => void;
  /** Categories the loaded records already use, plus the user's own category list. */
  categories: string[];
};

const trigger =
  "flex h-8 items-center gap-1 rounded-field bg-canvas px-2.5 text-[13px] leading-[18px] font-medium text-ink transition-colors hover:bg-white";

/**
 * The compact search and filter panel under the page title: one search field, 分类筛选 and 日期范围 triggers, and
 * — only when something is in effect — a 生效中 row of removable chips with 清除筛选.
 */
export function FilterBar({ filters, onChange, categories }: FilterBarProps) {
  const chips = activeFilters(filters);
  const set = (patch: Partial<FilterState>) => onChange({ ...filters, ...patch });
  const remove = (chip: ActiveFilter) => {
    if (chip.key === "fromDate") set({ fromDate: "", toDate: "" });
    else set({ [chip.key]: "" });
  };

  return (
    <div className="flex flex-col gap-2 rounded-card bg-paper p-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[180px] flex-1">
          <MagnifyingGlass size={18} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-soft" />
          <input
            id="timeline-search"
            type="search"
            value={filters.query}
            onChange={(event) => set({ query: event.target.value })}
            placeholder="搜索标题、地点或备注"
            aria-label="检索手账"
            className="field h-8 bg-canvas pr-3 pl-8 text-sm focus:bg-white"
          />
        </div>

        <div className="relative">
          <SquaresFour size={16} className="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-soft" />
          <select
            value={filters.category}
            onChange={(event) => set({ category: event.target.value })}
            aria-label="分类筛选"
            className={`${trigger} appearance-none pr-7 pl-8`}
          >
            <option value="">分类筛选</option>
            {categories.map((category) => (
              <option key={category} value={category}>
                {category}
              </option>
            ))}
          </select>
          <CaretDown size={14} className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-soft" />
        </div>

        <DateRange filters={filters} onChange={set} />
      </div>

      {chips.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 pt-0.5">
          <span className="text-xs text-soft">生效中:</span>
          {chips.map((chip) => (
            <span key={chip.key} className="inline-flex items-center gap-1 rounded-tag bg-subtle px-2 py-0.5 text-xs text-ink">
              <span>{chip.label}</span>
              <button
                type="button"
                onClick={() => remove(chip)}
                aria-label={`移除条件「${chip.label}」`}
                className="flex items-center text-soft transition-colors hover:text-ink"
              >
                <X size={14} />
              </button>
            </span>
          ))}
          <button
            type="button"
            onClick={() => onChange(EMPTY_FILTERS)}
            className="text-xs text-soft underline underline-offset-2 transition-colors hover:text-ink"
          >
            清除筛选
          </button>
        </div>
      )}
    </div>
  );
}

/** 日期范围: a trigger that opens a small floating panel with the two ends of the range. */
function DateRange({ filters, onChange }: { filters: FilterState; onChange: (patch: Partial<FilterState>) => void }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => {
      if (root.current !== null && !root.current.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);

  return (
    <div ref={root} className="relative">
      <button type="button" onClick={() => setOpen((current) => !current)} aria-expanded={open} aria-haspopup="true" className={trigger}>
        <CalendarBlank size={16} className="text-soft" />
        <span>日期范围</span>
        <CaretDown size={14} className="text-soft" />
      </button>
      {open && (
        <div className="absolute top-full right-0 z-20 mt-1.5 flex w-[260px] flex-col gap-2.5 rounded-card bg-canvas p-3 shadow-float ring-1 ring-line">
          <label className="flex flex-col gap-1 text-xs text-soft">
            <span className="inline-flex items-center gap-1">
              <Funnel size={12} />从
            </span>
            <input
              type="date"
              value={filters.fromDate}
              onChange={(event) => onChange({ fromDate: event.target.value })}
              aria-label="日期范围开始"
              className="field meta-numeric h-8 px-2.5"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-soft">
            <span>到</span>
            <input
              type="date"
              value={filters.toDate}
              onChange={(event) => onChange({ toDate: event.target.value })}
              aria-label="日期范围结束"
              className="field meta-numeric h-8 px-2.5"
            />
          </label>
        </div>
      )}
    </div>
  );
}
