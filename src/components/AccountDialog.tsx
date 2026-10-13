import { useEffect, useState, type FormEvent } from "react";
import {
  CloudCheck,
  GithubLogo,
  GoogleLogo,
  SignOut,
  UserCircle,
} from "@phosphor-icons/react";
import { authApi, type SessionInfo } from "@/api/auth";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { describeApiError } from "@/lib/errors";
import type { Cloud } from "@/hooks/useCloud";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cloud: Cloud;
  /** Footprints saved on this device before signing in. */
  localCount: number;
};

const when = (date: Date | null) =>
  date ? date.toLocaleString("zh-CN", { hour12: false }) : "—";
const providerIcon = (name: string) =>
  name === "github" ? (
    <GithubLogo size={17} />
  ) : name === "google" ? (
    <GoogleLogo size={17} />
  ) : null;

export default function AccountDialog({
  open,
  onOpenChange,
  cloud,
  localCount,
}: Props) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const signedIn = !!cloud.user;
  const { loadProviders } = cloud;

  useEffect(() => {
    if (!open) return;
    setError("");
    setConfirmLogout(false);
    setPassword("");
    if (!signedIn) void loadProviders();
  }, [open, signedIn, loadProviders]);

  const loadSessions = () =>
    authApi
      .listSessions()
      .then(setSessions)
      .catch(() => setSessions(null));
  useEffect(() => {
    if (open && signedIn) void loadSessions();
    else setSessions(null);
  }, [open, signedIn]);

  const run = async (action: () => Promise<unknown>) => {
    setPending(true);
    setError("");
    try {
      await action();
      return true;
    } catch (e) {
      setError(describeApiError(e));
      return false;
    } finally {
      setPending(false);
    }
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const name = username.trim();
    const ok = await run(() =>
      mode === "login"
        ? cloud.login(name, password)
        : cloud.register(name, password, displayName.trim()),
    );
    if (ok) {
      setPassword("");
      onOpenChange(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        {cloud.user ? (
          <>
            <DialogHeader>
              <span className="dialog-icon">
                <UserCircle size={22} />
              </span>
              <DialogTitle>
                {cloud.user.displayName || cloud.user.username}
              </DialogTitle>
              <DialogDescription>
                {cloud.user.username ? `@${cloud.user.username}` : "第三方账号"}
                ，同步完成的足迹可在其他设备登录查看。
              </DialogDescription>
            </DialogHeader>
            <p className="account-sync" role="status">
              <CloudCheck size={16} />
              {cloud.sync === "conflict"
                ? "相同记录发生冲突，请先处理"
                : cloud.cacheFailed
                  ? "本机保存失败，暂停编辑"
                  : cloud.sync === "syncing"
                    ? "正在同步…"
                    : cloud.loadFailed || cloud.pending
                      ? "修改已保留在本机，等待同步"
                      : "已同步"}
            </p>
            {localCount > 0 && (
              <p className="unreadable-note">
                本机还有 {localCount}{" "}
                条登录前保存的本地足迹。登录期间不会显示，也不会上传到云端（迁移功能待实现）；退出登录后仍可继续使用。
              </p>
            )}
            {sessions && sessions.length > 0 && (
              <div className="account-sessions">
                <h3>已登录的设备</h3>
                <ul>
                  {sessions.map((s) => (
                    <li key={s.id}>
                      <span>
                        {s.current ? "当前设备" : "其他设备"}
                        <small>最近使用 {when(s.lastUsedAt)}</small>
                      </span>
                      {!s.current && (
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={pending}
                          onClick={() =>
                            void run(async () => {
                              await authApi.revokeSession(s.id);
                              await loadSessions();
                            })
                          }
                        >
                          退出
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {error && (
              <p className="unreadable-note unreadable-error" role="alert">
                {error}
              </p>
            )}
            {confirmLogout && (
              <p className="unreadable-note">
                退出登录会清除本机上的账号缓存，云端数据不受影响。如有未同步修改，它们将被丢弃，请先关闭窗口并导出足迹备份。
              </p>
            )}
            <div className="dialog-actions">
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                关闭
              </Button>
              <Button
                variant={confirmLogout ? "destructive" : "outline"}
                disabled={pending}
                onClick={() => {
                  if (!confirmLogout) return setConfirmLogout(true);
                  void run(async () => {
                    await cloud.logout(true);
                    onOpenChange(false);
                  });
                }}
              >
                <SignOut size={16} />
                {confirmLogout ? "确认退出登录" : "退出登录"}
              </Button>
            </div>
          </>
        ) : (
          <form onSubmit={(e) => void submit(e)}>
            <DialogHeader>
              <span className="dialog-icon">
                <UserCircle size={22} />
              </span>
              <DialogTitle>
                {mode === "login" ? "登录山海足迹" : "创建账号"}
              </DialogTitle>
              <DialogDescription>
                登录后足迹保存到云端，可在多台设备间同步。不登录也能继续使用，足迹只保存在这台设备。
              </DialogDescription>
            </DialogHeader>
            <div className="account-tabs" role="tablist">
              {(["login", "register"] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  role="tab"
                  aria-selected={mode === m}
                  className={mode === m ? "nav-active" : ""}
                  onClick={() => {
                    setMode(m);
                    setError("");
                  }}
                >
                  {m === "login" ? "登录" : "注册"}
                </button>
              ))}
            </div>
            <label className="field-label">
              用户名
              {mode === "register" && (
                <span>3–32 位小写字母、数字或下划线</span>
              )}
              <Input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoCapitalize="none"
                maxLength={32}
                required
              />
            </label>
            {mode === "register" && (
              <label className="field-label">
                昵称<span>选填，最多 40 个字</span>
                <Input
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  maxLength={40}
                />
              </label>
            )}
            <label className="field-label">
              密码{mode === "register" && <span>至少 8 位</span>}
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete={
                  mode === "login" ? "current-password" : "new-password"
                }
                minLength={mode === "register" ? 8 : undefined}
                maxLength={72}
                required
              />
            </label>
            {error && (
              <p className="unreadable-note unreadable-error" role="alert">
                {error}
              </p>
            )}
            <div className="dialog-actions">
              <Button
                type="button"
                variant="outline"
                onClick={() => onOpenChange(false)}
              >
                暂不登录
              </Button>
              <Button type="submit" disabled={pending}>
                {mode === "login" ? "登录" : "注册并登录"}
              </Button>
            </div>
            {cloud.providers.length > 0 && (
              <div className="account-oauth">
                <span>或使用第三方账号</span>
                {cloud.providers.map((p) => (
                  <Button
                    key={p.name}
                    type="button"
                    variant="outline"
                    disabled={pending}
                    onClick={() => void run(() => cloud.startOAuth(p.name))}
                  >
                    {providerIcon(p.name)}
                    {p.displayName}
                  </Button>
                ))}
              </div>
            )}
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}
