import { useEffect } from "react";
import { AuthPanel, type AuthMode } from "@kongken/account/forms";
import { account } from "@/lib/rpc";
import { navigate } from "@/lib/router";
import { safeNext } from "@/lib/next";
import { useSession } from "@/lib/session";
import { Alert, Spinner } from "@/components/extras";

const APP_NAME = "地铁足迹";

/** Sign in, request registration or reset a password; the mailed links come back to /account/... pages. */
export default function LoginPage() {
  const { session, refresh } = useSession();
  const params = new URLSearchParams(window.location.search);
  const next = safeNext(params.get("next"));
  const mode: AuthMode = params.get("mode") === "register" ? "register" : "login";

  // Already signed in (for example in another tab): nothing to do here.
  useEffect(() => {
    if (session.status === "signed-in") navigate(next, true);
  }, [session.status, next]);

  if (session.status === "loading" || session.status === "signed-in") return <Spinner />;
  return (
    <div className="mx-auto max-w-md">
      {params.get("changed") === "1" ? <Alert kind="success" className="mb-4">密码已修改，所有设备都已退出登录。请用新密码重新登录。</Alert> : null}
      {params.get("deleted") === "1" ? <Alert kind="success" className="mb-4">账号已注销，共用账号和所有产品的数据都已删除。</Alert> : null}
      <AuthPanel
        api={account}
        appName={APP_NAME}
        initialMode={mode}
        onSignedIn={async () => {
          await refresh();
          navigate(next);
        }}
      />
    </div>
  );
}
