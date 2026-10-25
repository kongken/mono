import { useEffect, useState } from "react";
import {
  ArrowLeft,
  BellRinging,
  ClockCounterClockwise,
  CloudCheck,
  ShareNetwork,
  Gear,
  SignOut,
  UserCircle,
  Users,
} from "@phosphor-icons/react";
import { AuthPanel, ChangePasswordForm, DeleteAccountPanel, ProfileForm, SessionsList } from "@kongken/account/forms";
import { requireAccount } from "@/api/auth";
import { getBackendStatus } from "@/api/system";
import { Button } from "@kongken/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@kongken/ui/dialog";
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
  onOpenNotifications: () => void;
  /** Which form the signed-out window opens on. */
  initialMode?: "login" | "register" | "forgot";
};

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
  onOpenNotifications,
  initialMode = "login",
}: Props) {
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const [view, setView] = useState<"main" | "settings">("main");
  const signedIn = !!cloud.user;

  useEffect(() => {
    if (!open) return;
    setError("");
    setConfirmLogout(false);
    setView("main");
  }, [open]);

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

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        {cloud.user && view === "settings" ? (
          <SettingsView
            cloud={cloud}
            onBack={() => setView("main")}
            onClosed={() => onOpenChange(false)}
          />
        ) : cloud.user ? (
          <>
            <DialogHeader>
              <span className="dialog-icon">
                <UserCircle size={22} />
              </span>
              <DialogTitle>
                {cloud.user.displayName}
              </DialogTitle>
              <DialogDescription>
                同步完成的足迹可在其他设备登录查看。
              </DialogDescription>
            </DialogHeader>
            <p className="account-sync" role="status">
              <CloudCheck size={16} />
              {cloud.deletionBlocked
                ? "云端数据删除待完成，暂停编辑"
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
            {cloud.deletionBlocked && (
              <p role="alert" className="unreadable-note">
                账号注销尚未完成，已暂停云端编辑。请打开“账号设置”重试注销。
              </p>
            )}
            {cloud.sessionExpired && !cloud.user.deletionPending && (
              <section className="unreadable-note grid gap-3" data-testid="session-expired" aria-label="重新登录">
                <p role="alert">
                  登录已失效。未同步的修改仍保留在本机；重新登录后会继续同步，也可以先导出备份。
                </p>
                <div>
                  <Button variant="outline" size="sm" onClick={() => cloud.exportRawCache()}>
                    导出账号缓存
                  </Button>
                </div>
                <AuthPanel
                  api={requireAccount()}
                  appName="山海足迹"
                  onSignedIn={({ user, expiresAt }) => cloud.signIn(user, expiresAt)}
                />
              </section>
            )}
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
            <div className="unreadable-note account-migrate">
              <span>开启“一年前的今天”等回忆提醒，可选邮件或浏览器推送，随时关闭。</span>
              <Button variant="outline" size="sm" disabled={cloud.deletionBlocked} onClick={onOpenNotifications}>
                <BellRinging size={16} />
                提醒与通知
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
              <Button variant="outline" disabled={pending} onClick={() => setView("settings")}>
                <Gear size={16} />
                账号设置
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
          <>
            <DialogHeader>
              <span className="dialog-icon"><UserCircle size={22} /></span>
              <DialogTitle>登录山海足迹</DialogTitle>
              <DialogDescription>
                登录后可以在不同设备同步足迹；不登录也能继续在本机记录。
              </DialogDescription>
            </DialogHeader>
            {error && <p className="unreadable-note unreadable-error" role="alert">{error}</p>}
            <AuthPanel
              api={requireAccount()}
              appName="山海足迹"
              initialMode={initialMode}
              onSignedIn={({ user, expiresAt }) => {
                cloud.signIn(user, expiresAt);
                onOpenChange(false);
              }}
            />
            <div className="dialog-actions">
              <Button variant="outline" onClick={() => onOpenChange(false)}>暂不登录</Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

/** Profile, password, devices and closing the shared account. */
function SettingsView({ cloud, onBack, onClosed }: { cloud: Cloud; onBack: () => void; onClosed: () => void }) {
  const api = requireAccount();
  const user = cloud.user!;
  const [email, setEmail] = useState<string | null>(null);
  const [failed, setFailed] = useState("");
  useEffect(() => {
    // A closed account has no session left to read the email with.
    if (user.deletionPending) return;
    let alive = true;
    api
      .me()
      .then((me) => alive && setEmail(me.email))
      .catch((e) => alive && setFailed(describeApiError(e)));
    return () => {
      alive = false;
    };
  }, [api, user.deletionPending]);

  return (
    <>
      <DialogHeader>
        <span className="dialog-icon">
          <Gear size={22} />
        </span>
        <DialogTitle>账号设置</DialogTitle>
        <DialogDescription>同一个账号也用于地铁足迹。</DialogDescription>
      </DialogHeader>
      <div>
        <Button variant="outline" size="sm" onClick={onBack}>
          <ArrowLeft size={16} />
          返回
        </Button>
      </div>
      {failed && (
        <p className="unreadable-note unreadable-error" role="alert">
          {failed}
        </p>
      )}
      {email !== null && !user.deletionPending && (
        <>
          <ProfileForm
            key={user.id}
            api={api}
            user={{ id: user.id, displayName: user.displayName, avatarUrl: user.avatarUrl }}
            email={email}
            onUpdated={(u) => cloud.updateUser({ displayName: u.displayName, avatarUrl: u.avatarUrl })}
          />
          <ChangePasswordForm api={api} email={email} onChanged={async () => {
            if ((await cloud.passwordChanged()) === "left") onClosed();
            else onBack();
          }} />
          <SessionsList api={api} identity={user.id} />
        </>
      )}
      <DeleteAccountPanel
        api={api}
        appName="山海足迹"
        resumePending={!!user.deletionPending}
        warning={
          <p className="text-muted-foreground">
            尚未同步的修改也会丢弃，请先在主页导出足迹备份。
          </p>
        }
        prepare={cloud.deletion.prepare}
        onNotStarted={cloud.deletion.notStarted}
        onPending={cloud.deletion.pending}
        onDeleted={() => {
          cloud.deletion.done();
          onClosed();
        }}
      />
    </>
  );
}
