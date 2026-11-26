import { MagnifyingGlass, NotePencil, User } from "@phosphor-icons/react";

type TopBarProps = {
  /** `2026年 阳历记录本`: the book the page is showing, the header's left label. */
  title: string;
  onSearch: () => void;
  onWrite: () => void;
  signedIn: boolean;
};

/** The sticky 56px header: the book's name on the left; 检索手账, 写手账 and the avatar on the right. */
export function TopBar({ title, onSearch, onWrite, signedIn }: TopBarProps) {
  return (
    <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-3 bg-canvas/95 px-5 shadow-[0_1px_8px_rgba(43,66,52,0.04)] backdrop-blur-sm sm:px-8">
      <span className="meta-numeric min-w-0 truncate text-soft">{title}</span>
      <div className="flex shrink-0 items-center gap-2 sm:gap-3">
        <button
          type="button"
          onClick={onSearch}
          aria-label="检索手账" className="flex items-center gap-1 rounded-lg px-2 py-1.5 text-soft transition-colors hover:bg-subtle hover:text-ink"
        >
          <MagnifyingGlass size={18} aria-hidden="true" />
          <span className="hidden text-xs whitespace-nowrap sm:inline">检索手账</span>
        </button>
        <button
          type="button"
          onClick={onWrite}
          className="flex items-center gap-1 rounded-lg bg-leaf px-3 py-1.5 text-[13px] leading-[18px] font-medium text-white transition-colors hover:bg-leaf-hover"
        >
          <NotePencil size={16} />
          <span className="whitespace-nowrap">写手账</span>
        </button>
        {signedIn && (
          <span aria-hidden="true" className="ml-1 grid size-8 shrink-0 place-items-center rounded-full bg-leaf text-white">
            <User size={18} weight="fill" />
          </span>
        )}
      </div>
    </header>
  );
}
