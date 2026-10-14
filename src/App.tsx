import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { geoMercator, geoPath } from "d3-geo";
import { normalizeMap, type MapData } from "./lib/maps";
import { mapProjection, seaInsetPaths } from "./lib/projection";
import { loadCityMaps, type CityMaps } from "./lib/cityMaps";
import {
  ArrowLeft,
  ArrowUpRight,
  Check,
  Compass,
  DownloadSimple,
  FloppyDisk,
  MapPin,
  Mountains,
  Plus,
  PencilSimple,
  MagnifyingGlass,
  X,
  Trash,
  UploadSimple,
  CaretRight,
  Minus,
  House,
  MapTrifold,
  CalendarBlank,
  ArrowCounterClockwise,
  Stack,
  UserCircle,
  Users,
  Warning,
  WifiSlash,
} from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import regions from "./regions.json";
import {
  automaticColor,
  colors,
  nationalProvinceColors,
  toggleProvince,
  toggleVisit,
  type Saved,
  type Trip,
  type Visit,
} from "./lib/travel";
import {
  InvalidDatesError,
  LEGACY_LOCAL_KEY,
  LOCAL_KEY,
  MAX_BACKUP_BYTES,
  acknowledgeLegacy,
  digestText,
  createLocalWriter,
  loadLocalSaved,
  preserveConflict,
  preserveUnreadable,
  readLegacyChange,
  validateSaved as validateSavedData,
  type LocalLoad,
} from "./lib/persistence";
import {
  TRIP_NOTE_LIMIT,
  canonicalTrips,
  cityLatestDate,
  displayTrips,
  latestTrip,
  newTripId,
  sortVisitsByLatest,
} from "./lib/trips";
import type { Op } from "./lib/sync";
import {
  fingerprint,
  isNoop,
  legacyFingerprint,
  migratedSaved,
  planMigration,
} from "./lib/migration";
import {
  draftChanged,
  mergeDraft,
  type DraftBase,
  type DraftChoices,
  type DraftConflict,
  type DraftValue,
} from "./lib/draft";
import { isMigrated, markMigrated } from "./lib/session";
import AccountDialog from "./components/AccountDialog";
import MigrationDialog from "./components/MigrationDialog";
import LegacyDialog from "./components/LegacyDialog";
import SnapshotDialog from "./components/SnapshotDialog";
import ShareDialog from "./components/ShareDialog";
import CollaborationDialog, {
  type CollaborationTarget,
} from "./components/CollaborationDialog";
import MediaDialog, { CityMediaPreview } from "./components/MediaDialog";
import { collaborationApi, type CollaborationOverview } from "./api/collaboration";
import {
  canEditSharedTrip,
  indexActiveSharedTripsByTripId,
  roleLabel,
} from "./lib/collaboration";
import CityPicker from "./components/CityPicker";
import { readBoot, useCloud } from "./hooks/useCloud";
import TravelReport from "./components/TravelReport";
import {
  totalCities,
  percentage,
  visitedCityCount,
  visitedTripCount,
  isDistrictDestination,
} from "./lib/statistics";
import "./App.css";

import type { ViewBounds } from "./components/OsmProvinceMap";
const OsmProvinceMap = lazy(() => import("./components/OsmProvinceMap"));
const BASEMAP_STORAGE = "shanhai-basemap-v1";
// Taiwan (WGS-84 source), Hong Kong and Macau are not GCJ-02 shifted.
const WGS84_PROVINCES = ["710000", "810000", "820000"];
// Initial national OSM view (WGS-84): mainland China without the far south islands.
const NATIONAL_VIEW: ViewBounds = [
  [18, 73],
  [53.6, 135.1],
];
function loadBasemap() {
  try {
    return localStorage.getItem(BASEMAP_STORAGE) === "osm";
  } catch {
    return false;
  }
}

type Region = (typeof regions)[number];
const empty: Saved = { version: 2, visits: [], provinces: {} };
const cityName = (code: string) =>
  regions.flatMap((r) => r.cities).find((c) => c.code === code)?.name ?? code;
const conflictLabel = (op: Op) => {
  switch (op.type) {
    case "upsertVisit":
    case "legacyUpsertVisit":
      return `${op.visit.name}（城市足迹）`;
    case "deleteVisit":
      return `${cityName(op.code)}（取消打卡及其全部旅行）`;
    case "upsertTrip":
      return `${cityName(op.code)} · ${op.trip.date || "日期未记录"} 的旅行`;
    case "deleteTrip":
      return `${cityName(op.code)} · 删除一次旅行`;
    default:
      return `${provinceByCode(op.province)?.name ?? op.province}（省份标记）`;
  }
};
const today = () => new Date().toLocaleDateString("en-CA");
const provinceByCode = (code: string) => regions.find((p) => p.code === code);
// City/district code -> province code, and the codes already in WGS-84.
const cityProvince = new Map(
  regions.flatMap((r) => r.cities.map((c) => [c.code, r.code] as const)),
);
const wgs84Codes = new Set([
  ...WGS84_PROVINCES,
  ...regions
    .filter((r) => WGS84_PROVINCES.includes(r.code))
    .flatMap((r) => r.cities.map((c) => c.code)),
]);
const validateSaved = (data: unknown) => validateSavedData(data, regions);
const loadSaved = () => loadLocalSaved(localStorage, regions);
const writerFor = (load: LocalLoad) =>
  createLocalWriter(
    localStorage,
    "canonicalRaw" in load ? load.canonicalRaw : null,
    "legacyDigest" in load ? load.legacyDigest : undefined,
  );
type Unreadable = Extract<LocalLoad, { status: "unreadable" }>;

function App() {
  const [initialLoad] = useState(loadSaved);
  // A restored account session starts from its cloud copy, never from the
  // local footprints saved before signing in.
  const [boot] = useState(() => readBoot(validateSaved));
  const [saved, setSaved] = useState<Saved>(
    boot.session ? (boot.cache?.saved ?? empty) : initialLoad.saved,
  );
  // While set, local data failed to load: nothing is written so the original
  // cache stays intact until the user chooses how to proceed.
  const [unreadable, setUnreadable] = useState<Unreadable | null>(
    !boot.session && initialLoad.status === "unreadable" ? initialLoad : null,
  );
  const [rawExported, setRawExported] = useState(false);
  const [confirmEmpty, setConfirmEmpty] = useState(false);
  const [unreadableError, setUnreadableError] = useState("");
  const [province, setProvince] = useState<Region | null>(null);
  const [map, setMap] = useState<MapData | null>(null);
  const [mapError, setMapError] = useState(false);
  const [retry, setRetry] = useState(0);
  // National map fill: by province (default) or by city. Not persisted.
  const [fillMode, setFillMode] = useState<"province" | "city">("province");
  const [cityMaps, setCityMaps] = useState<CityMaps | null>(null);
  const [cityMapError, setCityMapError] = useState(false);
  const [cityRetry, setCityRetry] = useState(0);
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<"map" | "visits">("map");
  const [cityPickerOpen, setCityPickerOpen] = useState(false);
  const [editor, setEditor] = useState(false);
  const [reportOpen, setReportOpen] = useState(false);
  const [draftProvince, setDraftProvince] = useState("330000");
  const [draftCity, setDraftCity] = useState("");
  const [draftColor, setDraftColor] = useState(colors[0].value);
  const [draftTrips, setDraftTrips] = useState<Trip[]>([]);
  // The city whose cancellation awaits confirmation (by code: the live city is read when it is confirmed).
  const [cancelCity, setCancelCity] = useState<string | null>(null);
  // The city as the editor opened it, to tell the user's edits from changes that arrive meanwhile.
  const [editBase, setEditBase] = useState<DraftBase | null>(null);
  const [editConflict, setEditConflict] = useState<{
    conflicts: DraftConflict[];
    choices: DraftChoices;
    /** The same record changed again while the question was open; the choice was asked for again. */
    refreshed?: boolean;
  } | null>(null);
  // The editor was left for the media window and comes back with its draft.
  const [returnToEditor, setReturnToEditor] = useState(false);
  // Which photo button opened the media window, so focus goes back to it.
  const mediaOpener = useRef<string | null>(null);
  const [toast, setToast] = useState("");
  const [undoVisit, setUndoVisit] = useState<Visit | null>(null);
  const [storageError, setStorageError] = useState(false);
  const [hover, setHover] = useState("");
  const [zoom, setZoom] = useState(1);
  const [basemap, setBasemap] = useState(loadBasemap);
  const [importOpen, setImportOpen] = useState<{
    saved: Saved;
    cleared: number;
  } | null>(null);
  const [accountOpen, setAccountOpen] = useState(false);
  const [migrationOpen, setMigrationOpen] = useState(false);
  const [snapshotsOpen, setSnapshotsOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [collabOpen, setCollabOpen] = useState(false);
  const [collabTarget, setCollabTarget] = useState<CollaborationTarget>(null);
  const [collabOverview, setCollabOverview] =
    useState<CollaborationOverview | null>(null);
  const [mediaTarget, setMediaTarget] = useState<{
    region: string;
    tripId: string;
  } | null>(null);
  // Another tab changed this device's footprints; null when there is no clash.
  const [localClash, setLocalClash] = useState<{
    other: string | null;
    /** The other page wrote the legacy key before this device was upgraded. */
    legacy?: boolean;
  } | null>(null);
  // The exact text of the other page's data that was exported, never a plain "yes": the
  // other page may write again, and only the version that was exported may be replaced.
  const [exportedOther, setExportedOther] = useState<string | null>(null);
  // Data an older page wrote to its own key after this version took over.
  const [legacyLocal, setLegacyLocal] = useState<{
    raw: string;
    saved: Saved | null;
    digest: string;
  } | null>(() =>
    !boot.session && initialLoad.status === "ok" && initialLoad.legacyChange
      ? { ...initialLoad.legacyChange, digest: initialLoad.legacyDigest }
      : null,
  );
  // Digest of the older page's data the user postponed; a newer change is offered again.
  const [legacyHidden, setLegacyHidden] = useState<string | null>(null);
  const [clashError, setClashError] = useState("");
  const [initialWriter] = useState(() => writerFor(initialLoad));
  const writer = useRef(initialWriter);
  // Accounts whose migration prompt was postponed in this page session.
  const migrationLater = useRef(new Set<string>());
  const fileInput = useRef<HTMLInputElement>(null);
  const savedRef = useRef(saved);
  savedRef.current = saved;
  const notify = (message: string, removed: Visit | null = null) => {
    setToast(message);
    setUndoVisit(removed);
  };
  const cloud = useCloud({
    boot,
    validate: validateSaved,
    notify,
    onCloudData: setSaved,
    onAccountChange: (data) => {
      setProvince(null);
      setQuery("");
      setTab("map");
      setEditor(false);
      setEditConflict(null);
      setReturnToEditor(false);
      setCityPickerOpen(false);
      setReportOpen(false);
      setImportOpen(null);
      setToast("");
      setUndoVisit(null);
      setStorageError(false);
      setMigrationOpen(false);
      setSnapshotsOpen(false);
      setShareOpen(false);
      setCollabOpen(false);
      setCollabTarget(null);
      setCollabOverview(null);
      setMediaTarget(null);
      setLocalClash(null);
      setExportedOther(null);
      setClashError("");
      if (data) {
        setSaved(data);
        setUnreadable(null);
      } else {
        // Back to this device's own footprints.
        const local = loadSaved();
        writer.current = writerFor(local);
        setSaved(local.saved);
        setUnreadable(local.status === "unreadable" ? local : null);
        setLegacyLocal(
          local.status === "ok" && local.legacyChange
            ? { ...local.legacyChange, digest: local.legacyDigest }
            : null,
        );
        setLegacyHidden(null);
        setRawExported(false);
        setConfirmEmpty(false);
        setUnreadableError("");
      }
    },
  });
  // All user edits go through here so cloud mode can sync them, and refuse
  // them while the cloud copy is not loaded and confirmed.
  const commit = (update: (s: Saved) => Saved, atomic = false) => {
    if (cloud.mode === "cloud") {
      if (!cloud.canWrite) {
        notify(
          cloud.cacheFailed
            ? "本机保存失败，请先导出备份"
            : cloud.sync === "conflict"
              ? "请先处理相同记录的同步冲突"
              : "正在连接云端，请稍候再修改",
        );
        return false;
      }
      const next = update(savedRef.current);
      if (!cloud.push(next, atomic)) return false;
      savedRef.current = next;
      setSaved(next);
      return true;
    }
    if (unreadable || localClash) {
      notify("本机数据暂时无法保存，请先处理页面上的提示");
      return false;
    }
    const next = update(savedRef.current);
    const result = writer.current.write(next);
    if (result.status === "conflict") {
      setLocalClash({ other: result.other });
      return false;
    }
    setStorageError(result.status === "error");
    savedRef.current = next;
    setSaved(next);
    return true;
  };
  // Offer to move this device's footprints once the account's cloud data is
  // confirmed. Nothing is prompted when there is nothing to move.
  const userId = cloud.user?.id;
  const cloudReady =
    cloud.mode === "cloud" &&
    cloud.fresh &&
    cloud.canWrite &&
    !cloud.pending &&
    cloud.sync === "idle";
  useEffect(() => {
    if (!userId || !cloudReady || migrationOpen) return;
    if (migrationLater.current.has(userId)) return;
    const local = loadSaved();
    if (local.status !== "ok") return;
    const print = fingerprint(local.saved);
    if (
      !local.saved.visits.length && !Object.keys(local.saved.provinces).length
    )
      return;
    const legacyPrint = legacyFingerprint(local.saved);
    if (
      isMigrated(localStorage, userId, print) ||
      (legacyPrint && isMigrated(localStorage, userId, legacyPrint))
    )
      return;
    if (isNoop(planMigration(local.saved, savedRef.current))) {
      markMigrated(localStorage, userId, print);
      return;
    }
    setMigrationOpen(true);
  }, [userId, cloudReady, migrationOpen]);
  useEffect(() => {
    // Footprints are written when they are edited (see commit), never on load,
    // so merely opening the page cannot replace stored data. Cloud data is not
    // written to the local key: that key belongs to local mode.
    if (cloud.mode === "cloud") return;
    const onStorage = (event: StorageEvent) => {
      if (event.storageArea && event.storageArea !== localStorage) return;
      if (event.key === LEGACY_LOCAL_KEY) {
        if (localStorage.getItem(LOCAL_KEY) === null) {
          // Not upgraded yet: the legacy key is still this device's data.
          setLocalClash({ other: localStorage.getItem(LEGACY_LOCAL_KEY), legacy: true });
          return;
        }
        // Upgraded: this version's data is untouched; the older page's change is offered.
        const change = readLegacyChange(localStorage, regions);
        if (change) {
          setLegacyLocal(change);
          setLegacyHidden(null);
        }
        return;
      }
      if (event.key !== LOCAL_KEY && event.key !== null) return;
      setLocalClash({ other: localStorage.getItem(LOCAL_KEY) });
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [cloud.mode]);
  useEffect(() => {
    try {
      localStorage.setItem(BASEMAP_STORAGE, basemap ? "osm" : "off");
    } catch {
      // The preference is optional; the map keeps working without storage.
    }
  }, [basemap]);
  useEffect(() => {
    if (!toast) return;
    const timeout = setTimeout(() => setToast(""), undoVisit ? 8000 : 4000);
    return () => clearTimeout(timeout);
  }, [toast, undoVisit]);
  useEffect(() => {
    const controller = new AbortController();
    setMap(null);
    setMapError(false);
    setZoom(1);
    setHover("");
    fetch(
      `${import.meta.env.BASE_URL}maps/${province?.code || "100000"}.json`,
      { signal: controller.signal },
    )
      .then((r) => {
        if (!r.ok) throw new Error();
        return r.json();
      })
      .then((data) => setMap(normalizeMap(data)))
      .catch((e) => {
        if (e.name !== "AbortError") setMapError(true);
      });
    return () => controller.abort();
  }, [province, retry]);
  // City boundaries are only requested the first time the city fill is chosen.
  const needCityMaps = !province && fillMode === "city" && !cityMaps;
  useEffect(() => {
    if (!needCityMaps) return;
    const controller = new AbortController();
    setCityMapError(false);
    loadCityMaps(
      import.meta.env.BASE_URL,
      regions.map((r) => r.code),
      controller.signal,
    )
      .then(setCityMaps)
      .catch((e) => {
        if (e.name !== "AbortError") setCityMapError(true);
      });
    return () => controller.abort();
  }, [needCityMaps, cityRetry]);
  const visitedProvinces = new Set([
    ...Object.keys(saved.provinces),
    ...saved.visits.map((v) => v.province),
  ]);
  const provinceColors = useMemo(() => nationalProvinceColors(saved), [saved]);
  const provinceColor = (code: string) => provinceColors[code];
  const currentVisits = sortVisitsByLatest(
    saved.visits.filter((v) => !province || v.province === province.code),
  );
  const tripTotal = visitedTripCount(saved);
  const filteredProvinces = regions.filter(
    (p) =>
      p.name.includes(query) || p.cities.some((c) => c.name.includes(query)),
  );
  const draftRegion = provinceByCode(draftProvince)!;
  const cityCount = visitedCityCount(saved);
  const isDistrict = province && isDistrictDestination(province.code);
  const unit = isDistrict ? "区县" : "城市";
  const projection = useMemo(
    () => (map ? mapProjection(map, province?.code) : null),
    [map, province],
  );
  const path = projection ? geoPath(projection) : null;
  const sansha =
    province?.code === "460000"
      ? map?.features.find((f) => String(f.properties.adcode) === "460300")
      : undefined;
  const sanshaPath = sansha
    ? geoPath(
        geoMercator().fitExtent(
          [
            [670, 355],
            [795, 505],
          ],
          sansha,
        ),
      )(sansha)
    : null;
  const drawPath = (feature: MapData["features"][number]) =>
    String(feature.properties.adcode) === "460300" && sanshaPath
      ? sanshaPath
      : path?.(feature) || "";
  const visitColors = useMemo(
    () => Object.fromEntries(saved.visits.map((v) => [v.code, v.color])),
    [saved.visits],
  );
  // Per-city paths for the national map, drawn with the national projection.
  const cityShapes = useMemo(() => {
    if (province || !cityMaps || !projection) return null;
    const draw = geoPath(projection);
    return regions.flatMap((r) =>
      (cityMaps[r.code]?.features ?? [])
        .filter((f) => f.properties.name)
        .map((f) => ({
          code: String(f.properties.adcode),
          province: r.code,
          name: f.properties.name,
          d: draw(f) || "",
        })),
    );
  }, [province, cityMaps, projection]);
  const cityMode = !province && fillMode === "city";
  const cityLoading = cityMode && !cityShapes && !cityMapError;
  const showOsm = basemap;
  // National OSM data: every city as one collection, tinted where only the province is marked.
  const cityCollection = useMemo<MapData | null>(
    () =>
      cityMaps
        ? {
            type: "FeatureCollection",
            features: regions.flatMap((r) => cityMaps[r.code]?.features ?? []),
          }
        : null,
    [cityMaps],
  );
  const provinceNamed = useMemo(
    () =>
      map && !province
        ? { ...map, features: map.features.filter((f) => f.properties.name) }
        : null,
    [map, province],
  );
  const cityTints = useMemo(
    () =>
      Object.fromEntries(
        [...cityProvince].flatMap(([code, p]) =>
          saved.provinces[p] ? [[code, saved.provinces[p]]] : [],
        ),
      ),
    [saved.provinces],
  );
  // Cancelling a city check-in also deletes its trips; two or more need a
  // confirmation first, one can be taken back with the undo toast. The city is
  // read when the deletion happens, so trips that arrived while the confirmation
  // was open are removed knowingly and come back with the undo.
  const cancelVisit = (code: string) => {
    const box: { visit?: Visit } = {};
    if (
      !commit((s) => {
        box.visit = s.visits.find((v) => v.code === code);
        return box.visit
          ? { ...s, visits: s.visits.filter((v) => v.code !== code) }
          : s;
      })
    )
      return false;
    const removed = box.visit;
    if (!removed) {
      notify(`${cityName(code)} 的足迹已经不存在`);
      return true;
    }
    notify(
      removed.trips.length
        ? `已取消 ${removed.name} 的足迹，${removed.trips.length} 次旅行记录一并移除`
        : `已取消 ${removed.name} 的足迹`,
      removed,
    );
    return true;
  };
  const toggleCity = (provinceCode: string, cityCode: string) => {
    const city = provinceByCode(provinceCode)?.cities.find(
      (c) => c.code === cityCode,
    );
    if (!city) return;
    const removed = savedRef.current.visits.find((v) => v.code === cityCode);
    if (removed) {
      if (removed.trips.length >= 2) setCancelCity(cityCode);
      else cancelVisit(cityCode);
      return;
    }
    if (!commit((s) => toggleVisit(s, provinceCode, city, today()))) return;
    notify(`已标记去过 ${city.name}，颜色已自动选择`);
  };
  const defaultTrip = (): Trip => ({ id: newTripId(), date: today(), note: "" });
  const loadDraft = (p: string, city: string) => {
    const existing = savedRef.current.visits.find((v) => v.code === city);
    const color = existing?.color || automaticColor(savedRef.current, p);
    setDraftProvince(p);
    setDraftCity(city);
    setDraftColor(color);
    setDraftTrips(
      existing ? displayTrips(existing.trips) : city ? [defaultTrip()] : [],
    );
    setEditBase(
      city
        ? { code: city, existed: !!existing, color, trips: existing?.trips ?? [] }
        : null,
    );
    setEditConflict(null);
  };
  // Radix returns focus only to a DialogTrigger, which the controlled editor does
  // not have: remember what opened it and put focus back there when it closes.
  const editorOpener = useRef<HTMLElement | null>(null);
  const openEditor = (p: string, city = "") => {
    const active = document.activeElement;
    editorOpener.current =
      active instanceof HTMLElement && active !== document.body ? active : null;
    loadDraft(p, city);
    setEditor(true);
  };
  const returnEditorFocus = () => {
    // Handed over to the photos window: that window and its close handler own focus.
    if (mediaOpener.current !== null) return;
    const opener = editorOpener.current;
    editorOpener.current = null;
    const usable = (el: HTMLElement | null): el is HTMLElement =>
      !!el &&
      el.isConnected &&
      !(el as HTMLButtonElement).disabled &&
      el.getClientRects().length > 0;
    // The opening city card may be gone (a cancelled city): use the always-present add button.
    const target = usable(opener)
      ? opener
      : [
          ...document.querySelectorAll<HTMLElement>("[data-add-footprint]"),
        ].find((el) => usable(el));
    target?.focus();
  };
  const selectDraftCity = (code: string) => loadDraft(draftProvince, code);
  const changeDraftTrip = (id: string, patch: Partial<Trip>) =>
    setDraftTrips((trips) =>
      trips.map((t) => (t.id === id ? { ...t, ...patch } : t)),
    );
  // What the editor holds, ready to merge. An untouched note is kept exactly as
  // stored; only edited text is trimmed.
  const currentDraft = (): DraftValue | null => {
    const city = draftRegion.cities.find((c) => c.code === draftCity);
    if (!city || !editBase) return null;
    return {
      code: city.code,
      name: city.name,
      province: draftProvince,
      color: draftColor,
      trips: draftTrips.map((t) => {
        const before = editBase.trips.find((o) => o.id === t.id);
        return {
          ...t,
          note: before && before.note === t.note ? t.note : t.note.trim(),
        };
      }),
    };
  };
  // Saving applies the user's own edits (the difference to the city as the editor
  // opened) onto the city as it is now. Changes that arrived meanwhile stay; the
  // same record changed on both sides asks first.
  // The conflicts as they are against the city right now. Choices are only ever
  // applied to the exact values the user was shown: a conflicting record that changed
  // again drops its choice, a new conflict asks, and one that no longer conflicts goes.
  const liveConflicts = (): DraftConflict[] => {
    const draft = currentDraft();
    if (!draft || !editBase) return [];
    const result = mergeDraft(
      editBase,
      draft,
      savedRef.current.visits.find((v) => v.code === draft.code),
      {},
    );
    return result.kind === "ask" ? result.conflicts : [];
  };
  const reconcileConflict = (
    prev: NonNullable<typeof editConflict>,
    fresh: DraftConflict[],
  ): NonNullable<typeof editConflict> | null => {
    if (!fresh.length) return null;
    const shown = (c: DraftConflict) => JSON.stringify(c);
    const known = new Map(prev.conflicts.map((c) => [shown(c), c.key]));
    const unchanged =
      fresh.length === prev.conflicts.length &&
      fresh.every((c) => known.has(shown(c)));
    if (unchanged) return prev;
    const choices: DraftChoices = {};
    for (const c of fresh) {
      const choice = prev.choices[c.key];
      if (choice && known.has(shown(c))) choices[c.key] = choice;
    }
    return { conflicts: fresh, choices, refreshed: true };
  };
  useEffect(() => {
    if (!editConflict) return;
    const next = reconcileConflict(editConflict, liveConflicts());
    if (next === editConflict) return;
    setEditConflict(next);
    if (!next)
      notify("另一台设备的修改已与你的编辑一致，没有需要选择的冲突，请重新保存");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved]);
  const saveVisit = (choices: DraftChoices = {}) => {
    const draft = currentDraft();
    if (!draft || !editBase) return;
    if (editConflict) {
      // Confirming: never apply choices to values that were not on screen.
      const next = reconcileConflict(editConflict, liveConflicts());
      if (next !== editConflict) {
        setEditConflict(next);
        if (!next)
          notify("另一台设备的修改已与你的编辑一致，没有需要选择的冲突，请重新保存");
        return;
      }
    }
    const result = mergeDraft(
      editBase,
      draft,
      savedRef.current.visits.find((v) => v.code === draft.code),
      choices,
    );
    if (result.kind === "ask") {
      setEditConflict({ conflicts: result.conflicts, choices });
      return;
    }
    if (result.kind === "noop") {
      setEditConflict(null);
      setEditor(false);
      notify(`已保留 ${draft.name} 的取消状态，没有保存这次编辑`);
      return;
    }
    const merged = result.visit;
    if (
      !commit((s) => ({
        ...s,
        visits: s.visits.some((v) => v.code === merged.code)
          ? s.visits.map((v) => (v.code === merged.code ? merged : v))
          : [...s.visits, merged],
      }))
    )
      return;
    setEditConflict(null);
    setEditor(false);
    notify(
      `已记录 ${draft.name} 的足迹${merged.trips.length > 1 ? `（共 ${merged.trips.length} 次旅行）` : ""}`,
    );
  };
  // The media window opens over a draft with unsaved edits: the draft is kept and
  // comes back when the window closes. An untouched draft is reloaded instead,
  // so it shows the city as it is by then.
  const openMedia = (tripId: string) => {
    mediaOpener.current = tripId;
    setReturnToEditor(true);
    setEditor(false);
    setMediaTarget({ region: draftCity, tripId });
  };
  // After the media window closes over the editor, focus goes back to the button
  // that opened it. An effect, because the editor may be mid-way through its exit
  // animation and then never remounts (so it would not autofocus again).
  useEffect(() => {
    if (!editor || mediaTarget || mediaOpener.current === null) return;
    const id = mediaOpener.current;
    mediaOpener.current = null;
    const button = document.querySelector<HTMLButtonElement>(
      `.editor-dialog [data-media-opener="${CSS.escape(id)}"]`,
    );
    if (button && !button.disabled) button.focus();
  }, [editor, mediaTarget]);
  const mediaOpen = cloud.enabled && !!cloud.user && mediaTarget !== null;
  const closeMedia = () => {
    setMediaTarget(null);
    if (!returnToEditor) return;
    setReturnToEditor(false);
    const draft = currentDraft();
    if (!draft || !editBase || !draftChanged(editBase, draft))
      loadDraft(draftProvince, draftCity);
    setEditor(true);
  };
  useEffect(() => {
    if (!editor || !cloud.enabled || !cloud.user) return;
    let alive = true;
    void collaborationApi
      .getOverview()
      .then((res) => {
        if (alive) setCollabOverview(res);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [editor, cloud.enabled, cloud.user]);
  const sharedTripsByTripId = useMemo(
    () =>
      collabOverview
        ? indexActiveSharedTripsByTripId(collabOverview.sharedTrips)
        : new Map(),
    [collabOverview],
  );
  const openTripCollaboration = (tripId: string) => {
    setEditor(false);
    setCollabTarget({ regionCode: draftCity, tripId });
    setCollabOpen(true);
  };
  const exportData = () => {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(saved, null, 2)], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `山海足迹-${today()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    notify("足迹备份已导出");
  };
  const exportRaw = () => {
    if (!unreadable) return;
    const url = URL.createObjectURL(
      new Blob([unreadable.raw], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `山海足迹-原始数据-${today()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    setRawExported(true);
    setUnreadableError("");
  };
  const resolveUnreadable = (next: Saved, message: string) => {
    if (!unreadable) return;
    // Keep a copy in the browser; if that fails, require a file export first.
    const kept = preserveUnreadable(localStorage, unreadable.sourceKey, unreadable.raw);
    if (!kept && !rawExported) {
      setUnreadableError(
        "浏览器无法再保存一份原始数据副本，请先点击“导出原始数据”，再继续。",
      );
      return;
    }
    // The result goes to this version's own key; an unreadable legacy key stays as it is.
    const fresh = createLocalWriter(
      localStorage,
      unreadable.canonicalRaw,
      unreadable.legacyDigest,
    );
    const result = fresh.write(next);
    if (result.status !== "ok") {
      setUnreadableError(
        result.status === "conflict"
          ? "本机数据刚刚又被另一个页面修改，请刷新页面后重新处理。"
          : "浏览器无法保存数据，请先导出原始数据并释放存储空间。",
      );
      return;
    }
    writer.current = fresh;
    savedRef.current = next;
    setSaved(next);
    setUnreadable(null);
    setConfirmEmpty(false);
    setUnreadableError("");
    notify(
      kept
        ? `${message}，原始数据已另存为浏览器内的副本`
        : `${message}，原始数据已导出为文件`,
    );
  };
  // Another tab changed the local footprints: the user decides whose data wins.
  const loadOtherTab = () => {
    const other = loadSaved();
    writer.current = writerFor(other);
    savedRef.current = other.saved;
    setSaved(other.saved);
    setUnreadable(other.status === "unreadable" ? other : null);
    setLegacyLocal(
      other.status === "ok" && other.legacyChange
        ? { ...other.legacyChange, digest: other.legacyDigest }
        : null,
    );
    setLegacyHidden(null);
    setRawExported(false);
    setConfirmEmpty(false);
    setUnreadableError("");
    setLocalClash(null);
    setExportedOther(null);
    setClashError("");
    notify("已载入另一个页面的最新数据");
  };
  // An older page's data, taken in only where it adds something: records this
  // version has stay as they are (a record both sides have keeps this version's
  // content), nothing is deleted, and nothing happens until the user presses merge.
  const legacyDownload = (raw: string, name: string) => {
    const url = URL.createObjectURL(new Blob([raw], { type: "application/json" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = name;
    a.click();
    URL.revokeObjectURL(url);
  };
  const legacyNotice = cloud.mode === "cloud" ? cloud.legacyNotice : legacyLocal;
  const mergeLegacy = () => {
    if (!legacyNotice?.saved) return false;
    const incoming = legacyNotice.saved;
    if (
      !commit((s) => migratedSaved(s, planMigration(incoming, s), {}))
    )
      return false;
    const ok =
      cloud.mode === "cloud"
        ? cloud.acknowledgeLegacy()
        : acknowledgeLegacy(localStorage, legacyNotice.digest);
    if (!ok) return false;
    setLegacyLocal(null);
    notify("已把旧版页面的新增内容合并进来，相同记录保留了当前内容");
    return true;
  };
  const keepLegacyAside = () => {
    if (!legacyNotice) return;
    const ok =
      cloud.mode === "cloud"
        ? cloud.acknowledgeLegacy()
        : acknowledgeLegacy(localStorage, legacyNotice.digest);
    if (!ok) {
      notify("无法记录处理结果，浏览器存储不可用");
      return;
    }
    setLegacyLocal(null);
    notify("旧版页面的数据仍保留在浏览器中，没有合并");
  };
  const exportOtherTab = () => {
    if (localClash?.other == null) return;
    const url = URL.createObjectURL(
      new Blob([localClash.other], { type: "application/json" }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `山海足迹-另一个页面的数据-${today()}.json`;
    a.click();
    URL.revokeObjectURL(url);
    setExportedOther(localClash.other);
    setClashError("");
  };
  const overwriteOtherTab = () => {
    if (!localClash) return;
    const other = localClash.other;
    let kept = false;
    if (other !== null) {
      // The data about to be replaced is the other page's: keep that text, not this page's.
      kept = preserveConflict(localStorage, other);
      if (!kept && exportedOther !== other) {
        setClashError(
          "浏览器无法保存另一个页面数据的副本，请先点击“导出另一个页面的数据”，再继续覆盖；也可以改为载入另一个页面的数据。",
        );
        return;
      }
    }
    // An older page's write before the upgrade is accounted for by its digest.
    if (localClash.legacy)
      writer.current = createLocalWriter(
        localStorage,
        null,
        digestText(other),
      );
    else writer.current.adopt(other);
    const result = writer.current.write(savedRef.current);
    if (result.status !== "ok") {
      setClashError(
        result.status === "conflict"
          ? "数据又被修改，请再试一次。"
          : "浏览器无法保存数据，请先导出本页数据。",
      );
      if (result.status === "conflict") setLocalClash({ other: result.other });
      return;
    }
    setLocalClash(null);
    setExportedOther(null);
    setClashError("");
    notify(
      other === null
        ? "已保存本页数据"
        : kept
          ? "已用本页数据覆盖，另一个页面的数据保留为浏览器内的副本"
          : "已用本页数据覆盖，另一个页面的数据已导出为文件",
    );
  };
  const importFile = async (file?: File) => {
    if (!file) return;
    try {
      if (file.size > MAX_BACKUP_BYTES)
        throw new Error("文件过大，请选择足迹备份文件");
      const data = JSON.parse(await file.text());
      try {
        setImportOpen({ saved: validateSaved(data), cleared: 0 });
      } catch (e) {
        // Dates that are not on the calendar are cleared only after the user agrees.
        if (!(e instanceof InvalidDatesError)) throw e;
        setImportOpen({
          saved: validateSavedData(data, regions, { correctDates: true }),
          cleared: e.count,
        });
      }
    } catch (e) {
      notify(e instanceof Error ? e.message : "无法读取文件");
    }
    if (fileInput.current) fileInput.current.value = "";
  };
  const navigate = (p: Region | null) => {
    setProvince(p);
    setQuery("");
    setTab("map");
  };

  if (cloud.busy || (cloud.mode === "cloud" && cloud.waiting)) {
    return (
      <div className="cloud-gate">
        <Compass size={40} weight="duotone" />
        {cloud.busy || !cloud.loadFailed ? (
          <p role="status">{cloud.busy || "正在加载云端足迹…"}</p>
        ) : (
          <>
            <h2>无法加载云端足迹</h2>
            <p role="alert">{cloud.loadError}</p>
            <div className="dialog-actions">
              <Button
                variant="outline"
                onClick={() => {
                  try {
                    cloud.exportRawCache();
                    void cloud
                      .logout(true)
                      .catch((error) => notify(String(error)));
                  } catch {
                    notify("导出失败，未清除账号缓存");
                  }
                }}
              >
                导出原始账号缓存并退出
              </Button>
              <Button onClick={() => void cloud.retry()}>重试</Button>
            </div>
          </>
        )}
      </div>
    );
  }

  return (
    <div className="app-shell">
      <header className="header">
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            navigate(null);
          }}
        >
          <span className="brand-icon">
            <Compass size={26} weight="duotone" />
          </span>
          <span>
            山海足迹<small>SHANHAI TRAVEL</small>
          </span>
        </a>
        <nav aria-label="主导航">
          <button
            className={tab === "map" ? "nav-active" : ""}
            onClick={() => setTab("map")}
          >
            <MapTrifold size={18} />
            我的地图
          </button>
          <button
            className={tab === "visits" ? "nav-active" : ""}
            onClick={() => setTab("visits")}
          >
            <MapPin size={18} />
            旅行足迹<span className="nav-count">{saved.visits.length}</span>
          </button>
        </nav>
        <div className="header-end">
          <span className="local-status">
            <span />
            {cloud.mode === "cloud"
              ? cloud.cacheFailed
                ? "本机保存失败"
                : cloud.sync === "conflict"
                  ? "同步冲突待处理"
                  : cloud.sync === "syncing"
                    ? "同步中…"
                    : cloud.loadFailed || cloud.pending
                      ? "本机已保存，待同步"
                      : "已同步到云端"
              : storageError
                ? "保存失败，请导出备份"
                : "本地自动保存"}
          </span>
          <Button
            variant="outline"
            className="export-button"
            onClick={exportData}
          >
            <DownloadSimple size={17} />
            导出足迹
          </Button>
          {cloud.enabled ? (
            <button
              className="account-button"
              aria-label={cloud.user ? "账号" : "登录"}
              onClick={() => setAccountOpen(true)}
            >
              {cloud.user ? (
                <span className="account-initial">
                  {(cloud.user.displayName || cloud.user.username || "?")
                    .slice(0, 1)
                    .toUpperCase()}
                </span>
              ) : (
                <>
                  <UserCircle size={18} />
                  <span className="account-label">登录</span>
                </>
              )}
            </button>
          ) : (
            <span className="avatar">
              <Mountains size={22} />
            </span>
          )}
        </div>
      </header>
      {cloud.mode === "cloud" &&
        (cloud.loadFailed || cloud.cacheFailed) &&
        !cloud.waiting && (
          <div className="cloud-banner" role="status">
            <WifiSlash size={17} />
            <span>
              {cloud.cacheFailed
                ? "本机缓存无法保存，已暂停编辑，请先导出备份。"
                : `云端未连接，修改保留在本机，联网后重试同步。${cloud.loadError}`}
            </span>
            {cloud.loadFailed && (
              <button onClick={() => void cloud.retry()}>重试</button>
            )}
          </div>
        )}
      <Dialog open={cloud.sync === "conflict"} onOpenChange={() => {}}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>多台设备修改了相同足迹</DialogTitle>
            <DialogDescription>
              有 {cloud.conflicts}{" "}
              条城市足迹、旅行记录或省份标记冲突（包括删除）。请选择这些冲突记录保留哪一份；其他记录会自动合并。处理前可导出当前本机足迹。
            </DialogDescription>
          </DialogHeader>
          <ul>
            {cloud.conflictRecords.map((op, i) => (
              <li key={i}>
                {conflictLabel(op)}
              </li>
            ))}
          </ul>
          <div className="dialog-actions">
            <Button variant="outline" onClick={exportData}>
              导出本机足迹
            </Button>
            <Button
              variant="outline"
              onClick={() => cloud.resolveConflict("cloud")}
            >
              冲突以云端为准
            </Button>
            <Button onClick={() => cloud.resolveConflict("local")}>
              冲突以本机为准
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <main>
        <section className="intro">
          <div>
            <div className="eyebrow">
              <span />
              每一段旅程，都值得被记住
            </div>
            <h1>
              世界很大，<span>慢慢留下足迹。</span>
            </h1>
            <p>用颜色收藏走过的城市，让回忆在地图上生长。</p>
          </div>
          <div className="stats">
            <div>
              <strong>
                {visitedProvinces.size}
                <small>/ 34</small>
              </strong>
              <span>已探索省份</span>
            </div>
            <i />
            <div>
              <strong>
                {cityCount}
                <small>/ {totalCities}</small>
              </strong>
              <span>
                已打卡城市 · {percentage(cityCount, totalCities)}
              </span>
            </div>
            <i />
            <div>
              <strong>{tripTotal}</strong>
              <span>旅行次数</span>
            </div>
            <i />
            <div>
              <strong>
                {Math.round((visitedProvinces.size / 34) * 100)}
                <small>%</small>
              </strong>
              <span>中国探索度</span>
            </div>
          </div>
        </section>
        <div className="report-entry">
          <span>每一站都算数，看看你的山海进度。</span>
          <Button variant="outline" onClick={() => setReportOpen(true)}>
            <DownloadSimple size={17} />
            足迹统计 / 生成图片
          </Button>
        </div>
        <div className="workspace">
          <section className="map-panel">
            <div className="map-toolbar">
              <div className="breadcrumbs">
                <button onClick={() => navigate(null)}>
                  <House size={16} />
                  中国
                </button>
                {province && (
                  <>
                    <CaretRight size={13} />
                    <span>{province.short}</span>
                  </>
                )}
              </div>
              <span className="map-mode">
                {tab === "visits"
                  ? "足迹收藏"
                  : province
                    ? `${unit}地图`
                    : "全国地图"}
              </span>
              {province && (
                <Button
                  size="sm"
                  className="mobile-city-picker"
                  onClick={() => setCityPickerOpen(true)}
                >
                  <MagnifyingGlass size={16} />
                  选择城市
                </Button>
              )}
              <Button
                size="sm"
                className={`add-main ${province ? "province-add" : ""}`}
                data-add-footprint
                onClick={() => openEditor(province?.code || "330000")}
              >
                <Plus size={16} />
                添加足迹
              </Button>
            </div>
            {tab === "map" ? (
              <div className={`map-stage ${showOsm ? "with-basemap" : ""}`}>
                <div className="map-heading">
                  {province && (
                    <button
                      className="back-button"
                      aria-label="返回全国地图"
                      onClick={() => navigate(null)}
                    >
                      <ArrowLeft size={18} />
                    </button>
                  )}
                  <div>
                    <h2>
                      {province
                        ? `${province.short}，走过的风景`
                        : "我的中国旅行地图"}
                    </h2>
                    <p>
                      {province
                        ? `点击${unit}标记去过，再次点击取消 · 自动配色`
                        : fillMode === "city"
                          ? "按城市着色 · 点击任意城市进入所在省份"
                          : "点击一个省份，开启你的城市足迹"}
                    </p>
                  </div>
                </div>
                {mapError ? (
                  <div className="map-message">
                    地图加载失败
                    <Button
                      variant="outline"
                      onClick={() => setRetry((r) => r + 1)}
                    >
                      重新加载
                    </Button>
                  </div>
                ) : cityMode && cityMapError ? (
                  <div className="map-message">
                    城市地图加载失败
                    <Button
                      variant="outline"
                      onClick={() => setCityRetry((r) => r + 1)}
                    >
                      重新加载
                    </Button>
                  </div>
                ) : !map || !path || !projection || cityLoading ? (
                  <div className="map-message">
                    <Compass className="loading" size={30} />
                    {cityLoading && map ? "正在加载城市边界…" : "正在展开地图…"}
                  </div>
                ) : showOsm ? (
                  <Suspense
                    fallback={
                      <div className="map-message">
                        <Compass className="loading" size={30} />
                        正在加载底图…
                      </div>
                    }
                  >
                    {province ? (
                      <OsmProvinceMap
                        key={province.code}
                        map={map}
                        colors={visitColors}
                        convertCoords={!WGS84_PROVINCES.includes(province.code)}
                        label={`${province.name}${unit}地图（OpenStreetMap 底图）`}
                        onToggle={(code) => toggleCity(province.code, code)}
                        onHover={setHover}
                        onTileError={() =>
                          notify(
                            "OpenStreetMap 底图加载失败，可关闭底图后继续标记",
                          )
                        }
                      />
                    ) : (
                      <OsmProvinceMap
                        key={`national-${fillMode}`}
                        map={
                          cityMode && cityCollection
                            ? cityCollection
                            : (provinceNamed ?? map)
                        }
                        colors={cityMode ? visitColors : provinceColors}
                        softColors={cityMode ? cityTints : undefined}
                        convertCoords
                        skipConvert={(code) => wgs84Codes.has(code)}
                        fine={cityMode}
                        viewBounds={NATIONAL_VIEW}
                        resetLabel="回到全国视图"
                        label={`中国${cityMode ? "城市" : "省份"}地图（OpenStreetMap 底图）`}
                        regionLabel={
                          cityMode
                            ? () => null
                            : (name, code) =>
                                provinceByCode(code)?.short || name
                        }
                        outline={cityMode ? provinceNamed ?? undefined : undefined}
                        outlineLabel={(name, code) =>
                          provinceByCode(code)?.short || name
                        }
                        onOutlineSelect={(code) =>
                          navigate(provinceByCode(code) || null)
                        }
                        onToggle={(code) =>
                          navigate(
                            provinceByCode(
                              cityMode ? (cityProvince.get(code) ?? "") : code,
                            ) || null,
                          )
                        }
                        onHover={(name, code) =>
                          setHover(
                            !name
                              ? ""
                              : cityMode
                                ? `${name} · ${provinceByCode(cityProvince.get(code) ?? "")?.short ?? ""}，点击进入省内地图`
                                : `${name}，点击进入省内地图`,
                          )
                        }
                        onTileError={() =>
                          notify(
                            "OpenStreetMap 底图加载失败，可关闭底图后继续浏览",
                          )
                        }
                      />
                    )}
                  </Suspense>
                ) : (
                  <svg
                    className="china-map"
                    viewBox="0 0 850 620"
                    role="group"
                    aria-label={
                      province ? `${province.name}${unit}地图` : "中国省份地图"
                    }
                  >
                    <defs>
                      <pattern
                        id="dots"
                        width="16"
                        height="16"
                        patternUnits="userSpaceOnUse"
                      >
                        <circle cx="1" cy="1" r="0.6" fill="#d5dcd7" />
                      </pattern>
                    </defs>
                    <rect width="850" height="620" fill="url(#dots)" />
                    <g
                      transform={`translate(${425 * (1 - zoom)},${310 * (1 - zoom)}) scale(${zoom})`}
                    >
                      {cityMode && cityShapes && (
                        <g className="city-layer" aria-hidden="true">
                          {cityShapes.map((shape) => {
                            const color = visitColors[shape.code];
                            const tint = saved.provinces[shape.province];
                            return (
                              <path
                                key={shape.code}
                                className={`city-shape ${color ? "visited" : ""}`}
                                d={shape.d}
                                fill={color || (tint ? `${tint}40` : "#e9eee9")}
                                stroke={color ? "#ffffff" : "#fafcf9"}
                                strokeWidth={0.6}
                                vectorEffect="non-scaling-stroke"
                                onClick={() =>
                                  navigate(provinceByCode(shape.province) || null)
                                }
                                onMouseEnter={() =>
                                  setHover(
                                    `${shape.name} · ${provinceByCode(shape.province)?.short}${color ? " · 已去过" : ""}，点击进入省内地图`,
                                  )
                                }
                                onMouseLeave={() => setHover("")}
                              />
                            );
                          })}
                        </g>
                      )}
                      {map.features
                        .filter((f) => f.properties.name)
                        .map((feature) => {
                          const code = String(feature.properties.adcode),
                            fill = province
                              ? saved.visits.find((v) => v.code === code)?.color
                              : provinceColor(code);
                          const center =
                            feature.properties.centroid ||
                            feature.properties.center;
                          const point =
                            code === "460300" && sanshaPath
                              ? [732, 530]
                              : center
                                ? projection(center)
                                : null;
                          const label = province
                            ? feature.properties.name
                                .replace(/(?:黎族苗族|黎族)自治县$/, "")
                                .replace(/市$/, "")
                            : provinceByCode(code)?.short ||
                              feature.properties.name;
                          const activate = () => {
                            if (province) toggleCity(province.code, code);
                            else navigate(provinceByCode(code) || null);
                          };
                          return (
                            <g
                              key={code}
                              className={`map-region ${fill ? "visited" : ""} ${cityMode ? "province-outline" : ""}`}
                              role="button"
                              tabIndex={0}
                              aria-label={`${feature.properties.name}${fill ? "，已去过" : ""}`}
                              aria-pressed={province ? !!fill : undefined}
                              onClick={activate}
                              onKeyDown={(e) => {
                                if (e.key === "Enter" || e.key === " ") {
                                  e.preventDefault();
                                  activate();
                                }
                              }}
                              onMouseEnter={() =>
                                setHover(feature.properties.name)
                              }
                              onMouseLeave={() => setHover("")}
                            >
                              {code === "460300" && sanshaPath && (
                                <>
                                  <rect
                                    x="650"
                                    y="325"
                                    width="165"
                                    height="225"
                                    rx="8"
                                    fill={fill ? `${fill}22` : "#fafbf8"}
                                    stroke="#a5b6a6"
                                  />
                                  <text
                                    x="732"
                                    y="345"
                                    textAnchor="middle"
                                    fill="#61765a"
                                    fontSize="14"
                                    pointerEvents="none"
                                  >
                                    三沙 · 岛屿附图
                                  </text>
                                </>
                              )}
                              <path
                                d={drawPath(feature)}
                                fill={cityMode ? "none" : fill || "#e9eee9"}
                                pointerEvents={cityMode ? "stroke" : undefined}
                                stroke={
                                  cityMode
                                    ? "#6f8a72"
                                    : code === "460300" && sanshaPath
                                      ? fill || "#8aa18c"
                                      : fill
                                        ? "#ffffff"
                                        : "#fafcf9"
                                }
                                strokeOpacity={cityMode ? 0.7 : undefined}
                                strokeWidth={
                                  cityMode
                                    ? 1
                                    : code === "460300" && sanshaPath
                                      ? 2
                                      : province
                                        ? 1.4
                                        : 1.5
                                }
                                vectorEffect="non-scaling-stroke"
                              >
                                <title>
                                  {feature.properties.name}
                                  {province
                                    ? fill
                                      ? " · 再次点击取消"
                                      : " · 点击标记去过"
                                    : fill
                                      ? " · 已去过"
                                      : " · 点击探索"}
                                </title>
                              </path>
                              {point && (
                                <text
                                  x={point[0]}
                                  y={point[1]}
                                  textAnchor="middle"
                                  dy=".35em"
                                  className={`map-label ${fill ? "colored" : ""} ${["110000", "120000", "310000", "810000", "820000"].includes(code) && !province ? "small-label" : ""}`}
                                >
                                  {label}
                                </text>
                              )}
                            </g>
                          );
                        })}
                    </g>
                    {!province && (
                      <g className="sea-inset" transform="translate(736,458)">
                        <rect
                          width="83"
                          height="109"
                          rx="4"
                          fill="#fafbf8"
                          stroke="#d6ddd7"
                        />
                        {seaInsetPaths(map).map((d, i) => (
                          <path
                            key={i}
                            d={d}
                            fill="none"
                            stroke="#b6c5ba"
                            strokeWidth="1"
                          />
                        ))}
                        <text x="41" y="99" textAnchor="middle">
                          南海诸岛
                        </text>
                      </g>
                    )}
                  </svg>
                )}
                <div className="map-caption">
                  <Compass size={17} />
                  <span>
                    {hover ||
                      (province
                        ? "给每一座走过的城市，留下专属记忆"
                        : "山川湖海，下一站总有新的风景")}
                  </span>
                </div>
                {!mapError && (
                  <div className="map-options">
                    {!province && (
                      <div
                        className="fill-mode"
                        role="group"
                        aria-label="全国地图着色方式"
                      >
                        {(
                          [
                            ["province", "按省份"],
                            ["city", "按城市"],
                          ] as const
                        ).map(([mode, text]) => (
                          <button
                            key={mode}
                            aria-pressed={fillMode === mode}
                            onClick={() => {
                              setFillMode(mode);
                              setHover("");
                            }}
                          >
                            {text}
                          </button>
                        ))}
                      </div>
                    )}
                    <button
                      className="basemap-toggle"
                      aria-pressed={basemap}
                      title="开启后会向 openstreetmap.org 请求地图瓦片"
                      onClick={() => setBasemap((v) => !v)}
                    >
                      <Stack size={16} />
                      {basemap ? "关闭 OSM 底图" : "OSM 底图"}
                    </button>
                  </div>
                )}
                {!showOsm && (
                  <div className="map-controls">
                    <button
                      aria-label="放大地图"
                      disabled={zoom >= 1.6}
                      onClick={() => setZoom((z) => Math.min(1.6, z + 0.2))}
                    >
                      <Plus size={18} />
                    </button>
                    <button
                      aria-label="缩小地图"
                      disabled={zoom <= 1}
                      onClick={() => setZoom((z) => Math.max(1, z - 0.2))}
                    >
                      <Minus size={18} />
                    </button>
                    <button
                      aria-label="重置地图缩放"
                      onClick={() => setZoom(1)}
                    >
                      <ArrowCounterClockwise size={17} />
                    </button>
                  </div>
                )}
              </div>
            ) : (
              <div className="collection">
                <div className="collection-heading">
                  <h2>我的旅行收藏</h2>
                  <p>每一个城市，都有属于你的故事。</p>
                </div>
                {currentVisits.length ? (
                  <div className="visit-grid">
                    {currentVisits.map((v) => (
                      <button
                        key={v.code}
                        className="visit-card"
                        onClick={() => openEditor(v.province, v.code)}
                      >
                        <span
                          className="visit-card-icon"
                          style={{ background: v.color + "20", color: v.color }}
                        >
                          <MapPin size={25} weight="duotone" />
                        </span>
                        <small>{provinceByCode(v.province)?.short}</small>
                        <h3>{v.name}</h3>
                        <p>
                          {latestTrip(v)?.note ||
                            "有些风景，只属于走过的人。"}
                        </p>
                        <span className="visit-date">
                          <CalendarBlank size={14} />
                          {v.trips.length === 0
                            ? "暂无旅行记录"
                            : cityLatestDate(v) || "日期未记录"}
                          {v.trips.length > 1 && (
                            <em className="trip-badge">
                              共 {v.trips.length} 次旅行
                            </em>
                          )}
                          <ArrowUpRight size={16} />
                        </span>
                      </button>
                    ))}
                  </div>
                ) : (
                  <div className="empty-collection">
                    <MapTrifold size={54} weight="duotone" />
                    <h3>你的故事，从第一座城市开始</h3>
                    <p>记录去过的地方，慢慢拼出属于你的旅行地图。</p>
                    <Button
                      onClick={() => openEditor(province?.code || "330000")}
                    >
                      <Plus size={16} />
                      记录第一站
                    </Button>
                  </div>
                )}
              </div>
            )}
            {tab === "map" && !province && (
              <nav className="compact-region-nav" aria-label="小区域快捷入口">
                <span className="compact-region-title">快速进入</span>
                <div className="compact-region-links">
                  {["110000", "120000", "810000", "820000"].map((code) => {
                    const region = provinceByCode(code)!;
                    const color = provinceColor(code);
                    return (
                      <Button
                        key={code}
                        variant="outline"
                        className="compact-region-button"
                        aria-label={`进入${region.name}地图${color ? "，已探索" : "，未探索"}`}
                        onClick={() => navigate(region)}
                      >
                        <span
                          className="compact-region-dot"
                          style={{ background: color || "#e9eee9" }}
                          aria-hidden="true"
                        />
                        {region.short}
                        <CaretRight size={14} aria-hidden="true" />
                      </Button>
                    );
                  })}
                </div>
              </nav>
            )}
            <div className="map-legend">
              <span>足迹色彩</span>
              {colors.map((c) => (
                <span key={c.value}>
                  <i style={{ background: c.value }} />
                  {c.name}
                </span>
              ))}
              <span className="not-visited">
                <i />
                未探索
              </span>
            </div>
          </section>
          <aside className="sidebar">
            <div className="sidebar-title">
              <div>
                <h2>{province ? province.short : "探索目的地"}</h2>
                <p>
                  {province
                    ? `已走过 ${currentVisits.length} / ${province.cities.length} 个${unit} · ${percentage(currentVisits.length, province.cities.length)}`
                    : "从一个省份，开始一段旅程"}
                </p>
              </div>
              <span className="sidebar-icon">
                <MapPin size={21} weight="duotone" />
              </span>
            </div>
            <div className="search-wrap">
              <MagnifyingGlass size={17} />
              <Input
                aria-label={province ? "搜索城市" : "搜索省份或城市"}
                placeholder={province ? `搜索${unit}名称` : "搜索省份或城市…"}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              {query && (
                <button aria-label="清空搜索" onClick={() => setQuery("")}>
                  <X size={14} />
                </button>
              )}
            </div>
            {!province ? (
              <>
                <div className="list-heading">
                  <span>全部省份</span>
                  <span>{visitedProvinces.size} / 34 已探索</span>
                </div>
                <div className="province-list">
                  {filteredProvinces.map((p) => {
                    const count = saved.visits.filter(
                      (v) => v.province === p.code,
                    ).length;
                    const visited = visitedProvinces.has(p.code);
                    return (
                      <button
                        key={p.code}
                        className="province-row"
                        onClick={() => navigate(p)}
                      >
                        <span
                          className={`province-dot ${visited ? "has-visit" : ""}`}
                          style={
                            visited ? { background: provinceColor(p.code) } : {}
                          }
                        />
                        <span>{p.short}</span>
                        <small>
                          {count
                            ? `${count} 处足迹`
                            : visited
                              ? "已去过"
                              : "待探索"}
                        </small>
                        <CaretRight size={14} />
                      </button>
                    );
                  })}
                  {!filteredProvinces.length && (
                    <p className="no-results">没有找到目的地，试试其他名称。</p>
                  )}
                </div>
              </>
            ) : (
              <>
                <div className="province-actions">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      if (!commit((s) => toggleProvince(s, province.code)))
                        return;
                      notify(
                        saved.provinces[province.code]
                          ? "已取消省份单独标记，城市足迹保留"
                          : `已标记去过${province.short}`,
                      );
                    }}
                  >
                    <Check size={15} />
                    {saved.provinces[province.code]
                      ? "取消省份标记"
                      : "标记去过此省"}
                  </Button>
                  <Button size="sm" onClick={() => openEditor(province.code)}>
                    <Plus size={15} />
                    记录{unit}
                  </Button>
                </div>
                <div className="list-heading">
                  <span>全部{unit}</span>
                  <span>{province.cities.length} 个区域</span>
                </div>
                <div className="province-list city-list">
                  {province.cities
                    .filter((c) => c.name.includes(query))
                    .map((c) => {
                      const visit = saved.visits.find((v) => v.code === c.code);
                      return (
                        <div
                          key={c.code}
                          className={`city-row ${visit ? "city-visited" : ""}`}
                        >
                          <button
                            className="province-row city-toggle"
                            aria-pressed={!!visit}
                            aria-label={`${c.name}，${visit ? "取消去过" : "标记去过"}`}
                            onClick={() => toggleCity(province.code, c.code)}
                          >
                            <span
                              className={`province-dot ${visit ? "has-visit" : ""}`}
                              style={visit ? { background: visit.color } : {}}
                            />
                            <span>{c.name}</span>
                            <small>{visit ? "已去过" : "点击标记"}</small>
                            {visit ? <Check size={14} /> : <Plus size={14} />}
                          </button>
                          {visit && (
                            <button
                              className="city-edit"
                              aria-label={`编辑${c.name}足迹`}
                              title="编辑日期与备注"
                              onClick={() => openEditor(province.code, c.code)}
                            >
                              <PencilSimple size={15} />
                            </button>
                          )}
                        </div>
                      );
                    })}
                  {!province.cities.some((c) => c.name.includes(query)) && (
                    <p className="no-results">没有找到匹配的{unit}。</p>
                  )}
                </div>
              </>
            )}
            <div className="travel-note">
              <span className="note-icon">
                <Mountains size={28} weight="duotone" />
              </span>
              <div>
                <strong>不必走遍世界</strong>
                <p>每一步，都是自己的风景。</p>
              </div>
              <ArrowUpRight size={18} />
            </div>
          </aside>
        </div>
        <section className="recent-section">
          <div className="recent-heading">
            <div>
              <h2>
                最近的足迹 <span>RECENT JOURNEYS</span>
              </h2>
              <p>回头看看，那些让你心动的地方。</p>
            </div>
            <button
              onClick={() => {
                setTab("visits");
                setProvince(null);
              }}
            >
              查看全部
              <ArrowUpRight size={16} />
            </button>
          </div>
          {saved.visits.length ? (
            <div className="recent-grid">
              {sortVisitsByLatest(saved.visits)
                .slice(0, 4)
                .map((v) => (
                  <button
                    className="recent-item"
                    key={v.code}
                    onClick={() => openEditor(v.province, v.code)}
                  >
                    <span
                      style={{ color: v.color, background: v.color + "18" }}
                    >
                      <MapPin size={22} weight="duotone" />
                    </span>
                    <div>
                      <h3>
                        {v.name}
                        <small>{provinceByCode(v.province)?.short}</small>
                      </h3>
                      <p>
                        {v.trips.length === 0
                          ? "暂无旅行记录"
                          : cityLatestDate(v) || "日期未记录"}
                        {v.trips.length > 1 && ` · 共 ${v.trips.length} 次旅行`}
                      </p>
                    </div>
                    <ArrowUpRight size={17} />
                  </button>
                ))}
            </div>
          ) : (
            <div className="recent-empty">
              <MapPin size={20} />
              <span>还没有记录足迹。把你去过的第一座城市，留在这里。</span>
              <button onClick={() => openEditor("330000")}>
                添加第一站
                <Plus size={15} />
              </button>
            </div>
          )}
        </section>
        <footer>
          <span>
            <Compass size={15} />
            山海足迹 · 让每一次出发都有迹可循
          </span>
          <div>
            <button
              disabled={cloud.mode === "cloud"}
              title={
                cloud.mode === "cloud" ? "登录期间暂不支持导入备份" : undefined
              }
              onClick={() => fileInput.current?.click()}
            >
              <UploadSimple size={14} />
              导入备份
            </button>
            <button onClick={exportData}>
              <DownloadSimple size={14} />
              导出备份
            </button>
            <span>地图数据：GeoAtlas / twgeojson</span>
          </div>
        </footer>
      </main>
      <input
        type="file"
        accept=".json,application/json"
        hidden
        ref={fileInput}
        onChange={(e) => void importFile(e.target.files?.[0])}
      />
      {cityPickerOpen && province && (
        <CityPicker
          name={province.short}
          cities={province.cities}
          visits={saved.visits}
          onClose={() => setCityPickerOpen(false)}
          onToggle={(code) => toggleCity(province.code, code)}
          onEdit={(code) => {
            setCityPickerOpen(false);
            openEditor(province.code, code);
          }}
        />
      )}
      {reportOpen && (
        <TravelReport
          open={reportOpen}
          onOpenChange={setReportOpen}
          saved={saved}
          province={province}
          shapes={
            map && path && !(cityMode && !cityShapes)
              ? [
                  ...(cityMode && cityShapes
                    ? cityShapes.map((s) => {
                        const tint = saved.provinces[s.province];
                        return {
                          d: s.d,
                          strokeWidth: 0.5,
                          fill:
                            visitColors[s.code] ||
                            (tint ? `${tint}40` : "#e9eee9"),
                        };
                      })
                    : []),
                  ...map.features
                    .filter((f) => f.properties.name)
                    .map((f) => ({
                      d: drawPath(f),
                      stroke: cityMode
                        ? "#6f8a72"
                        : province?.code === "460000" &&
                            String(f.properties.adcode) === "460300"
                          ? "#8aa18c"
                          : undefined,
                      strokeWidth: cityMode ? 1 : undefined,
                      fill: cityMode
                        ? "none"
                        : (province
                            ? visitColors[String(f.properties.adcode)]
                            : provinceColor(String(f.properties.adcode))) ||
                          "#e9eee9",
                    })),
                ]
              : null
          }
          inset={map && !province ? seaInsetPaths(map) : []}
          cloudSynced={cloudReady}
          onInspectRecord={(pCode, rCode) => openEditor(pCode, rCode)}
        />
      )}
      <Dialog
        open={editor}
        onOpenChange={(open) => {
          // A question that opened on top of the editor owns the dismiss: neither Escape,
          // a click outside nor focus moving into it may close the editor and its draft.
          if (!open && (cancelCity || editConflict || mediaOpen))
            return;
          setEditor(open);
        }}
      >
        <DialogContent
          className="editor-dialog"
          onEscapeKeyDown={(e) => {
            // Radix only hands Escape to the top layer once it has registered, which
            // happens after focus has already moved into the new question. Within that
            // gap the editor still receives the key: answer it for the question.
            if (cancelCity) {
              e.preventDefault();
              setCancelCity(null);
            } else if (editConflict) {
              e.preventDefault();
              setEditConflict(null);
            } else if (mediaOpen) {
              e.preventDefault();
            }
          }}
          onCloseAutoFocus={(e) => {
            e.preventDefault();
            returnEditorFocus();
          }}
        >
          <DialogHeader>
            <span className="dialog-icon">
              <MapPin size={25} weight="duotone" />
            </span>
            <DialogTitle>
              {saved.visits.some((v) => v.code === draftCity)
                ? "编辑城市足迹"
                : "记录一段新的旅程"}
            </DialogTitle>
            <DialogDescription>
              颜色默认自动分配，直接保存即可；每次旅行可以单独记录日期和备注。
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              saveVisit();
            }}
          >
            <div className="form-row">
              <label>
                省份
                <select
                  value={draftProvince}
                  onChange={(e) => loadDraft(e.target.value, "")}
                >
                  {regions.map((p) => (
                    <option key={p.code} value={p.code}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                城市 / 区县
                <select
                  required
                  value={draftCity}
                  onChange={(e) => selectDraftCity(e.target.value)}
                >
                  <option value="">选择目的地</option>
                  {draftRegion.cities.map((c) => (
                    <option key={c.code} value={c.code}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            <fieldset>
              <legend>
                足迹颜色{" "}
                <span className="auto-color-note">
                  默认自动配色，可按喜好调整
                </span>
              </legend>
              <div className="color-picker">
                {colors.map((c) => (
                  <button
                    type="button"
                    key={c.value}
                    className={draftColor === c.value ? "selected" : ""}
                    aria-label={c.name}
                    aria-pressed={draftColor === c.value}
                    onClick={() => setDraftColor(c.value)}
                  >
                    <span style={{ background: c.value }}>
                      {draftColor === c.value && (
                        <Check size={17} weight="bold" />
                      )}
                    </span>
                    <small>{c.name}</small>
                  </button>
                ))}
              </div>
            </fieldset>
            {draftCity && (
              <fieldset className="trip-list">
                <legend>
                  旅行记录{" "}
                  <span className="auto-color-note">
                    同一城市可以记录多次，城市数和颜色不受影响
                  </span>
                </legend>
                {draftTrips.length === 0 && (
                  <p className="media-hint">
                    还没有旅行记录。城市仍会保持打卡，也可以添加一次旅行。
                  </p>
                )}
                {draftTrips.map((t, i) => {
                  const synced = cloud
                    .confirmedSaved()
                    ?.visits.some(
                      (v) =>
                        v.code === draftCity &&
                        v.trips.some((o) => o.id === t.id),
                    );
                  const sharedTrip = sharedTripsByTripId.get(
                    `${draftCity}:${t.id}`,
                  );
                  const readOnlyMember =
                    !!sharedTrip &&
                    !canEditSharedTrip(sharedTrip.myRole, sharedTrip.myStatus);
                  return (
                    <div className="trip-card" key={t.id}>
                      <div className="trip-card-head">
                        <strong>
                          第 {i + 1} 次旅行
                          {sharedTrip
                            ? `（共同旅行 · ${roleLabel(sharedTrip.myRole)}）`
                            : ""}
                        </strong>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="delete-button"
                          aria-label={`删除第 ${i + 1} 次旅行`}
                          disabled={readOnlyMember}
                          title={
                            readOnlyMember
                              ? "只读成员不能直接删除共同旅行，请在共同旅行窗口中退出"
                              : undefined
                          }
                          onClick={() =>
                            setDraftTrips((trips) =>
                              trips.filter((o) => o.id !== t.id),
                            )
                          }
                        >
                          <Trash size={15} />
                          删除
                        </Button>
                      </div>
                      {readOnlyMember && (
                        <p className="media-hint">
                          你在这次共同旅行中为只读成员，日期与记忆由创建者或共同编辑者维护；如需移除本页记录，可在共同旅行窗口中退出。
                        </p>
                      )}
                      <label className="field-label">
                        旅行日期
                        <span className="sr-only"> 第 {i + 1} 次</span>
                        <Input
                          type="date"
                          value={t.date}
                          disabled={readOnlyMember}
                          onChange={(e) =>
                            changeDraftTrip(t.id, { date: e.target.value })
                          }
                        />
                      </label>
                      <label className="field-label">
                        旅行记忆
                        <span className="sr-only"> 第 {i + 1} 次</span>{" "}
                        <span>选填</span>
                        <textarea
                          placeholder="那天的天气、遇见的人，或一顿念念不忘的美食…"
                          maxLength={TRIP_NOTE_LIMIT}
                          value={t.note}
                          disabled={readOnlyMember}
                          onChange={(e) =>
                            changeDraftTrip(t.id, { note: e.target.value })
                          }
                        />
                      </label>
                      {cloud.user && (
                        <div className="trip-card-actions">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            aria-label={`这次旅行的照片与附件（第 ${i + 1} 次）`}
                            disabled={cloud.deletionBlocked || !synced}
                            title={
                              synced
                                ? undefined
                                : "保存并同步这次旅行后才能添加照片与附件"
                            }
                            data-media-opener={t.id}
                            onClick={() => openMedia(t.id)}
                          >
                            这次旅行的照片与附件
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            aria-label={`这次旅行的共同旅行（第 ${i + 1} 次）`}
                            disabled={cloud.deletionBlocked || !synced}
                            title={
                              synced
                                ? undefined
                                : "保存并同步这次旅行后才能邀请好友共同旅行"
                            }
                            onClick={() => openTripCollaboration(t.id)}
                          >
                            <Users size={15} />
                            {sharedTrip ? "共同旅行详情" : "邀请共同旅行"}
                          </Button>
                        </div>
                      )}
                    </div>
                  );
                })}
                <Button
                  type="button"
                  variant="outline"
                  onClick={() =>
                    setDraftTrips((trips) => [...trips, defaultTrip()])
                  }
                >
                  <Plus size={16} />
                  {draftTrips.length ? "添加另一次旅行" : "添加旅行记录"}
                </Button>
              </fieldset>
            )}
            {cloud.user && draftCity && (
              <div>
                <CityMediaPreview
                  key={`${cloud.user.id}:${draftCity}`}
                  regionCode={draftCity}
                />
                <Button
                  type="button"
                  variant="outline"
                  disabled={cloud.deletionBlocked}
                  data-media-opener=""
                  onClick={() => openMedia("")}
                >
                  照片与附件
                </Button>
                <p className="media-hint">
                  取消城市打卡会同时删除这座城市的旅行记录，但会保留照片与附件，文件需在媒体窗口单独删除。
                </p>
              </div>
            )}
            <div className="dialog-actions">
              {saved.visits.some((v) => v.code === draftCity) && (
                <Button
                  type="button"
                  variant="ghost"
                  className="delete-button"
                  onClick={() => {
                    const removed = saved.visits.find(
                      (v) => v.code === draftCity,
                    );
                    if (!removed) return;
                    // The editor stays open behind the confirmation, so its draft survives "keep".
                    if (removed.trips.length >= 2) {
                      setCancelCity(draftCity);
                      return;
                    }
                    if (cancelVisit(draftCity)) setEditor(false);
                  }}
                >
                  <Trash size={16} />
                  取消城市打卡
                </Button>
              )}
              <Button
                type="button"
                variant="outline"
                onClick={() => setEditor(false)}
              >
                取消
              </Button>
              <Button type="submit" disabled={!draftCity}>
                <Check size={17} />
                保存足迹
              </Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!editConflict}
        onOpenChange={(open) => {
          if (!open) setEditConflict(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <span className="dialog-icon">
              <Warning size={25} weight="duotone" />
            </span>
            <DialogTitle>编辑期间这条记录有了新的变化</DialogTitle>
            <DialogDescription>
              你编辑时，{cityName(draftCity)} 的同一条记录在别处也被修改了。逐条选择保留哪一份；没有冲突的修改已按你的编辑和最新数据合并，不受影响。
            </DialogDescription>
          </DialogHeader>
          {editConflict?.refreshed && (
            <p className="unreadable-note" role="status">
              你选择期间，这条记录在别处又有了新的变化：下面已按最新内容重新显示，变化过的记录需要重新选择；其余已选的不受影响。
            </p>
          )}
          <ul className="migration-conflicts">
            {editConflict?.conflicts.map((c) => {
              const choice = editConflict.choices[c.key];
              const pick = (value: "mine" | "latest") =>
                setEditConflict((prev) =>
                  prev
                    ? { ...prev, choices: { ...prev.choices, [c.key]: value } }
                    : prev,
                );
              const trip = (t: Trip | null) =>
                t
                  ? `${t.date || "日期未记录"}${t.note ? ` · ${t.note.length > 24 ? `${t.note.slice(0, 24)}…` : t.note}` : ""}`
                  : "已删除";
              const title =
                c.kind === "city-removed"
                  ? `${cityName(draftCity)} 的足迹已被取消`
                  : c.kind === "color"
                    ? "足迹颜色"
                    : "一次旅行";
              const mine =
                c.kind === "city-removed"
                  ? "恢复这座城市，并保存我的修改"
                  : c.kind === "color"
                    ? "用我选的颜色"
                    : `用我的：${trip(c.mine)}`;
              const latest =
                c.kind === "city-removed"
                  ? "保持取消，放弃我的修改"
                  : c.kind === "color"
                    ? "保留最新的颜色"
                    : `保留最新：${trip(c.latest)}`;
              return (
                <li className="migration-conflict" key={c.key}>
                  <span className="migration-conflict-name">{title}</span>
                  <div>
                    <button
                      type="button"
                      aria-pressed={choice === "latest"}
                      className={`migration-choice${choice === "latest" ? " selected" : ""}`}
                      onClick={() => pick("latest")}
                    >
                      <strong>{latest}</strong>
                    </button>
                    <button
                      type="button"
                      aria-pressed={choice === "mine"}
                      className={`migration-choice${choice === "mine" ? " selected" : ""}`}
                      onClick={() => pick("mine")}
                    >
                      <strong>{mine}</strong>
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
          <div className="dialog-actions">
            <Button variant="outline" onClick={() => setEditConflict(null)}>
              返回编辑
            </Button>
            <Button
              disabled={
                !editConflict ||
                editConflict.conflicts.some((c) => !editConflict.choices[c.key])
              }
              onClick={() => editConflict && saveVisit(editConflict.choices)}
            >
              确认并保存
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {legacyNotice && legacyNotice.digest !== legacyHidden && !unreadable && !localClash && (
        <LegacyDialog
          open
          kind={cloud.mode === "cloud" ? "cloud" : "local"}
          raw={legacyNotice.raw}
          saved={legacyNotice.saved}
          current={saved}
          onExport={() =>
            legacyDownload(
              legacyNotice.raw,
              cloud.mode === "cloud"
                ? "山海足迹-旧版页面账号缓存.json"
                : "山海足迹-旧版页面数据.json",
            )
          }
          onMerge={mergeLegacy}
          onKeep={keepLegacyAside}
          onClose={() => setLegacyHidden(legacyNotice.digest)}
        />
      )}
      <Dialog open={!!unreadable} onOpenChange={() => {}}>
        <DialogContent
          showCloseButton={false}
          onEscapeKeyDown={(e) => e.preventDefault()}
          onInteractOutside={(e) => e.preventDefault()}
        >
          <DialogHeader>
            <span className="dialog-icon">
              <Warning size={25} weight="duotone" />
            </span>
            <DialogTitle>本地足迹数据无法读取</DialogTitle>
            <DialogDescription>
              {unreadable?.reason}
              。为避免覆盖，已暂停自动保存，原始数据仍保留在浏览器中，处理前请先导出。
            </DialogDescription>
          </DialogHeader>
          {unreadable?.recovery && (
            <p className="unreadable-note">
              可识别 {unreadable.recovery.saved.visits.length} 条城市足迹、
              {Object.keys(unreadable.recovery.saved.provinces).length}{" "}
              个省份标记； 恢复时将丢弃 {unreadable.recovery.discardedVisits}{" "}
              条足迹、{unreadable.recovery.discardedTrips} 条旅行记录、
              {unreadable.recovery.discardedProvinces} 个省份标记。
            </p>
          )}
          {unreadable?.recoveryCorrected && (
            <p className="unreadable-note">
              其中 {unreadable.recoveryCorrected.correctedDates}{" "}
              个旅行日期不是真实存在的日历日期。可以只清除这些日期，其余日期和全部备注原样保留
              {unreadable.recoveryCorrected.discardedVisits +
                unreadable.recoveryCorrected.discardedTrips >
                0 &&
                `（另有 ${unreadable.recoveryCorrected.discardedVisits} 条足迹、${unreadable.recoveryCorrected.discardedTrips} 条旅行记录无法识别，将被丢弃）`}
              。原始数据会先另存一份。
            </p>
          )}
          {unreadableError && (
            <p className="unreadable-note unreadable-error" role="alert">
              {unreadableError}
            </p>
          )}
          <div className="dialog-actions unreadable-actions">
            <Button variant="outline" onClick={exportRaw}>
              <DownloadSimple size={16} />
              导出原始数据
            </Button>
            {unreadable?.recovery && (
              <Button
                variant="outline"
                onClick={() =>
                  unreadable.recovery &&
                  resolveUnreadable(
                    unreadable.recovery.saved,
                    "已恢复可识别的记录",
                  )
                }
              >
                恢复可识别的记录
              </Button>
            )}
            {unreadable?.recoveryCorrected && (
              <Button
                onClick={() =>
                  unreadable.recoveryCorrected &&
                  resolveUnreadable(
                    unreadable.recoveryCorrected.saved,
                    `已清除 ${unreadable.recoveryCorrected.correctedDates} 个不存在的日期并恢复记录`,
                  )
                }
              >
                清除 {unreadable.recoveryCorrected.correctedDates}{" "}
                个不存在的日期并恢复
              </Button>
            )}
            <Button
              variant={confirmEmpty ? "destructive" : "outline"}
              onClick={() =>
                confirmEmpty
                  ? resolveUnreadable(empty, "已以空数据开始")
                  : setConfirmEmpty(true)
              }
            >
              {confirmEmpty ? "确认以空数据开始" : "以空数据开始"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!importOpen}
        onOpenChange={(open) => {
          if (!open) setImportOpen(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>导入旅行足迹</DialogTitle>
            <DialogDescription>
              备份包含 {importOpen?.saved.visits.length} 条城市足迹、
              {importOpen?.saved.visits.reduce(
                (n, v) => n + v.trips.length,
                0,
              )}{" "}
              次旅行。导入将替换当前记录，建议先导出当前备份。
              {importOpen && importOpen.cleared > 0
                ? `备份里有 ${importOpen.cleared} 个旅行日期不是真实存在的日历日期，导入时会清除这些日期，其余内容（包括备注）原样保留。`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="dialog-actions">
            <Button variant="outline" onClick={exportData}>
              <DownloadSimple size={16} />
              备份当前数据
            </Button>
            <Button
              onClick={() => {
                if (!importOpen) return;
                const target = importOpen.saved;
                if (!commit(() => target)) return;
                setImportOpen(null);
                notify("备份已导入");
              }}
            >
              {importOpen && importOpen.cleared > 0
                ? "清除不存在的日期并导入"
                : "确认替换并导入"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {cloud.enabled && (
        <AccountDialog
          open={accountOpen}
          onOpenChange={setAccountOpen}
          cloud={cloud}
          local={
            accountOpen && cloud.mode === "cloud"
              ? (() => {
                  const l = loadSaved();
                  return {
                    status: l.status,
                    count: l.saved.visits.length + Object.keys(l.saved.provinces).length,
                  };
                })()
              : { status: "empty", count: 0 }
          }
          onMigrate={() => {
            setAccountOpen(false);
            setMigrationOpen(true);
          }}
          onOpenSnapshots={() => {
            setAccountOpen(false);
            setSnapshotsOpen(true);
          }}
          onOpenMedia={() => {
            setAccountOpen(false);
            setMediaTarget({ region: "", tripId: "" });
          }}
          onOpenShare={() => {
            setAccountOpen(false);
            setShareOpen(true);
          }}
          onOpenCollaboration={() => {
            setAccountOpen(false);
            setCollabTarget(null);
            setCollabOpen(true);
          }}
        />
      )}
      {cloud.enabled && cloud.user && collabOpen && (
        <CollaborationDialog
          key={`${cloud.user.id}:${collabTarget?.tripId ?? ""}`}
          open
          onOpenChange={(open) => {
            setCollabOpen(open);
            if (!open) setCollabTarget(null);
          }}
          cloud={cloud}
          saved={saved}
          initialTarget={collabTarget}
          onOverviewChange={setCollabOverview}
        />
      )}
      {mediaOpen && cloud.user && (
        <MediaDialog
          key={`${cloud.user.id}:${mediaTarget.region}:${mediaTarget.tripId}`}
          regionCode={mediaTarget.region}
          tripId={mediaTarget.tripId}
          trips={Object.fromEntries(
            saved.visits.map((v) => [v.code, v.trips] as const),
          )}
          syncedTripIds={
            new Set(
              (cloud.confirmedSaved()?.visits ?? []).flatMap((v) =>
                v.trips.map((t) => t.id),
              ),
            )
          }
          writable={cloud.canWrite && cloud.fresh && !cloud.deletionBlocked}
          returnsToEditor={returnToEditor}
          onClose={closeMedia}
        />
      )}
      {cloud.enabled && cloud.user && snapshotsOpen && (
        <SnapshotDialog
          open
          onOpenChange={setSnapshotsOpen}
          cloud={cloud}
          current={saved}
        />
      )}
      {cloud.enabled && cloud.user && shareOpen && (
        <ShareDialog
          key={cloud.user.id}
          open
          onOpenChange={setShareOpen}
          cloud={cloud}
          visitedCities={new Set(saved.visits.map((v) => v.code))}
        />
      )}
      {cloud.enabled && cloud.user && migrationOpen && (
        <MigrationDialog
          open
          onOpenChange={setMigrationOpen}
          local={loadSaved().saved}
          cloudSaved={saved}
          cloud={cloud}
          provinceName={(code) => provinceByCode(code)?.name ?? code}
          apply={(target) => commit(() => target, true)}
          onExportCloud={exportData}
          onLater={() => {
            if (userId) migrationLater.current.add(userId);
          }}
          onMigrated={() => {
            if (userId)
              markMigrated(
                localStorage,
                userId,
                fingerprint(loadSaved().saved),
              );
          }}
        />
      )}
      <Dialog
        open={!!cancelCity}
        onOpenChange={(open) => {
          if (!open) setCancelCity(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <span className="dialog-icon">
              <Warning size={25} weight="duotone" />
            </span>
            <DialogTitle>取消 {cancelCity ? cityName(cancelCity) : ""} 的打卡？</DialogTitle>
            <DialogDescription>
              这座城市的{" "}
              {saved.visits.find((v) => v.code === cancelCity)?.trips.length ?? 0}{" "}
              次旅行记录（日期和备注）会一并删除。照片与附件会保留，需要在媒体窗口单独删除。取消后可在提示条里撤销。
            </DialogDescription>
          </DialogHeader>
          <div className="dialog-actions">
            <Button variant="outline" onClick={() => setCancelCity(null)}>
              保留足迹
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const target = cancelCity;
                if (!target) return;
                if (cancelVisit(target)) {
                  setCancelCity(null);
                  setEditor(false);
                }
              }}
            >
              确认取消打卡
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      <Dialog open={!!localClash} onOpenChange={() => {}}>
        <DialogContent
          showCloseButton={false}
          onEscapeKeyDown={(e) => e.preventDefault()}
          onInteractOutside={(e) => e.preventDefault()}
        >
          <DialogHeader>
            <span className="dialog-icon">
              <Warning size={25} weight="duotone" />
            </span>
            <DialogTitle>另一个页面修改了本机足迹</DialogTitle>
            <DialogDescription>
              为避免互相覆盖，本页已暂停保存。请选择保留哪一份数据；覆盖前会把另一个页面的数据另存一份，浏览器无法保存时需要先导出它。
            </DialogDescription>
          </DialogHeader>
          {clashError && (
            <p className="unreadable-note unreadable-error" role="alert">
              {clashError}
            </p>
          )}
          <div className="dialog-actions unreadable-actions">
            <Button variant="outline" onClick={exportData}>
              <DownloadSimple size={16} />
              导出本页数据
            </Button>
            {localClash?.other != null && (
              <Button variant="outline" onClick={exportOtherTab}>
                <DownloadSimple size={16} />
                导出另一个页面的数据
              </Button>
            )}
            <Button variant="outline" onClick={loadOtherTab}>
              载入另一个页面的数据
            </Button>
            <Button onClick={overwriteOtherTab}>
              用本页数据覆盖（保留对方副本）
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {toast && (
        <div className="toast" role="status">
          <Check size={18} />
          <span>{toast}</span>
          {undoVisit && (
            <button
              className="undo-button"
              onClick={() => {
                if (
                  !commit((s) => {
                    const live = s.visits.find((v) => v.code === undoVisit.code);
                    if (!live) return { ...s, visits: [...s.visits, undoVisit] };
                    // Added again meanwhile: bring back the removed trips next to the new ones.
                    const have = new Set(live.trips.map((t) => t.id));
                    return {
                      ...s,
                      visits: s.visits.map((v) =>
                        v.code === live.code
                          ? {
                              ...v,
                              trips: canonicalTrips([
                                ...v.trips,
                                ...undoVisit.trips.filter((t) => !have.has(t.id)),
                              ]),
                            }
                          : v,
                      ),
                    };
                  })
                )
                  return;
                notify(`已恢复 ${undoVisit.name} 的足迹`);
              }}
            >
              撤销
            </button>
          )}
          <button aria-label="关闭提示" onClick={() => notify("")}>
            <X size={16} />
          </button>
        </div>
      )}
      {storageError && (
        <div className="storage-warning" role="alert">
          <FloppyDisk size={18} />
          浏览器存储不可用，请导出备份以保留足迹。
          <button onClick={exportData}>立即导出</button>
        </div>
      )}
    </div>
  );
}
export default App;
