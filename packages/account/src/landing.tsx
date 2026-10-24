import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Button } from "@kongken/ui/button";
import type { AccountApi } from "./api.ts";
import { classifyAccountError, LINK_INVALID, type AccountField } from "./errors.ts";
import { heldToken, releaseToken, watchLandingFragment, type LandingKind } from "./fragment.ts";
import {
  checkDisplayName,
  checkNewPassword,
  checkPasswordConfirmation,
} from "./validate.ts";
import { FORM_CLASS, Notice, PasswordField, TextField, useFocusFirstInvalid, useRunner } from "./ui.tsx";

type Errors = Partial<Record<AccountField | "again", string>>;

type LandingProps = {
  kind: LandingKind;
  api: AccountApi;
  appName: string;
  /** Where "log in" and "request a new link" lead: the app's own sign-in entrance. */
  loginHref: string;
};

/**
 * The page behind the links in the account mails. It reads nothing from the network until
 * the user submits: opening the page (or a mail scanner fetching it) consumes no token.
 * The token was moved out of the address bar into memory before the app started
 * (`captureLandingToken`); a reload therefore needs the mail's link again.
 */
export function AccountLanding({ kind, api, appName, loginHref }: LandingProps) {
  // Another mailed link opened in this tab changes only the fragment: start over with the new token.
  const [generation, setGeneration] = useState(0);
  useEffect(() => watchLandingFragment(() => setGeneration((g) => g + 1)), []);
  return (
    <main className="mx-auto grid min-h-screen w-full max-w-md content-start gap-5 px-4 py-10" data-testid={`landing-${kind}`}>
      <p className="text-sm text-muted-foreground">{appName}</p>
      {kind === "verify-email" ? (
        <CompleteRegistration key={generation} api={api} appName={appName} loginHref={loginHref} />
      ) : (
        <ResetPassword key={generation} api={api} loginHref={loginHref} />
      )}
    </main>
  );
}

function Unusable({ loginHref, children }: { loginHref: string; children?: ReactNode }) {
  return (
    <div className="grid gap-4" data-testid="link-invalid">
      <h1 className="text-xl font-semibold">链接不可用</h1>
      <Notice kind="error">{children ?? LINK_INVALID}</Notice>
      <p className="text-sm text-muted-foreground">邮件里的链接只能使用一次，并且很快过期；刷新或重新打开页面后也需要用邮件里的链接重新进入。</p>
      <div>
        <Button asChild>
          <a href={loginHref}>返回登录，重新申请</a>
        </Button>
      </div>
    </div>
  );
}

function Done({ title, children, loginHref }: { title: string; children: ReactNode; loginHref: string }) {
  return (
    <div className="grid gap-4" data-testid="landing-done">
      <h1 className="text-xl font-semibold">{title}</h1>
      <Notice kind="success">{children}</Notice>
      <div>
        <Button asChild>
          <a href={loginHref}>去登录</a>
        </Button>
      </div>
    </div>
  );
}

function CompleteRegistration({ api, appName, loginHref }: { api: AccountApi; appName: string; loginHref: string }) {
  // The token is held in memory by captureLandingToken; this component never touches the URL.
  const [token] = useState(heldToken);
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [state, setState] = useState<"form" | "done" | "invalid">(token ? "form" : "invalid");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  if (state === "invalid") return <Unusable loginHref={loginHref} />;
  if (state === "done")
    return (
      <Done title="邮箱已验证，注册完成" loginHref={loginHref}>
        账号已经创建。验证邮箱不会自动登录，请用邮箱和刚设置的密码登录。
      </Done>
    );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found: Errors = {};
    const nameError = checkDisplayName(displayName, true);
    const passwordError = checkNewPassword(password);
    const againError = passwordError ? null : checkPasswordConfirmation(password, again);
    if (nameError) found.displayName = nameError;
    if (passwordError) found.password = passwordError;
    if (againError) found.again = againError;
    setErrors(found);
    setNotice("");
    if (nameError || passwordError || againError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        await api.completeRegistration(token!, password, displayName.trim());
      } catch (error) {
        const failure = classifyAccountError(error, "register-complete");
        if (failure.kind === "link_invalid") {
          // The link is spent for good; do not keep the secret around.
          releaseToken();
          setPassword("");
          setAgain("");
          return setState("invalid");
        }
        if (failure.field) setErrors({ [failure.field]: failure.message });
        else setNotice(failure.message);
        setTick((t) => t + 1);
        return;
      }
      releaseToken();
      setPassword("");
      setAgain("");
      setState("done");
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="landing-title">
      <div>
        <h1 id="landing-title" className="text-xl font-semibold">验证邮箱并设置密码</h1>
        <p className="mt-1 text-sm text-muted-foreground">邮箱已经确认。设置一个密码，就能注册{appName}；同一个账号也可用于地铁足迹和山海足迹。</p>
      </div>
      <TextField
        label="昵称（可选）"
        name="display_name"
        autoComplete="nickname"
        value={displayName}
        error={errors.displayName}
        hint="不填写则使用“旅行者”，之后可以修改。"
        onChange={(e) => setDisplayName(e.target.value)}
      />
      <PasswordField
        label="密码"
        name="new_password"
        autoComplete="new-password"
        hint="12 到 128 个字符，可以使用几个词组成的短句，不要求大小写或符号。"
        value={password}
        error={errors.password}
        onChange={(e) => setPassword(e.target.value)}
      />
      <PasswordField label="再次输入密码" name="new_password_confirm" autoComplete="new-password" value={again} error={errors.again} onChange={(e) => setAgain(e.target.value)} />
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <Button type="submit" disabled={pending}>
        {pending ? "提交中…" : "完成注册"}
      </Button>
    </form>
  );
}

function ResetPassword({ api, loginHref }: { api: AccountApi; loginHref: string }) {
  const [token] = useState(heldToken);
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [notice, setNotice] = useState("");
  const [state, setState] = useState<"form" | "done" | "invalid">(token ? "form" : "invalid");
  const [tick, setTick] = useState(0);
  const form = useRef<HTMLFormElement>(null);
  const { pending, run } = useRunner();
  useFocusFirstInvalid(form, tick);

  if (state === "invalid") return <Unusable loginHref={loginHref} />;
  if (state === "done")
    return (
      <Done title="密码已重置" loginHref={loginHref}>
        新密码已生效，所有设备都已退出登录。重置不会自动登录，请用新密码登录。
      </Done>
    );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const found: Errors = {};
    const passwordError = checkNewPassword(password);
    const againError = passwordError ? null : checkPasswordConfirmation(password, again);
    if (passwordError) found.newPassword = passwordError;
    if (againError) found.again = againError;
    setErrors(found);
    setNotice("");
    if (passwordError || againError) return setTick((t) => t + 1);
    await run(async () => {
      try {
        await api.resetPassword(token!, password);
      } catch (error) {
        const failure = classifyAccountError(error, "reset-complete");
        if (failure.kind === "link_invalid") {
          releaseToken();
          setPassword("");
          setAgain("");
          return setState("invalid");
        }
        if (failure.field) setErrors({ [failure.field]: failure.message });
        else setNotice(failure.message);
        setTick((t) => t + 1);
        return;
      }
      releaseToken();
      setPassword("");
      setAgain("");
      setState("done");
    });
  };

  return (
    <form ref={form} noValidate onSubmit={(e) => void submit(e)} className={FORM_CLASS} aria-labelledby="landing-title">
      <div>
        <h1 id="landing-title" className="text-xl font-semibold">设置新密码</h1>
        <p className="mt-1 text-sm text-muted-foreground">设置成功后，这个账号所有设备上的登录都会失效，需要用新密码重新登录。</p>
      </div>
      <PasswordField
        label="新密码"
        name="new_password"
        autoComplete="new-password"
        hint="12 到 128 个字符，可以使用几个词组成的短句，不要求大小写或符号。"
        value={password}
        error={errors.newPassword}
        onChange={(e) => setPassword(e.target.value)}
      />
      <PasswordField label="再次输入新密码" name="new_password_confirm" autoComplete="new-password" value={again} error={errors.again} onChange={(e) => setAgain(e.target.value)} />
      {notice ? <Notice kind="error">{notice}</Notice> : null}
      <Button type="submit" disabled={pending}>
        {pending ? "提交中…" : "重置密码"}
      </Button>
    </form>
  );
}
