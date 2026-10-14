import { useEffect, useState, type FormEvent } from "react";
import {
  ClockCounterClockwise,
  CloudCheck,
  GithubLogo,
  GoogleLogo,
  ShareNetwork,
  SignOut,
  UserCircle,
  Users,
} from "@phosphor-icons/react";
import { authApi, type SessionInfo } from "@/api/auth";
import { getBackendStatus } from "@/api/system";
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
  local: { status: string; count: number };
  onMigrate: () => void;
  onOpenSnapshots: () => void;
  onOpenRegions: () => void;
  onOpenMedia: () => void;
  onOpenShare: () => void;
  onOpenCollaboration: () => void;
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
  local,
  onMigrate,
  onOpenSnapshots,
  onOpenRegions,
  onOpenMedia,
  onOpenShare,
  onOpenCollaboration,
}: Props) {
  const [mode, setMode] = useState<"login" | "register">("login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [sessions, setSessions] = useState<SessionInfo[] | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteWord, setDeleteWord] = useState("");
  const [deletePassword, setDeletePassword] = useState("");
  const [confirmLogout, setConfirmLogout] = useState(false);
  const signedIn = !!cloud.user;
  const { loadProviders } = cloud;

  useEffect(() => {
    if (!open) return;
    setError("");
    setConfirmLogout(false);
    setConfirmDelete(false);
    setDeleteWord("");
    setDeletePassword("");
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

  // Sharing appears only when the server advertises it (share.public_base_url is set).
  const [shareEnabled, setShareEnabled] = useState(false);
  useEffect(() => {
    if (!open || !signedIn) {
      setShareEnabled(false);
      return;
    }
    let alive = true;
    void getBackendStatus()
      ?.then((s) => alive && setShareEnabled(s.shareEnabled))
      .catch(() => alive && setShareEnabled(false));
    return () => {
      alive = false;
    };
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
              {cloud.deletionBlocked
                ? "账号注销待完成，暂停编辑"
                : cloud.sync === "conflict"
                  ? "相同记录发生冲突，请先处理"
                  : cloud.cacheFailed
                    ? "本机保存失败，暂停编辑"
                    : cloud.sync === "syncing"
                      ? "正在同步…"
                      : cloud.loadFailed || cloud.pending
                        ? "修改已保留在本机，等待同步"
                        : "已同步"}
            </p>
            {local.status === "unreadable" && (
              <p className="unreadable-note">
                本机保存的本地足迹无法读取，暂不能迁移。请退出登录后在本地模式中处理。
              </p>
            )}
            {local.status !== "unreadable" && local.count > 0 && (
              <div className="unreadable-note account-migrate">
                <span>
                  本机还有 {local.count}{" "}
                  条登录前保存的本地足迹，登录期间不显示。可以导入到当前账号；导入后本机副本仍保留。
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={
                    !(cloud.fresh && cloud.canWrite && !cloud.pending && cloud.sync === "idle")
                  }
                  onClick={onMigrate}
                >
                  导入本机足迹
                </Button>
              </div>
            )}
            <div className="unreadable-note account-migrate">
              <span>
                云端自动保存历史快照；误删或误导入后，可以恢复到之前的版本。
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={cloud.deletionBlocked}
                onClick={onOpenSnapshots}
              >
                <ClockCounterClockwise size={16} />
                历史版本
              </Button>
            </div>
            <div className="unreadable-note account-migrate">
              <span>私有保存旅途照片和附件，取消打卡后仍可在这里查看。</span>
              <Button variant="outline" size="sm" disabled={cloud.deletionBlocked} onClick={onOpenRegions}>区域数据与兼容</Button>
              <Button variant="outline" size="sm" disabled={cloud.deletionBlocked} onClick={onOpenMedia}>照片与附件</Button>
            </div>
            <div className="unreadable-note account-migrate">
              <span>添加好友对比双方足迹，或邀请好友加入同一次共同旅行。</span>
              <Button variant="outline" size="sm" disabled={cloud.deletionBlocked} onClick={onOpenCollaboration}>
                <Users size={16} />
                好友与共同旅行
              </Button>
            </div>
            {shareEnabled && (
              <div className="unreadable-note account-migrate">
                <span>生成只读的公开页面和分享图。发布前可以预览，随时可以撤销链接。</span>
                <Button variant="outline" size="sm" disabled={cloud.deletionBlocked} onClick={onOpenShare}>
                  <ShareNetwork size={16} />
                  分享页面
                </Button>
              </div>
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
            {cloud.deletionBlocked && (
              <p role="alert" className="unreadable-note">
                账号注销可能尚未完成，暂停云端编辑。请重试注销；第三方账号需最近
                15 分钟内重新登录。
              </p>
            )}
            <Button
              variant="ghost"
              disabled={pending}
              onClick={() => setConfirmDelete(true)}
            >
              注销账号
            </Button>
            {confirmDelete && (
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  if (deleteWord !== "注销") return;
                  void run(async () => {
                    await cloud.deleteAccount(deletePassword);
                    onOpenChange(false);
                  });
                }}
              >
                <p className="unreadable-note" role="alert">
                  注销将永久删除此账号的云端足迹、省份标记、会话、同步历史和照片，无法恢复。未同步修改也将丢弃，请先导出备份。登录前的本地足迹会保留。
                </p>
                {cloud.user.username && (
                  <label className="field-label">
                    确认当前密码
                    <Input
                      type="password"
                      autoComplete="current-password"
                      required
                      value={deletePassword}
                      onChange={(e) => setDeletePassword(e.target.value)}
                    />
                  </label>
                )}
                <label className="field-label">
                  输入“注销”确认
                  <Input
                    required
                    autoComplete="off"
                    value={deleteWord}
                    onChange={(e) => setDeleteWord(e.target.value)}
                  />
                </label>
                <div className="dialog-actions">
                  <Button
                    type="button"
                    variant="outline"
                    disabled={pending}
                    onClick={() => setConfirmDelete(false)}
                  >
                    保留账号
                  </Button>
                  <Button
                    type="submit"
                    variant="destructive"
                    disabled={pending || deleteWord !== "注销"}
                  >
                    确认永久注销
                  </Button>
                </div>
              </form>
            )}
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
