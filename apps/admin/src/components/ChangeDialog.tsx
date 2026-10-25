import { useEffect, useRef, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { classifyAccountError } from "@kongken/account/errors";
import { Field, Notice, useRunner } from "@kongken/account/ui";
import { CHANGES, checkReason, newMutationId, type ChangeKind } from "@/lib/change";
import { goToOzid } from "@/lib/ozid";
import { useSession } from "@/lib/session";
import { changeUser, ROLE_LABEL, STATUS_LABEL, type ChangeOutcome, type UserRow } from "@/lib/users";

type Props = {
  user: UserRow;
  kind: ChangeKind;
  onClose: () => void;
  /** The server's answer; the page shows it and closes the dialog. */
  onDone: (outcome: ChangeOutcome) => void;
  /** Read the account again (after it changed under the administrator). */
  onRefresh: () => void;
};

/**
 * One confirmation for one change. It shows who is changed and what that does to them, asks for a reason and
 * sends exactly the change named by `kind`: there is no field for a role, a status or an operator.
 *
 * The operation ID is made when the administrator first confirms and kept for retries of the same change (same
 * reason); editing the reason is a new intent and gets a new ID. A write that was refused is never sent again
 * by the page: after a password confirmation at OzID the administrator comes back to a fresh page and decides again.
 */
export function ChangeDialog({ user, kind, onClose, onDone, onRefresh }: Props) {
  const info = CHANGES[kind];
  const { sessionLost } = useSession();
  const { pending, run } = useRunner();
  const [reason, setReason] = useState("");
  const [reasonError, setReasonError] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const attempt = useRef<{ key: string; reason: string } | null>(null);
  // The dialog belongs to the account that opened it. Once it is gone (the page was replaced because
  // the session became another account) a late answer is dropped: it reports nothing and asks nothing.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const failure = error ? classifyAccountError(error, "admin-change") : null;

  async function submit() {
    const problem = checkReason(reason);
    setReasonError(problem);
    if (problem) return;
    const trimmed = reason.trim();
    if (!attempt.current || attempt.current.reason !== trimmed) attempt.current = { key: newMutationId(), reason: trimmed };
    const { key } = attempt.current;
    await run(async () => {
      setError(null);
      try {
        const outcome = await changeUser(kind, user, key, trimmed);
        if (alive.current) onDone(outcome);
      } catch (e) {
        if (!alive.current) return;
        const f = classifyAccountError(e, "admin-change");
        if (f.kind === "not_signed_in") sessionLost();
        // An ID that belongs to another request cannot be reused: the next confirmation starts a new one.
        if (f.kind === "mutation_reused") attempt.current = null;
        if (f.kind === "validation") setReasonError(f.message);
        else setError(e);
      }
    });
  }

  const unconfirmed = failure && (failure.kind === "network" || failure.kind === "unavailable");
  const stale = failure && (failure.kind === "conflict" || failure.kind === "target_protected" || failure.kind === "target_state");

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto" data-testid="change-dialog">
        <DialogHeader>
          <DialogTitle>{info.label}</DialogTitle>
          <DialogDescription>请确认对象和影响。操作会记入审计记录，并需要填写原因。</DialogDescription>
        </DialogHeader>

        <dl className="grid gap-2 rounded-md border bg-muted/40 p-3 text-sm sm:grid-cols-[6rem_1fr]" aria-label="操作对象" data-testid="change-target">
          <dt className="text-muted-foreground">昵称</dt>
          <dd className="break-all">{user.displayName}</dd>
          <dt className="text-muted-foreground">邮箱</dt>
          <dd className="break-all">{user.email}</dd>
          <dt className="text-muted-foreground">用户 ID</dt>
          <dd className="break-all">{user.id}</dd>
          <dt className="text-muted-foreground">现在</dt>
          <dd data-testid="change-now">
            状态：{STATUS_LABEL[user.status]}；角色：{user.roles.map((r) => ROLE_LABEL[r] ?? r).join("、") || "无"}
          </dd>
          <dt className="text-muted-foreground">之后</dt>
          <dd data-testid="change-after">{info.after({ status: user.status, roles: user.roles })}</dd>
        </dl>

        <div className="grid gap-1 text-sm" data-testid="change-impact">
          <p className="font-medium">影响</p>
          <ul className="list-disc space-y-1 pl-5 leading-relaxed text-muted-foreground">
            {info.impact.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </div>

        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
          noValidate
        >
          <Field label="原因（必填）" hint="1 到 200 个字符，如工单号和简短说明。不要写邮箱、密码或令牌。" error={reasonError}>
            {(a) => (
              <textarea
                {...a}
                data-testid="change-reason"
                rows={3}
                // The server counts characters (code points) after trimming, the browser counts UTF-16 units
                // before: 400 is the most 200 valid characters can take, so this never cuts a valid reason.
                // The real limit is checkReason.
                maxLength={400}
                value={reason}
                disabled={pending}
                onChange={(e) => {
                  setReason(e.target.value);
                  setReasonError(null);
                }}
                className="min-h-20 w-full rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 aria-invalid:border-destructive md:text-sm"
              />
            )}
          </Field>

          {failure ? <Failure kind={failure.kind} message={failure.message} /> : null}

          <DialogFooter>
            {failure?.kind === "reauth_required" ? (
              <Button type="button" data-testid="change-reauth" onClick={() => goToOzid("security")}>
                前往 OzID 验证密码
              </Button>
            ) : stale ? (
              <Button type="button" data-testid="change-refresh" onClick={onRefresh}>
                刷新账号信息
              </Button>
            ) : (
              <Button type="submit" variant={kind === "disable" || kind === "revoke" ? "destructive" : "default"} disabled={pending} data-testid="change-confirm">
                {pending ? "正在提交…" : unconfirmed ? "用同一操作编号重试" : info.confirm}
              </Button>
            )}
            <Button type="button" variant="outline" disabled={pending} onClick={onClose} data-testid="change-cancel">
              取消
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Failure({ kind, message }: { kind: string; message: string }) {
  if (kind === "reauth_required")
    return (
      <div data-testid="change-problem-reauth">
        <Notice kind="info">
          {message}。验证后会回到这个页面。刚才的修改没有执行，也不会被自动重新提交，需要你重新确认。
        </Notice>
      </div>
    );
  if (kind === "forbidden") return <div data-testid="change-problem-forbidden"><Notice kind="error">当前账号没有执行这个操作的权限，没有做任何修改。</Notice></div>;
  return (
    <div data-testid={`change-problem-${kind}`}>
      <Notice kind="error">{message}</Notice>
    </div>
  );
}
