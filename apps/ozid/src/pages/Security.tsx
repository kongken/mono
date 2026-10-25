import { useCallback, useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { ReauthenticateForm } from "@kongken/account/forms";
import { Notice } from "@kongken/account/ui";
import { account } from "@/lib/rpc";
import { Link, navigate, useSearch } from "@/lib/router";
import { useSession } from "@/lib/session";
import { goToTarget, useTarget } from "@/lib/target";
import { Spinner } from "@/components/Layout";
import { TargetNotice } from "./Login";

/**
 * Password confirmation for a signed-in session. A product or the admin console sends the
 * browser here when a sensitive action needs a recent password check, and the user comes
 * back afterwards to an address the BFF accepted. Nothing that was being done before is
 * replayed: the user starts the action again.
 *
 * Only the session that is valid right now can be confirmed. If it is gone (expired,
 * signed out elsewhere, password or roles changed), the way on is signing in again.
 */
export default function SecurityPage() {
  const { session, refresh } = useSession();
  const search = useSearch();
  const target = useTarget(search);
  const [confirmed, setConfirmed] = useState(false);

  useEffect(() => {
    document.title = "确认密码 · OzID";
  }, []);

  const toLogin = useCallback(() => {
    // Keep the (still unvalidated) context so signing in leads back; the BFF checks it again there.
    const params = new URLSearchParams(search);
    params.set("reason", "expired");
    navigate(`/login?${params.toString()}`, true);
  }, [search]);
  useEffect(() => {
    if (session.status === "anonymous") toLogin();
  }, [session.status, toLogin]);

  if (session.status === "loading" || session.status === "anonymous" || target.status === "resolving") return <Spinner />;
  if (session.status === "unavailable")
    return (
      <div className="mx-auto grid max-w-md gap-4">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );

  if (confirmed) {
    return (
      <div className="mx-auto grid max-w-md gap-4" data-testid="reauthenticated">
        <Notice kind="success">密码已确认。</Notice>
        <Link to="/" className="text-sm text-primary underline-offset-4 hover:underline">进入账号概览</Link>
      </div>
    );
  }

  return (
    <div className="mx-auto grid max-w-md gap-4">
      <TargetNotice target={target} action="确认密码后将停留在 OzID。" />
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
}
