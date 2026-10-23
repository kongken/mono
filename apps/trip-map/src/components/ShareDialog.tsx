import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy, Eye, ShareNetwork, Trash } from "@phosphor-icons/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { mediaApi, type MediaInfo } from "@/api/media";
import {
  privateDraft,
  shareApi,
  type ShareSettings,
  type SharePreview,
  type ShareState,
  type Visibility,
} from "@/api/share";
import { Button } from "@kongken/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import type { Cloud } from "@/hooks/useCloud";
import { describeApiError } from "@/lib/errors";
import regions from "@kongken/geo-data/regions.json";
import "./ShareDialog.css";

const MAX_PHOTOS = 12;

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  cloud: Cloud;
  /** Cities with a check-in on this account; a shared photo's city must be one of them. */
  visitedCities: Set<string>;
};

const cityName = (code: string) =>
  regions.flatMap((p) => p.cities).find((c) => c.code === code)?.name ?? code;
const when = (ms: number) => new Date(ms).toLocaleString("zh-CN", { hour12: false });

const visibilityOptions: { value: Visibility; label: string; hint: string }[] = [
  { value: "private", label: "不公开", hint: "只保存设置，没有任何公开链接。" },
  { value: "link", label: "知道链接的人可查看", hint: "不会被搜索引擎收录。" },
  { value: "public", label: "公开页面", hint: "允许搜索引擎收录，仍不提供目录或发现入口。" },
];

/** Settings after a field switch, with the dependent fields kept consistent. */
function normalize(s: ShareSettings): ShareSettings {
  const cities = s.cities;
  const photos = cities && s.photos;
  return {
    ...s,
    dates: cities && s.dates,
    notes: cities && s.notes,
    photos,
    photoIds: photos ? s.photoIds : [],
  };
}

const sameSettings = (a: ShareSettings, b: ShareSettings) => JSON.stringify(a) === JSON.stringify(b);

/** True when `next` publishes something `old` did not, which needs a fresh read of the footprints. */
function adds(old: ShareSettings, next: ShareSettings) {
  return (
    (next.cities && !old.cities) ||
    (next.dates && !old.dates) ||
    (next.notes && !old.notes) ||
    (next.photos && !old.photos) ||
    next.photoIds.some((id) => !old.photoIds.includes(id))
  );
}

export default function ShareDialog({ open, onOpenChange, cloud, visitedCities }: Props) {
  const [share, setShare] = useState<ShareState | null>(null);
  const [enabled, setEnabled] = useState(true);
  const [draft, setDraft] = useState<ShareSettings>(privateDraft);
  const [media, setMedia] = useState<MediaInfo[] | null>(null);
  const [preview, setPreview] = useState<{ key: string; data: SharePreview } | null>(null);
  const [refresh, setRefresh] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [copied, setCopied] = useState("");
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  // A retry of exactly this request keeps its ID, so a lost reply is never applied twice.
  const attempt = useRef<{ key: string; id: string } | null>(null);
  const linkField = useRef<HTMLInputElement>(null);

  const adopt = useCallback((next: ShareState, keepDraft = false) => {
    setShare(next);
    if (keepDraft) return;
    // Nothing was ever saved: offer the link option, which still publishes nothing until confirmed.
    const first = next.version === 0 && next.settings.visibility === "private";
    setDraft(first ? { ...next.settings, visibility: "link" } : next.settings);
  }, []);

  const load = useCallback(async (keepDraft = false) => {
    try {
      const r = await shareApi.get();
      setEnabled(r.enabled);
      adopt(r.share, keepDraft);
    } catch (e) {
      setError(describeApiError(e));
    }
  }, [adopt]);

  useEffect(() => {
    if (!open) return;
    setShare(null);
    setPreview(null);
    setRefresh(false);
    setNote("");
    setError("");
    setCopied("");
    setConfirmRevoke(false);
    attempt.current = null;
    void load();
  }, [open, load]);

  // The photo list is only needed once photos are switched on.
  useEffect(() => {
    if (!open || !draft.photos || media) return;
    let alive = true;
    void mediaApi
      .list()
      .then((list) => alive && setMedia(list))
      .catch((e) => alive && setError(describeApiError(e)));
    return () => {
      alive = false;
    };
  }, [open, draft.photos, media]);

  const candidates = useMemo(
    () =>
      (media ?? []).filter(
        (m) => m.status === "ready" && m.contentType === "image/jpeg" && m.width > 0 && visitedCities.has(m.regionCode),
      ),
    [media, visitedCities],
  );

  // A chosen photo that was deleted, or whose city check-in is gone, can no longer be shared.
  useEffect(() => {
    if (!media) return;
    const usable = new Set(candidates.map((m) => m.id));
    setDraft((d) => (d.photoIds.every((id) => usable.has(id)) ? d : { ...d, photoIds: d.photoIds.filter((id) => usable.has(id)) }));
  }, [media, candidates]);

  const settingsKey = JSON.stringify(draft);
  const published = !!share?.published;
  const dirty = !!share && !sameSettings(draft, share.settings);
  const live = draft.visibility !== "private";
  const needsPreview = live && (!published || (!!share && adds(share.settings, draft)) || refresh);
  const previewReady = !!preview && preview.key === settingsKey;
  const behind = share && published ? Math.max(0, share.currentRevision - share.publishedRevision) : 0;
  const idle = cloud.fresh && !cloud.deletionBlocked;

  const update = (patch: Partial<ShareSettings>) => {
    setDraft((d) => normalize({ ...d, ...patch }));
    setPreview(null);
    setNote("");
    setError("");
  };

  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await action();
    } catch (e) {
      if (e instanceof ConnectError && e.code === Code.Aborted) {
        // The share or the footprints changed under the owner: show the fresh state and ask again.
        attempt.current = null;
        setPreview(null);
        // The owner's unsaved choices stay; only the stored state is read again.
        await load(true);
        setError("分享设置或足迹在预览后发生了变化，没有发布任何内容。已刷新，请重新确认后再发布。");
      } else {
        setError(describeApiError(e));
      }
    } finally {
      setBusy("");
    }
  };

  const doPreview = () =>
    run("previewing", async () => {
      setPreview({ key: settingsKey, data: await shareApi.preview(draft) });
    });

  const save = () =>
    run("saving", async () => {
      if (!share) return;
      const refreshContent = needsPreview;
      if (refreshContent && !previewReady) throw new Error("preview first");
      const previewRevision = refreshContent && preview ? preview.data.revision : 0;
      const key = JSON.stringify([share.version, draft, refreshContent, previewRevision]);
      if (attempt.current?.key !== key) attempt.current = { key, id: crypto.randomUUID() };
      const next = await shareApi.save({
        mutationId: attempt.current.id,
        expectedVersion: share.version,
        settings: draft,
        refresh: refreshContent,
        previewRevision,
      });
      attempt.current = null;
      adopt(next);
      setPreview(null);
      setRefresh(false);
      setNote(
        !next.published
          ? "已保存为不公开，没有公开链接。"
          : published
            ? "分享页已更新。"
            : "分享页已发布，复制下方链接即可分享。",
      );
    });

  const revoke = () =>
    run("revoking", async () => {
      const key = `revoke:${share?.version}`;
      if (attempt.current?.key !== key) attempt.current = { key, id: crypto.randomUUID() };
      const next = await shareApi.revoke(attempt.current.id);
      attempt.current = null;
      adopt(next);
      setPreview(null);
      setRefresh(false);
      setConfirmRevoke(false);
      setNote("已撤销，链接立即失效。已被他人下载或缓存的内容无法收回。");
    });

  const copy = async () => {
    if (!share?.url) return;
    try {
      await navigator.clipboard.writeText(share.url);
      setCopied("已复制链接");
    } catch {
      linkField.current?.select();
      setCopied("无法自动复制，请手动复制选中的链接");
    }
  };

  const primary = (() => {
    if (!live) return { label: "保存为不公开", action: save, disabled: !dirty };
    if (needsPreview && !previewReady) return { label: "预览公开内容", action: doPreview, disabled: false };
    if (!published) return { label: "确认发布", action: save, disabled: false };
    return { label: refresh || (share && adds(share.settings, draft)) ? "确认更新" : "保存设置", action: save, disabled: !dirty && !refresh };
  })();

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="migration-dialog share-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <ShareNetwork size={22} />
          </span>
          <DialogTitle>分享我的足迹</DialogTitle>
          <DialogDescription>
            生成一个只读页面。发布时保存的是一份快照：之后新增的足迹、备注和照片不会自动公开，需要你再次确认更新。
          </DialogDescription>
        </DialogHeader>

        {!share && !error && <p className="share-dialog-note">加载中…</p>}
        {!enabled && (
          <p className="unreadable-note" role="status">
            这台服务器没有开启分享（未配置公开访问地址）。
          </p>
        )}
        {share && enabled && (
          <>
            {published && (
              <div className="share-link">
                <label className="field-label">
                  公开链接
                  <span>
                    {share.publishedAt ? `发布于 ${when(share.publishedAt)}` : ""}
                    {behind > 0 ? `，之后有 ${behind} 项足迹改动未包含在页面中` : "，页面内容是最新的"}
                  </span>
                  <Input ref={linkField} readOnly value={share.url} onFocus={(e) => e.currentTarget.select()} />
                </label>
                <div className="share-link-actions">
                  <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
                    <Copy size={15} />
                    复制链接
                  </Button>
                  <span role="status">{copied}</span>
                </div>
              </div>
            )}

            <fieldset className="share-fieldset">
              <legend>谁可以查看</legend>
              {visibilityOptions.map((o) => (
                <label key={o.value} className="share-option">
                  <input
                    type="radio"
                    name="share-visibility"
                    value={o.value}
                    checked={draft.visibility === o.value}
                    disabled={!!busy}
                    onChange={() => update({ visibility: o.value })}
                  />
                  <span>
                    {o.label}
                    <small>{o.hint}</small>
                  </span>
                </label>
              ))}
              {published && draft.visibility === "private" && (
                <p className="share-dialog-note">改为不公开会关闭当前链接；以后再发布会得到新的链接。</p>
              )}
            </fieldset>

            <fieldset className="share-fieldset" disabled={!live || !!busy}>
              <legend>公开哪些内容</legend>
              <label className="share-option">
                <input type="checkbox" checked disabled aria-label="省份地图和统计" />
                <span>
                  省份地图和统计
                  <small>始终公开，只包含已探索省份和数量。</small>
                </span>
              </label>
              <label className="share-option">
                <input type="checkbox" checked={draft.cities} onChange={(e) => update({ cities: e.target.checked })} />
                <span>
                  城市名称和颜色
                  <small>不勾选时，页面和预览图只显示到省份。</small>
                </span>
              </label>
              <label className="share-option">
                <input
                  type="checkbox"
                  checked={draft.dates}
                  disabled={!draft.cities}
                  onChange={(e) => update({ dates: e.target.checked })}
                />
                <span>旅行日期</span>
              </label>
              <label className="share-option">
                <input
                  type="checkbox"
                  checked={draft.notes}
                  disabled={!draft.cities}
                  onChange={(e) => update({ notes: e.target.checked })}
                />
                <span>
                  旅行备注
                  <small>备注原文会公开，请先预览。</small>
                </span>
              </label>
              <label className="share-option">
                <input
                  type="checkbox"
                  checked={draft.photos}
                  disabled={!draft.cities}
                  onChange={(e) => update({ photos: e.target.checked })}
                />
                <span>
                  照片
                  <small>逐张选择，最多 {MAX_PHOTOS} 张 JPEG；以后上传的照片不会自动公开。</small>
                </span>
              </label>
              {!draft.cities && <p className="share-dialog-note">日期、备注和照片需要同时公开城市名称。</p>}
            </fieldset>

            {live && draft.photos && (
              <fieldset className="share-fieldset share-photo-picker" disabled={!!busy}>
                <legend>
                  选择照片（{draft.photoIds.length}/{MAX_PHOTOS}）
                </legend>
                {media === null ? (
                  <p className="share-dialog-note">加载中…</p>
                ) : candidates.length === 0 ? (
                  <p className="share-dialog-note">
                    没有可分享的照片。只有已确认的 JPEG 照片，且所在城市已打卡，才能选择。
                  </p>
                ) : (
                  <ul>
                    {candidates.map((m) => {
                      const checked = draft.photoIds.includes(m.id);
                      return (
                        <li key={m.id}>
                          <label className="share-option">
                            <input
                              type="checkbox"
                              checked={checked}
                              disabled={!checked && draft.photoIds.length >= MAX_PHOTOS}
                              onChange={(e) =>
                                update({
                                  photoIds: e.target.checked
                                    ? [...draft.photoIds, m.id]
                                    : draft.photoIds.filter((id) => id !== m.id),
                                })
                              }
                            />
                            <span>
                              {m.filename}
                              <small>{cityName(m.regionCode)}</small>
                            </span>
                          </label>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </fieldset>
            )}

            {published && live && (
              <label className="share-option share-refresh">
                <input
                  type="checkbox"
                  checked={refresh}
                  disabled={!!busy}
                  onChange={(e) => {
                    setRefresh(e.target.checked);
                    setPreview(null);
                  }}
                />
                <span>
                  用最新足迹更新页面内容
                  <small>需要预览并再次确认。不勾选时，已发布的内容保持原样。</small>
                </span>
              </label>
            )}

            {cloud.pending && live && (
              <p className="unreadable-note">有尚未同步的修改；预览和发布只包含已同步到云端的内容。</p>
            )}

            {preview && previewReady && (
              <section className="share-preview" aria-label="公开内容预览">
                <h3>将公开的内容</h3>
                <p>
                  已探索 {preview.data.content.stats.explored_provinces} / {preview.data.content.stats.total_provinces} 个省份，
                  打卡 {preview.data.content.stats.cities} 座城市，共 {preview.data.content.stats.trips} 次旅行。
                </p>
                {preview.data.content.fields.cities ? (
                  <ul>
                    {preview.data.content.cities.map((c) => (
                      <li key={c.code}>
                        <strong>{c.name}</strong>
                        {c.trips.map((t, i) => (
                          <span key={i}>
                            {[t.date, t.note].filter(Boolean).join(" · ")}
                            {t.photos && t.photos.length > 0 ? `（${t.photos.length} 张照片）` : ""}
                          </span>
                        ))}
                        {c.photos && c.photos.length > 0 && <span>{c.photos.length} 张照片</span>}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p>不公开城市名称、日期、备注和照片。</p>
                )}
              </section>
            )}

            {note && (
              <p className="account-sync" role="status">
                <Check size={16} />
                {note}
              </p>
            )}
            {!idle && (
              <p className="unreadable-note">云端数据还没有加载完成或云端数据删除未完成，暂时不能发布。</p>
            )}
          </>
        )}
        {error && (
          <p className="unreadable-note unreadable-error" role="alert">
            {error}
          </p>
        )}
        {share?.published && confirmRevoke && (
          <p className="unreadable-note" role="alert">
            撤销后链接、页面和预览图立即返回“不存在”，再次分享会生成新链接。已经被他人保存、转发或被平台缓存的内容无法收回。
          </p>
        )}
        <div className="dialog-actions">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
          {share?.published && enabled && (
            <Button
              variant={confirmRevoke ? "destructive" : "outline"}
              disabled={!!busy}
              onClick={() => (confirmRevoke ? void revoke() : setConfirmRevoke(true))}
            >
              <Trash size={16} />
              {busy === "revoking" ? "正在撤销…" : confirmRevoke ? "确认撤销链接" : "撤销分享"}
            </Button>
          )}
          {share && enabled && (
            <Button disabled={!!busy || !idle || primary.disabled} onClick={() => void primary.action()}>
              {primary.action === doPreview && <Eye size={16} />}
              {busy === "saving" ? "正在保存…" : busy === "previewing" ? "正在生成预览…" : primary.label}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
