import { Button } from "@kongken/ui/button";
import { classifyAccountError } from "@kongken/account/errors";
import { Notice } from "@kongken/account/ui";
import { useEffect, useState } from "react";
import { goToOzid } from "@/lib/ozid";
import { useSession } from "@/lib/session";
import { NoPermission } from "./Gate";

/** What a screen with something typed in it can hand over when it goes to OzID: how to keep it, and its text. */
export type Keep = {
  /** Stores the input in this tab (lib/draft.ts). False when the browser refuses (storage blocked or full). */
  save: () => boolean;
  /** The input as text, for copying or saving as a file; read again at each click, so it is what is on the page now. */
  text: () => string;
  /** File name offered when the person saves the text as a file. */
  filename: string;
};

/**
 * What a failed management call looks like. Each refusal keeps its meaning:
 * a lost session leads back to OzID sign-in; an old password check offers password confirmation at
 * OzID (it is a choice, not an automatic redirect, so a screen can never loop); a missing role is
 * "no permission"; the closed perimeter says only that the network or origin is not admitted.
 *
 * `keep` is for a screen with something typed in it. The trip to OzID leaves this page, so the input is stored
 * in this tab first (lib/draft.ts) and the trip is made only if that worked: when the browser refuses, the
 * person stays here with the input untouched and is told so, and can retry, copy the text, save it as a file,
 * or knowingly go without it. After the return the screen offers the input back; nothing is submitted again
 * until the person says so.
 */
export function Problem({ error, retry, keep }: { error: unknown; retry?: () => void; keep?: Keep }) {
  const { sessionLost } = useSession();
  const failure = classifyAccountError(error);
  const [refused, setRefused] = useState(false);
  const [copy, setCopy] = useState<"idle" | "done" | "failed">("idle");
  useEffect(() => {
    if (failure.kind === "not_signed_in") sessionLost();
  }, [failure.kind, sessionLost]);

  switch (failure.kind) {
    case "forbidden":
      return <NoPermission />;
    case "reauth_required":
      return (
        <div className="grid max-w-lg gap-3" data-testid="problem-reauth">
          <Notice kind="info">
            {failure.message}。验证后会回到这个页面，
            {keep
              ? "点击下面的按钮时会先把你已填写、尚未提交的内容保存在这个标签页，保存成功才会前往；回来后需要你核对并重新提交，不会自动重放。"
              : "当前输入不会被保留或重放。"}
          </Notice>
          {refused && keep ? (
            <div className="grid gap-2" data-testid="keep-refused">
              <Notice kind="error">
                浏览器拒绝了保存（存储被禁用或空间已满），所以没有离开这个页面，你填写的内容仍在页面上，没有丢失。可以重试保存，也可以先复制或导出内容再去验证；回来后需要自己重新填写。
              </Notice>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    navigator.clipboard.writeText(keep.text()).then(
                      () => setCopy("done"),
                      () => setCopy("failed"),
                    );
                  }}
                >
                  复制内容
                </Button>
                <Button type="button" size="sm" variant="outline" onClick={() => download(keep.filename, keep.text())}>
                  导出为文件
                </Button>
                <Button type="button" size="sm" variant="ghost" onClick={() => goToOzid("security")}>
                  不保留，直接前往 OzID
                </Button>
              </div>
              {copy === "done" ? <p role="status" className="text-sm text-muted-foreground">已复制。</p> : null}
              {copy === "failed" ? <p role="status" className="text-sm text-destructive">浏览器不允许复制，请改用“导出为文件”。</p> : null}
            </div>
          ) : null}
          <div>
            <Button
              type="button"
              onClick={() => {
                if (keep && !keep.save()) {
                  setRefused(true);
                  setCopy("idle");
                  return;
                }
                goToOzid("security");
              }}
            >
              {refused ? "重试保存并前往 OzID" : "前往 OzID 验证密码"}
            </Button>
          </div>
        </div>
      );
    case "not_signed_in":
      return <Notice kind="info">登录已失效，正在重新确认登录状态…</Notice>;
    default:
      return (
        <div className="grid max-w-lg gap-3" data-testid={`problem-${failure.kind}`}>
          <Notice kind="error">{failure.message}</Notice>
          {retry && failure.kind !== "not_allowed" ? (
            <div>
              <Button type="button" variant="outline" onClick={retry}>
                重试
              </Button>
            </div>
          ) : null}
        </div>
      );
  }
}

function download(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
