import { useState, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { ShieldCheck } from "@phosphor-icons/react";
import { Link, usePath } from "@/lib/router";
import { ozidHome } from "@/lib/ozid";
import { useRoles, useSession } from "@/lib/session";
import { sectionsFor } from "@/lib/sections";

export function Layout({ children }: { children: ReactNode }) {
  const { session, signOut } = useSession();
  const roles = useRoles();
  const path = usePath();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
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
  const sections = sectionsFor(roles);
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b bg-card">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3">
          <Link to="/" className="flex items-center gap-2 rounded-sm text-lg font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
            <ShieldCheck className="size-6 text-primary" weight="bold" aria-hidden /> 管理控制台
          </Link>
          <div className="ml-auto flex items-center gap-3">
            {session.status === "signed-in" ? (
              <>
                <span className="hidden max-w-40 truncate text-sm sm:inline" data-testid="header-name">{session.profile.user.displayName}</span>
                <a
                  href={ozidHome()}
                  className="rounded-sm text-sm text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
                  data-testid="manage-ozid"
                >
                  管理 OzID 账号
                </a>
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void leave()}>
                  {busy ? "退出中…" : "退出登录"}
                </Button>
              </>
            ) : null}
          </div>
        </div>
        {sections.length > 0 ? (
          <nav aria-label="分区" className="mx-auto flex max-w-5xl gap-1 overflow-x-auto px-4 pb-2 text-sm" data-testid="sections">
            {sections.map((s) => (
              <Link
                key={s.to}
                to={s.to}
                aria-current={path === s.to || path.startsWith(`${s.to}/`) ? "page" : undefined}
                className="whitespace-nowrap rounded-md px-3 py-1.5 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-[current=page]:bg-muted aria-[current=page]:font-medium aria-[current=page]:text-foreground"
              >
                {s.label}
              </Link>
            ))}
          </nav>
        ) : null}
        {failed ? (
          <div className="mx-auto max-w-5xl px-4 pb-3">
            <Notice kind="error">退出登录没有完成，请检查网络后重试。</Notice>
          </div>
        ) : null}
      </header>
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-8">{children}</main>
      <footer className="border-t bg-card">
        <p className="mx-auto max-w-5xl px-4 py-3 text-xs text-muted-foreground">管理功能只在受控网络内可用；账号的登录、改密与注销在 OzID 完成。</p>
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
