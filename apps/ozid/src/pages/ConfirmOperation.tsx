import { useEffect, useState } from "react";
import { Notice } from "@kongken/account/ui";
import { Button } from "@kongken/ui/button";
import { identity, ensureCsrf } from "@/lib/rpc";
import { navigate } from "@/lib/router";

export default function ConfirmOperationPage({ challengeId, token }: { challengeId: string; token: string | null }) {
  const [state, setState] = useState<"working" | "done" | "error">("working");
  useEffect(() => { if (!token || !challengeId) { setState("error"); return; } void ensureCsrf().then(() => identity.confirmIdentityOperation({ challengeId, token })).then(() => setState("done")).catch(() => setState("error")); }, [challengeId, token]);
  if (state === "working") return <Notice kind="info">正在确认这次安全操作……</Notice>;
  if (state === "error") return <Notice kind="error">验证码无效或已过期，请回到登录方式页面重新开始。</Notice>;
  return <div className="grid gap-4"><Notice kind="success">操作已确认。回到登录方式页面完成绑定或解绑。</Notice><Button onClick={() => navigate("/identities")}>返回登录方式</Button></div>;
}
