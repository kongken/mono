import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Button } from "@kongken/ui/button";
import { Check, Plus } from "@phosphor-icons/react";
import { formatKm, yearOf } from "@/lib/format";
import { vehicleTypes } from "@kongken/metro-core/catalog";
import { markable, statusLabel } from "@/lib/status";
import { Badge, Eyebrow } from "./extras";

const stockOf = (line: Line) => {
  const cars = line.cars ? `${line.cars}${line.carsAddition ? `+${line.carsAddition}` : ""} 节` : "";
  const vehicle = vehicleTypes.find((t) => t.value === line.vehicleType)?.label;
  return [line.rollingStockCode, vehicle, cars].filter(Boolean).join(" · ");
};

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt><Eyebrow className="block">{label}</Eyebrow></dt>
      <dd className="num mt-0.5 truncate text-sm font-semibold text-on-surface">{children}</dd>
    </div>
  );
}

/** One catalog line and the entry to its ride editor. */
export function LineCard({
  line,
  ridden,
  onEdit,
  busy,
  riddenYear,
  showCity,
}: {
  line: Line;
  /** undefined hides the toggle's state (not signed in). */
  ridden?: boolean;
  onEdit?: (line: Line) => void;
  busy?: boolean;
  riddenYear?: number;
  showCity?: boolean;
}) {
  const stock = stockOf(line);
  const year = yearOf(line.commencedOn);
  const canMark = markable(line.status);
  return (
    <li
      className="group relative flex flex-col overflow-hidden rounded-xl bg-surface-lowest shadow-card transition-shadow hover:shadow-float"
      data-testid={`line-${line.code}`}
    >
      <div className="h-1.5 w-full shrink-0" style={{ backgroundColor: line.color }} aria-hidden />
      <div className="flex flex-1 flex-col gap-4 p-4">
        <div className="flex items-start gap-3">
          <span className="mt-1 size-3.5 shrink-0 rounded-full ring-4 ring-surface-low" style={{ backgroundColor: line.color }} aria-hidden />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <h3 className="max-w-full font-headline text-lg leading-snug font-bold break-words text-ink">{line.name}</h3>
              {line.status !== LineStatus.OPERATING && <Badge tone="warn">{statusLabel(line.status)}</Badge>}
              {line.isLoop && <Badge>环线</Badge>}
            </div>
            {showCity && line.cityName ? <p className="mt-0.5 text-sm text-on-variant">{line.cityName}</p> : null}
            {stock ? <p className="mt-0.5 truncate font-code text-xs text-on-variant">{stock}</p> : null}
            {line.stationNames.length ? <p className="mt-1 text-sm break-words text-on-variant">{line.stationNames[0]} ⇄ {line.stationNames.at(-1)}</p> : null}
          </div>
        </div>
        <dl className="grid grid-cols-3 gap-3 rounded-lg bg-surface-low px-3 py-2.5">
          <Fact label="站点">{line.stations} 站</Fact>
          <Fact label="里程">{formatKm(line.lengthKm)}</Fact>
          <Fact label="开通">{year ? `${year} 年` : "—"}</Fact>
        </dl>
        {ridden ? <p className="text-sm font-semibold text-brand">已坐过{riddenYear ? ` · ${riddenYear} 年` : " · 年份未记录"}</p> : null}
        {onEdit && canMark ? (
          <Button
            size="sm"
            variant={ridden ? "default" : "outline"}
            className={ridden ? "mt-auto h-10 w-full rounded-lg bg-brand text-white hover:bg-brand/90" : "mt-auto h-10 w-full rounded-lg border-outline-variant text-ink"}
            aria-haspopup={ridden !== undefined ? "dialog" : undefined}
            disabled={busy}
            onClick={() => onEdit(line)}
          >
            {ridden ? <Check weight="bold" aria-hidden /> : <Plus weight="bold" aria-hidden />}
            {ridden ? "编辑足迹" : "标记坐过"}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
