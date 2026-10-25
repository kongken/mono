import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@kongken/ui/button";
import type { AccountApi, DeletionStatus } from "./api.ts";
import { classifyAccountError } from "./errors.ts";
import { ACCOUNT_PRODUCT_SCOPE, ACCOUNT_SHARED_SCOPE, DELETE_CONFIRM_WORD } from "./scope.ts";
import { checkExistingPassword, newMutationId } from "./validate.ts";
import { FORM_CLASS, Notice, PasswordField, TextField, useFocusFirstInvalid, useRunner } from "./ui.tsx";

export type CloseAccountFlowProps = {
  api: AccountApi;
  /**
   * `start`: a session exists, so the form that begins the deletion is offered; first the page asks
   * whether this browser still holds the recovery Cookie of another, unfinished deletion and, if so,
   * shows that one (it has to finish before another can begin).
   * `resume`: there is no session (it ended when the deletion began, or the page was
   * reloaded): look for the deletion this browser's recovery Cookie belongs to and show where it is.
   */
  mode: "start" | "resume";
  /**
   * `start` mode: the account the page shows. The calls that begin the deletion carry it, so a
   * Cookie that meanwhile became another account's (a sign-in in another tab) is refused rather
   * than closed.
   */
  account?: { id: string; label: string };
  /** There is no session and the Cookie has no deletion to show: the way on is signing in. */
  onNoSession: () => void;
};

type Phase =
  | { kind: "loading" }
  | { kind: "form" }
  /** The state of the deletion could not be read (an outage, not a verdict). */
  | { kind: "lookup-failed"; message: string }
  /** `deletion` is null when the start request got no answer: whether it began is not known yet. */
  | { kind: "progress"; deletion: DeletionStatus | null }
  | { kind: "done" };

const productName = (id: string) => ACCOUNT_PRODUCT_SCOPE.find((p) => p.id === id)?.name ?? id;

/** Everything closing the account removes, and what it leaves alone. Shown before, and kept on screen during, the deletion. */
export function CloseAccountScope() {
  return (
    <div className="grid gap-3 text-sm leading-relaxed" data-testid="close-scope">
      <p>
        注销会<strong>永久删除</strong>下列内容，<strong>无法恢复</strong>。这不是退出登录，也不是只清空某一个产品的数据：
      </p>
      <ul className="m-0 grid list-disc gap-1 pl-5">
        <li data-testid="scope-shared">{ACCOUNT_SHARED_SCOPE}。</li>
        {ACCOUNT_PRODUCT_SCOPE.map((p) => (
          <li key={p.id} data-testid={`scope-${p.id}`}>
            {p.name}的数据：{p.data}。
          </li>
        ))}
        <li data-testid="scope-media">上述数据里的照片、附件等媒体文件，会一并从文件存储中删除。</li>
      </ul>
      <p className="text-muted-foreground" data-testid="scope-kept">
        不会删除你浏览器里没登录时保存的本地内容，也不会清除别人在这个浏览器里的缓存。各产品里登录期间缓存的内容由产品自己管理，OzID 不会替你向它们发出清除命令，所以
        <strong>请在注销前先到对应产品里导出尚未同步的内容</strong>。
      </p>
      <p className="text-muted-foreground">同一个邮箱以后可以重新注册，但得到的是没有任何历史的全新账号。</p>
      <p className="text-muted-foreground" data-testid="scope-admin">
        平台管理员账号不能在网页上注销，也没有代他人注销的入口：请联系运维，先用命令行回收管理员角色，再自行注销。
      </p>
    </div>
  );
}

/**
 * The self-service "close account" flow of OzID.
 *
 * Beginning needs the current password checked again and a typed confirmation. From the
 * moment the BFF accepts it the account is closed and the session is revoked on the server (no deletion answer rewrites the session Cookie). Before it accepts it,
 * the BFF has had the browser store a dedicated HttpOnly recovery Cookie (the API call repeats once
 * for that), which is the only credential that can show or continue this deletion: it is not a
 * session and signs nothing in. So after a lost response, a reload, a restart, a failing product or
 * another account signing in, the page asks that Cookie where the deletion is and offers to continue
 * it. The page never clears it: the server drops the finished deletion's own Cookie, and an answer
 * about nothing writes nothing.
 */
export function CloseAccountFlow({ api, mode, account, onNoSession }: CloseAccountFlowProps) {
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  // The progress on screen is another account's: the one signed in here has not begun anything.
  const [other, setOther] = useState(false);
  const [password, setPassword] = useState("");
  const [word, setWord] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [notice, setNoticeText] = useState("");
  // A failure is announced as an alert; where the deletion simply is, is a calm status.
  const [noticeIsFailure, setNoticeIsFailure] = useState(true);
  const setNotice = (text: string, failure = true) => {
    setNoticeText(text);
    setNoticeIsFailure(failure);
  };
  const [tick, setTick] = useState(0);
  const mutationId = useRef<string | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  const backToForm = (text: string) => {
    setOther(false);
    mutationId.current = null;
    setPhase({ kind: "form" });
    setNotice(text, false);
  };
  const settle = (d: DeletionStatus) => {
    if (d.completed && other) return backToForm("之前那个账号的注销已经完成，当前登录的账号没有受到影响。现在可以按需注销当前账号。");
    setPhase(d.completed ? { kind: "done" } : { kind: "progress", deletion: d });
  };

  const look = async () => {
    setNotice("");
    setPhase({ kind: "loading" });
    try {
      settle(await api.getDeletion());
    } catch (error) {
      const failure = classifyAccountError(error, "other");
      if (failure.kind === "not_signed_in") return other ? backToForm("之前那个账号的注销记录已经不在了。") : onNoSession();
      setPhase({ kind: "lookup-failed", message: failure.message });
    }
  };

  // `start`: is another deletion unfinished in this browser? The BFF refuses a second one, so say so first.
  const lookForOther = async () => {
    try {
      const d = await api.getDeletion();
      if (d.completed) return setPhase({ kind: "form" });
      setOther(true);
      setPhase({ kind: "progress", deletion: d });
    } catch {
      // Nothing to show (or no answer): the form is right, and the BFF still decides.
      setPhase({ kind: "form" });
    }
  };
  const lookForOtherRef = useRef(lookForOther);
  lookForOtherRef.current = lookForOther;
  const lookRef = useRef(look);
  lookRef.current = look;
  useEffect(() => {
    if (mode === "resume") void lookRef.current();
    else void lookForOtherRef.current();
  }, [mode]);

  const begin = async (e: FormEvent) => {
    e.preventDefault();
    setNotice("");
    const invalid = checkExistingPassword(password);
    setPasswordError(invalid ?? "");
    if (invalid) return setTick((t) => t + 1);
    if (word.trim() !== DELETE_CONFIRM_WORD) return;
    await run(async () => {
      try {
        await api.reauthenticate(password, account?.id);
      } catch (error) {
        const failure = classifyAccountError(error, "reauthenticate");
        if (failure.kind === "wrong_password") setPasswordError(failure.message);
        else if (failure.kind === "not_signed_in") return onNoSession();
        else setNotice(failure.message);
        return setTick((t) => t + 1);
      }
      // One key per attempt: repeating the call after a lost response continues the same deletion.
      mutationId.current ??= newMutationId();
      try {
        const status = await api.deleteAccount(mutationId.current, account?.id);
        setPassword("");
        settle(status);
      } catch (error) {
        const failure = classifyAccountError(error, "delete");
        if (failure.kind === "deletion_pending_here") {
          // Another deletion of this browser is unfinished: show it, it has to finish first.
          mutationId.current = null;
          setPassword("");
          return void lookForOther();
        }
        if (failure.kind === "unavailable" || failure.kind === "network") {
          // No answer: it may well have begun. Only the same key may continue it.
          setPassword("");
          setNotice(failure.message);
          // Best effort: when it did begin, the Cookie can say where it stands.
          const known = await api.getDeletion().catch(() => null);
          if (known) setNotice("");
          setPhase(known ? (known.completed ? { kind: "done" } : { kind: "progress", deletion: known }) : { kind: "progress", deletion: null });
        } else {
          // Refused before anything happened (confirmation, recent password, platform administrator): a new attempt gets a new key.
          mutationId.current = null;
          setNotice(failure.message);
        }
      }
    });
  };

  const goOn = () =>
    run(async () => {
      setNotice("");
      const known = phase.kind === "progress" ? phase.deletion : null;
      try {
        const status = known || !mutationId.current ? await api.retryDeletion() : await api.deleteAccount(mutationId.current, account?.id);
        settle(status);
        if (!status.completed) setNotice("注销仍在进行，还有数据没有清理完。请稍后再次重试，进度不会丢失。", false);
      } catch (error) {
        const failure = classifyAccountError(error, "delete");
        if (failure.kind === "not_signed_in") {
          if (other) return backToForm("之前那个账号的注销记录已经不在了。");
          if (known) return onNoSession();
          // The start request never arrived: nothing was closed, and a fresh attempt is possible.
          mutationId.current = null;
          setPhase({ kind: "form" });
          return setNotice("注销没有开始，账号仍然有效。需要的话请重新确认。");
        }
        setNotice(failure.message);
      }
    });

  if (phase.kind === "loading")
    return (
      <div role="status" className="py-8 text-sm text-muted-foreground">
        正在读取注销进度…
      </div>
    );

  if (phase.kind === "lookup-failed")
    return (
      <div className="grid max-w-md gap-4" data-testid="close-lookup-failed">
        <Notice kind="error">{phase.message}。这不影响注销本身：它由服务器继续完成，进度不会丢失。</Notice>
        <div>
          <Button onClick={() => void look()}>重新读取进度</Button>
        </div>
      </div>
    );

  if (phase.kind === "done")
    return (
      <div className="grid gap-4" data-testid="close-done">
        <Notice kind="success">账号已注销：共用账号和各产品里的数据，包括媒体文件，都已删除。</Notice>
        <p className="text-sm text-muted-foreground">这个浏览器已不再登录。同一个邮箱以后可以重新注册，得到的是全新的空账号。</p>
        <p className="text-sm text-muted-foreground">24 小时内重新打开这个页面仍能看到这条完成结果；之后它会自动消失。</p>
      </div>
    );

  if (phase.kind === "progress") {
    const d = phase.deletion;
    return (
      <div className="grid gap-4" data-testid="close-progress">
        {other ? (
          <Notice kind="info">
            <span data-testid="close-other-account">
              这是此前在这个浏览器里开始、尚未完成的另一个账号的注销进度。当前登录的是{account ? `“${account.label}”` : "另一个账号"}，它没有被注销；上一次注销完成之前，不能开始注销当前账号。
            </span>
          </Notice>
        ) : null}
        <Notice kind={notice && noticeIsFailure ? "error" : "info"}>
          {notice ||
            (d
              ? "注销尚未完成：账号已经关闭，还有数据在清理。进度不会丢失，可以反复重试。"
              : "没有收到服务器的确认：账号可能已经关闭，数据清理会继续。请点击“重试注销”确认，用的是同一次注销，不会重复执行。")}
        </Notice>
        {d ? (
          <ul className="m-0 grid list-none gap-2 p-0 text-sm" data-testid="close-products" aria-label="各产品的清理进度">
            {d.products.map((p) => {
              const done = d.completedProducts.includes(p);
              return (
                <li key={p} className="flex items-center justify-between gap-3 rounded-md border bg-card px-3 py-2" data-testid={`close-product-${p}`} data-state={done ? "done" : "pending"}>
                  <span>{productName(p)}的数据</span>
                  <span className={done ? "text-primary" : "text-muted-foreground"}>{done ? "已清理" : "待清理"}</span>
                </li>
              );
            })}
            <li className="rounded-md border bg-card px-3 py-2 text-muted-foreground">共用账号会在各产品清理完成后最后删除。</li>
          </ul>
        ) : null}
        <p className="text-sm text-muted-foreground">请保留这个浏览器：它保存着查看和继续这次注销的专用凭据（只用于这次注销，不能用来登录）。刷新页面也不会丢失进度。</p>
        <div className="flex flex-wrap gap-2">
          <Button variant="destructive" disabled={pending} onClick={() => void goOn()}>
            {pending ? "重试中…" : "重试注销"}
          </Button>
          {d ? (
            <Button variant="outline" disabled={pending} onClick={() => void look()}>
              刷新进度
            </Button>
          ) : null}
        </div>
        <CloseAccountScope />
      </div>
    );
  }

  return (
    <div className="grid gap-6" data-testid="close-form-page">
      <CloseAccountScope />
      <form ref={form} noValidate onSubmit={(e) => void begin(e)} className={FORM_CLASS} aria-label="确认注销账号" data-testid="close-form">
        {account ? (
          <p className="text-sm" data-testid="close-account-who">
            将注销的账号：<strong>{account.label}</strong>
          </p>
        ) : null}
        <p className="text-sm font-medium">确认注销：需要再次输入当前密码，并输入“{DELETE_CONFIRM_WORD}”。</p>
        <PasswordField label="当前密码" name="close_password" autoComplete="current-password" value={password} error={passwordError || undefined} onChange={(e) => setPassword(e.target.value)} />
        <TextField label={`输入“${DELETE_CONFIRM_WORD}”确认`} name="close_confirmation" autoComplete="off" autoCapitalize="none" spellCheck={false} value={word} onChange={(e) => setWord(e.target.value)} />
        {notice ? <Notice kind={noticeIsFailure ? "error" : "info"}>{notice}</Notice> : null}
        <div>
          <Button type="submit" variant="destructive" disabled={pending || word.trim() !== DELETE_CONFIRM_WORD}>
            {pending ? "注销中…" : "确认永久注销"}
          </Button>
        </div>
      </form>
    </div>
  );
}
