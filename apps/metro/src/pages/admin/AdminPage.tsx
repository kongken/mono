import { Link } from "@/lib/router";
import { useSession } from "@/lib/session";
import { adminSections } from "@/lib/route";
import { Button } from "@kongken/ui/button";
import { cn } from "@kongken/ui/utils";
import { Alert, PageTitle, Spinner } from "@/components/extras";
import { cities, linesEntity, rollingStocks, topics } from "./entities";
import EntityAdmin from "./EntityAdmin";
import ImportPage from "./ImportPage";
import ExportPage from "./ExportPage";
import AuditPage from "./AuditPage";
import TokensPage from "./TokensPage";

const titles: Record<(typeof adminSections)[number], string> = {
  cities: "城市", lines: "线路", "rolling-stocks": "车型", topics: "专题", import: "导入", export: "导出", audit: "审计日志", tokens: "管理令牌",
};

export default function AdminPage({ section }: { section: string }) {
  const { session, isAdmin, signIn } = useSession();
  if (session.status === "loading") return <Spinner />;
  if (session.status === "anonymous") {
    return (
      <>
        <PageTitle>管理</PageTitle>
        <p className="mb-3 text-sm text-muted-foreground">请先登录。</p>
        <Button onClick={signIn}>登录</Button>
      </>
    );
  }
  // The entry point is hidden for others, but the server checks every call again.
  if (!isAdmin) {
    return (
      <>
        <PageTitle>无权限</PageTitle>
        <Alert>你的账号没有管理员权限。</Alert>
      </>
    );
  }
  const current = (adminSections as readonly string[]).includes(section) ? (section as keyof typeof titles) : "cities";
  return (
    <>
      <PageTitle>管理</PageTitle>
      <nav aria-label="管理分区" className="mb-5 flex gap-1 overflow-x-auto border-b pb-2">
        {adminSections.map((s) => (
          <Link
            key={s}
            to={`/admin/${s}`}
            aria-current={s === current ? "page" : undefined}
            className={cn("shrink-0 rounded-md px-3 py-1.5 text-sm hover:bg-accent", s === current && "bg-accent font-medium text-accent-foreground")}
          >
            {titles[s]}
          </Link>
        ))}
      </nav>
      {current === "cities" && <EntityAdmin entity={cities} />}
      {current === "lines" && <EntityAdmin entity={linesEntity} />}
      {current === "rolling-stocks" && <EntityAdmin entity={rollingStocks} />}
      {current === "topics" && <EntityAdmin entity={topics} />}
      {current === "import" && <ImportPage />}
      {current === "export" && <ExportPage />}
      {current === "audit" && <AuditPage />}
      {current === "tokens" && <TokensPage />}
    </>
  );
}
