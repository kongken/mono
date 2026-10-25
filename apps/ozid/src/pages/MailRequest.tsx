import { useEffect } from "react";
import { MailRequestPanel } from "@kongken/account/forms";
import { account } from "@/lib/rpc";
import { navigate, useSearch } from "@/lib/router";
import { pageOf } from "@/lib/routes";
import { fromTarget, useTarget } from "@/lib/target";
import { Spinner } from "@/components/Layout";
import { TargetNotice } from "@/pages/Login";

/**
 * Registration and password reset are asked for here, whichever app the user came from. The
 * mail always lands at OzID; the app the user came from is only a return context the BFF
 * validates and binds to the link, so finishing in another browser still leads back.
 */
export default function MailRequestPage({ mode }: { mode: "register" | "forgot" }) {
  const search = useSearch();
  const target = useTarget(search);
  useEffect(() => {
    document.title = `${mode === "register" ? "注册" : "找回密码"} · OzID`;
  }, [mode]);

  if (target.status === "resolving") return <Spinner />;
  return (
    <div className="mx-auto grid max-w-md gap-4">
      <TargetNotice target={target} action="完成后将进入 OzID 的登录页。" />
      <MailRequestPanel
        api={account}
        appName="OzID"
        mode={mode}
        from={fromTarget(target)}
        onSwitch={(next) => navigate(pageOf(next, search))}
      />
    </div>
  );
}
