import { useEffect, useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  Eye,
  EyeSlash,
  Path,
  PencilSimple,
  Plus,
  Sparkle,
  Trash,
} from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import regions from "@kongken/geo-data/regions.json";
import { colors, type Visit } from "@/lib/travel";
import {
  ITINERARY_NOTE_LIMIT,
  ITINERARY_TITLE_LIMIT,
  STOP_NOTE_LIMIT,
  TRANSPORT_MODES,
  buildItineraryInfo,
  formatDistanceKm,
  formatDurationMinutes,
  moveStop,
  newItineraryId,
  newStopId,
  normalizeTransport,
  transportLabel,
  validateItineraryInput,
  type ItineraryEstimationPolicy,
  type ItineraryInfo,
  type ItineraryStopInput,
} from "@/lib/itinerary";
import "./itinerary.css";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  itineraries: ItineraryInfo[];
  activeItineraryId: string;
  initialEditId?: string | null;
  policy: ItineraryEstimationPolicy;
  visits: Visit[];
  onSave: (
    input: {
      id: string;
      title: string;
      note: string;
      color: string;
      stops: ItineraryStopInput[];
      expectedVersion: number;
    },
    activateOnMap: boolean,
  ) => Promise<ItineraryInfo>;
  onDelete: (id: string, expectedVersion: number) => Promise<void>;
  onSelectActive: (id: string) => void;
};

const provinceByCityCode = new Map<string, string>(
  regions.flatMap((p) => p.cities.map((c) => [c.code, p.code])),
);

function defaultDraftStops(visits: Visit[]): ItineraryStopInput[] {
  if (visits.length >= 2) {
    return visits.slice(0, 3).map((v, i) => ({
      id: newStopId(),
      regionCode: v.code,
      date: v.trips[0]?.date ?? "",
      note: i === 0 ? "起点" : "",
      transport: "train",
      tripId: v.trips[0]?.id ?? "",
    }));
  }
  return [
    {
      id: newStopId(),
      regionCode: "310101",
      date: "",
      note: "",
      transport: "train",
    },
    {
      id: newStopId(),
      regionCode: "330100",
      date: "",
      note: "",
      transport: "train",
    },
  ];
}

export default function ItineraryDialog({
  open,
  onOpenChange,
  itineraries,
  activeItineraryId,
  initialEditId,
  policy,
  visits,
  onSave,
  onDelete,
  onSelectActive,
}: Props) {
  const [editingId, setEditingId] = useState<string>("");
  const [expectedVersion, setExpectedVersion] = useState<number>(0);
  const [title, setTitle] = useState<string>("");
  const [note, setNote] = useState<string>("");
  const [color, setColor] = useState<string>(colors[0].value);
  const [stops, setStops] = useState<ItineraryStopInput[]>(() => defaultDraftStops(visits));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const startNewDraft = () => {
    setEditingId(newItineraryId());
    setExpectedVersion(0);
    setTitle("");
    setNote("");
    setColor(colors[0].value);
    setStops(defaultDraftStops(visits));
    setError("");
  };

  const loadIntoEditor = (it: ItineraryInfo) => {
    setEditingId(it.id);
    setExpectedVersion(it.version);
    setTitle(it.title);
    setNote(it.note);
    setColor(it.color || colors[0].value);
    setStops(
      it.stops.map((s) => ({
        id: s.id,
        regionCode: s.regionCode,
        date: s.date,
        note: s.note,
        transport: normalizeTransport(s.transport),
        ...(s.tripId ? { tripId: s.tripId } : {}),
      })),
    );
    setError("");
  };

  useEffect(() => {
    if (!open) return;
    if (initialEditId) {
      const target = itineraries.find((item) => item.id === initialEditId);
      if (target) {
        loadIntoEditor(target);
        return;
      }
    }
    if (itineraries.length > 0 && editingId) {
      const existing = itineraries.find((item) => item.id === editingId);
      if (existing) {
        loadIntoEditor(existing);
        return;
      }
    }
    startNewDraft();
  }, [open, initialEditId]);

  const previewInfo = useMemo(() => {
    return buildItineraryInfo({
      id: editingId || "preview",
      title: title.trim() || "未命名行程",
      note,
      color,
      version: expectedVersion || 1,
      stops,
    });
  }, [editingId, title, note, color, expectedVersion, stops]);

  const handleProvinceChange = (index: number, nextProvinceCode: string) => {
    const prov = regions.find((r) => r.code === nextProvinceCode);
    const firstCityCode = prov?.cities[0]?.code ?? "330100";
    setStops((prev) =>
      prev.map((s, i) => (i === index ? { ...s, regionCode: firstCityCode } : s)),
    );
  };

  const handleStopChange = (index: number, patch: Partial<ItineraryStopInput>) => {
    setStops((prev) => prev.map((s, i) => (i === index ? { ...s, ...patch } : s)));
  };

  const handleMoveStop = (index: number, direction: -1 | 1) => {
    setStops((prev) => moveStop(prev, index, direction));
  };

  const handleRemoveStop = (index: number) => {
    setStops((prev) => (prev.length <= 2 ? prev : prev.filter((_, i) => i !== index)));
  };

  const handleAddStop = () => {
    const lastCode = stops[stops.length - 1]?.regionCode ?? "330100";
    const lastProvCode = provinceByCityCode.get(lastCode) ?? "330000";
    const prov = regions.find((p) => p.code === lastProvCode) ?? regions[0];
    const nextCity =
      prov.cities.find((c) => c.code !== lastCode)?.code ?? prov.cities[0].code;
    setStops((prev) => [
      ...prev,
      {
        id: newStopId(),
        regionCode: nextCity,
        date: "",
        note: "",
        transport: "train",
      },
    ]);
  };

  const handlePopulateFromVisits = () => {
    const dated = visits
      .flatMap((v) =>
        v.trips.length > 0
          ? v.trips.map((t) => ({
              regionCode: v.code,
              date: t.date,
              note: t.note.slice(0, 60),
              tripId: t.id,
            }))
          : [{ regionCode: v.code, date: "", note: "", tripId: "" }],
      )
      .sort((a, b) => {
        if (a.date && b.date) return a.date.localeCompare(b.date);
        if (a.date) return -1;
        if (b.date) return 1;
        return a.regionCode.localeCompare(b.regionCode);
      });
    if (dated.length < 2) {
      setError("至少需要 2 条城市足迹才能自动串联行程");
      return;
    }
    setStops(
      dated.slice(0, 20).map((item) => ({
        id: newStopId(),
        regionCode: item.regionCode,
        date: item.date,
        note: item.note,
        transport: "train",
        ...(item.tripId ? { tripId: item.tripId } : {}),
      })),
    );
    if (!title.trim()) {
      setTitle("我的足迹串联路线");
    }
    setError("");
  };

  const submitSave = async (activateOnMap: boolean) => {
    setError("");
    let validated;
    try {
      validated = validateItineraryInput({
        id: editingId || newItineraryId(),
        title,
        note,
        color,
        version: expectedVersion || 1,
        stops,
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "行程校验失败");
      return;
    }
    setBusy(true);
    try {
      const saved = await onSave(
        {
          id: validated.id,
          title: validated.title,
          note: validated.note,
          color: validated.color,
          stops: validated.stops,
          expectedVersion,
        },
        activateOnMap,
      );
      loadIntoEditor(saved);
      if (activateOnMap) {
        onOpenChange(false);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存行程失败");
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async (it: ItineraryInfo) => {
    setError("");
    setBusy(true);
    try {
      await onDelete(it.id, it.version);
      if (editingId === it.id) {
        startNewDraft();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "删除行程失败");
    } finally {
      setBusy(false);
    }
  };

  const isEditingExisting = itineraries.some((item) => item.id === editingId);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="itinerary-dialog-content">
        <DialogHeader>
          <DialogTitle>行程与路线连线</DialogTitle>
          <DialogDescription>
            规划与回顾跨城路线，调整途经城市顺序并在全国与省内地图绘制连线。
          </DialogDescription>
        </DialogHeader>

        <div
          className="itinerary-policy-card"
          data-testid="itinerary-estimation-policy"
        >
          <div className="itinerary-policy-badges">
            <span className="itinerary-policy-badge">
              估算供应方：{policy.providerLabel}（{policy.provider}）
            </span>
            <span className="itinerary-policy-badge">
              费用规则：内置离线计算 · 零外部 API 费用（{policy.costPolicy}）
            </span>
            <span className="itinerary-policy-badge">
              缓存策略：{policy.cacheStrategy}
            </span>
          </div>
          <p className="itinerary-policy-disclaimer">{policy.disclaimer}</p>
        </div>

        <section className="itinerary-section" aria-label="已保存的行程列表">
          <div className="itinerary-section-header">
            <h3>已保存行程（{itineraries.length}）</h3>
            <div className="itinerary-card-actions">
              {visits.length >= 2 && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={busy}
                  onClick={handlePopulateFromVisits}
                >
                  <Sparkle size={14} />
                  从现有足迹串联
                </Button>
              )}
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={startNewDraft}
              >
                <Plus size={14} />
                新建行程
              </Button>
            </div>
          </div>

          {itineraries.length === 0 ? (
            <p className="itinerary-card-meta">
              暂无已保存的行程，可在下方填写行程名称与途经城市后保存。
            </p>
          ) : (
            <div className="itinerary-list" data-testid="itinerary-saved-list">
              {itineraries.map((it) => {
                const isActive = activeItineraryId === it.id;
                return (
                  <div
                    key={it.id}
                    className={`itinerary-card ${isActive ? "active" : ""}`}
                    data-testid={`itinerary-card-${it.id}`}
                  >
                    <div className="itinerary-card-main">
                      <div className="itinerary-card-title-row">
                        <span
                          className="itinerary-color-dot"
                          style={{ background: it.color }}
                        />
                        <span className="itinerary-card-title">{it.title}</span>
                        {isActive && (
                          <span className="itinerary-active-pill">
                            地图展示中
                          </span>
                        )}
                      </div>
                      <div className="itinerary-card-stops">
                        {it.stops.map((s) => s.regionName).join(" → ")}
                      </div>
                      <div className="itinerary-card-meta">
                        共 {it.stops.length} 站 · {it.cityCount} 城 /{" "}
                        {it.provinceCount} 省 · 预计路程{" "}
                        {formatDistanceKm(it.totalEstimatedDistanceKm)}（直线{" "}
                        {formatDistanceKm(it.totalStraightDistanceKm)}） · 约{" "}
                        {formatDurationMinutes(it.totalEstimatedDurationMinutes)}
                      </div>
                    </div>
                    <div className="itinerary-card-actions">
                      <Button
                        type="button"
                        size="sm"
                        variant={isActive ? "secondary" : "outline"}
                        disabled={busy}
                        onClick={() => onSelectActive(isActive ? "" : it.id)}
                      >
                        {isActive ? (
                          <>
                            <EyeSlash size={14} />
                            隐藏路线
                          </>
                        ) : (
                          <>
                            <Eye size={14} />
                            展示路线
                          </>
                        )}
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        disabled={busy}
                        onClick={() => loadIntoEditor(it)}
                      >
                        <PencilSimple size={14} />
                        编辑
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        disabled={busy}
                        aria-label={`删除行程 ${it.title}`}
                        onClick={() => void handleDelete(it)}
                      >
                        <Trash size={14} />
                        删除
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>

        <section className="itinerary-editor" aria-label="行程编辑器">
          <div className="itinerary-section-header">
            <h3>{isEditingExisting ? "编辑行程" : "创建新行程"}</h3>
            <span className="itinerary-card-meta">
              至少包含 2 个站点，可随时上下调整途经顺序
            </span>
          </div>

          <div className="itinerary-form-row">
            <label className="itinerary-field">
              <span>行程名称</span>
              <Input
                aria-label="行程名称"
                placeholder="例如：江南春行、川滇自驾环线"
                maxLength={ITINERARY_TITLE_LIMIT}
                value={title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </label>
            <div className="itinerary-field">
              <span>路线颜色</span>
              <div
                className="itinerary-color-picker"
                role="radiogroup"
                aria-label="路线颜色"
              >
                {colors.map((c) => (
                  <button
                    key={c.value}
                    type="button"
                    role="radio"
                    aria-checked={color === c.value}
                    aria-label={`路线颜色：${c.name}`}
                    className={`itinerary-color-swatch ${color === c.value ? "selected" : ""}`}
                    style={{ background: c.value }}
                    onClick={() => setColor(c.value)}
                  />
                ))}
              </div>
            </div>
          </div>

          <label className="itinerary-field">
            <span>行程备注（选填）</span>
            <Input
              aria-label="行程备注"
              placeholder="记录这段路线的主题、同伴或整体印象"
              maxLength={ITINERARY_NOTE_LIMIT}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </label>

          <div className="itinerary-section">
            <div className="itinerary-section-header">
              <h3>途经城市与顺序（{stops.length} 站）</h3>
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={busy || stops.length >= 100}
                onClick={handleAddStop}
              >
                <Plus size={14} />
                添加途经城市
              </Button>
            </div>

            <div className="itinerary-stops-list">
              {stops.map((stop, idx) => {
                const provCode =
                  provinceByCityCode.get(stop.regionCode) ?? regions[0].code;
                const currentProvince =
                  regions.find((r) => r.code === provCode) ?? regions[0];
                const leg = idx > 0 ? previewInfo.legs[idx - 1] : null;
                const enriched = previewInfo.stops[idx];

                return (
                  <div key={stop.id || idx}>
                    {leg && (
                      <div
                        className="itinerary-leg-connector"
                        data-testid={`itinerary-leg-${idx}`}
                      >
                        <Path size={14} />
                        <span>
                          {leg.fromRegionName} → {leg.toRegionName} ·{" "}
                          {transportLabel(leg.transport)} · 预计路程{" "}
                          <strong>
                            {formatDistanceKm(leg.estimatedDistanceKm)}
                          </strong>
                          （直线 {formatDistanceKm(leg.straightDistanceKm)}） ·
                          约{" "}
                          <strong>
                            {formatDurationMinutes(leg.estimatedDurationMinutes)}
                          </strong>
                        </span>
                      </div>
                    )}

                    <div
                      className="itinerary-stop-item"
                      data-testid={`itinerary-stop-${idx}`}
                    >
                      <div className="itinerary-stop-top">
                        <span className="itinerary-stop-badge">
                          <span
                            className="itinerary-stop-num"
                            style={{ background: color }}
                          >
                            {idx + 1}
                          </span>
                          第 {idx + 1} 站 · {enriched?.regionName ?? stop.regionCode}
                        </span>
                        <div className="itinerary-stop-controls">
                          <button
                            type="button"
                            aria-label={`上移第 ${idx + 1} 站`}
                            disabled={busy || idx === 0}
                            onClick={() => handleMoveStop(idx, -1)}
                          >
                            <ArrowUp size={12} />
                            上移
                          </button>
                          <button
                            type="button"
                            aria-label={`下移第 ${idx + 1} 站`}
                            disabled={busy || idx === stops.length - 1}
                            onClick={() => handleMoveStop(idx, 1)}
                          >
                            <ArrowDown size={12} />
                            下移
                          </button>
                          <button
                            type="button"
                            aria-label={`移除第 ${idx + 1} 站`}
                            disabled={busy || stops.length <= 2}
                            onClick={() => handleRemoveStop(idx)}
                          >
                            <Trash size={12} />
                            移除
                          </button>
                        </div>
                      </div>

                      <div className="itinerary-stop-grid">
                        <select
                          aria-label={`第 ${idx + 1} 站省份`}
                          value={currentProvince.code}
                          onChange={(e) =>
                            handleProvinceChange(idx, e.target.value)
                          }
                        >
                          {regions.map((r) => (
                            <option key={r.code} value={r.code}>
                              {r.name}
                            </option>
                          ))}
                        </select>

                        <select
                          aria-label={`第 ${idx + 1} 站城市`}
                          value={stop.regionCode}
                          onChange={(e) =>
                            handleStopChange(idx, {
                              regionCode: e.target.value,
                            })
                          }
                        >
                          {currentProvince.cities.map((c) => (
                            <option key={c.code} value={c.code}>
                              {c.name}
                            </option>
                          ))}
                        </select>

                        {idx > 0 && (
                          <select
                            aria-label={`第 ${idx + 1} 站交通方式`}
                            value={stop.transport}
                            onChange={(e) =>
                              handleStopChange(idx, {
                                transport: normalizeTransport(e.target.value),
                              })
                            }
                          >
                            {TRANSPORT_MODES.map((m) => (
                              <option key={m.value} value={m.value}>
                                交通：{m.label}
                              </option>
                            ))}
                          </select>
                        )}

                        <input
                          type="date"
                          aria-label={`第 ${idx + 1} 站日期`}
                          value={stop.date}
                          onChange={(e) =>
                            handleStopChange(idx, { date: e.target.value })
                          }
                        />

                        <input
                          type="text"
                          aria-label={`第 ${idx + 1} 站备注`}
                          placeholder="站点备注（选填）"
                          maxLength={STOP_NOTE_LIMIT}
                          value={stop.note}
                          onChange={(e) =>
                            handleStopChange(idx, { note: e.target.value })
                          }
                        />
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          <div
            className="itinerary-summary-box"
            data-testid="itinerary-editor-summary"
          >
            <span>
              路线汇总：共 <strong>{previewInfo.stops.length}</strong> 站（途经{" "}
              <strong>{previewInfo.cityCount}</strong> 城 /{" "}
              <strong>{previewInfo.provinceCount}</strong> 省）
            </span>
            <span>
              预计总路程{" "}
              <strong>
                {formatDistanceKm(previewInfo.totalEstimatedDistanceKm)}
              </strong>
              （直线 {formatDistanceKm(previewInfo.totalStraightDistanceKm)}） ·
              预计交通总用时{" "}
              <strong>
                {formatDurationMinutes(
                  previewInfo.totalEstimatedDurationMinutes,
                )}
              </strong>
            </span>
          </div>

          {error && (
            <div className="itinerary-error" role="alert">
              {error}
            </div>
          )}

          <div className="dialog-actions">
            {isEditingExisting && (
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => {
                  const current = itineraries.find(
                    (item) => item.id === editingId,
                  );
                  if (current) void handleDelete(current);
                }}
              >
                <Trash size={15} />
                删除当前行程
              </Button>
            )}
            <Button
              type="button"
              variant="outline"
              disabled={busy}
              onClick={() => void submitSave(false)}
            >
              仅保存行程
            </Button>
            <Button
              type="button"
              disabled={busy}
              onClick={() => void submitSave(true)}
            >
              <Path size={16} />
              保存并在地图展示路线
            </Button>
          </div>
        </section>
      </DialogContent>
    </Dialog>
  );
}
