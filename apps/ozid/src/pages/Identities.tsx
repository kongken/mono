import { useEffect, useState } from "react";
import { Button } from "@kongken/ui/button";
import { Notice } from "@kongken/account/ui";
import { identity } from "@/lib/rpc";
import { PageHeader, Spinner } from "@/components/Layout";

export default function IdentitiesPage() {
  const [data, setData] = useState<Awaited<ReturnType<typeof identity.getIdentityCapabilities>> | null>(null);
  const [error, setError] = useState<string>();
  useEffect(() => { void identity.getIdentityCapabilities({}).then(setData).catch(() => setError("暂时无法读取登录方式，请稍后重试。")); }, []);
  if (error) return <Notice kind="error">{error}</Notice>;
  if (!data) return <Spinner />;
  return <div className="grid max-w-2xl gap-8" data-testid="identities-page">
    <PageHeader eyebrow="OzID 统一身份管理" title="登录方式">查看已绑定的第三方登录方式。绑定或解绑前会要求邮箱确认，最后一个登录方式不能被移除。</PageHeader>
    <section className="grid gap-3 rounded-xl bg-card p-6 shadow-sm sm:p-8">
      <div className="text-sm text-muted-foreground">{data.hasPassword ? "已设置密码" : "尚未设置密码"}</div>
      {data.identities.length === 0 ? <div>暂无第三方登录方式。</div> : data.identities.map((item) => <div className="flex items-center justify-between rounded-lg border p-3" key={item.id}><span>{item.displayLabel || item.provider}</span><Button variant="outline" disabled>解绑需邮件确认</Button></div>)}
    </section>
  </div>;
}
