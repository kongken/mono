import { useEffect } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { Link, navigate } from "@/lib/router";
import { useSession } from "@/lib/session";
import { Spinner } from "@/components/Layout";

/** The account overview: where OzID ends when it was opened directly. */
export default function OverviewPage() {
  const { session, refresh } = useSession();
  useEffect(() => {
    document.title = "账号概览 · OzID";
  }, []);
  useEffect(() => {
    if (session.status === "anonymous") navigate("/login", true);
  }, [session.status]);

  if (session.status === "loading" || session.status === "anonymous") return <Spinner />;
  if (session.status === "unavailable")
    return (
      <div className="grid max-w-md gap-4">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );
  const { user, email } = session.profile;
  return (
    <div className="grid gap-6" data-testid="overview">
      <h1 className="text-xl font-semibold">账号概览</h1>
      <dl className="grid gap-3 rounded-lg border bg-card p-4 text-sm sm:grid-cols-[8rem_1fr]">
        <dt className="text-muted-foreground">昵称</dt>
        <dd data-testid="overview-name">{user.displayName}</dd>
        <dt className="text-muted-foreground">登录邮箱</dt>
        <dd className="break-all" data-testid="overview-email">{email}</dd>
      </dl>
      <section aria-labelledby="security-title" className="grid gap-2 rounded-lg border bg-card p-4">
        <h2 id="security-title" className="text-base font-semibold">安全</h2>
        <p className="text-sm text-muted-foreground">敏感操作前，可以再次确认当前密码。</p>
        <div>
          <Link to="/security" className="text-sm text-primary underline-offset-4 hover:underline">确认密码</Link>
        </div>
      </section>
    </div>
  );
}
