import { NotePencil, FolderOpen, SignOut, Sliders, Trash, User, ClockCounterClockwise } from "@phosphor-icons/react";
import type { AccountProfile } from "@kongken/account/api";
import type { View } from "@/lib/entries.ts";
import { cn } from "@kongken/ui/utils";

type SidebarProps = {
  view: View;
  onView: (view: View) => void;
  profile: AccountProfile | null;
  signedOut: boolean;
  loginUrl: string | null;
  signingOut: boolean;
  onSignOut: () => void;
};

/**
 * The left rail: brand, the four pages the design lists, and the account card at the bottom. On a narrow window it
 * folds into a strip across the top so the pages stay reachable.
 */
export function Sidebar({ view, onView, profile, signedOut, loginUrl, signingOut, onSignOut }: SidebarProps) {
  return (
    <aside className="flex w-full shrink-0 flex-col gap-4 border-b border-line bg-white px-3 py-3 lg:sticky lg:top-0 lg:h-screen lg:w-[220px] lg:justify-between lg:gap-0 lg:border-r lg:border-b-0 lg:py-5">
      <div className="flex flex-col gap-4 lg:gap-5">
        <div className="flex items-center gap-2 px-2 py-1">
          <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-account text-leaf">
            <NotePencil size={18} />
          </span>
          <div className="flex flex-col">
            <span className="text-lg leading-none font-semibold tracking-tight text-ink">Timeline</span>
            <span className="mt-1 text-xs leading-none tracking-wider text-soft">手账生活志</span>
          </div>
        </div>

        <nav aria-label="主导航" className="flex gap-1 overflow-x-auto lg:flex-col">
          <NavItem icon={<ClockCounterClockwise size={18} />} label="时间线" active={view === "timeline"} onClick={() => onView("timeline")} />
          <NavItem icon={<FolderOpen size={18} />} label="分类管理" active={view === "categories"} onClick={() => onView("categories")} />
          <NavItem icon={<Trash size={18} />} label="回收站" active={view === "trash"} onClick={() => onView("trash")} />
          <NavItem icon={<Sliders size={18} />} label="设置与 MCP" active={view === "settings"} onClick={() => onView("settings")} />
        </nav>
      </div>

      <div className="rounded-lg bg-account px-2.5 py-2">
        {profile !== null ? (
          <div className="flex items-center justify-between gap-2">
            <div className="flex min-w-0 items-center gap-2">
              <span className="grid size-7 shrink-0 place-items-center rounded-full bg-leaf text-white">
                <User size={16} weight="fill" />
              </span>
              <div className="flex min-w-0 flex-col">
                <span className="truncate text-[13px] leading-[18px] font-medium text-ink">{profile.user.displayName || "未命名"}</span>
                <span className="text-xs leading-4 text-soft">已同步</span>
              </div>
            </div>
            <button
              type="button"
              onClick={onSignOut}
              disabled={signingOut}
              title="退出登录"
              aria-label="退出登录"
              className="grid size-7 shrink-0 place-items-center rounded text-soft transition-colors hover:text-danger disabled:opacity-60"
            >
              <SignOut size={18} />
            </button>
          </div>
        ) : signedOut ? (
          <div className="text-xs text-soft">
            <p>还没有登录。</p>
            {loginUrl !== null ? (
              <a href={loginUrl} className="mt-1.5 inline-flex font-medium text-leaf underline-offset-4 hover:underline">
                去 OzID 登录
              </a>
            ) : (
              <p className="mt-1">本构建没有配置 OzID 地址。</p>
            )}
          </div>
        ) : (
          <p className="text-xs text-soft">正在读取账号…</p>
        )}
      </div>
    </aside>
  );
}

function NavItem({ icon, label, active = false, onClick }: { icon: React.ReactNode; label: string; active?: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex shrink-0 items-center gap-2 rounded-lg px-2 py-2 text-left text-sm transition-colors",
        active ? "bg-sidebar-active font-medium text-ink" : "text-soft hover:bg-account/60 hover:text-ink",
      )}
    >
      {icon}
      <span>{label}</span>
    </button>
  );
}
