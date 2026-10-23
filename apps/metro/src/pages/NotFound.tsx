import { Link } from "@/lib/router";
import { PageTitle } from "@/components/extras";

export default function NotFound({ what = "页面" }: { what?: string }) {
  return (
    <>
      <PageTitle>{what}不存在</PageTitle>
      <p className="text-sm text-muted-foreground">
        地址可能有误，或内容不公开。 <Link to="/" className="text-primary underline">回到首页</Link>
      </p>
    </>
  );
}
