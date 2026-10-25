import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@kongken/ui/button";
import { Input } from "@kongken/ui/input";
import { createEpoch } from "@kongken/account/latest";
import { Field } from "@kongken/account/ui";
import { Problem } from "@/components/Problem";
import { Spinner } from "@/components/Layout";
import { Link } from "@/lib/router";
import { EMPTY_FILTERS, lookupCount, type UserFilters } from "@/lib/routes.ts";
import { listUsers, ROLE_LABEL, STATUS_LABEL, type UserPage, type UserRow } from "@/lib/users";

type Lookup = "none" | "email" | "userId" | "nickname";

const SELECT = "h-9 w-full rounded-md border bg-background px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50";

type View = { kind: "loading" } | { kind: "ready"; page: UserPage } | { kind: "failed"; error: unknown };

/**
 * The user list. The filters live in this component only (never in the address, history or
 * storage: an email address is not something to leave in a URL), and every answer is written only if it is the
 * newest request, so a slow older page can never replace a newer one.
 */
export default function UsersPage() {
  const [lookup, setLookup] = useState<Lookup>("none");
  const [text, setText] = useState("");
  const [status, setStatus] = useState("");
  const [role, setRole] = useState("");
  // The filters of the list on screen: edited fields only take effect on "查询".
  const [applied, setApplied] = useState<UserFilters>(EMPTY_FILTERS);
  // Page tokens of the pages shown so far; "" is the first page. The server owns paging.
  const [tokens, setTokens] = useState<string[]>([""]);
  const [view, setView] = useState<View>({ kind: "loading" });
  const [reads] = useState(createEpoch);
  const aborter = useRef<AbortController | null>(null);

  const token = tokens[tokens.length - 1]!;
  const load = useCallback(() => {
    const current = reads.begin();
    aborter.current?.abort();
    const abort = (aborter.current = new AbortController());
    setView({ kind: "loading" });
    listUsers(applied, token, abort.signal).then(
      (page) => current() && setView({ kind: "ready", page }),
      (error: unknown) => current() && setView({ kind: "failed", error }),
    );
  }, [applied, token, reads]);

  useEffect(() => {
    load();
    return () => {
      reads.invalidate();
      aborter.current?.abort();
    };
  }, [load, reads]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next: UserFilters = {
      ...EMPTY_FILTERS,
      status,
      role,
      ...(lookup === "email" ? { email: text } : lookup === "userId" ? { userId: text } : lookup === "nickname" ? { nickname: text } : {}),
    };
    if (lookupCount(next) > 1) return;
    setTokens([""]);
    setApplied(next);
  };

  return (
    <div className="grid gap-6">
      <h1 className="text-xl font-semibold">用户</h1>
      <form onSubmit={submit} className="grid gap-3 rounded-lg border bg-card p-4 sm:grid-cols-2 lg:grid-cols-4" aria-label="查询用户" data-testid="user-filters">
        <Field label="查找方式">
          {(a) => (
            <select {...a} className={SELECT} value={lookup} onChange={(e) => (setLookup(e.target.value as Lookup), setText(""))}>
              <option value="none">不限</option>
              <option value="email">邮箱（精确）</option>
              <option value="userId">用户 ID</option>
              <option value="nickname">昵称（包含）</option>
            </select>
          )}
        </Field>
        <Field label="查找内容" hint={lookup === "none" ? "选择查找方式后输入" : undefined}>
          {(a) => (
            <Input
              {...a}
              value={text}
              disabled={lookup === "none"}
              onChange={(e) => setText(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              maxLength={lookup === "userId" ? 36 : lookup === "nickname" ? 40 : 254}
              type={lookup === "email" ? "email" : "text"}
            />
          )}
        </Field>
        <Field label="账号状态">
          {(a) => (
            <select {...a} className={SELECT} value={status} onChange={(e) => setStatus(e.target.value)}>
              <option value="">全部</option>
              <option value="active">正常</option>
              <option value="disabled">已禁用</option>
              <option value="deleting">注销中</option>
            </select>
          )}
        </Field>
        <Field label="角色">
          {(a) => (
            <select {...a} className={SELECT} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="">全部</option>
              <option value="ozid-admin">平台管理员</option>
              <option value="metro-admin">地铁目录管理员</option>
            </select>
          )}
        </Field>
        <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
          <Button type="submit" disabled={lookup !== "none" && text.trim() === ""}>查询</Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setLookup("none");
              setText("");
              setStatus("");
              setRole("");
              setTokens([""]);
              setApplied(EMPTY_FILTERS);
            }}
          >
            清除条件
          </Button>
        </div>
      </form>

      {view.kind === "loading" ? <Spinner label="正在查询" /> : null}
      {view.kind === "failed" ? <Problem error={view.error} retry={load} /> : null}
      {view.kind === "ready" ? (
        <>
          {view.page.users.length === 0 ? (
            <p className="rounded-lg border bg-card p-6 text-sm text-muted-foreground" data-testid="no-users">没有符合条件的账号。</p>
          ) : (
            <UserTable users={view.page.users} />
          )}
          <nav aria-label="分页" className="flex items-center gap-3 text-sm">
            <Button variant="outline" size="sm" disabled={tokens.length <= 1} onClick={() => setTokens((t) => t.slice(0, -1))}>
              上一页
            </Button>
            <span className="text-muted-foreground" data-testid="page-number">第 {tokens.length} 页</span>
            <Button variant="outline" size="sm" disabled={!view.page.next} onClick={() => setTokens((t) => [...t, view.page.next])}>
              下一页
            </Button>
          </nav>
        </>
      ) : null}
    </div>
  );
}

function UserTable({ users }: { users: UserRow[] }) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="w-full min-w-[40rem] text-left text-sm" data-testid="user-table">
        <caption className="sr-only">账号列表</caption>
        <thead className="border-b bg-muted text-xs text-muted-foreground">
          <tr>
            <th scope="col" className="px-3 py-2 font-medium">昵称</th>
            <th scope="col" className="px-3 py-2 font-medium">邮箱</th>
            <th scope="col" className="px-3 py-2 font-medium">状态</th>
            <th scope="col" className="px-3 py-2 font-medium">角色</th>
            <th scope="col" className="px-3 py-2 font-medium">注册时间</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id} className="border-b last:border-0" data-testid="user-row">
              <td className="px-3 py-2">
                <Link to={`/users/${u.id}`} className="rounded-sm text-primary underline-offset-4 outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50">
                  {u.displayName}
                </Link>
              </td>
              <td className="px-3 py-2 break-all">{u.email}</td>
              <td className="px-3 py-2">{STATUS_LABEL[u.status]}</td>
              <td className="px-3 py-2">{u.roles.map((r) => ROLE_LABEL[r] ?? r).join("、") || "—"}</td>
              <td className="px-3 py-2 whitespace-nowrap">{u.createdAt ? u.createdAt.toLocaleDateString("zh-CN") : "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
