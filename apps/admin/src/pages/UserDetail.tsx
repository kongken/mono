import { useCallback, useEffect, useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { createEpoch } from "@kongken/account/latest";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { ChangeDialog } from "@/components/ChangeDialog";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { CHANGES, offerFor, type ChangeKind } from "@/lib/change";
import { Link } from "@/lib/router";
import { auditPathOf } from "@/lib/routes.ts";
import { getUser, ROLE_LABEL, STATUS_LABEL, type ChangeOutcome, type UserRow } from "@/lib/users";

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
      {view.kind === "ready" ? <Detail user={view.user} reload={load} /> : null}
    </div>
  );
}

function NotFoundOr({ error, retry }: { error: unknown; retry: () => void }) {
  // The server's NOT_FOUND is its own answer ("no such account"); every other failure keeps its meaning.
  if (ConnectError.from(error).code === Code.NotFound)
    return <p className="rounded-lg border bg-card p-6 text-sm" data-testid="user-missing">没有找到这个账号，它可能已经注销。</p>;
  return <Problem error={error} retry={retry} />;
}

function Detail({ user: loaded, reload }: { user: UserRow; reload: () => void }) {
  // The page shows the account as the server last said it: the read, then the answer to each change.
  const [user, setUser] = useState(loaded);
  const [change, setChange] = useState<ChangeKind | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  useEffect(() => setUser(loaded), [loaded]);

  function done(kind: ChangeKind, outcome: ChangeOutcome) {
    setUser((u) => ({ ...u, status: outcome.status, roles: outcome.roles, authVersion: outcome.authVersion }));
    setChange(null);
    setNotice(
      [
        outcome.changed ? CHANGES[kind].done : "账号已经是这个状态，没有做任何修改。",
        outcome.replayed ? "这次操作之前已经完成，这里显示的是它当时的结果，没有重复执行。" : "",
      ]
        .filter(Boolean)
        .join(""),
    );
  }

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
      {notice ? (
        <div data-testid="change-notice">
          <Notice kind="success">{notice}</Notice>
        </div>
      ) : null}
      <p className="text-sm">
        <Link to={auditPathOf(user.id)} className="text-primary underline-offset-4 hover:underline" data-testid="audit-link">查看此账号的审计记录</Link>
      </p>
      <Actions user={user} open={(kind) => { setNotice(null); setChange(kind); }} />
      <p className="text-xs leading-relaxed text-muted-foreground">
        这里只显示识别账号所需的信息，不含密码、令牌或任何产品里的私有数据。网页上只能禁用或启用普通账号、授予或收回地铁目录管理员；平台角色、密码和注销都不在这里处理。
      </p>
      {change ? (
        <ChangeDialog
          key={change}
          user={user}
          kind={change}
          onClose={() => setChange(null)}
          onDone={(outcome) => done(change, outcome)}
          onRefresh={() => {
            setChange(null);
            setNotice(null);
            reload();
          }}
        />
      ) : null}
    </section>
  );
}

function Actions({ user, open }: { user: UserRow; open: (kind: ChangeKind) => void }) {
  const offer = offerFor(user);
  if (offer.kind === "protected")
    return (
      <p className="rounded-lg border bg-card p-4 text-sm leading-relaxed" data-testid="actions-protected">
        这是平台管理员账号。网页上不能修改它的状态或角色；需要时请联系运维使用命令行处理。
      </p>
    );
  if (offer.kind === "closing")
    return (
      <p className="rounded-lg border bg-card p-4 text-sm leading-relaxed" data-testid="actions-closing">
        这个账号正在注销，只能查看。“启用”不会取消注销，其他修改也不可用。
      </p>
    );
  if (offer.kind === "unknown") return null;
  return (
    <section aria-labelledby="actions-title" className="grid gap-3 rounded-lg border bg-card p-4" data-testid="actions">
      <h2 id="actions-title" className="text-sm font-medium">状态与角色</h2>
      <div className="flex flex-wrap gap-2">
        <Button variant={offer.status === "disable" ? "destructive" : "default"} onClick={() => open(offer.status)} data-testid="act-status">
          {CHANGES[offer.status].label}
        </Button>
        <Button variant="outline" disabled={offer.roleBlocked !== null} onClick={() => open(offer.role)} data-testid="act-role">
          {CHANGES[offer.role].label}
        </Button>
      </div>
      {offer.roleBlocked ? <p className="text-xs text-muted-foreground" data-testid="act-role-hint">{offer.roleBlocked}</p> : null}
    </section>
  );
}
