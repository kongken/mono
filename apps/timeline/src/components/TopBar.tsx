import { MagnifyingGlass, PenNib } from "@phosphor-icons/react";
import { Input } from "@kongken/ui/input";

type TopBarProps = {
  query: string;
  onQuery: (value: string) => void;
  onWrite: () => void;
  /** The count the list currently holds, shown next to the search field the way the design's header does. */
  total: number;
};

/** The sticky header: 检索手账 on the left, 写手账 on the right. */
export function TopBar({ query, onQuery, onWrite, total }: TopBarProps) {
  return (
    <header className="sticky top-0 z-10 flex items-center gap-4 border-b border-outline-variant/60 bg-surface/95 px-10 py-3.5 backdrop-blur">
      <div className="relative max-w-[420px] flex-1">
        <MagnifyingGlass size={16} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="search"
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="检索手账：标题、备注或地点"
          aria-label="检索手账"
          className="h-9 border-outline-variant bg-card pl-9 text-sm text-card-foreground placeholder:text-muted-foreground"
        />
      </div>
      <span className="tabular hidden text-xs text-muted-foreground sm:inline">{total > 0 ? `${total} 条记录` : ""}</span>
      <button
        type="button"
        onClick={onWrite}
        className="ml-auto inline-flex items-center gap-1.5 rounded-md border border-outline-variant bg-card px-3.5 py-2 text-sm font-medium text-card-foreground transition-colors hover:bg-muted"
      >
        <PenNib size={15} />
        写手账
      </button>
    </header>
  );
}
