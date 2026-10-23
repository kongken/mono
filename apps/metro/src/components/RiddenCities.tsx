import type { ReactNode } from "react";
import type { RiddenCity, RiddenLine } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { formatKm } from "@/lib/format";
import { statusLabel } from "@/lib/status";
import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Badge } from "./extras";
import { Link } from "@/lib/router";

/** A rider's lines grouped by city; `action` adds per-line controls (the owner's page). */
export function RiddenCities({ cities, action }: { cities: RiddenCity[]; action?: (line: RiddenLine) => ReactNode }) {
  return (
    <div className="grid gap-5">
      {cities.map((c) => (
        <section key={c.citySlug} aria-label={c.cityName}>
          <h3 className="mb-2 font-medium">
            <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="hover:underline">{c.cityName}</Link>
            <span className="ml-2 text-sm font-normal text-muted-foreground">{c.lines.length} 条</span>
          </h3>
          <ul className="grid gap-2">
            {c.lines.map((l) => (
              <li key={l.lineCode} className="flex flex-wrap items-center gap-3 rounded-lg border bg-card p-3">
                <span className="h-6 w-2 rounded-sm" style={{ backgroundColor: l.color }} aria-hidden />
                <span className="min-w-0 flex-1 basis-40">
                  <span className="font-medium">{l.lineName}</span>
                  {l.status !== LineStatus.OPERATING && <> <Badge tone="warn">{statusLabel(l.status)}</Badge></>}
                  <span className="ml-2 text-sm text-muted-foreground">
                    {formatKm(l.lengthKm)}
                    {l.riddenYear ? ` · ${l.riddenYear} 年` : ""}
                  </span>
                </span>
                {action?.(l)}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
