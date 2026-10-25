import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import type { AccountApi, AccountUser, ReturnFrom, SessionInfo } from "./api.ts";
import { classifyAccountError, type AccountField, type AccountFailure } from "./errors.ts";
import { ACCOUNT_PRODUCT_SCOPE, ACCOUNT_SHARED_SCOPE, DELETE_CONFIRM_WORD } from "./scope.ts";
import {
  checkAvatarUrl,
  checkDisplayName,
  checkEmail,
  checkExistingPassword,
  checkNewPassword,
  checkPasswordConfirmation,
  newMutationId,
  trimEmail,
} from "./validate.ts";
import { FORM_CLASS, LinkButton, Notice, PasswordField, TextField, useCooldown, useFocusFirstInvalid, useRunner } from "./ui.tsx";

/** The resend cooldown of mail requests; usersrv enforces its own, this only explains the wait. */
const RESEND_SECONDS = 60;

type Errors = Partial<Record<AccountField, string>>;

/** Puts a server verdict on its field when it named one, otherwise in the form-wide notice. */
function place(failure: AccountFailure, setErrors: (e: Errors) => void, setNotice: (m: string) => void) {
  if (failure.field) setErrors({ [failure.field]: failure.message });
  else setNotice(failure.message);
}

// ---------------------------------------------------------------------------------------
// Sign in, request registration, forgot password
// ---------------------------------------------------------------------------------------

export type AuthMode = "login" | "register" | "forgot";

export type SignedIn = { user: AccountUser; expiresAt: Date | null };

type AuthPanelProps = {
  api: AccountApi;
  appName: string;
  /** Called after the Cookie session exists. The panel keeps no credentials afterwards. */
  onSignedIn: (result: SignedIn) => void | Promise<void>;
  initialMode?: AuthMode;
  /**
   * Whether to offer registration and password reset (the two mail-based entrances).
   * Default true. A front end that does not host them yet shows the sign-in form alone.
   */
  allowMail?: boolean;
  /** The sentence under the title; the default names the two products. */
  intro?: ReactNode;
  /**
   * The return context mail requests bind to their challenge (default: the app of `api`;
   * null: none). The BFF validates it; it never decides where the mail links to.
   */
  from?: ReturnFrom | null;
  /**
   * Called instead of switching inside the panel when the user picks registration or
   * password reset (or goes back to sign-in). A front end that routes those forms as pages
   * of their own navigates here; without it the panel switches by itself.
   */
  onSwitchMode?: (mode: AuthMode) => void;
};

/**
 * Email + password sign-in with the two mail-based entrances: registration (an email is
 * sent, the password is chosen after the address is verified) and password reset. The
 * texts never say whether an address is registered.
 */
export function AuthPanel({ api, appName, onSignedIn, initialMode = "login", allowMail = true, intro, from, onSwitchMode }: AuthPanelProps) {
  const [mode, setMode] = useState<AuthMode>(allowMail ? initialMode : "login");
  const [email, setEmail] = useState("");
  // Switching forms drops what the previous one showed, but keeps the address typed.
  const switchTo = (next: AuthMode) => (onSwitchMode ? onSwitchMode(next) : setMode(next));
  return (
    <div data-testid="auth-panel" className="grid gap-4">
      {mode === "login" ? (
        <LoginForm api={api} appName={appName} email={email} setEmail={setEmail} onSignedIn={onSignedIn} onSwitch={switchTo} allowMail={allowMail} intro={intro} />
      ) : (
        <MailRequestForm key={mode} api={api} appName={appName} mode={mode} email={email} setEmail={setEmail} onSwitch={switchTo} from={from} />
      )}
    </div>
  );
}

function LoginForm({
  api,
  appName,
  email,
  setEmail,
  onSignedIn,
  onSwitch,
  allowMail,
  intro,
}: Pick<AuthPanelProps, "api" | "appName" | "onSignedIn" | "intro"> & { email: string; setEmail: (v: string) => void; onSwitch: (m: AuthMode) => void; allowMail: boolean }) {
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next: Errors = {};
    const emailError = checkEmail(email);
    const passwordError = checkExistingPassword(password);
    if (emailError) next.email = emailError;
    if (passwordError) next.password = passwordError;
    setErrors(next);
    setNotice("");
    if (emailError || passwordError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        const result = await api.login(trimEmail(email), password);
        setPassword("");
        await onSignedIn(result);
      } catch (error) {
        const failure = classifyAccountError(error, "login");
        // A wrong password is not kept in the field; a network failure leaves it for the retry.
        if (failure.kind === "credentials") setPassword("");
        place(failure, setErrors, setNotice);
        setTick((t) => t + 1);
        if (failure.kind === "credentials") form.current?.querySelector<HTMLElement>('input[autocomplete="current-password"]')?.focus();
      }
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="account-login-title">
      <div>
        <h2 id="account-login-title" className="text-lg font-semibold">登录{appName}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{intro ?? "使用邮箱和密码登录；同一个账号可在山海足迹和地铁足迹通用。"}</p>
      </div>
      <TextField
        label="邮箱"
        type="email"
        name="email"
        inputMode="email"
        autoComplete="username"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        value={email}
        error={errors.email}
        onChange={(e) => setEmail(e.target.value)}
      />
      <PasswordField
        label="密码"
        name="password"
        autoComplete="current-password"
        value={password}
        error={errors.password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <Button type="submit" disabled={pending}>
        {pending ? "登录中…" : "登录"}
      </Button>
      {allowMail ? (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <LinkButton onClick={() => onSwitch("forgot")}>忘记密码？</LinkButton>
          <LinkButton onClick={() => onSwitch("register")}>没有账号？申请注册</LinkButton>
        </div>
      ) : null}
    </form>
  );
}

type MailPanelProps = {
  api: AccountApi;
  appName: string;
  mode: "register" | "forgot";
  /** Where "back to sign in" and the switch to the other mail form lead (a page of their own, or a mode switch). */
  onSwitch: (m: AuthMode) => void;
  from?: ReturnFrom | null;
};

/**
 * One mail request form as a page of its own (registration or password reset), for a front
 * end that routes them instead of switching inside {@link AuthPanel}. Same states: sending,
 * the resend cooldown, rate limiting, closed registration, network failure.
 */
export function MailRequestPanel({ api, appName, mode, onSwitch, from }: MailPanelProps) {
  const [email, setEmail] = useState("");
  return (
    <div data-testid={`mail-panel-${mode}`} className="grid gap-4">
      <MailRequestForm api={api} appName={appName} mode={mode} email={email} setEmail={setEmail} onSwitch={onSwitch} from={from} />
    </div>
  );
}

function MailRequestForm({
  api,
  appName,
  mode,
  email,
  setEmail,
  onSwitch,
  from,
}: Pick<AuthPanelProps, "api" | "appName" | "from"> & { mode: "register" | "forgot"; email: string; setEmail: (v: string) => void; onSwitch: (m: AuthMode) => void }) {
  const register = mode === "register";
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  const cooldown = useCooldown();
  useFocusFirstInvalid(form, tick);

  const send = async () => {
    const emailError = checkEmail(email);
    setErrors(emailError ? { email: emailError } : {});
    setNotice("");
    if (emailError) return setTick((t) => t + 1);
    const address = trimEmail(email);
    await run(async () => {
      try {
        if (register) await api.requestRegistration(address, from);
        else await api.requestPasswordReset(address, from);
        setSentTo(address);
        cooldown.start(RESEND_SECONDS);
      } catch (error) {
        const failure = classifyAccountError(error, register ? "register-request" : "reset-request");
        if (failure.kind === "rate_limited") cooldown.start(RESEND_SECONDS);
        place(failure, setErrors, setNotice);
        setTick((t) => t + 1);
      }
    });
  };

  const title = register ? "申请注册" : "找回密码";
  if (sentTo !== null) {
    return (
      <div className={FORM_CLASS} data-testid={register ? "register-sent" : "forgot-sent"}>
        <h2 className="text-lg font-semibold">{register ? "验证邮件已申请" : "重置邮件已申请"}</h2>
        <Notice kind="success">
          {register
            ? `如果 ${sentTo} 可以注册，验证邮件很快会发送到这个邮箱。打开邮件中的链接，设置密码即可完成注册。`
            : `如果 ${sentTo} 对应一个可用的账号，重置密码的邮件很快会发送到这个邮箱。打开邮件中的链接，设置新密码。`}
        </Notice>
        <p className="text-sm text-muted-foreground">
          链接的有效期较短，请尽快打开。没有收到？先看看垃圾邮件箱，或者等待倒计时结束后重新发送。
        </p>
        {notice ? <Notice kind="error">{notice}</Notice> : null}
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" disabled={pending || cooldown.seconds > 0} onClick={() => void send()}>
            {pending ? "发送中…" : cooldown.seconds > 0 ? `重新发送（${cooldown.seconds} 秒后可用）` : "重新发送"}
          </Button>
          <Button variant="ghost" onClick={() => setSentTo(null)}>
            换一个邮箱
          </Button>
        </div>
        <LinkButton className="justify-self-start" onClick={() => onSwitch("login")}>返回登录</LinkButton>
      </div>
    );
  }

  return (
    <form
      ref={form}
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
      className={FORM_CLASS}
      aria-labelledby="account-mail-title"
    >
      <div>
        <h2 id="account-mail-title" className="text-lg font-semibold">{title}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {register
            ? `输入邮箱，我们会发一封验证邮件；验证后再设置密码，注册${appName}。`
            : "输入注册时使用的邮箱，我们会发一封重置密码的邮件。"}
        </p>
      </div>
      <TextField
        label="邮箱"
        type="email"
        name="email"
        inputMode="email"
        autoComplete="username"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        value={email}
        error={errors.email}
        onChange={(e) => setEmail(e.target.value)}
      />
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <Button type="submit" disabled={pending || (cooldown.seconds > 0 && notice !== "")}>
        {pending ? "发送中…" : cooldown.seconds > 0 && notice !== "" ? `请 ${cooldown.seconds} 秒后再试` : register ? "发送验证邮件" : "发送重置邮件"}
      </Button>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <LinkButton onClick={() => onSwitch("login")}>返回登录</LinkButton>
        {register ? <LinkButton onClick={() => onSwitch("forgot")}>忘记密码？</LinkButton> : <LinkButton onClick={() => onSwitch("register")}>申请注册</LinkButton>}
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------------------
// Password confirmation (reauthentication)
// ---------------------------------------------------------------------------------------

type ReauthenticateFormProps = {
  api: AccountApi;
  /** Called once the BFF recorded a fresh password check on this session. */
  onConfirmed: () => void | Promise<void>;
  /**
   * Called when the server says there is no valid session any more (expired, signed out
   * elsewhere, password or roles changed). A session like that cannot be upgraded by
   * entering the password; the page must send the user to sign in again.
   */
  onSessionInvalid: () => void | Promise<void>;
  /** What the confirmation is for, shown under the title. */
  intro?: ReactNode;
  submitLabel?: string;
};

/**
 * Asks for the current password and has the BFF check it against the account of this
 * session (never an identity, time or user ID the page supplies). It records nothing in
 * the browser and replays nothing: whatever the user was doing before is not retried.
 */
export function ReauthenticateForm({ api, onConfirmed, onSessionInvalid, intro, submitLabel = "确认密码" }: ReauthenticateFormProps) {
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const passwordError = checkExistingPassword(password);
    setErrors(passwordError ? { password: passwordError } : {});
    setNotice("");
    if (passwordError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        await api.reauthenticate(password);
      } catch (error) {
        const failure = classifyAccountError(error, "reauthenticate");
        setPassword("");
        if (failure.kind === "not_signed_in") return await onSessionInvalid();
        if (failure.kind === "wrong_password") setErrors({ password: failure.message });
        else setNotice(failure.message);
        setTick((t) => t + 1);
        return;
      }
      setPassword("");
      await onConfirmed();
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="account-reauth-title" data-testid="reauthenticate">
      <div>
        <h2 id="account-reauth-title" className="text-lg font-semibold">确认密码</h2>
        <p className="mt-1 text-sm text-muted-foreground">{intro ?? "为了账号安全，请再次输入当前密码。"}</p>
      </div>
      <PasswordField
        label="当前密码"
        name="password"
        autoComplete="current-password"
        value={password}
        error={errors.password}
        onChange={(e) => setPassword(e.target.value)}
      />
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <Button type="submit" disabled={pending}>
        {pending ? "确认中…" : submitLabel}
      </Button>
    </form>
  );
}

// ---------------------------------------------------------------------------------------
// Signed in: profile, password, devices
// ---------------------------------------------------------------------------------------

export function ProfileForm({
  api,
  user,
  email,
  onUpdated,
}: {
  api: AccountApi;
  user: AccountUser;
  email: string;
  onUpdated: (user: AccountUser) => void;
}) {
  const [displayName, setDisplayName] = useState(user.displayName);
  const [avatarUrl, setAvatarUrl] = useState(user.avatarUrl);
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState<{ kind: "error" | "success"; text: string } | null>(null);
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);
  const unchanged = displayName.trim() === user.displayName && avatarUrl === user.avatarUrl;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next: Errors = {};
    const nameError = checkDisplayName(displayName, false);
    const avatarError = checkAvatarUrl(avatarUrl);
    if (nameError) next.displayName = nameError;
    if (avatarError) next.avatarUrl = avatarError;
    setErrors(next);
    setNotice(null);
    if (nameError || avatarError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        const updated = await api.updateProfile(displayName.trim(), avatarUrl);
        setDisplayName(updated.displayName);
        setAvatarUrl(updated.avatarUrl);
        setNotice({ kind: "success", text: "资料已保存" });
        onUpdated(updated);
      } catch (error) {
        const failure = classifyAccountError(error, "profile");
        if (failure.field) setErrors({ [failure.field]: failure.message });
        else setNotice({ kind: "error", text: failure.message });
        setTick((t) => t + 1);
      }
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="account-profile-title" data-testid="profile-form">
      <h3 id="account-profile-title" className="text-base font-semibold">基础资料</h3>
      <TextField label="登录邮箱" type="email" value={email} readOnly autoComplete="off" hint="登录邮箱暂不支持修改，也不会出现在公开页面和好友列表中。" />
      <TextField
        label="昵称"
        name="display_name"
        autoComplete="nickname"
        value={displayName}
        error={errors.displayName}
        hint="1 到 40 个字符，山海足迹和地铁足迹共用；不要求唯一。"
        onChange={(e) => setDisplayName(e.target.value)}
      />
      <TextField
        label="头像地址（可选）"
        name="avatar_url"
        type="url"
        inputMode="url"
        autoComplete="off"
        autoCapitalize="none"
        spellCheck={false}
        value={avatarUrl}
        error={errors.avatarUrl}
        hint="https 图片地址，留空则不显示头像；服务器不会抓取这个地址。"
        onChange={(e) => setAvatarUrl(e.target.value)}
      />
      {notice ? <Notice kind={notice.kind}>{notice.text}</Notice> : null}
      <div>
        <Button type="submit" disabled={pending || unchanged}>
          {pending ? "保存中…" : "保存资料"}
        </Button>
      </div>
    </form>
  );
}

export function ChangePasswordForm({
  api,
  email,
  onChanged,
}: {
  api: AccountApi;
  /** Lets a password manager tie the new password to the right login. */
  email?: string;
  /** Every session of the account, this one too, stopped working: the app must sign the user out here. */
  onChanged: () => void | Promise<void>;
}) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [errors, setErrors] = useState<Errors & { again?: string }>({});
  const [notice, setNotice] = useState("");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found: Errors & { again?: string } = {};
    const currentError = checkExistingPassword(current);
    const newError = checkNewPassword(next);
    const againError = newError ? null : checkPasswordConfirmation(next, again);
    if (currentError) found.password = currentError;
    if (newError) found.newPassword = newError;
    if (againError) found.again = againError;
    setErrors(found);
    setNotice("");
    if (currentError || newError || againError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        await api.changePassword(current, next);
      } catch (error) {
        const failure = classifyAccountError(error, "change-password");
        if (failure.kind === "wrong_password") setErrors({ password: failure.message });
        else if (failure.field) setErrors({ [failure.field]: failure.message });
        else setNotice(failure.message);
        setTick((t) => t + 1);
        return;
      }
      setCurrent("");
      setNext("");
      setAgain("");
      await onChanged();
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="account-password-title" data-testid="password-form">
      <h3 id="account-password-title" className="text-base font-semibold">修改密码</h3>
      {email ? <input type="text" name="username" autoComplete="username" value={email} readOnly tabIndex={-1} aria-hidden className="sr-only" /> : null}
      <PasswordField label="当前密码" name="current_password" autoComplete="current-password" value={current} error={errors.password} onChange={(e) => setCurrent(e.target.value)} />
      <PasswordField
        label="新密码"
        name="new_password"
        autoComplete="new-password"
        hint="12 到 128 个字符，可以使用几个词组成的短句，不要求大小写或符号。"
        value={next}
        error={errors.newPassword}
        onChange={(e) => setNext(e.target.value)}
      />
      <PasswordField label="再次输入新密码" name="new_password_confirm" autoComplete="new-password" value={again} error={errors.again} onChange={(e) => setAgain(e.target.value)} />
      <p className="text-xs leading-relaxed text-muted-foreground">修改成功后，所有设备（包括当前设备）都会退出登录，需要用新密码重新登录。</p>
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <div>
        <Button type="submit" disabled={pending}>
          {pending ? "修改中…" : "修改密码"}
        </Button>
      </div>
    </form>
  );
}

const when = (date: Date | null) => (date ? date.toLocaleString("zh-CN", { hour12: false }) : "—");

export function SessionsList({ api, onSignedOutHere }: { api: AccountApi; onSignedOutHere?: () => void }) {
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [notice, setNotice] = useState("");
  const { pending, run } = useRunner();

  const load = useCallback(async () => {
    try {
      setSessions(await api.listSessions());
      setLoadFailed(false);
    } catch {
      setLoadFailed(true);
    }
  }, [api]);
  useEffect(() => {
    void load();
  }, [load]);

  const revoke = (id: string) =>
    run(async () => {
      setNotice("");
      try {
        await api.revokeSession(id);
        await load();
      } catch (error) {
        const failure = classifyAccountError(error, "other");
        if (failure.kind === "not_signed_in") onSignedOutHere?.();
        setNotice(failure.message);
      }
    });

  return (
    <section className="account-sessions grid gap-2" aria-labelledby="account-sessions-title" data-testid="sessions">
      <h3 id="account-sessions-title" className="text-base font-semibold">已登录的设备</h3>
      {sessions === null && !loadFailed ? <p role="status" className="text-sm text-muted-foreground">正在读取…</p> : null}
      {loadFailed ? (
        <Notice kind="error">
          暂时无法读取设备列表。 <LinkButton onClick={() => void load()}>重试</LinkButton>
        </Notice>
      ) : null}
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      {sessions && sessions.length > 0 ? (
        <ul className="m-0 grid list-none gap-0 p-0">
          {sessions.map((s) => (
            <li key={s.id} className="flex items-center justify-between gap-3 border-b py-2 text-sm last:border-b-0">
              <span className="min-w-0">
                {s.current ? "当前设备" : "其他设备"}
                <small className="mt-0.5 block text-xs text-muted-foreground">最近使用 {when(s.lastUsedAt)}</small>
              </span>
              {!s.current ? (
                <Button variant="outline" size="sm" disabled={pending} aria-label={`退出其他设备（最近使用 ${when(s.lastUsedAt)}）`} onClick={() => void revoke(s.id)}>
                  退出
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

// ---------------------------------------------------------------------------------------
// Close the account
// ---------------------------------------------------------------------------------------

type DeleteAccountPanelProps = {
  api: AccountApi;
  /** The product this panel is in, for the warning text. */
  appName: string;
  /** Extra warning for this app, such as unsynced edits. */
  warning?: ReactNode;
  /** Runs before the account is touched (stop syncing, flush edits); throw to stop the deletion. */
  prepare?: () => Promise<void>;
  /** The deletion was refused before it started; the app may resume what `prepare` paused. */
  onNotStarted?: () => void;
  /** The server did not confirm the end: the account may already be closed and cleanup continues. */
  onPending?: () => void;
  /** The account and every product's data are gone: sign this browser out and clear its account cache. */
  onDeleted: () => void | Promise<void>;
  /** A deletion that was started earlier and not confirmed: only a retry is offered. */
  resumePending?: boolean;
};

/**
 * "Close account": states the whole scope (the shared account and every product's data),
 * needs the password again and a typed Chinese confirmation, then sends the "DELETE"
 * confirmation with a fresh idempotency key. When the server cannot finish, the account
 * stays closed and the panel offers a retry that continues the same deletion.
 */
export function DeleteAccountPanel({ api, appName, warning, prepare, onNotStarted, onPending, onDeleted, resumePending = false }: DeleteAccountPanelProps) {
  const [open, setOpen] = useState(false);
  const [password, setPassword] = useState("");
  const [word, setWord] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [unfinished, setUnfinished] = useState(resumePending);
  const [tick, setTick] = useState(0);
  const mutationId = useRef<string | null>(null);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);
  useEffect(() => {
    if (resumePending) setUnfinished(true);
  }, [resumePending]);

  const finish = async () => {
    setPassword("");
    await onDeleted();
  };

  const start = async (e: FormEvent) => {
    e.preventDefault();
    const next: Errors = {};
    const passwordError = checkExistingPassword(password);
    if (passwordError) next.password = passwordError;
    setErrors(next);
    setNotice("");
    if (passwordError) return setTick((t) => t + 1);
    if (word.trim() !== DELETE_CONFIRM_WORD) return;
    await run(async () => {
      try {
        await api.reauthenticate(password);
      } catch (error) {
        const failure = classifyAccountError(error, "reauthenticate");
        if (failure.kind === "wrong_password") setErrors({ password: failure.message });
        else setNotice(failure.message);
        setTick((t) => t + 1);
        return;
      }
      try {
        await prepare?.();
      } catch (error) {
        setNotice(error instanceof Error ? error.message : "无法开始注销，请稍后重试");
        return;
      }
      // One key per attempt: repeating the call after a lost response continues the same deletion.
      mutationId.current ??= newMutationId();
      try {
        const status = await api.deleteAccount(mutationId.current);
        if (status.completed) return await finish();
        // Accepted but not finished: keep going with the retry endpoint.
        setUnfinished(true);
        onPending?.();
        setNotice("账号已关闭，数据仍在清理中。请点击“重试注销”继续。");
      } catch (error) {
        const failure = classifyAccountError(error, "delete");
        if (failure.kind === "unavailable" || failure.kind === "network") {
          setUnfinished(true);
          onPending?.();
        } else {
          // Refused before anything happened (not signed in, confirmation, recent password): a new attempt gets a new key.
          mutationId.current = null;
          onNotStarted?.();
        }
        setNotice(failure.message);
      }
    });
  };

  const retry = () =>
    run(async () => {
      setNotice("");
      try {
        const status = await api.retryDeletion();
        if (status.completed) return await finish();
        setNotice("注销仍在进行，部分数据还在清理。请稍后再次重试。");
      } catch (error) {
        setNotice(classifyAccountError(error, "delete").message);
      }
    });

  return (
    <section aria-labelledby="account-delete-title" className="grid gap-3 border-t pt-4" data-testid="delete-account">
      <h3 id="account-delete-title" className="text-base font-semibold text-destructive">注销账号</h3>
      <div className="grid gap-2 text-sm leading-relaxed">
        <p>
          注销会<strong>永久删除</strong>下列内容，无法恢复；这不是退出登录，也不是只清空{appName}的数据：
        </p>
        <ul className="m-0 grid list-disc gap-1 pl-5">
          <li>{ACCOUNT_SHARED_SCOPE}。</li>
          {ACCOUNT_PRODUCT_SCOPE.map((p) => (
            <li key={p.id}>
              {p.name}的数据：{p.data}。
            </li>
          ))}
        </ul>
        <p className="text-muted-foreground">不会删除你浏览器里没登录时保存的本地内容。同一个邮箱以后可以重新注册，但得到的是全新的空账号。</p>
        {warning}
      </div>

      {unfinished ? (
        <div className="grid gap-2">
          <Notice kind="error">
            {notice || "注销尚未完成：账号已关闭，数据清理需要重试。进度不会丢失，可以反复重试。"}
          </Notice>
          <div>
            <Button variant="destructive" disabled={pending} onClick={() => void retry()}>
              {pending ? "重试中…" : "重试注销"}
            </Button>
          </div>
        </div>
      ) : !open ? (
        <div>
          <Button variant="outline" onClick={() => setOpen(true)}>
            注销账号…
          </Button>
        </div>
      ) : (
        <form ref={form} noValidate onSubmit={(e) => void start(e)} className={FORM_CLASS} aria-label="确认注销账号">
          <PasswordField label="当前密码" name="delete_password" autoComplete="current-password" value={password} error={errors.password} onChange={(e) => setPassword(e.target.value)} />
          <TextField
            label={`输入“${DELETE_CONFIRM_WORD}”确认`}
            name="delete_confirmation"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            value={word}
            onChange={(e) => setWord(e.target.value)}
          />
          {notice ? <Notice kind="error">{notice}</Notice> : null}
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => {
                setOpen(false);
                setPassword("");
                setWord("");
                setNotice("");
                setErrors({});
              }}
            >
              保留账号
            </Button>
            <Button type="submit" variant="destructive" disabled={pending || word.trim() !== DELETE_CONFIRM_WORD}>
              {pending ? "注销中…" : "确认永久注销"}
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
