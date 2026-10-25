import { useEffect } from "react";
import { Link } from "@/lib/router";
import { RequireSession } from "@/components/RequireSession";

const SECTIONS = [
  { to: "/profile", title: "资料", text: "修改昵称和头像；登录邮箱只读。" },
  { to: "/security", title: "安全", text: "修改密码，或再次确认当前密码。" },
  { to: "/sessions", title: "设备与登录", text: "查看已登录的设备，退出其他设备或这个浏览器。" },
  { to: "/close-account", title: "注销账号", text: "永久删除共用账号和各产品的数据，无法恢复。" },
];

/** The account overview: where OzID ends when it was opened directly. */
export default function OverviewPage() {
  useEffect(() => {
    document.title = "账号概览 · OzID";
  }, []);
  return (
    <RequireSession>
      {({ user, email }) => (
        <div className="grid gap-6" data-testid="overview">
          <h1 className="text-xl font-semibold">账号概览</h1>
          <dl className="grid gap-3 rounded-lg border bg-card p-4 text-sm sm:grid-cols-[8rem_1fr]">
            <dt className="text-muted-foreground">昵称</dt>
            <dd data-testid="overview-name">{user.displayName}</dd>
            <dt className="text-muted-foreground">登录邮箱</dt>
            <dd className="break-all" data-testid="overview-email">{email}</dd>
          </dl>
          <ul className="m-0 grid list-none gap-3 p-0 sm:grid-cols-2">
            {SECTIONS.map((s) => (
              <li key={s.to} className="rounded-lg border bg-card p-4">
                <h2 className="text-base font-semibold">
                  <Link to={s.to} className="rounded-sm text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50">
                    {s.title}
                  </Link>
                </h2>
                <p className="mt-1 text-sm text-muted-foreground">{s.text}</p>
              </li>
            ))}
          </ul>
        </div>
      )}
    </RequireSession>
  );
}
