import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@kongken/ui/button";
import { ChangePasswordForm, ReauthenticateForm } from "@kongken/account/forms";
import { Notice } from "@kongken/account/ui";
import { readNavContext } from "@kongken/account/navigation";
import { account } from "@/lib/rpc";
import { Link, navigate, useSearch } from "@/lib/router";
import { useSession } from "@/lib/session";
import { goToTarget, useTarget } from "@/lib/target";
import { PageHeader, Spinner } from "@/components/Layout";
import { TargetNotice } from "./Login";

/**
 * Password changes and password confirmation, in two modes.
 *
 * With a navigation context in the address (`app_id`, `return_path`), a product or the admin
 * console sent the browser here because a sensitive action needs a recent password check. Only
 * the confirmation is shown; afterwards the user comes back to an address the BFF accepted and
 * starts the action again. Nothing that was being done before is replayed.
 *
 * Opened directly, the page offers the password change as well. A change ends every session of
 * the account, this device included, so it ends at sign-in with the new password.
 *
 * Only the session that is valid right now can be confirmed. If it is gone (expired, signed out
 * elsewhere, password or roles changed), the way on is signing in again.
 */
export default function SecurityPage() {
  const { session, refresh } = useSession();
  const search = useSearch();
  const target = useTarget(search);
  const controlled = readNavContext(search).kind !== "none";
  const [confirmed, setConfirmed] = useState(false);
  // Why the user is sent to sign-in: expired by default, or a password change they just made.
  const reason = useRef("expired");

  useEffect(() => {
    document.title = controlled ? "确认密码 · OzID" : "安全 · OzID";
  }, [controlled]);

  const toLogin = useCallback(() => {
    // Keep the (still unvalidated) context so signing in leads back; the BFF checks it again there.
    const params = new URLSearchParams(search);
    params.set("reason", reason.current);
    navigate(`/login?${params.toString()}`, true);
  }, [search]);
  useEffect(() => {
    if (session.status === "anonymous") toLogin();
  }, [session.status, toLogin]);

  if (session.status === "loading" || session.status === "anonymous" || target.status === "resolving") return <Spinner />;
  if (session.status === "unavailable")
    return (
      <div className="mx-auto grid max-w-md gap-4" data-testid="session-unavailable">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );

  const confirmation = confirmed ? (
    <div className="grid gap-4" data-testid="reauthenticated">
      <Notice kind="success">密码已确认。</Notice>
      {controlled ? <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">进入账号概览</Link> : null}
    </div>
  ) : (
    <div className="grid gap-4">
      {controlled ? <TargetNotice target={target} action="确认密码后将停留在 OzID。" /> : null}
      <ReauthenticateForm
        api={account}
        intro={target.status === "ok" ? "继续之前，请再次输入当前密码；确认后会回到刚才的应用，需要你重新发起刚才的操作。" : "请再次输入当前密码。"}
        onConfirmed={async () => {
          if (target.status === "ok") return goToTarget(target.target);
          await refresh(true);
          setConfirmed(true);
        }}
        onSessionInvalid={async () => {
          await refresh(true);
          toLogin();
        }}
      />
    </div>
  );

  if (controlled) return <div className="mx-auto max-w-md">{confirmation}</div>;

  return (
    <div className="grid max-w-2xl gap-8" data-testid="security-page">
      <PageHeader eyebrow="OzID 统一身份管理" title="安全">
        修改登录密码，或在继续敏感操作前再次确认当前密码。
      </PageHeader>
      <section className="grid gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8">
        <ChangePasswordForm
          api={account}
          email={session.profile.email}
          onSessionInvalid={() => void refresh(true)}
          onChanged={async () => {
            // The password changed and every session ended: this one too, and its Cookie is cleared.
            reason.current = "password-changed";
            await refresh(true);
            toLogin();
          }}
        />
      </section>
      <section className="rounded-xl bg-card p-6 shadow-sm sm:p-8">{confirmation}</section>
    </div>
  );
}
