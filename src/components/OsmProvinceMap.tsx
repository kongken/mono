import { useEffect, useRef } from "react";
import L from "leaflet";
import "leaflet/dist/leaflet.css";
import { geometryGcj02ToWgs84 } from "@/lib/coords";
import type { Feature } from "geojson";
import type { MapData } from "@/lib/maps";

const TILE_URL = "https://tile.openstreetmap.org/{z}/{x}/{y}.png";
const ATTRIBUTION =
  '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors';

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

const baseStyle = (color?: string): L.PathOptions => ({
  color: color ? "#ffffff" : "#4f7358",
  weight: color ? 1.5 : 1.2,
  fillColor: color || "#ffffff",
  fillOpacity: color ? 0.68 : 0.12,
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

  useEffect(() => {
    if (!container.current) return;
    const instance = L.map(container.current, {
      zoomSnap: 0.25,
      worldCopyJump: false,
      attributionControl: true,
    });
    instance.zoomControl.setPosition("bottomright");
    const tiles = L.tileLayer(TILE_URL, {
      maxZoom: 19,
      attribution: ATTRIBUTION,
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
            path.setStyle({ weight: 2.2, fillOpacity: 0.5 });
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
    const bounds = geojson.getBounds();
    if (bounds.isValid())
      instance.fitBounds(bounds, {
        paddingTopLeft: [24, 70],
        paddingBottomRight: [24, 24],
      });
    return () => {
      geojson.remove();
      layers.current = new Map();
    };
  }, [map, convertCoords]);

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
    <div
      ref={container}
      className="osm-map"
      role="group"
      aria-label={label}
    />
  );
}
