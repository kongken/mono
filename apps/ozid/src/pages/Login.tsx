import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { AuthPanel } from "@kongken/account/forms";
import { Notice } from "@kongken/account/ui";
import { account } from "@/lib/rpc";
import { navigate, useSearch } from "@/lib/router";
import { pageOf } from "@/lib/routes";
import { useSession } from "@/lib/session";
import { goToTarget, useTarget, type TargetState } from "@/lib/target";
import { Spinner } from "@/components/Layout";

/** What the user is told when the requested return address was not accepted. */
export function TargetNotice({ target, action }: { target: TargetState; action: string }) {
  if (target.status === "refused")
    return <Notice kind="error">无法确认要返回的应用或页面，因此不会跳转。{action}</Notice>;
  if (target.status === "unavailable")
    return <Notice kind="error">暂时无法确认要返回的应用或页面，因此不会跳转。{action}</Notice>;
  return null;
}

/**
 * Sign in with email and password at the shared BFF, then go back to where the user came
 * from (an address the BFF accepted) or to the account overview. A valid session already
 * in this browser only needs a click to continue; switching account means signing out of
 * the shared session first.
 */
export default function LoginPage() {
  const { session, refresh, signOut } = useSession();
  const search = useSearch();
  const target = useTarget(search);
  const reason = new URLSearchParams(search).get("reason");
  const [switching, setSwitching] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    document.title = "登录 · OzID";
  }, []);

  const proceed = () => {
    if (target.status === "ok") goToTarget(target.target);
    else navigate("/");
  };

  if (session.status === "loading" || target.status === "resolving") return <Spinner />;
  if (session.status === "unavailable")
    return (
      <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8">
        <Notice kind="error">{session.message}。这不代表你已退出登录。</Notice>
        <Button onClick={() => void refresh()}>重试</Button>
      </div>
    );

  if (session.status === "signed-in" && !switching) {
    const name = session.profile.user.displayName;
    return (
      <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8" data-testid="signed-in-here">
        <TargetNotice target={target} action="可以继续进入账号概览。" />
        <section aria-labelledby="signed-in-title" className="grid gap-3">
          <h1 id="signed-in-title" className="text-lg font-semibold">已登录</h1>
          <p className="text-sm text-muted-foreground">
            这个浏览器已经以 <strong className="text-foreground">{name}</strong> 登录。
            {target.status === "ok" ? "继续回到刚才的应用。" : "可以进入账号概览。"}
          </p>
          <Button onClick={proceed}>{target.status === "ok" ? "继续" : "进入账号概览"}</Button>
          <Button variant="outline" onClick={() => setSwitching(true)}>使用其他账号登录</Button>
        </section>
      </div>
    );
  }

  if (session.status === "signed-in" && switching) {
    return (
      <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8" data-testid="switch-account">
        <h1 className="text-lg font-semibold">使用其他账号登录</h1>
        <Notice kind="info">
          账号在已接入的产品间共用，所以切换账号会退出这个浏览器上<strong>所有应用</strong>的当前登录；其他设备不受影响。
        </Notice>
        {error ? <Notice kind="error">{error}</Notice> : null}
        <Button
          disabled={busy}
          onClick={() => {
            setBusy(true);
            setError("");
            signOut()
              .then(() => setSwitching(false))
              .catch(() => setError("退出登录没有完成，请检查网络后重试。"))
              .finally(() => setBusy(false));
          }}
        >
          {busy ? "退出中…" : "退出并切换账号"}
        </Button>
        <Button variant="outline" onClick={() => setSwitching(false)}>取消</Button>
      </div>
    );
  }

  return (
    <div className="mx-auto grid max-w-md gap-4">
      {reason === "expired" ? <Notice kind="info">登录已失效，请重新登录。</Notice> : null}
      {reason === "password-changed" ? (
        <Notice kind="success">密码已修改。所有设备上的登录都已退出，请用新密码重新登录。</Notice>
      ) : null}
      <TargetNotice target={target} action="登录后将进入账号概览。" />
      <div className="rounded-xl bg-card p-6 shadow-sm sm:p-8">
        <AuthPanel
          api={account}
          appName="OzID"
          onSwitchMode={(mode) => navigate(pageOf(mode, search))}
          intro="使用邮箱和密码登录。同一个账号可在已接入的产品间通用。"
          onSignedIn={async () => {
            await refresh(true);
            proceed();
          }}
        />
      </div>
    </div>
  );
}
