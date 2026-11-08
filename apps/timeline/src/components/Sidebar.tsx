import { CheckCircle, GearSix, Notebook, SignOut, Tag, Trash } from "@phosphor-icons/react";
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
 * The screen's left rail: the three pages the design lists, the account card at the bottom. 分类管理 and
 * 设置与 MCP have no server side yet (categories come from the records themselves and the MCP endpoint is a
 * machine protocol), so they are shown as the design shows them but stay unreachable instead of pretending.
 */
export function Sidebar({ view, onView, profile, signedOut, loginUrl, signingOut, onSignOut }: SidebarProps) {
  return (
    <aside className="flex w-[220px] shrink-0 flex-col border-r border-outline-variant/60 bg-surface-low px-3 py-6">
      <div className="px-3 pb-6">
        <p className="text-[15px] font-semibold tracking-tight text-primary">手账生活志</p>
        <p className="mt-0.5 text-[11px] tracking-[0.16em] text-muted-foreground uppercase">Timeline</p>
      </div>

      <nav aria-label="主导航" className="flex flex-col gap-1">
        <NavItem icon={<Notebook size={18} />} label="时间线" active={view === "timeline"} onClick={() => onView("timeline")} />
        <NavItem icon={<Tag size={18} />} label="分类管理" hint="分类跟着记录走，独立的管理页在后续切片接入" />
        <NavItem icon={<Trash size={18} />} label="回收站" active={view === "trash"} onClick={() => onView("trash")} />
        <NavItem icon={<GearSix size={18} />} label="设置与 MCP" hint="MCP 端点由 BFF 提供，账号设置页在后续切片接入" />
      </nav>

      <div className="mt-auto rounded-card border border-outline-variant/70 bg-surface-container/60 p-3">
        {profile !== null ? (
          <>
            <div className="flex items-center gap-2.5">
              <span className="grid size-8 shrink-0 place-items-center rounded-full bg-primary text-sm font-semibold text-on-primary">
                {[...profile.user.displayName][0] ?? "手"}
              </span>
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-on-surface">{profile.user.displayName || "未命名"}</p>
                <p className="flex items-center gap-1 text-[11px] text-muted-foreground">
                  <CheckCircle size={11} weight="fill" className="text-primary" />
                  已同步
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={onSignOut}
              disabled={signingOut}
              className="mt-3 flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs text-muted-foreground transition-colors hover:text-destructive disabled:opacity-60"
            >
              <SignOut size={13} />
              退出登录
            </button>
          </>
        ) : signedOut ? (
          <div className="text-xs text-muted-foreground">
            <p>还没有登录。</p>
            {loginUrl !== null ? (
              <a href={loginUrl} className="mt-2 inline-flex font-medium text-primary underline-offset-4 hover:underline">
                去 OzID 登录
              </a>
            ) : (
              <p className="mt-1">本构建没有配置 OzID 地址。</p>
            )}
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">正在读取账号…</p>
        )}
      </div>
    </aside>
  );
}

function NavItem({ icon, label, active = false, hint, onClick }: { icon: React.ReactNode; label: string; active?: boolean; hint?: string; onClick?: () => void }) {
  const shared = "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors";
  if (onClick === undefined) {
    return (
      <span aria-disabled="true" title={hint} className={cn(shared, "cursor-not-allowed text-muted-foreground/60")}>
        {icon}
        {label}
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      className={cn(shared, active ? "bg-primary-container/15 font-semibold text-primary" : "text-on-surface-variant hover:bg-surface-container/70")}
    >
      {icon}
      {label}
    </button>
  );
}
