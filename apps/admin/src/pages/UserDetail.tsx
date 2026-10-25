import { useCallback, useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { createEpoch } from "@kongken/account/latest";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Link } from "@/lib/router";
import { getUser, ROLE_LABEL, STATUS_LABEL, type UserRow } from "@/lib/users";

type View = { kind: "loading" } | { kind: "ready"; user: UserRow } | { kind: "failed"; error: unknown };

export default function UserDetailPage({ id }: { id: string }) {
  const [view, setView] = useState<View>({ kind: "loading" });
  const [reads] = useState(createEpoch);
  const load = useCallback(() => {
    const current = reads.begin();
    setView({ kind: "loading" });
    getUser(id).then(
      (user) => current() && setView({ kind: "ready", user }),
      (error: unknown) => current() && setView({ kind: "failed", error }),
    );
  }, [id, reads]);
  useEffect(() => {
    load();
    return () => reads.invalidate();
  }, [load, reads]);

  return (
    <div className="grid gap-6">
      <p className="text-sm">
        <Link to="/users" className="text-primary underline-offset-4 hover:underline">← 返回用户列表</Link>
      </p>
      {view.kind === "loading" ? <Spinner /> : null}
      {view.kind === "failed" ? <NotFoundOr error={view.error} retry={load} /> : null}
      {view.kind === "ready" ? <Detail user={view.user} /> : null}
    </div>
  );
}

function NotFoundOr({ error, retry }: { error: unknown; retry: () => void }) {
  // The server's NOT_FOUND is its own answer ("no such account"); every other failure keeps its meaning.
  if (ConnectError.from(error).code === Code.NotFound)
    return <p className="rounded-lg border bg-card p-6 text-sm" data-testid="user-missing">没有找到这个账号，它可能已经注销。</p>;
  return <Problem error={error} retry={retry} />;
}

function Detail({ user }: { user: UserRow }) {
  const rows: [string, string, string][] = [
    ["昵称", user.displayName, "detail-name"],
    ["邮箱", user.email, "detail-email"],
    ["用户 ID", user.id, "detail-id"],
    ["状态", STATUS_LABEL[user.status], "detail-status"],
    ["角色", user.roles.map((r) => ROLE_LABEL[r] ?? r).join("、") || "无", "detail-roles"],
    ["注册时间", user.createdAt ? user.createdAt.toLocaleString("zh-CN") : "—", "detail-created"],
  ];
  return (
    <section aria-labelledby="detail-title" className="grid gap-4">
      <h1 id="detail-title" className="text-xl font-semibold">账号详情</h1>
      <dl className="grid gap-3 rounded-lg border bg-card p-4 text-sm sm:grid-cols-[8rem_1fr]">
        {rows.map(([k, v, id]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="break-all" data-testid={id}>{v}</dd>
          </div>
        ))}
      </dl>
      <p className="text-xs leading-relaxed text-muted-foreground">
        这里只显示识别账号所需的信息，不含密码、令牌或任何产品里的私有数据。本版本只能查看，不能修改账号。
      </p>
    </section>
  );
}
