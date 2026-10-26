import { useState, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { UserCircle } from "@phosphor-icons/react";
import { Link, usePath } from "@/lib/router";
import { useSession } from "@/lib/session";

const NAV = [
  { to: "/", label: "概览" },
  { to: "/profile", label: "资料" },
  { to: "/security", label: "安全" },
  { to: "/sessions", label: "设备" },
  { to: "/close-account", label: "注销账号" },
];

export function Layout({ children }: { children: ReactNode }) {
  const { session, signOut } = useSession();
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
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b bg-card">
        <div className="mx-auto flex max-w-3xl items-center gap-3 px-4 py-3">
          <Link to="/" className="flex items-center gap-2 rounded-sm text-lg font-semibold outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
            <UserCircle className="size-6 text-primary" weight="bold" aria-hidden /> OzID
          </Link>
          <span className="hidden text-sm text-muted-foreground sm:inline">山海足迹、地铁足迹共用的账号</span>
          <div className="ml-auto flex items-center gap-3">
            {session.status === "signed-in" && path !== "/close-account" ? (
              <>
                <span className="max-w-40 truncate text-sm" data-testid="header-name">{session.profile.user.displayName}</span>
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void leave()}>
                  {busy ? "退出中…" : "退出登录"}
                </Button>
              </>
            ) : null}
          </div>
        </div>
        {session.status === "signed-in" ? (
          <nav aria-label="账号" className="mx-auto flex max-w-3xl gap-1 overflow-x-auto px-4 pb-2 text-sm">
            {NAV.map((item) => (
              <Link
                key={item.to}
                to={item.to}
                aria-current={path === item.to ? "page" : undefined}
                className="whitespace-nowrap rounded-md px-3 py-1.5 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-[current=page]:bg-muted aria-[current=page]:font-medium aria-[current=page]:text-foreground"
              >
                {item.label}
              </Link>
            ))}
          </nav>
        ) : null}
        {failed ? (
          <div className="mx-auto max-w-3xl px-4 pb-3">
            <Notice kind="error">退出登录没有完成，请检查网络后重试。</Notice>
          </div>
        ) : null}
      </header>
      <main className="mx-auto w-full max-w-3xl flex-1 px-4 py-8">{children}</main>
      <footer className="border-t bg-card">
        <p className="mx-auto max-w-3xl px-4 py-3 text-xs text-muted-foreground">OzID 是各产品共用的账号：登录一次，已接入的产品都能识别你。</p>
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
