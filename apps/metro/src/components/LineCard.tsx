import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Button } from "@kongken/ui/button";
import { Check, Plus } from "@phosphor-icons/react";
import { formatKm, yearOf } from "@/lib/format";
import { markable, statusLabel } from "@/lib/status";
import { Badge, Eyebrow } from "./extras";

const stockOf = (line: Line) => {
  const cars = line.cars ? `${line.cars}${line.carsAddition ? `+${line.carsAddition}` : ""} 节` : "";
  return [line.rollingStockCode, cars].filter(Boolean).join(" · ");
};

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <Eyebrow className="block">{label}</Eyebrow>
      <div className="num mt-0.5 truncate text-sm font-semibold text-on-surface">{children}</div>
    </div>
  );
}

/** One line of a city or topic: its colour, facts and, for riders, the "ridden" toggle. */
export function LineCard({
  line,
  ridden,
  onToggle,
  busy,
  showCity,
}: {
  line: Line;
  /** undefined hides the toggle's state (not signed in). */
  ridden?: boolean;
  onToggle?: (line: Line) => void;
  busy?: boolean;
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
              <h3 className="font-headline text-lg leading-snug font-bold text-ink">{line.name}</h3>
              {line.status !== LineStatus.OPERATING && <Badge tone="warn">{statusLabel(line.status)}</Badge>}
              {line.isLoop && <Badge>环线</Badge>}
            </div>
            {showCity && line.cityName ? <p className="mt-0.5 text-sm text-on-variant">{line.cityName}</p> : null}
            {stock ? <p className="mt-0.5 truncate font-code text-xs text-on-variant">{stock}</p> : null}
          </div>
        </div>
        <dl className="grid grid-cols-3 gap-3 rounded-lg bg-surface-low px-3 py-2.5">
          <Fact label="站点">{line.stations} 站</Fact>
          <Fact label="里程">{formatKm(line.lengthKm)}</Fact>
          <Fact label="开通">{year ? `${year} 年` : "—"}</Fact>
        </dl>
        {onToggle && canMark ? (
          <Button
            size="sm"
            variant={ridden ? "default" : "outline"}
            className={ridden ? "mt-auto h-10 w-full rounded-lg bg-brand text-white hover:bg-brand/90" : "mt-auto h-10 w-full rounded-lg border-outline-variant text-ink"}
            aria-pressed={ridden}
            disabled={busy}
            onClick={() => onToggle(line)}
          >
            {ridden ? <Check weight="bold" aria-hidden /> : <Plus weight="bold" aria-hidden />}
            {ridden ? "坐过" : "标记坐过"}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
