import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { DownloadSimple, Info, MagnifyingGlass, Plus, UploadSimple, X } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { useWorldAssets } from "../hooks/useWorldAssets.ts";
import { useWorldCloud } from "../hooks/useWorldCloud.ts";
import { useWorldLocal, type WorldPhase } from "../hooks/useWorldLocal.ts";
import {
  coordinateText,
  emptyForm,
  formFromPlace,
  formLocation,
  MAP_NOT_READY_MESSAGE,
  readForm,
  type FormError,
  type PlaceForm,
  type Whereabouts,
} from "../lib/world-form.ts";
import { hitTestWorld } from "../lib/world-map.ts";
import { searchWorldCountries, type WorldCatalog, type WorldCountry, type WorldSearchIndex } from "../lib/world-catalog.ts";
import {
  WORLD_NOTICE_CHINA,
  WORLD_NOTICE_DISCLAIMER,
  WORLD_NOTICE_LIMITS,
  WORLD_NOTICE_TITLE,
  litSourceText,
  worldNoticeSource,
} from "../lib/world-notice.ts";
import { CHINA_UNREADABLE, litCountries, summarizeChina, worldStats } from "../lib/world-stats.ts";
import { removePlace, savePlace, WORLD_MAX_IMPORT_BYTES, type WorldCodes } from "../lib/world.ts";
import type { WorldImportPreview } from "../lib/world-persistence.ts";
import type { Saved } from "../lib/travel.ts";
import WorldMap from "./WorldMap.tsx";
import WorldPlaceDialog, { type Located } from "./WorldPlaceDialog.tsx";
import { ConflictDialog, DeletePlaceDialog, ImportDialog, UnreadableDialog } from "./WorldProtection.tsx";
import "./world.css";


export type WorldViewProps = {
  /** The China footprints, read only: they decide whether CN is lit and are never written here. */
  saved: Pick<Saved, "visits" | "provinces">;
  /** The local China footprints are under read protection: CN is not lit, and nothing is written. */
  chinaUnreadable: boolean;
  /** Signed in: the world view uses the account cache and WorldService. */
  cloudMode: boolean;
  /** Signed in account ID; its world document is loaded from the account cache and cloud API. */
  cloudUserId?: string;
  onOpenChina: () => void;
};

const today = () => new Date().toLocaleDateString("sv-SE");

function download(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export default function WorldView({ saved, chinaUnreadable, cloudMode, cloudUserId, onOpenChina }: WorldViewProps) {
  const assets = useWorldAssets(true);
  const [noticeOpen, setNoticeOpen] = useState(false);
  const catalog = assets.catalog.status === "ready" ? assets.catalog : null;

  return (
    <section className="world-view" aria-labelledby="world-title" data-offline={assets.offline} data-catalog={assets.catalog.status}>
      <div className="world-head">
        <div>
          <h1 id="world-title">世界足迹</h1>
          <p>点亮去过的国家和地区。世界足迹与中国足迹分开保存，互不改写。</p>
        </div>
        <Button variant="outline" className="world-notice-button" onClick={() => setNoticeOpen(true)}>
          <Info size={17} />
          {WORLD_NOTICE_TITLE}
        </Button>
      </div>

      {assets.catalog.status === "loading" ? (
        <p className="world-loading" role="status">
          正在载入国家/地区目录…
        </p>
      ) : assets.catalog.status === "failed" ? (
        <div className="world-card world-unavailable" role="alert">
          <strong>目录暂不可用，可重试</strong>
          <p>国家/地区目录没有加载成功。你保存的世界足迹没有被读取、修改或覆盖，目录恢复后会照常显示。</p>
          <Button onClick={assets.retryCatalog}>重试</Button>
        </div>
      ) : (
        <WorldLocal
          catalog={assets.catalog.catalog}
          index={assets.catalog.index}
          codes={assets.catalog.codes}
          assets={assets}
          cloudMode={cloudMode}
          cloudUserId={cloudUserId}
          saved={saved}
          chinaUnreadable={chinaUnreadable}
          onOpenChina={onOpenChina}
        />
      )}

      <NoticeDialog open={noticeOpen} onOpenChange={setNoticeOpen} catalog={catalog?.catalog ?? null} />
    </section>
  );
}

function NoticeDialog({ open, onOpenChange, catalog }: { open: boolean; onOpenChange: (open: boolean) => void; catalog: WorldCatalog | null }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="world-notice">
        <DialogHeader>
          <DialogTitle>{WORLD_NOTICE_TITLE}</DialogTitle>
          <DialogDescription>{WORLD_NOTICE_DISCLAIMER}</DialogDescription>
        </DialogHeader>
        <p>{WORLD_NOTICE_CHINA}</p>
        <ul>
          {WORLD_NOTICE_LIMITS.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
        {catalog ? (
          <>
            <p>{worldNoticeSource(catalog.source, catalog.transform.precisionDecimals)}</p>
            <details>
              <summary>授权文本与来源</summary>
              <p>
                来源：{catalog.source.url}
                <br />
                授权：{catalog.source.license}（{catalog.source.licenseUrl}）
              </p>
              <pre>{catalog.source.licenseText}</pre>
            </details>
          </>
        ) : (
          <p>目录载入后，这里还会显示边界数据的来源、版本与授权文本。</p>
        )}
        <div className="dialog-actions">
          <Button onClick={() => onOpenChange(false)}>知道了</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const PHASE_MESSAGE: Record<Exclude<WorldPhase, "ok">, string> = {
  unavailable: "浏览器没有开放本机存储，修改只保留在当前页面，不会被保存。请导出备份。",
  "save-failed": "世界足迹没有保存成功（存储已满或被浏览器拒绝）。修改只保留在当前页面，请先导出备份。",
  unreadable: "这台设备上保存的世界足迹无法读取，已暂停保存，原文原样保留，不会被覆盖。",
  conflict: "其他标签页修改了世界足迹，已暂停保存，避免覆盖对方的修改。本页的修改仍保留在页面中，请先导出，再选择保留哪一份。",
  "cloud-loading": "正在加载云端世界足迹…",
  "cloud-error": "无法加载云端世界足迹，请重试；中国视图不受影响。",
  "cloud-save-failed": "账号世界缓存保存失败，当前修改未接受，请先导出备份。",
  "cloud-unreadable": "账号世界缓存无法读取，原文已保留，编辑和覆盖已暂停，请先导出原始缓存。",
  "cloud-conflict": "云端世界足迹发生冲突，已暂停发送，请先导出两边数据再选择。",
};

const byName = (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name, "zh-Hans");

function WorldLocal({
  catalog,
  index,
  codes,
  assets,
  cloudMode,
  cloudUserId,
  saved,
  chinaUnreadable,
  onOpenChina,
}: {
  catalog: WorldCatalog;
  index: WorldSearchIndex;
  codes: WorldCodes;
  assets: ReturnType<typeof useWorldAssets>;
  cloudMode: boolean;
  cloudUserId?: string;
  saved: Pick<Saved, "visits" | "provinces">;
  chinaUnreadable: boolean;
  onOpenChina: () => void;
}) {
  const localState = useWorldLocal(codes, !cloudMode);
  const cloudState = useWorldCloud(codes, cloudMode ? cloudUserId ?? null : null);
  const local = cloudMode ? cloudState : localState;
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  const placesCard = useRef<HTMLDivElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const opener = useRef<HTMLElement | SVGElement | null>(null);
  const [form, setForm] = useState<PlaceForm | null>(null);
  // The form stays mounted (closed) after it is dismissed, so that the dialog can hand focus back.
  const shownForm = useRef<PlaceForm>(emptyForm());
  /** The form as it was when it opened: closing it asks first once it differs. */
  const baseline = useRef(JSON.stringify(shownForm.current));
  if (form) shownForm.current = form;
  const [formKey, setFormKey] = useState(0);
  const [formError, setFormError] = useState<FormError | null>(null);
  const [picking, setPicking] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [importing, setImporting] = useState<{ preview: WorldImportPreview; fileName: string } | null>(null);
  const [importError, setImportError] = useState<string | null>(null);
  const [dismissed, setDismissed] = useState("");
  const [exportedRaw, setExportedRaw] = useState(false);
  const [exportedTheirs, setExportedTheirs] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const china = useMemo(
    () => (chinaUnreadable ? CHINA_UNREADABLE : summarizeChina({ visits: saved.visits, provinces: saved.provinces })),
    [chinaUnreadable, saved.visits, saved.provinces],
  );

  const mapData = assets.map.status === "ready" ? assets.map.map : null;
  const lit = useMemo(() => new Map(litCountries(local.world, china).map((entry) => [entry.code, entry])), [local.world, china]);
  const marked = useMemo(() => new Set(local.world.countries.map((mark) => mark.code)), [local.world]);
  const stats = worldStats(local.world, china);
  const results = useMemo(() => searchWorldCountries(catalog.countries, index, query), [catalog, index, query]);
  const places = useMemo(() => [...local.world.places].sort(byName), [local.world.places]);
  const unreadable = local.phase === "unreadable";
  const cloudUnreadable = local.phase === "cloud-unreadable";
  const { toggle: toggleMark, announce } = local;
  const toggle = (country: Pick<WorldCountry, "code" | "name">) => toggleMark(country.code, country.name);
  const onToggleCode = useMemo(() => (code: string) => toggleMark(code, assets.names[code] ?? code), [toggleMark, assets.names]);
  const nameOf = (code: string) => assets.names[code] ?? code;

  // --- Where a typed or picked location falls ---------------------------------------------------
  const countryAt = (lat: number, lng: number): string | null => (mapData ? hitTestWorld(mapData, lng, lat) : null);
  const located = useMemo<Located>(() => {
    if (!form || (!form.lat.trim() && !form.lng.trim())) return { state: "none" };
    const at = formLocation(form);
    if (!at) return { state: "unreadable" };
    if (!mapData) return { state: "unchecked", failed: assets.map.status === "failed" };
    const code = hitTestWorld(mapData, at.lng, at.lat);
    return code === "CN" ? { state: "china" } : code ? { state: "country", code } : { state: "nothing" };
  }, [form, mapData, assets.map.status]);
  // The refusal for a missing map goes away on its own once the map is there.
  useEffect(() => {
    if (mapData) setFormError((error) => (error?.message === MAP_NOT_READY_MESSAGE ? null : error));
  }, [mapData]);
  const defaultCountry = (next: PlaceForm): string => {
    const at = formLocation(next);
    const code = at ? countryAt(at.lat, at.lng) : null;
    return code && code !== "CN" ? code : "";
  };

  // --- The city form ---------------------------------------------------------------------------
  const remember = () => {
    const active = document.activeElement;
    opener.current = active instanceof HTMLElement || active instanceof SVGElement ? active : null;
  };
  const openForm = (next: PlaceForm) => {
    remember();
    setFormError(null);
    setFormKey((n) => n + 1);
    baseline.current = JSON.stringify(next);
    setForm(next);
  };
  const openNew = () => openForm(emptyForm());
  const openEdit = (id: string) => {
    const place = local.world.places.find((p) => p.id === id);
    if (place) openForm(formFromPlace(place));
  };
  const closeForm = () => {
    setForm(null);
    setFormError(null);
    setPicking(false);
  };
  const change = (patch: Partial<PlaceForm>) => {
    setFormError(null);
    setForm((f) => {
      if (!f) return f;
      const next = { ...f, ...patch };
      if (("lat" in patch || "lng" in patch) && !next.countryChosen) next.country = defaultCountry(next);
      return next;
    });
  };
  const chooseCountry = (code: string) => change({ country: code, countryChosen: code !== "" });
  const save = () => {
    if (!form) return;
    // The location is held against the map here (the data layer has no geometry): typed, picked and
    // edited coordinates alike, whatever country the list says. Without the map (loading, or failed) it
    // cannot be told that the place is not in China, so nothing is saved and the draft stays.
    const at = formLocation(form);
    const where: Whereabouts = !at ? "elsewhere" : !mapData ? "unknown" : countryAt(at.lat, at.lng) === "CN" ? "china" : "elsewhere";
    const read = readForm(form, codes, where);
    if ("error" in read) {
      setFormError(read.error);
      return;
    }
    const result = local.edit((world) => savePlace(world, read.draft, codes), `${form.id ? "已保存" : "已添加"}城市${read.draft.name}`);
    if (!result.ok) setFormError({ message: result.error });
    else closeForm();
  };

  // --- Picking a location on the map ---------------------------------------------------------------
  const startPick = () => {
    setPicking(true);
    announce("请在地图上点击城市的位置，按 Esc 取消");
  };
  // The dialog is closed while a point is picked and a new one opened afterwards: reopening the same
  // Radix dialog within its exit animation can leave its overlay above its content.
  const endPick = useCallback(() => {
    setPicking(false);
    setFormKey((n) => n + 1);
  }, []);
  const stopPick = (message: string) => {
    endPick();
    announce(message);
  };
  useEffect(() => {
    if (!picking) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        endPick();
        announce("已取消选点");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking, announce, endPick]);
  const onPick = (at: { lat: number; lng: number }) => {
    const code = countryAt(at.lat, at.lng);
    setForm((f) => {
      if (!f) return f;
      const next = { ...f, lat: coordinateText(at.lat), lng: coordinateText(at.lng) };
      return f.countryChosen ? next : { ...next, country: code && code !== "CN" ? code : "" };
    });
    setFormError(null);
    endPick();
    announce(`已选择位置 ${coordinateText(at.lat)}，${coordinateText(at.lng)}${code ? `，位于${nameOf(code)}` : "，没有对应的国家或地区，请从列表选择"}`);
  };

  // --- Deleting a city -------------------------------------------------------------------------
  const doomed = deleting ? (local.world.places.find((p) => p.id === deleting) ?? null) : null;
  const darkens = (() => {
    if (!doomed) return false;
    const entry = lit.get(doomed.country);
    return !!entry && entry.sources.length === 1 && entry.sources[0] === "place" && local.world.places.filter((p) => p.country === doomed.country).length === 1;
  })();
  const confirmDelete = () => {
    if (!doomed) return;
    local.edit((world) => removePlace(world, doomed.id, codes), `已删除城市${doomed.name}及其 ${doomed.trips.length} 次旅行`);
    setDeleting(null);
    closeForm();
  };
  // Asked from inside the city form: keeping the city brings the form back with the draft as it was
  // (a new dialog, like after picking, so that its overlay is not left above its content).
  const keepCity = () => {
    setDeleting(null);
    if (form) setFormKey((n) => n + 1);
  };

  // --- Backup ------------------------------------------------------------------------------------
  const exportBackup = () => {
    const file = local.backup(today());
    download(file.filename, file.text);
  };
  const readImport = async (file?: File) => {
    if (!file) return;
    setImportError(null);
    try {
      if (file.size > WORLD_MAX_IMPORT_BYTES) throw new Error(`文件超过 ${WORLD_MAX_IMPORT_BYTES} 字节，不是世界足迹备份`);
      setImporting({ preview: local.previewImport(await file.text()), fileName: file.name });
    } catch (error) {
      setImportError(error instanceof Error ? error.message : "无法读取文件");
    }
    if (fileInput.current) fileInput.current.value = "";
  };
  const confirmImport = () => {
    if (!importing) return;
    const result = local.confirmImport(importing.preview);
    setImporting(null);
    if (!result.ok) setImportError(result.error);
  };

  // --- Protection dialogs -------------------------------------------------------------------------
  const protectKey = `${local.phase}:${local.theirs?.text ?? ""}`;
  const protectOpen = (local.phase === "unreadable" || local.phase === "conflict" || local.phase === "cloud-conflict") && dismissed !== protectKey;
  useEffect(() => {
    if (local.phase === "ok") {
      setCopyFailed(false);
      setExportedRaw(false);
      setExportedTheirs(false);
    }
  }, [local.phase]);
  const exportRaw = () => {
    download(`山海足迹-世界-原文-${today()}.json`, local.raw ?? "");
    setExportedRaw(true);
  };
  const exportTheirs = () => {
    if (local.theirs?.text == null) return;
    download(`山海足迹-世界-另一个标签页-${today()}.json`, local.theirs.text);
    setExportedTheirs(true);
  };

  return (
    <>
      {local.phase !== "ok" && (
        <div className="world-card world-protect" role="alert" data-phase={local.phase}>
          <strong>{PHASE_MESSAGE[local.phase]}</strong>
          {unreadable && local.reason && <p>原因：{local.reason}。</p>}
          <div className="world-protect-actions">
            {(unreadable || cloudUnreadable) && local.raw !== null && (
              <Button variant="outline" onClick={exportRaw}>
                <DownloadSimple size={17} />
                {cloudUnreadable ? "导出原始账号缓存" : "导出原文"}
              </Button>
            )}
            {!unreadable && (
              <Button variant="outline" onClick={exportBackup}>
                <DownloadSimple size={17} />
                导出当前世界足迹
              </Button>
            )}
            {(unreadable || local.phase === "conflict" || local.phase === "cloud-conflict") && (
              <Button onClick={() => setDismissed("")}>{unreadable ? "恢复或重新开始" : "选择如何处理"}</Button>
            )}
          </div>
        </div>
      )}
      {chinaUnreadable && (
        <p className="world-note" role="note">
          中国足迹暂时无法读取，所以中国不会点亮。世界视图不会改动它，请回到中国视图处理。
        </p>
      )}

      <div className="world-tools" role="group" aria-label="世界足迹备份">
        <Button variant="outline" onClick={exportBackup} disabled={unreadable}>
          <DownloadSimple size={17} />
          导出世界备份
        </Button>
        <Button variant="outline" onClick={() => fileInput.current?.click()} disabled={local.paused}>
          <UploadSimple size={17} />
          导入世界备份
        </Button>
        <input
          ref={fileInput}
          type="file"
          accept="application/json,.json"
          className="sr-only"
          tabIndex={-1}
          aria-label="选择世界备份文件"
          data-testid="world-import-input"
          onChange={(event) => void readImport(event.target.files?.[0])}
        />
      </div>
      {importError && (
        <div className="world-card world-import-error" role="alert">
          <p>
            <strong>无法导入：</strong>
            {importError}
          </p>
          <p className="world-hint">当前世界足迹没有任何改动。</p>
          <Button variant="outline" size="sm" onClick={() => setImportError(null)}>
            知道了
          </Button>
        </div>
      )}

      <dl className="world-stats" aria-label="世界统计">
        <div>
          <dt>国家/地区</dt>
          <dd>{unreadable ? "—" : stats.countries}</dd>
        </div>
        <div>
          <dt>海外城市</dt>
          <dd>{unreadable ? "—" : stats.places}</dd>
        </div>
        <div>
          <dt>旅行次数</dt>
          <dd>{unreadable ? "—" : stats.trips}</dd>
        </div>
      </dl>

      <div className="world-body">
        <div className="world-card world-map-card">
          {assets.map.status === "ready" ? (
            <>
              <div className="world-skips">
                <button type="button" className="world-skip" onClick={() => placesCard.current?.querySelector<HTMLElement>("button")?.focus()}>
                  跳到海外城市
                </button>
                <button type="button" className="world-skip" onClick={() => search.current?.focus()}>
                  跳到国家/地区列表
                </button>
              </div>
              {picking && (
                <div className="world-pick-banner" role="group" aria-label="选择城市位置">
                  <p>
                    <strong>正在选择“{form?.name.trim() || "新城市"}”的位置</strong>
                    点击地图上的位置；可以先缩放、平移。不方便点选时，取消后可直接输入经纬度。
                  </p>
                  <Button type="button" variant="outline" size="sm" autoFocus onClick={() => stopPick("已取消选点")}>
                    <X size={15} />
                    取消选点（Esc）
                  </Button>
                </div>
              )}
              <WorldMap
                map={assets.map.map}
                names={assets.names}
                lit={lit}
                marked={marked}
                chinaLit={china.status === "ok" && china.lit}
                editable={!local.paused}
                onToggle={onToggleCode}
                onOpenChina={onOpenChina}
                places={local.world.places}
                onOpenPlace={local.paused ? undefined : openEdit}
                picking={picking}
                onPick={onPick}
                pending={form ? formLocation(form) : null}
              />
              <p className="world-hint">
                点击或按 Enter 标记国家和地区；城市显示为圆点，Tab 到圆点后按 Enter 编辑。中国由中国足迹点亮，点击进入中国视图。滚轮、双指或按钮缩放，拖动或方向键平移。过小的区域请用右侧列表选择。
              </p>
            </>
          ) : assets.map.status === "loading" ? (
            <p className="world-loading" role="status">
              正在载入世界地图…
            </p>
          ) : (
            <div className="world-unavailable" role="alert">
              <strong>世界地图暂不可用</strong>
              <p>{assets.map.message}。仍可通过右侧列表标记国家和地区，添加城市时直接输入经纬度。</p>
              <Button onClick={assets.retryMap}>重试</Button>
            </div>
          )}
          <p className="world-status" role="status" aria-live="polite">
            {cloudMode && cloudState.syncPending ? "本机已保存，待同步" : local.announcement}
          </p>
        </div>

        <div className="world-side">
          <div className="world-card world-places-card" ref={placesCard}>
            <div className="world-places-head">
              <h2>海外城市 · {unreadable ? "—" : places.length}</h2>
              <Button onClick={openNew} disabled={local.paused}>
                <Plus size={16} />
                添加海外城市
              </Button>
            </div>
            {places.length === 0 ? (
              <p className="world-places-empty">还没有海外城市。点击“添加海外城市”，在地图上选点或输入经纬度，再记下每次旅行。</p>
            ) : (
              <ul className="world-place-list" aria-label="海外城市">
                {places.map((place) => (
                  <li key={place.id} data-place={place.id}>
                    <div className="world-list-name">
                      <strong>{place.name}</strong>
                      <span>
                        {nameOf(place.country)} · {place.trips.length} 次旅行
                      </span>
                    </div>
                    <Button variant="outline" size="sm" disabled={local.paused} aria-label={`编辑城市 ${place.name}`} onClick={() => openEdit(place.id)}>
                      编辑
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="delete-button"
                      disabled={local.paused}
                      aria-label={`删除城市 ${place.name}`}
                      onClick={() => setDeleting(place.id)}
                    >
                      删除
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <div className="world-card world-list-card">
            <label className="world-search">
              <MagnifyingGlass size={17} />
              <Input
                ref={search}
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索国家或地区：中文、拼音、英文、代码"
                aria-label="搜索国家或地区"
                autoComplete="off"
                maxLength={100}
              />
            </label>
            <p className="world-count" aria-live="polite">
              {query.trim() ? `找到 ${results.length} 个` : `共 ${catalog.countries.length} 个国家和地区`}
            </p>
            <ul className="world-list" aria-label="国家和地区">
              {results.map((country) => {
                const entry = lit.get(country.code);
                const isMarked = marked.has(country.code);
                return (
                  <li key={country.code} data-code={country.code} data-lit={entry ? "true" : "false"}>
                    <div className="world-list-name">
                      <strong>{country.name}</strong>
                      <span>
                        {country.en}
                        {!country.hasShape && country.code !== "CN" ? " · 仅可从列表选择" : ""}
                      </span>
                    </div>
                    <span className="world-list-state">
                      {country.code === "CN" && chinaUnreadable ? "中国足迹暂时无法读取" : litSourceText(entry)}
                    </span>
                    {country.code === "CN" ? (
                      <Button variant="outline" size="sm" onClick={onOpenChina} aria-label={`进入中国视图（${country.name}）`}>
                        进入中国视图
                      </Button>
                    ) : (
                      <Button
                        variant={isMarked ? "default" : "outline"}
                        size="sm"
                        disabled={local.paused}
                        aria-label={`${isMarked ? "取消标记" : "标记"} ${country.name}`}
                        onClick={() => toggle(country)}
                      >
                        {isMarked ? "取消标记" : "标记"}
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
            {results.length === 0 && <p className="world-empty">没有找到匹配的国家或地区。</p>}
          </div>
        </div>
      </div>

      {shownForm.current && (
        <WorldPlaceDialog
          key={formKey}
          open={form !== null && !picking && deleting === null}
          form={shownForm.current}
          error={formError}
          located={located}
          dirty={form !== null && JSON.stringify(form) !== baseline.current}
          countries={catalog.countries}
          index={index}
          names={assets.names}
          canPick={mapData !== null}
          opener={opener}
          onChange={change}
          onChooseCountry={chooseCountry}
          onPickOnMap={startPick}
          onSave={save}
          onCancel={closeForm}
          onDelete={() => form?.id && setDeleting(form.id)}
          onRetryMap={assets.retryMap}
        />
      )}
      <DeletePlaceDialog
        place={doomed}
        countryName={doomed ? nameOf(doomed.country) : ""}
        darkens={darkens}
        onConfirm={confirmDelete}
        onCancel={keepCity}
      />
      <ImportDialog
        preview={importing?.preview ?? null}
        fileName={importing?.fileName ?? ""}
        onExportCurrent={exportBackup}
        onConfirm={confirmImport}
        onCancel={() => setImporting(null)}
      />
      <UnreadableDialog
        open={protectOpen && unreadable}
        reason={local.reason}
        recovery={local.recovery}
        copyFailed={copyFailed}
        exported={exportedRaw}
        onExport={exportRaw}
        onRecover={() => setCopyFailed(local.resolve("recover", exportedRaw) === "copy-failed")}
        onEmpty={() => setCopyFailed(local.resolve("empty", exportedRaw) === "copy-failed")}
        onClose={() => setDismissed(protectKey)}
      />
      {local.theirs && (
        <ConflictDialog
          open={protectOpen && (local.phase === "conflict" || local.phase === "cloud-conflict")}
          theirs={local.theirs}
          mine={local.world}
          copyFailed={copyFailed}
          exported={exportedTheirs}
          onExportMine={exportBackup}
          onExportTheirs={exportTheirs}
          onKeepMine={() => setCopyFailed(local.settleConflict("mine", exportedTheirs) === "copy-failed")}
          onLoadTheirs={() => setCopyFailed(local.settleConflict("theirs", false) === "copy-failed")}
          onClose={() => setDismissed(protectKey)}
          cloud={cloudMode}
        />
      )}
    </>
  );
}
