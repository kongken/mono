import { useEffect, useState } from "react";
import {
  BellRinging,
  ClockCounterClockwise,
  CloudCheck,
  ShareNetwork,
  SignOut,
  UserCircle,
  Users,
} from "@phosphor-icons/react";
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
import { OzidLink, OzidSignIn } from "./OzidEntry";

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
  /** Leave for OzID to sign in (again). The caller saves local edits first and may refuse to go. */
  onSignIn: () => void;
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
  onSignIn,
}: Props) {
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [confirmLogout, setConfirmLogout] = useState(false);
  const signedIn = !!cloud.user;

  useEffect(() => {
    if (!open) return;
    setError("");
    setConfirmLogout(false);
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
        {cloud.user ? (
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
              <div role="alert" className="unreadable-note grid gap-3" data-testid="deletion-pending">
                <p>账号注销尚未完成，已暂停云端编辑。请前往 OzID 重试注销。</p>
                <div>
                  <OzidLink to="closeAccount">前往 OzID 重试注销</OzidLink>
                </div>
              </div>
            )}
            {cloud.sessionExpired && !cloud.user.deletionPending && (
              <section className="unreadable-note grid gap-3" data-testid="session-expired" aria-label="重新登录">
                <p role="alert">
                  登录已失效（可能在其他应用退出、修改了密码或登录已过期）。未同步的修改仍保留在本机；重新登录后会继续同步，也可以先导出备份。
                </p>
                <div>
                  <Button variant="outline" size="sm" onClick={() => cloud.exportRawCache()}>
                    导出账号缓存
                  </Button>
                </div>
                <OzidSignIn onSignIn={onSignIn} label="前往 OzID 重新登录" testId="ozid-relogin" />
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
              {!cloud.deletionBlocked && <OzidLink to="home">管理 OzID 账号</OzidLink>}
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
                账号由 OzID 统一管理，与地铁足迹共用。登录后可以在不同设备同步足迹；不登录也能继续在本机记录。
              </DialogDescription>
            </DialogHeader>
            {error && <p className="unreadable-note unreadable-error" role="alert">{error}</p>}
            <OzidSignIn onSignIn={onSignIn} />
            <div className="dialog-actions">
              <Button variant="outline" onClick={() => onOpenChange(false)}>暂不登录</Button>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
