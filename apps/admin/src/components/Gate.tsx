import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { goToOzid, ozidHome } from "@/lib/ozid";
import { useSession } from "@/lib/session";
import { isAdmin } from "@/lib/sections";
import { Spinner } from "./Layout";

/**
 * The gate of every console page. A valid session with a role shows the page; no session (never
 * signed in, signed out, expired, revoked, password changed) goes to OzID to sign in and comes
 * back to the same page; a failure to ask is shown as that failure, with a retry, never as a
 * sign-out; and a signed-in account without the role gets "no permission" with no redirect at all,
 * so there is nothing to loop on.
 *
 * `role` is the role the page needs ("" for any administrator role). This gate decides what is
 * shown; every call behind it is checked again by the server.
 */
export function Gate({ role, children }: { role: string; children: ReactNode }) {
  const { session, refresh } = useSession();
  useEffect(() => {
    if (session.status === "anonymous") goToOzid("login");
  }, [session.status]);

  if (session.status === "loading") return <Spinner />;
  if (session.status === "anonymous") return <Spinner label="正在前往 OzID 登录" />;
  if (session.status === "unavailable")
    return (
      <div className="grid max-w-md gap-4" data-testid="session-unavailable">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );
  const roles = session.profile.roles;
  if (!(role === "" ? isAdmin(roles) : roles.includes(role))) return <NoPermission />;
  return <>{children}</>;
}

/** A signed-in account that holds no role for this page. Nothing here asks the server for management data. */
export function NoPermission() {
  const { signOut } = useSession();
  const [failed, setFailed] = useState(false);
  return (
    <section className="grid max-w-md gap-4" data-testid="no-permission" aria-labelledby="no-permission-title">
      <h1 id="no-permission-title" className="text-xl font-semibold">没有权限</h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        当前登录的账号没有使用这个页面的权限。管理角色只能由运维授予，这里无法申请。
      </p>
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" asChild>
          <a href={ozidHome()}>管理 OzID 账号</a>
        </Button>
        <Button
          variant="outline"
          onClick={() => {
            setFailed(false);
            signOut().catch(() => setFailed(true));
          }}
        >
          换个账号登录
        </Button>
      </div>
      {failed ? <Notice kind="error">退出登录没有完成，请检查网络后重试。</Notice> : null}
    </section>
  );
}
