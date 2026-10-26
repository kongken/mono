import { memo, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ArrowCounterClockwise, Minus, Plus } from "@phosphor-icons/react";
import { createWorldProjection, pickLocation, worldPath, type WorldMap as WorldMapData } from "../lib/world-map.ts";
import type { WorldPlace } from "../lib/world.ts";
import { litSourceText } from "../lib/world-notice.ts";
import { DERIVED_COLOR, type LitCountry } from "../lib/world-stats.ts";
import {
  HOME,
  VIEW_H,
  VIEW_W,
  clampViewport,
  panBy,
  revealCentre,
  toBox,
  zoomAt,
  type Viewport,
} from "../lib/world-viewport.ts";

// The world map as an SVG (docs/trip-map/world-spec.md sections 10 and 11). CN is drawn from the
// 34 province-level features of the national map in one fill and no province borders, after every
// other country, so it covers the neighbours where the two sources overlap and is hit first. Every
// country/region with a polygon is a keyboard-focusable button; CN opens the China view.

const UNLIT = "#d8e1cf";
const KEY_STEP = 90;
const ZOOM_STEP = 1.6;
const DRAG_PX = 5;
/** A city is a button of at least this diameter on screen, in every viewport and at every zoom (touch targets). */
const PLACE_HIT_PX = 44;
const PLACE_DOT_PX = 5;
const PLACE_RING_PX = 1.6;

type Drawn = { code: string; d: string; bounds: [[number, number], [number, number]] };

function drawShapes(map: WorldMapData): { countries: Drawn[]; china: Drawn[]; lines: string[] } {
  const path = worldPath(VIEW_W, VIEW_H);
  const draw = (shape: WorldMapData["countries"][number]): Drawn => ({
    code: shape.code,
    d: path(shape.feature) ?? "",
    bounds: path.bounds(shape.feature),
  });
  return {
    countries: map.countries.map(draw),
    china: map.china.map(draw),
    lines: map.chinaLines.map((line) => path(line) ?? "").filter(Boolean),
  };
}

/**
 * True when the browser draws the focus for the keyboard (Tab, or a script after a key press), false for
 * the focus a mouse click or a touch tap gives. Panning to a focused country is for the keyboard: for a
 * pointer it would move the map between pressing and releasing, and the click would land on nothing.
 * A browser without `:focus-visible` is treated as keyboard, which only costs the pan on click.
 */
function focusedByKeyboard(element: Element): boolean {
  try {
    return element.matches(":focus-visible");
  } catch {
    return true;
  }
}

type ShapesProps = {
  drawn: ReturnType<typeof drawShapes>;
  names: Readonly<Record<string, string>>;
  lit: ReadonlyMap<string, LitCountry>;
  marked: ReadonlySet<string>;
  chinaLit: boolean;
  editable: boolean;
  onActivate: (code: string, viaKeyboard?: boolean) => void;
  /** Keyboard focus reached the country or region whose drawing has these bounds. */
  onFocusShape: (bounds: Drawn["bounds"]) => void;
};

const Shapes = memo(function Shapes({ drawn, names, lit, marked, chinaLit, editable, onActivate, onFocusShape }: ShapesProps) {
  const onKey = (code: string) => (event: ReactKeyboardEvent) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      event.stopPropagation();
      onActivate(code, true);
    }
  };
  const chinaFill = chinaLit ? DERIVED_COLOR : UNLIT;
  return (
    <>
      <g className="world-countries">
        {drawn.countries.map((shape) => {
          const entry = lit.get(shape.code);
          const isMarked = marked.has(shape.code);
          const name = names[shape.code] ?? shape.code;
          return (
            <path
              key={shape.code}
              className="world-country"
              d={shape.d}
              fill={entry?.color ?? UNLIT}
              data-code={shape.code}
              data-lit={entry ? "true" : "false"}
              role="button"
              tabIndex={0}
              aria-pressed={isMarked}
              aria-disabled={!editable || undefined}
              aria-label={entry && !isMarked ? `${name}（${litSourceText(entry)}）` : name}
              onClick={() => onActivate(shape.code)}
              onKeyDown={onKey(shape.code)}
              onFocus={(event) => focusedByKeyboard(event.currentTarget) && onFocusShape(shape.bounds)}
            />
          );
        })}
      </g>
      <g
        className="world-china"
        data-code="CN"
        data-lit={chinaLit ? "true" : "false"}
        role="button"
        tabIndex={0}
        aria-label={`${names.CN ?? "中国"}（${chinaLit ? "因中国足迹点亮" : "未点亮"}），按 Enter 进入中国视图`}
        fill={chinaFill}
        stroke={chinaFill}
        onClick={() => onActivate("CN")}
        onKeyDown={onKey("CN")}
        onFocus={(event) => focusedByKeyboard(event.currentTarget) && onFocusShape(chinaBounds(drawn.china))}
      >
        {drawn.china.map((shape, i) => (
          <path key={i} d={shape.d} />
        ))}
      </g>
      <g className="world-sea-line" aria-hidden="true">
        {drawn.lines.map((d, i) => (
          <path key={i} d={d} />
        ))}
      </g>
    </>
  );
});


type PlacesProps = {
  places: readonly WorldPlace[];
  names: Readonly<Record<string, string>>;
  project: (lng: number, lat: number) => [number, number];
  /** Map units that make one screen pixel at the current zoom and size of the map. */
  unit: number;
  /** Undefined while a point is being picked: the cities then only mark the map. */
  onOpen?: (id: string) => void;
  onFocusPoint: (point: [number, number]) => void;
};

/** The overseas cities as points: each one is a button of its own, drawn at a size that stays the same on screen. */
const Places = memo(function Places({ places, names, project, unit, onOpen, onFocusPoint }: PlacesProps) {
  return (
    <g className="world-places">
      {places.map((place) => {
        const [x, y] = project(place.lng, place.lat);
        const label = `${place.name}（${names[place.country] ?? place.country}），${place.trips.length} 次旅行`;
        return (
          <g
            key={place.id}
            className="world-place"
            data-place={place.id}
            role="button"
            tabIndex={onOpen ? 0 : -1}
            aria-label={label}
            aria-disabled={!onOpen || undefined}
            onClick={onOpen && (() => onOpen(place.id))}
            onKeyDown={(event) => {
              if (onOpen && (event.key === "Enter" || event.key === " ")) {
                event.preventDefault();
                event.stopPropagation();
                onOpen(place.id);
              }
            }}
            onFocus={(event) => focusedByKeyboard(event.currentTarget) && onFocusPoint([x, y])}
          >
            <title>{label}</title>
            <circle className="world-place-hit" cx={x} cy={y} r={(PLACE_HIT_PX / 2) * unit} />
            <circle className="world-place-dot" cx={x} cy={y} r={PLACE_DOT_PX * unit} strokeWidth={PLACE_RING_PX * unit} />
          </g>
        );
      })}
    </g>
  );
});

function chinaBounds(china: Drawn[]): Drawn["bounds"] {
  const box: Drawn["bounds"] = [
    [Infinity, Infinity],
    [-Infinity, -Infinity],
  ];
  for (const { bounds } of china) {
    box[0][0] = Math.min(box[0][0], bounds[0][0]);
    box[0][1] = Math.min(box[0][1], bounds[0][1]);
    box[1][0] = Math.max(box[1][0], bounds[1][0]);
    box[1][1] = Math.max(box[1][1], bounds[1][1]);
  }
  return box;
}

export type WorldMapProps = {
  map: WorldMapData;
  names: Readonly<Record<string, string>>;
  lit: ReadonlyMap<string, LitCountry>;
  marked: ReadonlySet<string>;
  chinaLit: boolean;
  /** False while saving is paused: the map is shown but a click or Enter marks nothing. */
  editable: boolean;
  onToggle: (code: string) => void;
  onOpenChina: () => void;
  /** The overseas cities drawn as points. */
  places?: readonly WorldPlace[];
  onOpenPlace?: (id: string) => void;
  /** While true a click or tap on the map picks a location instead of marking a country. */
  picking?: boolean;
  onPick?: (location: { lat: number; lng: number }) => void;
  /** A location not saved yet (the one being edited or just picked), drawn as a ring. */
  pending?: { lat: number; lng: number } | null;
};

const NO_PLACES: readonly WorldPlace[] = [];

export default function WorldMap({
  map,
  names,
  lit,
  marked,
  chinaLit,
  editable,
  onToggle,
  onOpenChina,
  places = NO_PLACES,
  onOpenPlace,
  picking = false,
  onPick,
  pending = null,
}: WorldMapProps) {
  const drawn = useMemo(() => drawShapes(map), [map]);
  const projection = useMemo(() => createWorldProjection(VIEW_W, VIEW_H), []);
  const project = useCallback((lng: number, lat: number) => projection([lng, lat]) ?? [0, 0], [projection]);
  const [view, setView] = useState<Viewport>(HOME);
  const svg = useRef<SVGSVGElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const dragging = useRef({ moved: false, start: { x: 0, y: 0 } });
  const suppressClick = useRef(false);
  // Screen pixels per map unit before the zoom (the svg is scaled to fit its box), so that points keep their size on screen.
  const [scale, setScale] = useState(1);
  useEffect(() => {
    const element = svg.current;
    if (!element) return;
    const measure = () => {
      const width = element.getBoundingClientRect().width;
      if (width > 0) setScale(Math.round((width / VIEW_W) * 1000) / 1000);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const box = useCallback((clientX: number, clientY: number): [number, number] => {
    const rect = svg.current?.getBoundingClientRect();
    return rect ? toBox(clientX - rect.left, clientY - rect.top, rect.width, rect.height) : [VIEW_W / 2, VIEW_H / 2];
  }, []);

  const zoomBy = useCallback((factor: number, at: [number, number] = [VIEW_W / 2, VIEW_H / 2]) => {
    setView((v) => zoomAt(v, factor, at[0], at[1]));
  }, []);

  // The wheel listener is native because React's is passive and could not stop the page from scrolling.
  useEffect(() => {
    const element = svg.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      event.preventDefault();
      const at = box(event.clientX, event.clientY);
      zoomBy(Math.exp(-event.deltaY * (event.deltaMode === 1 ? 0.05 : 0.0016)), at);
    };
    element.addEventListener("wheel", onWheel, { passive: false });
    return () => element.removeEventListener("wheel", onWheel);
  }, [box, zoomBy]);

  const onPointerDown = (event: React.PointerEvent<SVGSVGElement>) => {
    pointers.current.set(event.pointerId, { x: event.clientX, y: event.clientY });
    if (pointers.current.size === 1) {
      dragging.current = { moved: false, start: { x: event.clientX, y: event.clientY } };
      suppressClick.current = false;
    }
  };
  const onPointerMove = (event: React.PointerEvent<SVGSVGElement>) => {
    const previous = pointers.current.get(event.pointerId);
    if (!previous) return;
    const rect = svg.current?.getBoundingClientRect();
    if (!rect) return;
    const scale = VIEW_W / Math.max(1, rect.width);
    const next = { x: event.clientX, y: event.clientY };
    if (pointers.current.size === 1) {
      if (!dragging.current.moved && Math.hypot(next.x - dragging.current.start.x, next.y - dragging.current.start.y) < DRAG_PX) return;
      if (!dragging.current.moved) {
        dragging.current.moved = true;
        suppressClick.current = true;
        event.currentTarget.setPointerCapture?.(event.pointerId);
      }
      setView((v) => panBy(v, (next.x - previous.x) * scale, (next.y - previous.y) * scale));
    } else if (pointers.current.size === 2) {
      const [other] = [...pointers.current.entries()].filter(([id]) => id !== event.pointerId).map(([, p]) => p);
      suppressClick.current = true;
      dragging.current.moved = true;
      const before = Math.hypot(previous.x - other.x, previous.y - other.y);
      const after = Math.hypot(next.x - other.x, next.y - other.y);
      const middle = box((next.x + other.x) / 2, (next.y + other.y) / 2);
      if (before > 0 && after > 0) setView((v) => panBy(zoomAt(v, after / before, middle[0], middle[1]), (next.x - previous.x) * scale / 2, (next.y - previous.y) * scale / 2));
    }
    pointers.current.set(event.pointerId, next);
  };
  const onPointerEnd = (event: React.PointerEvent<SVGSVGElement>) => {
    pointers.current.delete(event.pointerId);
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };

  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    const pan = (dx: number, dy: number) => {
      event.preventDefault();
      setView((v) => panBy(v, dx, dy));
    };
    switch (event.key) {
      case "ArrowLeft":
        return pan(KEY_STEP, 0);
      case "ArrowRight":
        return pan(-KEY_STEP, 0);
      case "ArrowUp":
        return pan(0, KEY_STEP);
      case "ArrowDown":
        return pan(0, -KEY_STEP);
      case "+":
      case "=":
        event.preventDefault();
        return zoomBy(ZOOM_STEP);
      case "-":
      case "_":
        event.preventDefault();
        return zoomBy(1 / ZOOM_STEP);
      case "0":
        event.preventDefault();
        return setView(HOME);
    }
  };

  const activate = useCallback(
    (code: string, viaKeyboard = false) => {
      if (!viaKeyboard && suppressClick.current) {
        suppressClick.current = false;
        return;
      }
      if (picking) return;
      if (code === "CN") onOpenChina();
      else if (editable) onToggle(code);
    },
    [editable, onOpenChina, onToggle, picking],
  );
  const reveal = useCallback((bounds: Drawn["bounds"]) => setView((v) => revealCentre(v, bounds)), []);
  const revealPoint = useCallback((point: [number, number]) => reveal([point, point]), [reveal]);

  // While picking, the click is taken before any country or city sees it.
  const pickAt = (event: React.MouseEvent<SVGSVGElement>) => {
    if (!picking) return;
    event.stopPropagation();
    event.preventDefault();
    if (suppressClick.current) {
      suppressClick.current = false;
      return;
    }
    const [bx, by] = box(event.clientX, event.clientY);
    const found = pickLocation(projection, clampViewport(view), bx, by);
    if (found) onPick?.(found);
  };

  const shown = clampViewport(view);
  const unit = 1 / (shown.k * scale);
  const pendingAt = pending && Number.isFinite(pending.lat) && Number.isFinite(pending.lng) ? project(pending.lng, pending.lat) : null;
  return (
    <div className="world-map" data-zoom={shown.k.toFixed(2)} data-picking={picking ? "true" : undefined}>
      <div className="world-map-tools" role="group" aria-label="地图缩放">
        <button type="button" onClick={() => zoomBy(ZOOM_STEP)} aria-label="放大地图">
          <Plus size={16} />
        </button>
        <button type="button" onClick={() => zoomBy(1 / ZOOM_STEP)} aria-label="缩小地图">
          <Minus size={16} />
        </button>
        <button type="button" onClick={() => setView(HOME)} aria-label="复位地图">
          <ArrowCounterClockwise size={16} />
        </button>
      </div>
      <div
        className="world-map-stage"
        role="group"
        aria-label={picking ? "世界地图，正在选择城市位置。点击地图选点，方向键平移，加号减号缩放，Esc 取消" : "世界地图。Tab 在国家和地区间移动，Enter 或空格标记，方向键平移，加号减号缩放"}
        onKeyDown={onKeyDown}
      >
        <svg
          ref={svg}
          viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
          preserveAspectRatio="xMidYMid meet"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerEnd}
          onPointerCancel={onPointerEnd}
          onClickCapture={pickAt}
        >
          <g transform={`translate(${shown.x} ${shown.y}) scale(${shown.k})`}>
            <Shapes
              drawn={drawn}
              names={names}
              lit={lit}
              marked={marked}
              chinaLit={chinaLit}
              editable={editable}
              onActivate={activate}
              onFocusShape={reveal}
            />
            <Places
              places={places}
              names={names}
              project={project}
              unit={unit}
              onOpen={picking ? undefined : onOpenPlace}
              onFocusPoint={revealPoint}
            />
            {pendingAt && (
              <g className="world-pending" aria-hidden="true">
                <circle cx={pendingAt[0]} cy={pendingAt[1]} r={9 * unit} strokeWidth={2 * unit} />
                <circle cx={pendingAt[0]} cy={pendingAt[1]} r={2.5 * unit} />
              </g>
            )}
          </g>
        </svg>
      </div>
    </div>
  );
}
