import { useCallback, useEffect, useRef } from "react";
import { ArrowCounterClockwise, Minus, Plus } from "@phosphor-icons/react";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import { geometryGcj02ToWgs84 } from "@/lib/coords";
import { resolveTileConfig } from "@/lib/tiles";
import type { Feature } from "geojson";
import type { MapData } from "@/lib/maps";
import type { ItineraryInfo } from "@/lib/itinerary";

const TILES = resolveTileConfig({
  VITE_OSM_TILE_URL: import.meta.env.VITE_OSM_TILE_URL,
  VITE_OSM_TILE_MAX_ZOOM: import.meta.env.VITE_OSM_TILE_MAX_ZOOM,
  VITE_OSM_TILE_ATTRIBUTION: import.meta.env.VITE_OSM_TILE_ATTRIBUTION,
});

/** [[south, west], [north, east]] in WGS-84. */
export type ViewBounds = [[number, number], [number, number]];

type Props = {
  map: MapData;
  /** Region code -> visit color. Regions absent from the record are unvisited. */
  colors: Record<string, string>;
  /** Region code -> light tint for regions explored only at province level. */
  softColors?: Record<string, string>;
  /** Boundary coordinates are GCJ-02 and must be shifted to WGS-84 for OSM tiles. */
  convertCoords: boolean;
  /** Regions whose coordinates are already WGS-84 even when convertCoords is set. */
  skipConvert?: (code: string) => boolean;
  label: string;
  onToggle: (code: string) => void;
  onHover: (name: string, code: string) => void;
  onTileError: () => void;
  /** Tooltip text per region; return null for no label. Defaults to the name without 市. */
  regionLabel?: (name: string, code: string) => string | null;
  /** Non-interactive boundary lines drawn over the regions (e.g. provinces over cities). */
  outline?: MapData;
  outlineLabel?: (name: string, code: string) => string | null;
  /** Keyboard activation for outline regions, which cannot be clicked. */
  onOutlineSelect?: (code: string) => void;
  /** Fixed initial view; must be a stable reference. Defaults to the region bounds. */
  viewBounds?: ViewBounds;
  /** Thin borders for maps with many small regions. */
  fine?: boolean;
  resetLabel?: string;
  /** Optional active itinerary route drawn over the map. */
  route?: ItineraryInfo | null;
};

const baseStyle = (
  color?: string,
  hover = false,
  fine = false,
  soft?: string,
): L.PathOptions => {
  if (!color && soft)
    return {
      color: "#3d5f49",
      weight: hover ? 2.2 : fine ? 0.6 : 1.4,
      fillColor: soft,
      fillOpacity: hover ? 0.5 : 0.35,
    };
  return {
    color: color ? "#ffffff" : "#3d5f49",
    weight: hover ? 2.6 : fine ? 0.6 : color ? 2 : 1.4,
    fillColor: color || "#ffffff",
    fillOpacity: color ? (hover ? 0.92 : 0.8) : hover ? 0.42 : 0.2,
  };
};

const defaultLabel = (name: string) => name.replace(/市$/, "");

export default function OsmProvinceMap({
  map,
  colors,
  softColors,
  convertCoords,
  skipConvert,
  label,
  onToggle,
  onHover,
  onTileError,
  regionLabel,
  outline,
  outlineLabel,
  onOutlineSelect,
  viewBounds,
  fine = false,
  resetLabel = "回到全省视图",
  route,
}: Props) {
  const container = useRef<HTMLDivElement>(null);
  const leaflet = useRef<L.Map | null>(null);
  const layers = useRef(new Map<string, L.Path>());
  const latest = useRef({
    colors,
    softColors,
    skipConvert,
    regionLabel,
    outlineLabel,
    onToggle,
    onHover,
    onTileError,
    onOutlineSelect,
    fine,
  });
  useEffect(() => {
    latest.current = {
      colors,
      softColors,
      skipConvert,
      regionLabel,
      outlineLabel,
      onToggle,
      onHover,
      onTileError,
      onOutlineSelect,
      fine,
    };
  });
  const tileFailed = useRef(false);
  const bounds = useRef<L.LatLngBounds | null>(null);
  const fit = useCallback(() => {
    if (bounds.current?.isValid())
      leaflet.current?.fitBounds(bounds.current, {
        paddingTopLeft: [28, 56],
        paddingBottomRight: [28, 28],
      });
  }, []);

  useEffect(() => {
    if (!container.current) return;
    const instance = L.map(container.current, {
      zoomSnap: 0.25,
      worldCopyJump: false,
      zoomControl: false,
      attributionControl: false,
    });
    L.control.attribution({ position: "bottomleft" }).addTo(instance);
    const tiles = L.tileLayer(TILES.url, {
      maxZoom: TILES.maxZoom,
      attribution: TILES.attribution,
    });
    tiles.on("tileerror", () => {
      if (tileFailed.current) return;
      tileFailed.current = true;
      latest.current.onTileError();
    });
    tiles.addTo(instance);
    leaflet.current = instance;
    const observer = new ResizeObserver(() => instance.invalidateSize());
    observer.observe(container.current);
    return () => {
      observer.disconnect();
      instance.remove();
      leaflet.current = null;
    };
  }, []);

  useEffect(() => {
    const instance = leaflet.current;
    if (!instance) return;
    const convert = (feature: Feature): Feature => {
      const code = String(feature.properties?.adcode);
      return {
        ...feature,
        geometry:
          convertCoords &&
          !code.startsWith("tw-") &&
          !latest.current.skipConvert?.(code)
            ? geometryGcj02ToWgs84(feature.geometry)
            : feature.geometry,
      };
    };
    const regions = new Map<string, L.Path>();
    const geojson = L.geoJSON(undefined, {
      style: (feature) => {
        const code = String(feature?.properties.adcode);
        return baseStyle(
          latest.current.colors[code],
          false,
          latest.current.fine,
          latest.current.softColors?.[code],
        );
      },
      onEachFeature: (feature, layer) => {
        const code = String(feature.properties.adcode);
        const name: string = feature.properties.name;
        const path = layer as L.Path;
        const style = (hover = false) =>
          baseStyle(
            latest.current.colors[code],
            hover,
            latest.current.fine,
            latest.current.softColors?.[code],
          );
        regions.set(code, path);
        const text = (latest.current.regionLabel ?? defaultLabel)(name, code);
        if (text)
          layer.bindTooltip(text, {
            permanent: true,
            direction: "center",
            className: "osm-region-label",
          });
        layer.on({
          click: () => latest.current.onToggle(code),
          mouseover: () => {
            latest.current.onHover(name, code);
            path.setStyle(style(true));
          },
          mouseout: () => {
            latest.current.onHover("", code);
            path.setStyle(style());
          },
        });
        layer.once("add", () => {
          const el = path.getElement();
          if (!el) return;
          el.setAttribute("tabindex", outline ? "-1" : "0");
          el.setAttribute("role", "button");
          el.addEventListener("keydown", (event) => {
            const e = event as KeyboardEvent;
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              latest.current.onToggle(code);
            }
          });
        });
      },
    });
    for (const feature of map.features) {
      if (!feature.properties.name) continue;
      geojson.addData(convert(feature));
    }
    geojson.addTo(instance);
    layers.current = regions;

    // Province lines over many small regions: not clickable, but focusable so
    // keyboard users can still enter a province.
    const lines = outline
      ? L.geoJSON(undefined, {
          interactive: false,
          style: () => ({
            color: "#3d5f49",
            weight: 1.2,
            opacity: 0.75,
            fill: false,
          }),
          onEachFeature: (feature, layer) => {
            const code = String(feature.properties.adcode);
            const name: string = feature.properties.name;
            const text = latest.current.outlineLabel?.(name, code);
            if (text)
              layer.bindTooltip(text, {
                permanent: true,
                direction: "center",
                className: "osm-region-label",
              });
            layer.once("add", () => {
              const el = (layer as L.Path).getElement();
              if (!el) return;
              el.setAttribute("tabindex", "0");
              el.setAttribute("role", "button");
              el.setAttribute("aria-label", name);
              el.addEventListener("keydown", (event) => {
                const e = event as KeyboardEvent;
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  latest.current.onOutlineSelect?.(code);
                }
              });
            });
          },
        })
      : null;
    if (lines && outline) {
      for (const feature of outline.features) {
        if (!feature.properties.name) continue;
        lines.addData(convert(feature));
      }
      lines.addTo(instance);
    }

    // Faraway Sansha islands must not shrink the initial view of Hainan island.
    bounds.current = viewBounds
      ? L.latLngBounds(viewBounds)
      : regions.has("460300")
        ? L.featureGroup(
            [...regions.entries()]
              .filter(([code]) => code !== "460300")
              .map(([, layer]) => layer),
          ).getBounds()
        : geojson.getBounds();
    fit();
    return () => {
      geojson.remove();
      lines?.remove();
      layers.current = new Map();
    };
  }, [map, outline, convertCoords, viewBounds, fit]);

  useEffect(() => {
    for (const [code, path] of layers.current) {
      const color = colors[code];
      path.setStyle(baseStyle(color, false, fine, softColors?.[code]));
      const el = path.getElement();
      el?.setAttribute("aria-pressed", String(!!color));
      el?.setAttribute(
        "aria-label",
        `${(path as L.Path & { feature?: { properties: { name: string } } }).feature?.properties.name ?? code}${color ? "，已去过" : ""}`,
      );
    }
  }, [colors, softColors, fine, map, convertCoords]);

  useEffect(() => {
    const instance = leaflet.current;
    if (!instance || !route || route.stops.length < 2) return;
    const group = L.layerGroup();
    const latlngs: L.LatLngExpression[] = route.stops
      .filter((s) => s.wgsLatitude !== 0 && s.wgsLongitude !== 0)
      .map((s) => [s.wgsLatitude, s.wgsLongitude]);
    if (latlngs.length >= 2) {
      L.polyline(latlngs, {
        color: route.color || "#4b937d",
        weight: 3.5,
        opacity: 0.9,
        dashArray: "8 6",
        interactive: false,
      }).addTo(group);
    }
    for (const stop of route.stops) {
      if (stop.wgsLatitude === 0 && stop.wgsLongitude === 0) continue;
      const marker = L.circleMarker([stop.wgsLatitude, stop.wgsLongitude], {
        radius: 6,
        color: "#ffffff",
        weight: 2,
        fillColor: route.color || "#4b937d",
        fillOpacity: 1,
      });
      marker.bindTooltip(`${stop.order}. ${stop.regionName}`, {
        direction: "top",
      });
      marker.addTo(group);
    }
    group.addTo(instance);
    return () => {
      group.remove();
    };
  }, [route, map]);

  return (
    <>
      <div
        ref={container}
        className="osm-map"
        role="group"
        aria-label={label}
      />
      <div className="map-controls">
        <button aria-label="放大地图" onClick={() => leaflet.current?.zoomIn()}>
          <Plus size={18} />
        </button>
        <button
          aria-label="缩小地图"
          onClick={() => leaflet.current?.zoomOut()}
        >
          <Minus size={18} />
        </button>
        <button aria-label={resetLabel} title={resetLabel} onClick={fit}>
          <ArrowCounterClockwise size={17} />
        </button>
      </div>
    </>
  );
}
