import { Button } from "@kongken/ui/button";
import { external, ensureCsrf } from "@/lib/rpc";
import { useState } from "react";

export default function ExternalButtons() {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  async function start(provider: string) {
    setBusy(provider); setError("");
    try { await ensureCsrf(); const result = await external.startExternalLogin({ provider, intent: "login", appId: "ozid", returnPath: "/" }); window.location.assign(result.authorizationUrl); }
    catch { setError("第三方登录暂时不可用，请使用邮箱密码或稍后重试。"); setBusy(""); }
  }
  return <section className="grid gap-2" aria-label="第三方登录"><div className="text-center text-sm text-muted-foreground">或使用第三方账号</div><div className="grid grid-cols-2 gap-2"><Button variant="outline" disabled={!!busy} onClick={() => void start("google")}>{busy === "google" ? "跳转中…" : "Google"}</Button><Button variant="outline" disabled={!!busy} onClick={() => void start("github")}>{busy === "github" ? "跳转中…" : "GitHub"}</Button></div>{error ? <p className="text-sm text-destructive" role="alert">{error}</p> : null}</section>;
}
