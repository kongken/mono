import { Button } from "@kongken/ui/button";
import { classifyAccountError } from "@kongken/account/errors";
import { Notice } from "@kongken/account/ui";
import { useEffect } from "react";
import { goToOzid } from "@/lib/ozid";
import { useSession } from "@/lib/session";
import { NoPermission } from "./Gate";

/**
 * What a failed management call looks like. Each refusal keeps its meaning:
 * a lost session leads back to OzID sign-in; an old password check offers password confirmation at
 * OzID (it is a choice, not an automatic redirect, so a screen can never loop); a missing role is
 * "no permission"; the closed perimeter says only that the network or origin is not admitted.
 */
export function Problem({ error, retry }: { error: unknown; retry?: () => void }) {
  const { sessionLost } = useSession();
  const failure = classifyAccountError(error);
  useEffect(() => {
    if (failure.kind === "not_signed_in") sessionLost();
  }, [failure.kind, sessionLost]);

  switch (failure.kind) {
    case "forbidden":
      return <NoPermission />;
    case "reauth_required":
      return (
        <div className="grid max-w-lg gap-3" data-testid="problem-reauth">
          <Notice kind="info">{failure.message}。验证后会回到这个页面，当前输入不会被保留或重放。</Notice>
          <div>
            <Button onClick={() => goToOzid("security")}>前往 OzID 验证密码</Button>
          </div>
        </div>
      );
    case "not_signed_in":
      return <Notice kind="info">登录已失效，正在重新确认登录状态…</Notice>;
    default:
      return (
        <div className="grid max-w-lg gap-3" data-testid={`problem-${failure.kind}`}>
          <Notice kind="error">{failure.message}</Notice>
          {retry && failure.kind !== "not_allowed" ? (
            <div>
              <Button variant="outline" onClick={retry}>
                重试
              </Button>
            </div>
          ) : null}
        </div>
      );
  }
}
