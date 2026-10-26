import type { ReactNode } from "react";
import type { RiddenCity, RiddenLine } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { formatKm } from "@/lib/format";
import { statusLabel } from "@/lib/status";
import { Link } from "@/lib/router";
import { Badge, Eyebrow, Panel } from "./extras";

/** A rider's lines grouped by city; `action` adds per-line controls (the owner's page). */
export function RiddenCities({ cities, action }: { cities: RiddenCity[]; action?: (line: RiddenLine) => ReactNode }) {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {cities.map((c) => (
        <Panel key={c.citySlug} aria-label={c.cityName} className="overflow-hidden">
          <h3 className="flex items-baseline justify-between gap-3 bg-surface-low px-4 py-3">
            <Link to={`/cities/${encodeURIComponent(c.citySlug)}`} className="font-headline text-lg font-bold text-ink hover:underline">{c.cityName}</Link>
            <Eyebrow>{c.lines.length} 条</Eyebrow>
          </h3>
          <ul className="divide-y divide-surface-high">
            {c.lines.map((l) => (
              <li key={l.lineCode} className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
                <span className="h-8 w-1.5 shrink-0 rounded-full" style={{ backgroundColor: l.color }} aria-hidden />
                <span className="min-w-0 flex-1 basis-40">
                  <span className="font-semibold text-on-surface">{l.lineName}</span>
                  {l.status !== LineStatus.OPERATING && <> <Badge tone="warn">{statusLabel(l.status)}</Badge></>}
                  <span className="num mt-0.5 block text-sm text-on-variant">
                    {formatKm(l.lengthKm)}
                    {l.riddenYear ? ` · ${l.riddenYear} 年` : ""}
                  </span>
                </span>
                {action?.(l)}
              </li>
            ))}
          </ul>
        </Panel>
      ))}
    </div>
  );
}
