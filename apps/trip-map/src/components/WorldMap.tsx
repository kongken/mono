import { memo, useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ArrowCounterClockwise, Minus, Plus } from "@phosphor-icons/react";
import { worldPath, type WorldMap as WorldMapData } from "../lib/world-map.ts";
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

type ShapesProps = {
  drawn: ReturnType<typeof drawShapes>;
  names: Readonly<Record<string, string>>;
  lit: ReadonlyMap<string, LitCountry>;
  marked: ReadonlySet<string>;
  chinaLit: boolean;
  editable: boolean;
  onActivate: (code: string, viaKeyboard?: boolean) => void;
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
              onFocus={() => onFocusShape(shape.bounds)}
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
        onFocus={() => onFocusShape(chinaBounds(drawn.china))}
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
};

export default function WorldMap({ map, names, lit, marked, chinaLit, editable, onToggle, onOpenChina }: WorldMapProps) {
  const drawn = useMemo(() => drawShapes(map), [map]);
  const [view, setView] = useState<Viewport>(HOME);
  const svg = useRef<SVGSVGElement>(null);
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const dragging = useRef({ moved: false, start: { x: 0, y: 0 } });
  const suppressClick = useRef(false);

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
      if (code === "CN") onOpenChina();
      else if (editable) onToggle(code);
    },
    [editable, onOpenChina, onToggle],
  );
  const reveal = useCallback((bounds: Drawn["bounds"]) => setView((v) => revealCentre(v, bounds)), []);

  const shown = clampViewport(view);
  return (
    <div className="world-map" data-zoom={shown.k.toFixed(2)}>
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
        aria-label="世界地图。Tab 在国家和地区间移动，Enter 或空格标记，方向键平移，加号减号缩放"
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
          </g>
        </svg>
      </div>
    </div>
  );
}
