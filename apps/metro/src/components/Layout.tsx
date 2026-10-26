import type { ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { cn } from "@kongken/ui/utils";
import { Compass, Path, Subway, Train, User } from "@phosphor-icons/react";
import { Link, usePath } from "@/lib/router";
import { useSession } from "@/lib/session";
import { ozid } from "@/lib/account-center";
import { Alert, Eyebrow } from "@/components/extras";

/** The page's content column, shared by the header, the pages and the footer so their edges line up. */
export const container = "mx-auto w-full max-w-7xl px-4 sm:px-6 lg:px-12";

/**
 * A deletion started in this product before OzID took over, never confirmed finished (the flag it left in this
 * browser): the account is already closed and the cleanup continues. Retrying is OzID's now; this only says where.
 */
function PendingDeletion() {
  const { dismissDeletionPending } = useSession();
  return (
    <div className={cn(container, "mt-4")} data-testid="deletion-pending">
      <Alert kind="error">
        <p>账号注销尚未完成：账号已关闭，数据清理需要重试，进度不会丢失。</p>
        <p className="mt-2 flex flex-wrap items-center gap-3">
          {ozid.enabled ? (
            <a href={ozid.closeAccount} target="_blank" rel="noopener noreferrer" className="font-semibold underline" data-testid="ozid-close-account">
              前往 OzID 重试注销
            </a>
          ) : null}
          <Button size="sm" variant="outline" onClick={dismissDeletionPending}>
            已经完成，不再提示
          </Button>
        </p>
      </Alert>
    </div>
  );
}

type Section = "cities" | "topics" | "me" | undefined;

const sectionOf = (path: string): Section => {
  if (path === "/" || path.startsWith("/cities")) return "cities";
  if (path === "/topics" || path.startsWith("/topics/")) return "topics";
  if (path === "/me") return "me";
  return undefined;
};

export function Layout({ children }: { children: ReactNode }) {
  const { session, signIn, signingIn, signOut, refresh, notice, dismissNotice, deletionPending } = useSession();
  const path = usePath();
  const active = sectionOf(path);

  const desktopNav = (to: string, label: string, on: boolean) => (
    <Link
      to={to}
      aria-current={on ? "page" : undefined}
      className={cn(
        "rounded-lg px-3.5 py-1.5 text-[15px] font-semibold transition-colors",
        on ? "bg-deep text-white" : "text-on-variant hover:bg-surface-high hover:text-on-surface",
      )}
    >
      {label}
    </Link>
  );
  const tab = (to: string, label: string, name: string, on: boolean, icon: ReactNode) => (
    <Link
      to={to}
      aria-label={name}
      aria-current={on ? "page" : undefined}
      className={cn("flex min-h-12 flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-medium transition-colors", on ? "text-ink" : "text-on-variant")}
    >
      <span className={cn("flex h-7 w-12 items-center justify-center rounded-full transition-colors", on && "bg-brand-container")}>{icon}</span>
      <span aria-hidden>{label}</span>
    </Link>
  );

  return (
    <div className="flex min-h-screen flex-col bg-surface text-on-surface">
      <header className="sticky top-0 z-40 bg-surface/90 shadow-[0_1px_8px_rgba(18,28,42,0.05)] backdrop-blur-md">
        <div className={cn(container, "flex h-16 items-center gap-4")}>
          <Link to="/" className="flex shrink-0 items-center gap-2.5" aria-label="地铁足迹 首页">
            <span className="flex size-9 items-center justify-center rounded-lg bg-deep text-white">
              <Subway className="size-5" weight="fill" aria-hidden />
            </span>
            <span className="flex flex-col leading-none">
              <span className="font-headline text-[17px] font-bold tracking-tight text-ink">地铁足迹</span>
              <span className="mt-1 hidden font-code text-[9px] tracking-[0.18em] text-on-variant uppercase sm:block">Municipal Transit Log</span>
            </span>
          </Link>
          <nav aria-label="主导航" className="hidden items-center gap-1 border-l border-outline-variant/50 pl-4 md:flex">
            {desktopNav("/", "城市探索", active === "cities")}
            {desktopNav("/topics", "车型专题", active === "topics")}
            {desktopNav("/me", "我的足迹", active === "me")}
          </nav>
          <div className="ml-auto flex min-w-0 items-center gap-2">
            {session.status === "signed-in" ? (
              <>
                <span className="hidden items-center gap-1.5 rounded-full bg-surface-lowest px-2.5 py-1 shadow-card lg:flex">
                  <span className="size-1.5 rounded-full bg-brand" aria-hidden />
                  <Eyebrow>OzID connected</Eyebrow>
                </span>
                <span className="max-w-24 truncate text-sm font-semibold sm:max-w-40">{session.account.displayName}</span>
                {ozid.enabled ? (
                  <a
                    href={ozid.home}
                    target="_blank"
                    rel="noopener noreferrer"
                    data-testid="ozid-link-home"
                    aria-label="账号（在新标签页打开 OzID）"
                    title="OzID 账号"
                    className="flex size-9 shrink-0 items-center justify-center rounded-full bg-deep text-white transition-opacity hover:opacity-90"
                  >
                    <User className="size-[18px]" weight="bold" aria-hidden />
                  </a>
                ) : null}
                <Button variant="outline" size="sm" className="rounded-lg" onClick={() => void signOut()}>
                  退出
                </Button>
              </>
            ) : session.status === "anonymous" ? (
              <Button size="sm" className="rounded-lg" disabled={signingIn} onClick={signIn}>
                登录
              </Button>
            ) : session.status === "unavailable" ? (
              <Button size="sm" variant="outline" className="rounded-lg" onClick={() => void refresh()}>
                重新检查
              </Button>
            ) : null}
          </div>
        </div>
      </header>
      {deletionPending && session.status !== "signed-in" ? <PendingDeletion /> : null}
      {session.status === "unavailable" || (session.status === "signed-in" && session.unverified) ? (
        <div className={cn(container, "mt-4")} data-testid="session-unverified">
          <Alert kind="info">
            暂时无法确认登录状态{session.status === "signed-in" ? "，先按已登录的账号显示" : ""}。这不代表已退出，可以稍后重新检查。
            <Button size="sm" variant="outline" className="ml-3 rounded-lg" onClick={() => void refresh()}>
              重新检查
            </Button>
          </Alert>
        </div>
      ) : null}
      {notice ? (
        <div className={cn(container, "mt-4")} data-testid="session-notice">
          <Alert kind={notice.kind}>
            {notice.text}
            <Button size="sm" variant="ghost" className="ml-3" onClick={dismissNotice}>
              知道了
            </Button>
          </Alert>
        </div>
      ) : null}
      <main className="w-full flex-1 pb-24 md:pb-0">{children}</main>
      <footer className="mt-10 hidden bg-surface-low md:block">
        <div className={cn(container, "flex flex-wrap items-center justify-between gap-4 py-8")}>
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2.5">
              <span className="font-headline text-lg font-bold text-ink">地铁足迹</span>
              <span className="rounded-full bg-brand-container px-2 py-0.5 font-code text-[11px] font-medium text-on-brand-container">METRO</span>
            </div>
            <p className="text-sm text-on-variant">线路数据由管理员维护，仅供参考。记录每一次穿梭城市的轨道记忆。</p>
          </div>
          {ozid.enabled ? (
            <a
              href={ozid.home}
              target="_blank"
              rel="noopener noreferrer"
              className="rounded-lg bg-surface-lowest px-4 py-2.5 text-sm text-on-variant shadow-card hover:text-ink"
            >
              统一通行凭证：OzID 独立账号服务支持 <span className="font-semibold text-ink">管理账号 →</span>
            </a>
          ) : null}
        </div>
      </footer>
      <p className="px-4 pt-6 pb-2 text-center text-xs text-on-variant md:hidden">线路数据由管理员维护，仅供参考。</p>
      <nav aria-label="底部导航" className="pb-safe fixed inset-x-0 bottom-0 z-40 bg-surface/95 shadow-[0_-1px_12px_rgba(18,28,42,0.06)] backdrop-blur-md md:hidden">
        <div className="mx-auto flex h-16 max-w-md items-stretch px-2">
          {tab("/", "探索", "城市探索", active === "cities", <Compass className="size-[22px]" weight={active === "cities" ? "fill" : "regular"} aria-hidden />)}
          {tab("/topics", "车型", "车型专题", active === "topics", <Train className="size-[22px]" weight={active === "topics" ? "fill" : "regular"} aria-hidden />)}
          {tab("/me", "足迹", "我的足迹", active === "me", <Path className="size-[22px]" weight={active === "me" ? "fill" : "regular"} aria-hidden />)}
        </div>
      </nav>
    </div>
  );
}
