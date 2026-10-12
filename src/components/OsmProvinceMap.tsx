import { useCallback, useEffect, useRef } from "react";
import { ArrowCounterClockwise, Minus, Plus } from "@phosphor-icons/react";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import { geometryGcj02ToWgs84 } from "@/lib/coords";
import { resolveTileConfig } from "@/lib/tiles";
import type { Feature } from "geojson";
import type { MapData } from "@/lib/maps";

const TILES = resolveTileConfig({
  VITE_OSM_TILE_URL: import.meta.env.VITE_OSM_TILE_URL,
  VITE_OSM_TILE_MAX_ZOOM: import.meta.env.VITE_OSM_TILE_MAX_ZOOM,
  VITE_OSM_TILE_ATTRIBUTION: import.meta.env.VITE_OSM_TILE_ATTRIBUTION,
});

type Props = {
  map: MapData;
  /** Region code -> visit color. Regions absent from the record are unvisited. */
  colors: Record<string, string>;
  /** Boundary coordinates are GCJ-02 and must be shifted to WGS-84 for OSM tiles. */
  convertCoords: boolean;
  label: string;
  onToggle: (code: string) => void;
  onHover: (name: string) => void;
  onTileError: () => void;
};

const baseStyle = (color?: string, hover = false): L.PathOptions => ({
  color: color ? "#ffffff" : "#3d5f49",
  weight: hover ? 2.6 : color ? 2 : 1.4,
  fillColor: color || "#ffffff",
  fillOpacity: color ? (hover ? 0.92 : 0.8) : hover ? 0.42 : 0.2,
});

export default function OsmProvinceMap({
  map,
  colors,
  convertCoords,
  label,
  onToggle,
  onHover,
  onTileError,
}: Props) {
  const container = useRef<HTMLDivElement>(null);
  const leaflet = useRef<L.Map | null>(null);
  const layers = useRef(new Map<string, L.Path>());
  const latest = useRef({ colors, onToggle, onHover, onTileError });
  useEffect(() => {
    latest.current = { colors, onToggle, onHover, onTileError };
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
    const regions = new Map<string, L.Path>();
    const geojson = L.geoJSON(undefined, {
      style: (feature) =>
        baseStyle(latest.current.colors[String(feature?.properties.adcode)]),
      onEachFeature: (feature, layer) => {
        const code = String(feature.properties.adcode);
        const name: string = feature.properties.name;
        const path = layer as L.Path;
        regions.set(code, path);
        layer.bindTooltip(name.replace(/市$/, ""), {
          permanent: true,
          direction: "center",
          className: "osm-region-label",
        });
        layer.on({
          click: () => latest.current.onToggle(code),
          mouseover: () => {
            latest.current.onHover(name);
            path.setStyle(baseStyle(latest.current.colors[code], true));
          },
          mouseout: () => {
            latest.current.onHover("");
            path.setStyle(baseStyle(latest.current.colors[code]));
          },
        });
        layer.once("add", () => {
          const el = path.getElement();
          if (!el) return;
          el.setAttribute("tabindex", "0");
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
      const code = String(feature.properties.adcode);
      const converted: Feature = {
        ...feature,
        geometry:
          convertCoords && !code.startsWith("tw-")
            ? geometryGcj02ToWgs84(feature.geometry)
            : feature.geometry,
      };
      geojson.addData(converted);
    }
    geojson.addTo(instance);
    layers.current = regions;
    // Faraway Sansha islands must not shrink the initial view of Hainan island.
    bounds.current = regions.has("460300")
      ? L.featureGroup([...regions.entries()].filter(([code]) => code !== "460300").map(([, layer]) => layer)).getBounds()
      : geojson.getBounds();
    fit();
    return () => {
      geojson.remove();
      layers.current = new Map();
    };
  }, [map, convertCoords, fit]);

  useEffect(() => {
    for (const [code, path] of layers.current) {
      const color = colors[code];
      path.setStyle(baseStyle(color));
      const el = path.getElement();
      el?.setAttribute("aria-pressed", String(!!color));
      el?.setAttribute(
        "aria-label",
        `${(path as L.Path & { feature?: { properties: { name: string } } }).feature?.properties.name ?? code}${color ? "，已去过" : ""}`,
      );
    }
  }, [colors, map, convertCoords]);

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
        <button aria-label="回到全省视图" title="回到全省视图" onClick={fit}>
          <ArrowCounterClockwise size={17} />
        </button>
      </div>
    </>
  );
}
