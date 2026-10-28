import { useState, type ComponentType, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { cn } from "@kongken/ui/utils";
import { Notice } from "@kongken/account/ui";
import { Devices, Lock, ShieldCheck, SquaresFour, User, UserCircle, Warning, type IconProps } from "@phosphor-icons/react";
import { Link, usePath } from "@/lib/router";
import { useSession } from "@/lib/session";

type NavItem = { to: string; label: string; icon: ComponentType<IconProps>; danger?: boolean };

const NAV: NavItem[] = [
  { to: "/", label: "概览", icon: SquaresFour },
  { to: "/profile", label: "资料", icon: User },
  { to: "/security", label: "安全", icon: ShieldCheck },
  { to: "/sessions", label: "设备", icon: Devices },
];
const NAV_DANGER: NavItem[] = [{ to: "/close-account", label: "注销账号", icon: Warning, danger: true }];

const focus = "outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50";

function NavLink({ item, path }: { item: NavItem; path: string }) {
  const Icon = item.icon;
  return (
    <Link
      to={item.to}
      aria-current={path === item.to ? "page" : undefined}
      className={cn(
        "flex items-center gap-2.5 whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium text-muted-foreground transition-colors lg:py-2.5",
        focus,
        item.danger
          ? "hover:bg-destructive/10 hover:text-destructive aria-[current=page]:bg-destructive/10 aria-[current=page]:text-destructive"
          : "hover:bg-accent hover:text-foreground aria-[current=page]:bg-brand aria-[current=page]:font-semibold aria-[current=page]:text-brand-foreground aria-[current=page]:shadow-sm",
      )}
    >
      <Icon className="size-[18px] shrink-0" weight="bold" aria-hidden />
      <span>{item.label}</span>
    </Link>
  );
}

function Brand({ className }: { className?: string }) {
  return (
    <Link to="/" className={cn("flex shrink-0 items-center gap-2.5 rounded-lg", focus, className)}>
      <span className="grid size-9 place-items-center rounded-xl bg-brand text-brand-foreground">
        <UserCircle className="size-6" weight="bold" aria-hidden />
      </span>
      <span className="flex flex-col leading-tight">
        <span className="text-base font-semibold tracking-tight text-primary">OzID</span>
        <span className="text-[11px] font-medium text-muted-foreground">统一身份中心</span>
      </span>
    </Link>
  );
}

export function Layout({ children }: { children: ReactNode }) {
  const { session, signOut } = useSession();
  const path = usePath();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const signedIn = session.status === "signed-in";
  const leave = async () => {
    setBusy(true);
    setFailed(false);
    try {
      await signOut();
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className={cn("flex min-h-screen flex-col", signedIn && "lg:pl-60")}>
      <a href="#main-content" className="sr-only z-50 rounded-md bg-card p-3 text-primary focus:not-sr-only focus:fixed focus:left-4 focus:top-4">跳到主要内容</a>
      {signedIn ? (
        <aside className="fixed inset-y-0 left-0 z-40 hidden w-60 flex-col justify-between gap-6 overflow-y-auto bg-sidebar px-4 py-6 shadow-[0_1px_8px_rgba(0,0,0,0.04)] lg:flex">
          <div className="grid gap-6">
            <Brand className="px-2" />
            <nav aria-label="账号" className="grid gap-1">
              {NAV.map((item) => (
                <NavLink key={item.to} item={item} path={path} />
              ))}
            </nav>
          </div>
          <div className="grid gap-2 border-t border-border pt-4">
            <p className="px-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">账号操作</p>
            <nav aria-label="危险操作" className="grid gap-1">
              {NAV_DANGER.map((item) => (
                <NavLink key={item.to} item={item} path={path} />
              ))}
            </nav>
          </div>
        </aside>
      ) : null}
      <header className="sticky top-0 z-30 border-b border-border/60 bg-background/85 backdrop-blur-xl">
        <div className={cn("mx-auto flex min-h-16 items-center gap-3 px-4 py-3 sm:px-8", signedIn ? "max-w-5xl" : "max-w-3xl")}>
          <Brand className={cn(signedIn && "lg:hidden")} />
          <span className={cn("hidden text-sm text-muted-foreground", signedIn ? "md:inline lg:hidden" : "sm:inline")}>
            山海足迹、地铁足迹共用的账号
          </span>
          <div className="ml-auto flex min-w-0 items-center gap-3">
            {signedIn ? (
              <>
                <span className="hidden items-center gap-1.5 text-xs font-medium text-muted-foreground sm:flex">
                  <Lock className="size-4 text-signal" weight="bold" aria-hidden />
                  安全托管中
                </span>
                {path !== "/close-account" ? (
                  <>
                    <span className="max-w-40 truncate text-sm font-medium" data-testid="header-name">
                      {session.profile.user.displayName}
                    </span>
                    <Button size="sm" variant="outline" disabled={busy} onClick={() => void leave()}>
                      {busy ? "退出中…" : "退出登录"}
                    </Button>
                  </>
                ) : null}
              </>
            ) : null}
          </div>
        </div>
        {signedIn ? (
          <nav aria-label="账号" className="mx-auto flex max-w-5xl gap-1 overflow-x-auto px-4 pb-2 sm:px-8 lg:hidden">
            {[...NAV, ...NAV_DANGER].map((item) => (
              <NavLink key={item.to} item={item} path={path} />
            ))}
          </nav>
        ) : null}
        {failed ? (
          <div className="mx-auto max-w-5xl px-4 pb-3 sm:px-8">
            <Notice kind="error">退出登录没有完成，请检查网络后重试。</Notice>
          </div>
        ) : null}
      </header>
      <main id="main-content" tabIndex={-1} className={cn("mx-auto w-full flex-1 scroll-mt-32 px-4 py-8 outline-none sm:px-8", signedIn ? "max-w-5xl" : "max-w-3xl")}>{children}</main>
      <footer className="border-t border-border/60">
        <p className={cn("mx-auto px-4 py-4 text-xs text-muted-foreground sm:px-8", signedIn ? "max-w-5xl" : "max-w-3xl")}>
          OzID 是各产品共用的账号：登录一次，已接入的产品都能识别你。
        </p>
      </footer>
    </div>
  );
}

export function Spinner({ label = "加载中" }: { label?: string }) {
  return (
    <div role="status" className="py-8 text-sm text-muted-foreground">
      {label}…
    </div>
  );
}

/** The heading block shared by the self-service pages. */
export function PageHeader({ eyebrow, title, children, danger = false }: { eyebrow?: string; title: string; children?: ReactNode; danger?: boolean }) {
  return (
    <header className="grid gap-1.5">
      {eyebrow ? <p className="text-[11px] font-semibold uppercase tracking-wider text-signal">{eyebrow}</p> : null}
      <h1 className={cn("text-2xl font-semibold tracking-tight text-primary sm:text-3xl", danger && "text-destructive")}>{title}</h1>
      {children ? <p className="text-sm leading-relaxed text-muted-foreground">{children}</p> : null}
    </header>
  );
}
