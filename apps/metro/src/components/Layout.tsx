import { useState, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { Link, usePath } from "@/lib/router";
import { useSession } from "@/lib/session";
import { cn } from "@kongken/ui/utils";
import { describeAccountError } from "@kongken/account/errors";
import { Train } from "@phosphor-icons/react";
import { account } from "@/lib/rpc";
import { navigate } from "@/lib/router";
import { Alert } from "@/components/extras";

/**
 * A deletion started here that the server has not confirmed finished: the account is
 * already closed, the data cleanup continues, and this offers to push it along.
 */
function PendingDeletion() {
  const { setDeletionPending } = useSession();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const retry = async () => {
    setBusy(true);
    setMessage("");
    try {
      const status = await account.retryDeletion();
      if (status.completed) {
        setDeletionPending(false);
        navigate("/login?deleted=1", true);
      } else {
        setMessage("注销仍在进行，部分数据还在清理，请稍后再试。");
      }
    } catch (e) {
      setMessage(describeAccountError(e, "delete"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mx-auto mt-4 w-full max-w-5xl px-4">
      <Alert kind="error">
        <p>账号注销尚未完成：账号已关闭，数据清理需要重试，进度不会丢失。</p>
        {message ? <p className="mt-1">{message}</p> : null}
        <Button size="sm" variant="destructive" className="mt-2" disabled={busy} onClick={() => void retry()}>
          {busy ? "重试中…" : "重试注销"}
        </Button>
      </Alert>
    </div>
  );
}

export function Layout({ children }: { children: ReactNode }) {
  const { session, signIn, signOut, deletionPending } = useSession();
  const path = usePath();
  const nav = (to: string, label: string, active: boolean) => (
    <Link
      to={to}
      aria-current={active ? "page" : undefined}
      className={cn("rounded-md px-3 py-1.5 text-sm hover:bg-accent", active && "bg-accent font-medium text-accent-foreground")}
    >
      {label}
    </Link>
  );
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b bg-card">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
          <Link to="/" className="flex items-center gap-2 text-lg font-semibold">
            <Train className="size-5 text-primary" weight="bold" aria-hidden /> 地铁足迹
          </Link>
          <nav aria-label="主导航" className="flex flex-1 flex-wrap items-center gap-1">
            {nav("/", "城市", path === "/" || path.startsWith("/cities") || path.startsWith("/topics"))}
            {session.status === "signed-in" && nav("/me", "我的足迹", path === "/me")}
            {session.status === "signed-in" && nav("/account", "账号", path === "/account")}
          </nav>
          <div className="flex items-center gap-2">
            {session.status === "signed-in" ? (
              <>
                <span className="max-w-32 truncate text-sm text-muted-foreground">{session.account.displayName}</span>
                <Button variant="outline" size="sm" onClick={() => void signOut()}>
                  退出
                </Button>
              </>
            ) : session.status === "anonymous" ? (
              <Button size="sm" onClick={signIn}>
                登录
              </Button>
            ) : null}
          </div>
        </div>
      </header>
      {deletionPending && session.status !== "signed-in" ? <PendingDeletion /> : null}
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-6">{children}</main>
      <footer className="border-t py-4 text-center text-xs text-muted-foreground">线路数据由管理员维护，仅供参考。</footer>
    </div>
  );
}
