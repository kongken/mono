import { useEffect } from "react";
import { Button } from "@kongken/ui/button";
import { ozid } from "@/lib/account-center";
import { useSession } from "@/lib/session";
import { Link } from "@/lib/router";
import { Alert, PageTitle, Spinner } from "@/components/extras";

/**
 * The product's old account addresses, `/login` and `/account`. They are not pages of an account any more: sign-in,
 * registration, password reset, profile, password, devices and closing the account are all OzID's. Old bookmarks
 * and links still land here and get one explanation and one way on. Nothing in the URL is read or followed (no
 * `next`, no `mode`, no address of any kind); the query is dropped from the address bar. Nothing redirects by
 * itself, so no step of this page can start a sign-in loop.
 */
export default function LegacyAccountPage({ kind }: { kind: "login" | "account" }) {
  const { session, signIn, signingIn } = useSession();
  useEffect(() => {
    if (window.location.search || window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  }, []);
  const signedIn = session.status === "signed-in";
  return (
    <div className="grid max-w-xl gap-4" data-testid="legacy-account">
      <PageTitle>{kind === "login" ? "登录已迁移到 OzID" : "账号设置已迁移到 OzID"}</PageTitle>
      <p className="text-sm text-muted-foreground">
        登录、注册、找回密码、昵称与头像、修改密码、登录设备和注销账号，现在都在统一的用户中心 OzID 完成，地铁足迹与山海足迹使用同一个账号。
      </p>
      {session.status === "loading" ? <Spinner /> : null}
      {!ozid.enabled ? <Alert>账号中心尚未配置，暂时无法使用。</Alert> : null}
      {ozid.enabled && kind === "login" && !signedIn ? (
        <div>
          <Button disabled={signingIn || session.status === "loading"} onClick={signIn} data-testid="legacy-sign-in">
            前往 OzID 登录
          </Button>
        </div>
      ) : null}
      {ozid.enabled && kind === "login" && signedIn ? (
        <p className="text-sm">
          你已登录。<Link to="/me" className="text-primary underline">回到我的足迹</Link>
        </p>
      ) : null}
      {ozid.enabled && kind === "account" ? (
        <p className="text-sm">
          <a href={ozid.home} target="_blank" rel="noopener noreferrer" className="text-primary underline" data-testid="legacy-ozid-home">
            前往 OzID 管理账号
          </a>
        </p>
      ) : null}
      <p className="text-sm">
        <Link to="/" className="text-primary underline">回到城市列表</Link>
      </p>
    </div>
  );
}
