import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { external, ensureCsrf } from "@/lib/rpc";
import { navigate } from "@/lib/router";

export default function ConfirmExternalEmailPage({ challengeId, token }: { challengeId: string; token: string | null }) {
  const [state, setState] = useState<"working" | "done" | "error">("working");
  useEffect(() => { if (!challengeId || !token) { setState("error"); return; } void ensureCsrf().then(() => external.confirmExternalRegistrationEmail({ challengeId, token })).then(() => setState("done")).catch(() => setState("error")); }, [challengeId, token]);
  if (state === "working") return <div className="mx-auto max-w-md"><Notice kind="info">正在确认邮箱…</Notice></div>;
  return <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm">{state === "done" ? <Notice kind="success">邮箱已确认。请回到发起登录的浏览器完成创建。</Notice> : <Notice kind="error">确认链接无效或已过期，请重新申请。</Notice>}<Button onClick={() => navigate("/login")}>返回登录</Button></div>;
}
