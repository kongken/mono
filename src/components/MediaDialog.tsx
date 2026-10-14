import { useEffect, useRef, useState } from "react";
import {
  DownloadSimple,
  FilePdf,
  Image,
  Trash,
  UploadSimple,
} from "@phosphor-icons/react";
import { mediaApi, type MediaInfo } from "@/api/media";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { describeApiError } from "@/lib/errors";
import regions from "@/regions.json";
import type { Trip } from "@/lib/travel";
import { displayTrips, tripLabel as labelOf } from "@/lib/trips";
import "./MediaDialog.css";

const cities = regions.flatMap((p) =>
  p.cities.map((c) => ({ ...c, label: `${p.name} · ${c.name}` })),
);
type Props = {
  regionCode: string;
  /** Open scoped to one trip of the city; empty for the whole city. */
  tripId?: string;
  /** Trips per city, only to label media. */
  trips: Record<string, Trip[]>;
  /** Trips the server has confirmed; media can only link to these. */
  syncedTripIds: Set<string>;
  writable: boolean;
  /** Opened over the editor: the editor returns focus itself, so the default is skipped. */
  returnsToEditor?: boolean;
  onClose: () => void;
};
type Attempt = {
  file: File;
  uploadId: string;
  regionCode: string;
  tripId: string;
  mediaId?: string;
  uploaded?: boolean;
};

function Thumbnail({ media }: { media: MediaInfo }) {
  const [url, setUrl] = useState("");
  const [error, setError] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!media.width || media.status !== "ready") return;
    let alive = true,
      owned = "";
    setUrl("");
    setError(false);
    void mediaApi
      .read(media.id, true)
      .then(({ blob }) => {
        if (!alive) return;
        owned = URL.createObjectURL(blob);
        setUrl(owned);
      })
      .catch(() => {
        if (alive) setError(true);
      });
    return () => {
      alive = false;
      if (owned) URL.revokeObjectURL(owned);
    };
  }, [media.id, media.status, media.width, retry]);
  if (error)
    return (
      <button
        type="button"
        className="media-preview"
        onClick={() => setRetry((r) => r + 1)}
      >
        重试缩略图
      </button>
    );
  return (
    <div className="media-preview">
      {url ? (
        <img src={url} alt={media.filename} />
      ) : media.contentType === "application/pdf" ? (
        <FilePdf size={32} />
      ) : (
        <Image size={32} />
      )}
    </div>
  );
}

export function CityMediaPreview({ regionCode }: { regionCode: string }) {
  const [items, setItems] = useState<MediaInfo[]>([]);
  useEffect(() => {
    let alive = true;
    setItems([]);
    void mediaApi
      .list(regionCode)
      .then((list) => {
        if (alive)
          setItems(
            list.filter((m) => m.status === "ready" && m.width > 0).slice(0, 3),
          );
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [regionCode]);
  return items.length ? (
    <div className="city-media-preview" aria-label="城市照片缩略图">
      {items.map((m) => (
        <Thumbnail key={m.id} media={m} />
      ))}
    </div>
  ) : null;
}

export default function MediaDialog({
  regionCode,
  tripId = "",
  trips,
  syncedTripIds,
  writable,
  returnsToEditor = false,
  onClose,
}: Props) {
  const [city, setCity] = useState(regionCode);
  // "" shows the city's whole attachment list; a trip ID narrows it to that trip.
  const [scope, setScope] = useState(tripId);
  const cityTrips = city ? displayTrips(trips[city] ?? []) : [];
  const tripLabel = (id: string, region: string) => labelOf(trips[region] ?? [], id);
  const [items, setItems] = useState<MediaInfo[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [attempt, setAttempt] = useState<Attempt | null>(null);
  const [remove, setRemove] = useState<MediaInfo | null>(null);
  const [visible, setVisible] = useState(40);
  const generation = useRef(0);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  const load = async (code = city, trip = scope) => {
    const gen = ++generation.current;
    setLoaded(false);
    try {
      const list = await mediaApi.list(code, trip);
      if (gen === generation.current) {
        setItems(list);
        setLoaded(true);
      }
    } catch (e) {
      if (gen === generation.current) setError(describeApiError(e));
    }
  };
  useEffect(() => {
    setItems([]);
    setError("");
    setNote("");
    setRemove(null);
    setVisible(40);
    void load(city, scope);
  }, [city, scope]); // both selections are disabled during uploads
  const run = async (label: string, action: () => Promise<void>) => {
    setBusy(label);
    setError("");
    setNote("");
    try {
      await action();
    } catch (e) {
      setError(describeApiError(e));
    } finally {
      setBusy("");
    }
  };
  const upload = async (a: Attempt) => {
    await run("上传中…", async () => {
      if (a.uploaded && a.mediaId) {
        await mediaApi.complete(a.mediaId);
      } else {
        const r = await mediaApi.prepare(a.regionCode, a.file, a.uploadId, a.tripId);
        if (!r.media) throw new Error("incomplete upload response");
        a.mediaId = r.media.id;
        setAttempt({ ...a });
        if (r.media.status !== "ready") {
          if (!a.uploaded) {
            // No bearer token is sent to object storage. Browser supplies length.
            const response = await fetch(r.uploadUrl, {
              method: "PUT",
              body: a.file,
              headers: r.headers,
              credentials: "omit",
            });
            if (!response.ok) throw new Error("upload failed");
            a.uploaded = true;
            setAttempt({ ...a });
          }
          await mediaApi.complete(a.mediaId);
        }
      }
      setAttempt(null);
      await load(a.regionCode, a.tripId);
      setNote(
        a.file.type === "application/pdf"
          ? "附件已保存"
          : "已保存，照片的元数据已移除",
      );
    });
  };
  const download = (m: MediaInfo) =>
    run("读取中…", async () => {
      const { blob, filename } = await mediaApi.read(m.id);
      const url = URL.createObjectURL(blob),
        a = document.createElement("a");
      a.href = url;
      a.download = filename;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    });
  const locked = !!busy || !writable;
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent
        className="media-dialog"
        onCloseAutoFocus={(e) => {
          if (returnsToEditor) e.preventDefault();
        }}
        onEscapeKeyDown={(e) => {
          if (busy) e.preventDefault();
        }}
        onInteractOutside={(e) => {
          if (busy) e.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>照片与附件</DialogTitle>
          <DialogDescription>
            云账号私有保存。取消城市打卡、恢复历史版本都保留媒体；删除媒体无法撤销。
          </DialogDescription>
        </DialogHeader>
        <label className="field-label">
          目的地
          <select
            aria-label="媒体目的地"
            value={city}
            disabled={!!busy || !!attempt}
            onChange={(e) => {
              setCity(e.target.value);
              setScope("");
            }}
          >
            <option value="">全部目的地</option>
            {cities.map((c) => (
              <option key={c.code} value={c.code}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        {city && (
          <label className="field-label">
            关联旅行
            <select
              aria-label="关联旅行"
              value={scope}
              disabled={!!busy || !!attempt}
              onChange={(e) => setScope(e.target.value)}
            >
              <option value="">城市全部附件（上传时不关联旅行）</option>
              {cityTrips.map((t) => (
                <option
                  key={t.id}
                  value={t.id}
                  disabled={!syncedTripIds.has(t.id)}
                >
                  {tripLabel(t.id, city)}
                  {syncedTripIds.has(t.id) ? "" : "（尚未同步）"}
                </option>
              ))}
              {scope && !cityTrips.some((t) => t.id === scope) && (
                <option value={scope}>关联的旅行已删除</option>
              )}
            </select>
          </label>
        )}
        <p className="media-hint">
          JPEG、PNG、WebP 照片或 PDF 附件；每个文件最多 10 MB，每座城市最多 20
          个。照片转为 JPEG，最长边 8000 像素、最多 2400 万像素。PDF 仅供下载。
        </p>
        {!writable && (
          <p role="status">账号当前无法编辑，请恢复连接或重新登录后再上传。</p>
        )}
        <div className="media-toolbar">
          <label
            className={`media-upload ${locked || !city || attempt ? "disabled" : ""}`}
          >
            <UploadSimple size={18} />
            选择文件
            <input
              aria-label="上传照片或附件"
              type="file"
              accept="image/jpeg,image/png,image/webp,application/pdf"
              disabled={locked || !city || !!attempt}
              onChange={(e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                if (!file) return;
                if (
                  ![
                    "image/jpeg",
                    "image/png",
                    "image/webp",
                    "application/pdf",
                  ].includes(file.type) ||
                  !file.size ||
                  file.size > 10 * 1024 * 1024
                ) {
                  setError("请选择 10 MB 以内的 JPEG、PNG、WebP 或 PDF");
                  return;
                }
                const a = {
                  file,
                  uploadId: crypto.randomUUID(),
                  regionCode: city,
                  tripId: scope,
                };
                setAttempt(a);
                void upload(a);
              }}
            />
          </label>
          <Button
            type="button"
            variant="outline"
            disabled={!!busy}
            onClick={() => void load()}
          >
            刷新列表
          </Button>
        </div>
        {!city && (
          <p className="media-hint">
            选择目的地后可以上传，包括尚未打卡的城市。
          </p>
        )}
        {busy && <p role="status">{busy}</p>}
        {error && (
          <p className="unreadable-note unreadable-error" role="alert">
            {error}
          </p>
        )}
        {note && <p role="status">{note}</p>}
        {attempt && !busy && (
          <div className="media-retry">
            <span>{attempt.file.name} 未确认完成，重试不会重复保存。</span>
            <Button
              type="button"
              disabled={!writable}
              onClick={() => void upload(attempt)}
            >
              重试上传
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={!writable}
              onClick={() =>
                void run("取消中…", async () => {
                  if (attempt.mediaId) await mediaApi.delete(attempt.mediaId);
                  setAttempt(null);
                  await load();
                })
              }
            >
              放弃上传
            </Button>
          </div>
        )}
        <div className="media-grid">
          {items.slice(0, visible).map((m) => (
            <article className="media-card" key={m.id}>
              <Thumbnail media={m} />
              <strong title={m.filename}>{m.filename}</strong>
              <small>
                {cities.find((c) => c.code === m.regionCode)?.label ??
                  m.regionCode}{" "}
                · {(Number(m.sizeBytes) / 1024 / 1024).toFixed(1)} MB
              </small>
              {m.tripId && (
                <small className="media-trip">
                  {tripLabel(m.tripId, m.regionCode)
                    ? `关联旅行：${tripLabel(m.tripId, m.regionCode)}`
                    : "关联的旅行已删除，仍可在城市附件中查看"}
                </small>
              )}
              <div className="media-card-actions">
                {m.status === "ready" ? (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={!!busy}
                    onClick={() => void download(m)}
                    aria-label={`下载 ${m.filename}`}
                  >
                    <DownloadSimple size={16} />
                    下载
                  </Button>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={locked}
                    onClick={() =>
                      void run("确认上传…", async () => {
                        await mediaApi.complete(m.id);
                        await load();
                        setNote("上传已确认");
                      })
                    }
                  >
                    确认上传
                  </Button>
                )}
                <Button
                  type="button"
                  variant="ghost"
                  disabled={locked}
                  aria-label={`删除 ${m.filename}`}
                  onClick={() => setRemove(m)}
                >
                  <Trash size={16} />
                </Button>
              </div>
            </article>
          ))}
        </div>
        {items.length > visible && (
          <Button
            type="button"
            variant="outline"
            onClick={() => setVisible((n) => n + 40)}
          >
            显示更多媒体
          </Button>
        )}
        {loaded && !items.length && (
          <p className="media-empty">
            还没有照片或附件，把旅途中的记忆留在这里。
          </p>
        )}
        {remove && (
          <div className="media-remove" role="alert">
            <p>永久删除「{remove.filename}」？文件和缩略图会一并移除。</p>
            <Button
              type="button"
              variant="outline"
              disabled={!!busy}
              onClick={() => setRemove(null)}
            >
              保留
            </Button>
            <Button
              type="button"
              disabled={locked}
              onClick={() =>
                void run("删除中…", async () => {
                  await mediaApi.delete(remove.id);
                  setRemove(null);
                  await load();
                  setNote("已删除");
                })
              }
            >
              确认删除媒体
            </Button>
          </div>
        )}
        <div className="dialog-actions">
          <Button
            type="button"
            variant="outline"
            disabled={!!busy}
            onClick={onClose}
          >
            关闭
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
