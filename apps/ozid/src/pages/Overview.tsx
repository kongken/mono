import { useEffect, type ComponentType } from "react";
import { ArrowRight, Devices, IdentificationBadge, ShieldCheck, Warning, type IconProps } from "@phosphor-icons/react";
import { cn } from "@kongken/ui/utils";
import { Link } from "@/lib/router";
import { RequireSession } from "@/components/RequireSession";
import { PageHeader } from "@/components/Layout";

type Section = { to: string; title: string; text: string; cta: string; icon: ComponentType<IconProps>; danger?: boolean };

const SECTIONS: Section[] = [
  { to: "/profile", title: "资料", text: "修改昵称和头像；登录邮箱只读。", cta: "进入资料设置", icon: IdentificationBadge },
  { to: "/security", title: "安全", text: "修改密码，或再次确认当前密码。", cta: "检查安全凭据", icon: ShieldCheck },
  { to: "/sessions", title: "设备与登录", text: "查看已登录的设备，退出其他设备或这个浏览器。", cta: "查看登录设备", icon: Devices },
  { to: "/close-account", title: "注销账号", text: "永久删除共用账号和各产品的数据，无法恢复。", cta: "了解注销范围", icon: Warning, danger: true },
];

/** The account overview: where OzID ends when it was opened directly. */
export default function OverviewPage() {
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
          <section aria-label="当前账号" className="flex items-center gap-5 rounded-xl bg-card p-6 shadow-sm sm:p-8">
            <div
              aria-hidden
              className="grid size-16 shrink-0 select-none place-items-center rounded-full bg-brand text-lg font-semibold tracking-tight text-brand-foreground shadow-inner sm:size-20 sm:text-xl"
            >
              {initials(user.displayName)}
            </div>
            <dl className="grid min-w-0 gap-1 text-sm">
              <dt className="sr-only">昵称</dt>
              <dd className="truncate text-xl font-semibold text-foreground sm:text-2xl" data-testid="overview-name">{user.displayName}</dd>
              <dt className="sr-only">登录邮箱</dt>
              <dd className="break-all text-muted-foreground" data-testid="overview-email">{email}</dd>
            </dl>
          </section>
          <section className="grid gap-4" aria-labelledby="overview-manage">
            <div className="flex items-baseline justify-between gap-3">
              <h2 id="overview-manage" className="text-lg font-semibold">账号管理</h2>
              <span className="text-xs font-medium text-muted-foreground">快速导航</span>
            </div>
            <ul className="m-0 grid list-none gap-4 p-0 sm:grid-cols-2">
              {SECTIONS.map((s) => (
                <li key={s.to} className="group relative flex flex-col justify-between gap-6 rounded-xl bg-card p-6 shadow-sm transition-shadow focus-within:ring-[3px] focus-within:ring-ring/50 hover:shadow-md">
                  <div className="grid gap-3">
                    <span
                      className={cn(
                        "grid size-10 place-items-center rounded-lg transition-colors",
                        s.danger
                          ? "bg-destructive/10 text-destructive"
                          : "bg-muted text-primary group-hover:bg-brand group-hover:text-brand-foreground",
                      )}
                    >
                      <s.icon className="size-[22px]" weight="bold" aria-hidden />
                    </span>
                    <div className="grid gap-1">
                      <h3 className="text-base font-semibold">
                        <Link
                          to={s.to}
                          className={cn(
                            "rounded-sm outline-none after:absolute after:inset-0 after:rounded-xl",
                            s.danger ? "text-destructive" : "text-foreground group-hover:text-primary",
                          )}
                        >
                          {s.title}
                        </Link>
                      </h3>
                      <p className="text-sm leading-relaxed text-muted-foreground">{s.text}</p>
                    </div>
                  </div>
                  <span aria-hidden className={cn("flex items-center gap-1 text-xs font-semibold", s.danger ? "text-destructive" : "text-primary")}>
                    {s.cta}
                    <ArrowRight className="size-3.5 transition-transform group-hover:translate-x-1" weight="bold" />
                  </span>
                </li>
              ))}
            </ul>
          </section>
        </div>
      )}
    </RequireSession>
  );
}

/** The last two characters of the name, like the design's avatar fallback. */
function initials(name: string): string {
  const chars = Array.from(name.trim());
  return (chars.length > 2 ? chars.slice(-2) : chars).join("") || "?";
}
