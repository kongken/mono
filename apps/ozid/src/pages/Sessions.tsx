import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { SessionsList } from "@kongken/account/forms";
import { Notice } from "@kongken/account/ui";
import { account } from "@/lib/rpc";
import { navigate } from "@/lib/router";
import { useSession } from "@/lib/session";
import { RequireSession } from "@/components/RequireSession";
import { PageHeader } from "@/components/Layout";

/**
 * The live browser sessions of the account and the two kinds of sign-out: other devices
 * (one atomic call, this device stays) and this browser (the shared session every connected
 * app reads on its next check). Neither can clear what another origin keeps in its own
 * browser storage, and the page says so.
 */
export default function SessionsPage() {
  const { refresh, signOut } = useSession();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    document.title = "设备与登录 · OzID";
  }, []);

  const leave = async () => {
    setBusy(true);
    setFailed(false);
    try {
      await signOut();
      navigate("/login", true);
    } catch {
      // Not signed out: the Cookie is still there. A failed sign-out is its own message.
      setFailed(true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <RequireSession>
      {({ user }) => (
        <div className="grid max-w-2xl gap-8" data-testid="sessions-page">
          <PageHeader eyebrow="OzID 统一身份管理" title="设备与登录">
            只显示服务器实际记录的登录时间、最近使用时间和有效期，不记录设备型号或所在位置。
          </PageHeader>
          <div className="rounded-xl bg-card p-6 shadow-sm sm:p-8">
            <SessionsList api={account} identity={user.id} revokeOthers onSignedOutHere={() => void refresh(true)} />
          </div>
          <section aria-labelledby="sign-out-here-title" className="grid gap-3 rounded-xl bg-card p-6 shadow-sm sm:p-8" data-testid="sign-out-here">
            <h2 id="sign-out-here-title" className="text-base font-semibold">退出这个浏览器</h2>
            <p className="text-sm text-muted-foreground">
              会结束这个浏览器上共用的登录：已接入的应用（山海足迹、地铁足迹等）在下一次检查登录状态时都会发现已退出。其他设备保持登录。
            </p>
            <p className="text-sm text-muted-foreground">
              OzID 无法、也不会清除各应用保存在这个浏览器里的数据（例如本地足迹、尚未同步的修改）；这些数据由各应用自己保留。
            </p>
            {failed ? <Notice kind="error">退出登录没有完成，你仍处于登录状态。请检查网络后重试。</Notice> : null}
            <div>
              <Button variant="outline" disabled={busy} onClick={() => void leave()}>
                {busy ? "退出中…" : "退出这个浏览器的登录"}
              </Button>
            </div>
          </section>
        </div>
      )}
    </RequireSession>
  );
}
