import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { external, ensureCsrf } from "@/lib/rpc";
import { navigate, useSearch } from "@/lib/router";
import { readRegistrationMutation, clearRegistrationMutation } from "@/lib/registrationPending";
import { Spinner } from "@/components/Layout";

export default function ExternalFlowPage() {
  const search = useSearch();
  const flowId = new URLSearchParams(search).get("flow") ?? "";
  const [flow, setFlow] = useState<Awaited<ReturnType<typeof external.getExternalFlow>> | null>(null);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [mutationId, setMutationId] = useState<string | null>(() => readRegistrationMutation(flowId));
  const [error, setError] = useState("");
  useEffect(() => { document.title = "第三方登录 · OzID"; setMutationId(readRegistrationMutation(flowId)); if (flowId) void external.getExternalFlow({ flowId }).then(setFlow).catch(() => setError("登录流程已失效，请重新开始。")); }, [flowId]);
  if (!flowId) return <Notice kind="error">缺少登录流程，请重新开始。</Notice>;
  if (error) return <div className="mx-auto max-w-md"><Notice kind="error">{error}</Notice><Button onClick={() => navigate("/login")}>返回登录</Button></div>;
  if (!flow) return <Spinner />;
  if (flow.status === "authenticated") return <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm"><h1 className="text-lg font-semibold">登录成功</h1><p>第三方账号已登录 OzID。</p><Button onClick={() => flow.returnUrl ? window.location.assign(flow.returnUrl) : navigate("/")}>继续</Button></div>;
  if (flow.status !== "registration_pending") return <Notice kind="error">登录流程已取消或过期，请重新开始。</Notice>;
  const current = flow;
  async function requestEmail() { setBusy(true); setError(""); try { await ensureCsrf(); await external.requestExternalRegistrationEmail({ flowId, email: email.trim() }); setMessage("确认邮件已发送。请在邮件页面确认后回到此处。\n"); } catch { setError("无法发送确认邮件，请稍后重试。"); } finally { setBusy(false); } }
  async function complete() { setBusy(true); setError(""); const receipt = mutationId ?? readRegistrationMutation(flowId); setMutationId(receipt); if (!receipt) { setError("无法保存注册操作，请检查浏览器存储后重试。"); setBusy(false); return; } try { await ensureCsrf(); const result = await external.completeExternalRegistration({ flowId, mutationId: receipt, displayName: name.trim() || current.displayName, avatarUrl: current.avatarUrl }); clearRegistrationMutation(flowId); if (result.loginRequired) setError("账号已创建，请重新发起登录。"); else if (result.returnUrl) window.location.assign(result.returnUrl); else navigate("/"); } catch { setError("注册确认尚未完成或流程已过期。可安全重试，系统会复用同一注册回执。"); } finally { setBusy(false); } }
  return <div className="mx-auto grid max-w-md gap-4 rounded-xl bg-card p-6 shadow-sm" data-testid="external-registration"><h1 className="text-lg font-semibold">确认创建 OzID 账号</h1><p>候选邮箱：<strong>{flow.candidateEmail || "未提供"}</strong></p><p className="text-sm text-muted-foreground">首次使用第三方账号需要确认创建。账号不会因为邮箱相同而自动合并。</p>{message ? <Notice kind="success">{message}</Notice> : null}{error ? <Notice kind="error">{error}</Notice> : null}<label className="grid gap-1 text-sm">显示名称<input className="rounded border p-2" value={name} placeholder={flow.displayName || "你的名字"} onChange={(e) => setName(e.target.value)} /></label><label className="grid gap-1 text-sm">用于 OzID 验证的邮箱<input className="rounded border p-2" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" /></label><div className="flex gap-2"><Button disabled={busy || !email.trim()} onClick={() => void requestEmail()}>发送确认邮件</Button><Button variant="outline" disabled={busy || !flow.emailConfirmed} onClick={() => void complete()}>确认创建账号</Button></div></div>;
}
