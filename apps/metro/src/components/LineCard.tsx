import type { Line } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import { Button } from "@kongken/ui/button";
import { Check } from "@phosphor-icons/react";
import { formatKm, yearOf } from "@/lib/format";
import { markable, statusLabel } from "@/lib/status";
import { Badge } from "./extras";

/** One line of a city or topic: colour bar, facts and, for riders, the "ridden" toggle. */
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
  const stock = line.rollingStockCode
    ? `${line.rollingStockCode}${line.cars ? ` · ${line.cars}${line.carsAddition ? `+${line.carsAddition}` : ""} 节` : ""}`
    : line.cars
      ? `${line.cars}${line.carsAddition ? `+${line.carsAddition}` : ""} 节`
      : "";
  const year = yearOf(line.commencedOn);
  const canMark = markable(line.status);
  return (
    <li className="flex overflow-hidden rounded-lg border bg-card" data-testid={`line-${line.code}`}>
      <div className="w-2 shrink-0" style={{ backgroundColor: line.color }} aria-hidden />
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-4 gap-y-2 p-3">
        <div className="min-w-0 flex-1 basis-48">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-medium">{line.name}</h3>
            {line.status !== LineStatus.OPERATING && <Badge tone="warn">{statusLabel(line.status)}</Badge>}
            {line.isLoop && <Badge>环线</Badge>}
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {[showCity ? line.cityName : "", `${line.stations} 站`, formatKm(line.lengthKm), stock, year ? `${year} 年开通` : ""].filter(Boolean).join(" · ")}
          </p>
        </div>
        {onToggle && canMark ? (
          <Button
            size="sm"
            variant={ridden ? "default" : "outline"}
            aria-pressed={ridden}
            disabled={busy}
            onClick={() => onToggle(line)}
          >
            {ridden ? <Check weight="bold" aria-hidden /> : null}
            {ridden ? "坐过" : "标记坐过"}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
