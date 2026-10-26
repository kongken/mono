import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { heldToken, releaseToken, type LandingKind } from "./fragment.ts";
import { mailHandOffUrl } from "./navigation.ts";

type HandOffProps = {
  kind: LandingKind;
  appName: string;
  /** The build's OzID origin (`VITE_OZID_ORIGIN`), or null for a build that has none. The only place this page leads to. */
  ozidOrigin: string | null;
  /** Where "back to the start page" leads when there is no OzID. */
  home?: string;
  /** Navigation; the page replaces itself, so "back" does not return to a link whose token is spent. Replaceable in tests. */
  go?: (url: string) => void;
};

/**
 * What a product shows at the two paths of the account mails sent before OzID took the account over
 * (`/account/verify-email`, `/account/reset-password`). The product no longer has a form for them: it
 * hands the browser, and the one-time token, to the user center's page of the same name, which completes
 * the registration or the reset, or says the link is spent and asks for a new one.
 *
 * The token was moved out of the address bar into memory before anything else ran (`captureLandingToken`).
 * It goes only into the fragment of the fixed OzID address (`mailHandOffUrl`), never into a query, a
 * request, a log or storage; nothing in this page's URL chooses the destination; no request is made, and
 * the page loads no business or analytics script. A page without a token hands over without one, so OzID
 * answers it the way it answers any unusable link.
 */
const replaceLocation = (url: string) => window.location.replace(url);

export function MailHandOff({ kind, appName, ozidOrigin, home = "/", go = replaceLocation }: HandOffProps) {
  const [token] = useState(heldToken);
  const target = ozidOrigin === null ? null : mailHandOffUrl(ozidOrigin, kind, token);
  useEffect(() => {
    if (target === null) return;
    go(target);
    releaseToken();
  }, [target, go]);
  return (
    <main className="mx-auto grid min-h-screen w-full max-w-md content-start gap-5 px-4 py-10" data-testid={`handoff-${kind}`}>
      <p className="text-sm text-muted-foreground">{appName}</p>
      {target === null ? (
        <div className="grid gap-4" data-testid="handoff-unavailable">
          <h1 className="text-xl font-semibold">这个链接不能在这里使用</h1>
          <p className="text-sm text-muted-foreground">账号的注册验证与密码重置在用户中心完成，而这个页面没有配置用户中心的地址。请从邮件里的链接重新进入，或回到首页。</p>
          <div>
            <Button asChild>
              <a href={home}>回到首页</a>
            </Button>
          </div>
        </div>
      ) : (
        <div className="grid gap-4" data-testid="handoff-pending">
          <h1 className="text-xl font-semibold">正在转到用户中心</h1>
          <p className="text-sm text-muted-foreground">{kind === "verify-email" ? "邮箱验证" : "密码重置"}现在统一在用户中心完成。没有自动打开的话，请点下面的按钮。</p>
          <div>
            <Button asChild>
              <a href={target} data-testid="handoff-link" rel="noreferrer">
                前往用户中心
              </a>
            </Button>
          </div>
        </div>
      )}
    </main>
  );
}
