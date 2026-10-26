import { ArrowSquareOut, UserCircle } from "@phosphor-icons/react";
import { ozid } from "@/api/ozid";
import { Button } from "@kongken/ui/button";

/**
 * The only way to an account in this product: go to OzID. Used by the account window (signed out and
 * signed in again after the session was lost) and by the full-screen page for a session that expired with no
 * cache to show. There is no form here, no password field and no hidden copy of one: OzID is the single account UI.
 */
export function OzidSignIn({
  onSignIn,
  label = "使用 OzID 登录",
  testId = "ozid-sign-in",
}: {
  /** Leaves for OzID after the local edits were saved (see App's `leaveForOzid`). */
  onSignIn: () => void;
  label?: string;
  testId?: string;
}) {
  if (!ozid.enabled) {
    return (
      <p className="unreadable-note" role="alert" data-testid="ozid-unavailable">
        账号中心的地址尚未配置，暂时无法登录。本机记录不受影响，可以继续使用。
      </p>
    );
  }
  return (
    <div className="grid gap-2">
      <Button onClick={onSignIn} data-testid={testId}>
        <UserCircle size={16} />
        {label}
      </Button>
      <p className="text-muted-foreground text-xs">
        将前往 OzID 完成登录、注册或找回密码，之后自动回到山海足迹。离开前会先把本页修改保存在本机。
      </p>
    </div>
  );
}

/** A link to OzID's own pages (profile, password, devices, closing the account), opened in a new tab so this page and its edits stay as they are. */
export function OzidLink({ to, children }: { to: "home" | "closeAccount"; children: React.ReactNode }) {
  if (!ozid.enabled) return null;
  return (
    <Button variant="outline" size="sm" asChild>
      <a href={ozid[to]} target="_blank" rel="noopener noreferrer" data-testid={`ozid-link-${to}`}>
        <ArrowSquareOut size={16} />
        {children}
      </a>
    </Button>
  );
}
