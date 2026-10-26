import { useMemo, useRef, useState } from "react";
import { CloudSlash, DownloadSimple, Info, MagnifyingGlass } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { useWorldAssets } from "../hooks/useWorldAssets.ts";
import { useWorldLocal, type WorldPhase } from "../hooks/useWorldLocal.ts";
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
import type { WorldCodes } from "../lib/world.ts";
import type { Saved } from "../lib/travel.ts";
import WorldMap from "./WorldMap.tsx";
import "./world.css";


export type WorldViewProps = {
  /** The China footprints, read only: they decide whether CN is lit and are never written here. */
  saved: Pick<Saved, "visits" | "provinces">;
  /** The local China footprints are under read protection: CN is not lit, and nothing is written. */
  chinaUnreadable: boolean;
  /** Signed in: the world view shows a placeholder and never reads or writes the local world data. */
  cloudMode: boolean;
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

export default function WorldView({ saved, chinaUnreadable, cloudMode, onOpenChina }: WorldViewProps) {
  const assets = useWorldAssets(!cloudMode);
  const [noticeOpen, setNoticeOpen] = useState(false);
  const catalog = assets.catalog.status === "ready" ? assets.catalog : null;

  return (
    <section className="world-view" aria-labelledby="world-title" data-offline={assets.offline} data-catalog={cloudMode ? "none" : assets.catalog.status}>
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

      {cloudMode ? (
        <div className="world-card world-placeholder" role="status">
          <CloudSlash size={22} />
          <div>
            <strong>云端同步将在后续版本提供</strong>
            <p>登录状态下暂不显示世界足迹，也不会读取或上传这台设备上的世界足迹。退出登录后即可继续使用本机的世界足迹。</p>
          </div>
        </div>
      ) : assets.catalog.status === "loading" ? (
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
  conflict: "其他标签页修改了世界足迹，已暂停保存，避免覆盖对方的修改。请重新载入页面后再继续。",
};

function WorldLocal({
  catalog,
  index,
  codes,
  assets,
  saved,
  chinaUnreadable,
  onOpenChina,
}: {
  catalog: WorldCatalog;
  index: WorldSearchIndex;
  codes: WorldCodes;
  assets: ReturnType<typeof useWorldAssets>;
  saved: Pick<Saved, "visits" | "provinces">;
  chinaUnreadable: boolean;
  onOpenChina: () => void;
}) {
  const local = useWorldLocal(codes);
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  const china = useMemo(
    () => (chinaUnreadable ? CHINA_UNREADABLE : summarizeChina({ visits: saved.visits, provinces: saved.provinces })),
    [chinaUnreadable, saved.visits, saved.provinces],
  );

  const lit = useMemo(() => new Map(litCountries(local.world, china).map((entry) => [entry.code, entry])), [local.world, china]);
  const marked = useMemo(() => new Set(local.world.countries.map((mark) => mark.code)), [local.world]);
  const stats = worldStats(local.world, china);
  const results = useMemo(() => searchWorldCountries(catalog.countries, index, query), [catalog, index, query]);
  const unreadable = local.phase === "unreadable";
  const { toggle: toggleMark } = local;
  const toggle = (country: Pick<WorldCountry, "code" | "name">) => toggleMark(country.code, country.name);
  const onToggleCode = useMemo(() => (code: string) => toggleMark(code, assets.names[code] ?? code), [toggleMark, assets.names]);

  return (
    <>
      {local.phase !== "ok" && (
        <div className="world-card world-protect" role="alert" data-phase={local.phase}>
          <strong>{PHASE_MESSAGE[local.phase]}</strong>
          {unreadable && local.reason && <p>原因：{local.reason}。恢复或重新开始的选项将在后续版本提供。</p>}
          <div className="world-protect-actions">
            {unreadable && local.raw !== null && (
              <Button variant="outline" onClick={() => download(`山海足迹-世界-原文-${today()}.json`, local.raw ?? "")}>
                <DownloadSimple size={17} />
                导出原文
              </Button>
            )}
            {!unreadable && (
              <Button
                variant="outline"
                onClick={() => {
                  const file = local.backup(today());
                  download(file.filename, file.text);
                }}
              >
                <DownloadSimple size={17} />
                导出当前世界足迹
              </Button>
            )}
            {local.phase === "conflict" && <Button onClick={() => window.location.reload()}>重新载入页面</Button>}
          </div>
        </div>
      )}
      {chinaUnreadable && (
        <p className="world-note" role="note">
          中国足迹暂时无法读取，所以中国不会点亮。世界视图不会改动它，请回到中国视图处理。
        </p>
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
              <button type="button" className="world-skip" onClick={() => search.current?.focus()}>
                跳到国家/地区列表
              </button>
              <WorldMap
                map={assets.map.map}
                names={assets.names}
                lit={lit}
                marked={marked}
                chinaLit={china.status === "ok" && china.lit}
                editable={!local.paused}
                onToggle={onToggleCode}
                onOpenChina={onOpenChina}
              />
              <p className="world-hint">点击或按 Enter 标记国家和地区；中国由中国足迹点亮，点击进入中国视图。滚轮、双指或按钮缩放，拖动或方向键平移。过小的区域请用右侧列表选择。</p>
            </>
          ) : assets.map.status === "loading" ? (
            <p className="world-loading" role="status">
              正在载入世界地图…
            </p>
          ) : (
            <div className="world-unavailable" role="alert">
              <strong>世界地图暂不可用</strong>
              <p>{assets.map.message}。仍可通过右侧列表标记国家和地区。</p>
              <Button onClick={assets.retryMap}>重试</Button>
            </div>
          )}
          <p className="world-status" role="status" aria-live="polite">
            {local.announcement}
          </p>
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
    </>
  );
}
