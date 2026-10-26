import { useEffect, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import type { AccountProfile } from "@kongken/account/api";
import { navigate } from "@/lib/router";
import { useSession } from "@/lib/session";
import { Spinner } from "./Layout";

/**
 * The gate of the self-service pages. A valid session shows the page; no session (never
 * signed in, signed out, expired, revoked, password changed) goes to sign-in; and a failure
 * to ask is shown as that failure, with a retry, never as a sign-out: nothing about the
 * account or the sessions is concluded from an outage.
 */
export function RequireSession({ children }: { children: (profile: AccountProfile) => ReactNode }) {
  const { session, refresh } = useSession();
  useEffect(() => {
    if (session.status === "anonymous") navigate("/login", true);
  }, [session.status]);

  if (session.status === "loading" || session.status === "anonymous") return <Spinner />;
  if (session.status === "unavailable")
    return (
      <div className="grid max-w-md gap-4" data-testid="session-unavailable">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );
  return <>{children(session.profile)}</>;
}
