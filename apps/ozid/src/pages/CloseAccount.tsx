import { useEffect, useRef } from "react";
import { Button } from "@kongken/ui/button";
import { CloseAccountFlow } from "@kongken/account/close";
import { Notice } from "@kongken/account/ui";
import { account } from "@/lib/rpc";
import { navigate } from "@/lib/router";
import { useSession } from "@/lib/session";
import { Spinner } from "@/components/Layout";

/**
 * Closing the shared account. Unlike the other self-service pages this one is not behind
 * RequireSession: once the deletion has begun the account is closed and the session gone, yet the
 * page must keep showing the deletion and offering to continue it. The browser's Cookie is the
 * only credential for that, so the page asks it (read-only) when there is no session, and never
 * signs out or clears anything itself.
 *
 * Which of the two it is, is decided once, from the first answer about the session: a later
 * refresh (another tab, the page coming back to the front) that finds the session gone because the
 * deletion began must not restart the page.
 */
export default function CloseAccountPage() {
  const { session, refresh } = useSession();
  const mode = useRef<"start" | "resume" | null>(null);
  // The account the page was opened for; a later session refresh must not change it.
  const shown = useRef<{ id: string; label: string } | null>(null);
  useEffect(() => {
    document.title = "注销账号 · OzID";
  }, []);
  if (mode.current === null && session.status === "signed-in") {
    mode.current = "start";
    const { user, email } = session.profile;
    shown.current = { id: user.id, label: `${user.displayName}（${email}）` };
  }
  if (mode.current === null && session.status === "anonymous") mode.current = "resume";

  let body;
  if (mode.current) {
    body = <CloseAccountFlow api={account} mode={mode.current} account={shown.current ?? undefined} onNoSession={() => navigate("/login", true)} />;
  } else if (session.status === "unavailable") {
    body = (
      <div className="grid max-w-md gap-4" data-testid="session-unavailable">
        <Notice kind="error">{session.message}。这不代表你已退出登录，也不会开始注销。</Notice>
        <div>
          <Button onClick={() => void refresh()}>重试</Button>
        </div>
      </div>
    );
  } else {
    body = <Spinner />;
  }
  return (
    <div className="grid max-w-2xl gap-6" data-testid="close-account-page">
      <h1 className="text-2xl font-semibold tracking-tight text-destructive sm:text-3xl">注销账号</h1>
      {body}
    </div>
  );
}
