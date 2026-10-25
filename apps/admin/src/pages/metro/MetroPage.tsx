import { Link } from "@/lib/router";
import { METRO_SECTIONS, METRO_TITLES, metroPathOf, type MetroSection } from "@/lib/routes.ts";
import { cities, linesEntity, rollingStocks, topics } from "./entities";
import EntityAdmin from "./EntityAdmin";
import ImportPage from "./ImportPage";
import ExportPage from "./ExportPage";
import AuditPage from "./AuditPage";
import TokensPage from "./TokensPage";

/** The metro catalogue administration: one page per section under /metro, the role checked by the gate above it. */
export default function MetroPage({ section }: { section: MetroSection }) {
  return (
    <div className="grid gap-5">
      <h1 className="text-xl font-semibold">地铁目录</h1>
      <nav aria-label="地铁目录分区" className="flex gap-1 overflow-x-auto border-b pb-2">
        {METRO_SECTIONS.map((s) => (
          <Link
            key={s}
            to={metroPathOf(s)}
            aria-current={s === section ? "page" : undefined}
            className="shrink-0 rounded-md px-3 py-1.5 text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-[current=page]:bg-muted aria-[current=page]:font-medium aria-[current=page]:text-foreground"
          >
            {METRO_TITLES[s]}
          </Link>
        ))}
      </nav>
      {/* Keyed by section so a page's lists, dialogs and filters never carry over to the next. */}
      <div key={section}>
        {section === "cities" && <EntityAdmin entity={cities} />}
        {section === "lines" && <EntityAdmin entity={linesEntity} />}
        {section === "rolling-stocks" && <EntityAdmin entity={rollingStocks} />}
        {section === "topics" && <EntityAdmin entity={topics} />}
        {section === "import" && <ImportPage />}
        {section === "export" && <ExportPage />}
        {section === "audit" && <AuditPage />}
        {section === "tokens" && <TokensPage />}
      </div>
    </div>
  );
}
