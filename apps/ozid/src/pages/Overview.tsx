import { useEffect, useState, type ComponentType } from "react";
import { ArrowRight, Devices, IdentificationBadge, ShieldCheck, SignOut, type IconProps } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { AccountAvatar, Notice, useRunner } from "@kongken/account/ui";
import { Link } from "@/lib/router";
import { RequireSession } from "@/components/RequireSession";
import { PageHeader } from "@/components/Layout";
import { ProductLinks } from "@/components/ProductLinks";
import { useSession } from "@/lib/session";

type Section = { to: string; title: string; text: string; cta: string; icon: ComponentType<IconProps> };

const SECTIONS: Section[] = [
  { to: "/profile", title: "资料", text: "修改昵称和头像；登录邮箱只读。", cta: "进入资料设置", icon: IdentificationBadge },
  { to: "/security", title: "安全", text: "修改密码，或再次确认当前密码。", cta: "检查安全凭据", icon: ShieldCheck },
  { to: "/sessions", title: "设备与登录", text: "查看已登录的设备，退出其他设备或这个浏览器。", cta: "查看登录设备", icon: Devices },
];

/** The account overview: where OzID ends when it was opened directly. */
export default function OverviewPage() {
  const { signOut } = useSession();
  const { pending, run } = useRunner();
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    document.title = "账号概览 · OzID";
  }, []);
  return (
    <RequireSession>
      {({ user, email }) => (
        <div className="grid gap-8" data-testid="overview">
          <PageHeader eyebrow="OzID 统一身份管理" title="账号概览">
            一个账号，登录所有已接入的产品。
          </PageHeader>
          <section aria-label="当前账号" className="flex flex-wrap items-center justify-between gap-6 rounded-xl bg-card p-6 shadow-sm sm:p-8">
            <div className="flex min-w-0 items-center gap-5">
              <AccountAvatar name={user.displayName} className="bg-brand text-brand-foreground shadow-inner sm:size-20" />
              <dl className="grid min-w-0 gap-1 text-sm">
                <dt className="sr-only">昵称</dt>
                <dd className="break-all text-xl font-semibold text-foreground sm:text-2xl" data-testid="overview-name">{user.displayName}</dd>
                <dt className="sr-only">登录邮箱</dt>
                <dd className="break-all text-muted-foreground" data-testid="overview-email">{email}</dd>
              </dl>
            </div>
            <div className="flex flex-wrap gap-3">
              <Button asChild variant="outline"><Link to="/profile">修改资料</Link></Button>
              <Button asChild variant="outline"><Link to="/security">管理密码</Link></Button>
            </div>
          </section>
          <ProductLinks />
          <section className="grid gap-4" aria-labelledby="overview-manage">
            <div className="flex items-baseline justify-between gap-3">
              <h2 id="overview-manage" className="text-lg font-semibold">账号管理</h2>
              <span className="text-xs font-medium text-muted-foreground">快速导航</span>
            </div>
            <ul className="m-0 grid list-none gap-4 p-0 md:grid-cols-3">
              {SECTIONS.map((s) => (
                <li key={s.to} className="group relative flex flex-col justify-between gap-6 rounded-xl bg-card p-6 shadow-sm transition-shadow focus-within:ring-[3px] focus-within:ring-ring/50 hover:shadow-md">
                  <div className="grid gap-3">
                    <span
                      className="grid size-10 place-items-center rounded-lg bg-muted text-primary transition-colors group-hover:bg-brand group-hover:text-brand-foreground"
                    >
                      <s.icon className="size-[22px]" weight="bold" aria-hidden />
                    </span>
                    <div className="grid gap-1">
                      <h3 className="text-base font-semibold">
                        <Link
                          to={s.to}
                          className="rounded-sm text-foreground outline-none after:absolute after:inset-0 after:rounded-xl group-hover:text-primary"
                        >
                          {s.title}
                        </Link>
                      </h3>
                      <p className="text-sm leading-relaxed text-muted-foreground">{s.text}</p>
                    </div>
                  </div>
                  <span aria-hidden className="flex items-center gap-1 text-xs font-semibold text-primary">
                    {s.cta}
                    <ArrowRight className="size-3.5 motion-safe:transition-transform motion-safe:group-hover:translate-x-1" weight="bold" />
                  </span>
                </li>
              ))}
            </ul>
          </section>
          <section aria-labelledby="overview-sign-out" className="grid gap-4 rounded-xl bg-card p-6 shadow-sm sm:p-8">
            <h2 id="overview-sign-out" className="flex items-center gap-2 text-lg font-semibold"><SignOut aria-hidden />退出会话</h2>
            <p className="text-sm leading-relaxed text-muted-foreground">退出将结束这个浏览器中所有接入应用的当前登录，其他设备不受影响。各产品保存在本机的内容和未同步修改会保留。</p>
            {failed ? <Notice kind="error">退出登录没有完成，你仍处于登录状态。请检查网络后重试。</Notice> : null}
            <div><Button variant="outline" disabled={pending} onClick={() => void run(async () => {
              setFailed(false);
              try { await signOut(); } catch { setFailed(true); }
            })}>{pending ? "退出中…" : "退出当前浏览器登录"}</Button></div>
          </section>
          <section aria-labelledby="overview-close" className="flex flex-wrap items-center justify-between gap-4 rounded-xl border border-destructive/20 p-6">
            <div className="grid gap-1">
              <h2 id="overview-close" className="font-semibold text-destructive">永久删除账号</h2>
              <p className="text-sm text-muted-foreground">删除共用账号和各产品的数据，无法恢复。</p>
            </div>
            <Button asChild variant="outline"><Link to="/close-account" className="text-destructive">注销账号</Link></Button>
          </section>
        </div>
      )}
    </RequireSession>
  );
}
