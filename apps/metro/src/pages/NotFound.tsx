import { Compass } from "@phosphor-icons/react";
import { Link } from "@/lib/router";
import { Eyebrow, PageTitle } from "@/components/extras";
import { container } from "@/components/Layout";

export default function NotFound({ what = "页面" }: { what?: string }) {
  return (
    <div className={`${container} flex flex-col items-start gap-2 py-16 md:py-24`}>
      <Compass className="size-14 text-outline" weight="duotone" aria-hidden />
      <PageTitle eyebrow={<Eyebrow>Error 404</Eyebrow>} sub="地址可能有误，或内容不公开。">{what}不存在</PageTitle>
      <div className="flex gap-3">
        <Link to="/" className="rounded-lg bg-deep px-5 py-2.5 text-sm font-bold text-white hover:opacity-90">回到首页</Link>
        <Link to="/topics" className="rounded-lg bg-surface-high px-5 py-2.5 text-sm font-bold text-ink hover:bg-surface-highest">车型专题</Link>
      </div>
    </div>
  );
}
