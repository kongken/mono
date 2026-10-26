import { useEffect, useMemo, useRef, useState } from "react";
import { CrosshairSimple, Plus, Trash } from "@phosphor-icons/react";
import { Button } from "@kongken/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@kongken/ui/dialog";
import { Input } from "@kongken/ui/input";
import { searchWorldCountries, type WorldCountry, type WorldSearchIndex } from "../lib/world-catalog.ts";
import { CHINA_LOCATION_MESSAGE, newTrip, type FormError, type PlaceForm } from "../lib/world-form.ts";
import { WORLD_NAME_LIMIT, WORLD_NOTE_LIMIT, type WorldTrip } from "../lib/world.ts";

/** What the typed or picked coordinates fall on, for the hint under them. */
export type Located =
  | { state: "none" }
  /** The inputs are not (both) readable yet. */
  | { state: "unreadable" }
  | { state: "china" }
  /** The coordinates read, but the map is not there to say where they fall. */
  | { state: "unchecked" }
  /** Open sea, or a place too small to have a polygon of its own: the country is chosen from the list. */
  | { state: "nothing" }
  | { state: "country"; code: string };

type Props = {
  open: boolean;
  form: PlaceForm;
  error: FormError | null;
  located: Located;
  /** The form differs from what it opened with: closing it asks before the changes are given up. */
  dirty: boolean;
  countries: readonly WorldCountry[];
  index: WorldSearchIndex;
  names: Readonly<Record<string, string>>;
  /** False when the map did not load: coordinates can still be typed. */
  canPick: boolean;
  /** Where focus goes when the dialog closes (the button or point that opened it). */
  opener: React.RefObject<HTMLElement | SVGElement | null>;
  onChange: (patch: Partial<PlaceForm>) => void;
  onChooseCountry: (code: string) => void;
  onPickOnMap: () => void;
  onSave: () => void;
  onCancel: () => void;
  onDelete: () => void;
};

const fieldProps = (error: FormError | null, field: "name" | "country" | "lat" | "lng") => ({
  "data-field": field,
  "aria-invalid": error?.field === field || undefined,
  "aria-describedby": error?.field === field ? "world-form-error" : undefined,
});
const tripError = (error: FormError | null, id: string, part: "date" | "note") =>
  typeof error?.field === "object" && error.field.trip === id && error.field.part === part;

function locatedText(located: Located, names: Readonly<Record<string, string>>): string {
  switch (located.state) {
    case "none":
      return "点击“在地图上选点”，或直接输入纬度和经度。";
    case "unreadable":
      return "";
    case "china":
      return CHINA_LOCATION_MESSAGE;
    case "unchecked":
      return "地图没有载入，无法检查这个位置是否在中国，也不能判定所在国家或地区：请自行确认，并在下方列表中选择。";
    case "nothing":
      return "这个位置没有对应的国家或地区（海上，或区域过小），请在下方列表中选择所在国家或地区。";
    case "country":
      return `这个位置在${names[located.code] ?? located.code}。`;
  }
}

export default function WorldPlaceDialog({
  open,
  form,
  error,
  located,
  dirty,
  countries,
  index,
  names,
  canPick,
  opener,
  onChange,
  onChooseCountry,
  onPickOnMap,
  onSave,
  onCancel,
  onDelete,
}: Props) {
  const [query, setQuery] = useState("");
  // Asked once the user tries to close a form with changes (the button, the cross, Esc, a click outside).
  const [asking, setAsking] = useState(false);
  const keepEditing = useRef<HTMLButtonElement>(null);
  const focusTrip = useRef<string | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const isNew = form.id === undefined;

  const options = useMemo(() => {
    const found = searchWorldCountries(countries, index, query).filter((country) => country.code !== "CN");
    const chosen = countries.find((country) => country.code === form.country);
    return chosen && !found.some((country) => country.code === chosen.code) ? [chosen, ...found] : found;
  }, [countries, index, query, form.country]);

  // A new trip is where the user goes next.
  useEffect(() => {
    const id = focusTrip.current;
    if (!id) return;
    focusTrip.current = null;
    body.current?.querySelector<HTMLElement>(`[data-trip="${id}"] input`)?.focus();
  });
  // A rejected save leads the user to the field to fix.
  useEffect(() => {
    if (!error?.field || !open) return;
    const selector = typeof error.field === "string" ? `[data-field="${error.field}"]` : `[data-trip="${error.field.trip}"] ${error.field.part === "date" ? "input" : "textarea"}`;
    body.current?.querySelector<HTMLElement>(selector)?.focus();
  }, [error, open]);

  // Esc or a click outside while asking is a way back to the form, never a way to lose it.
  const requestClose = () => {
    if (asking) setAsking(false);
    else if (dirty) setAsking(true);
    else onCancel();
  };
  useEffect(() => {
    if (asking) keepEditing.current?.focus();
  }, [asking]);

  const changeTrip = (id: string, patch: Partial<WorldTrip>) =>
    onChange({ trips: form.trips.map((trip) => (trip.id === id ? { ...trip, ...patch } : trip)) });
  const chosenName = form.country ? (names[form.country] ?? form.country) : "";
  const differs = located.state === "country" && form.country && located.code !== form.country;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && requestClose()}>
      <DialogContent
        className="world-place-dialog"
        onCloseAutoFocus={(event) => {
          const element = opener.current;
          if (element?.isConnected) {
            event.preventDefault();
            element.focus();
          }
        }}
      >
        <DialogHeader>
          <DialogTitle>{isNew ? "添加海外城市" : `编辑海外城市：${form.name || "未命名"}`}</DialogTitle>
          <DialogDescription>
            城市由你自己命名，位置在地图上选取或输入经纬度（WGS-84），不会使用国家的中心点。国家或地区标记与城市互相独立。
          </DialogDescription>
        </DialogHeader>
        <form
          className="world-form"
          noValidate
          onSubmit={(event) => {
            event.preventDefault();
            onSave();
          }}
        >
          <div className="world-form-body" ref={body}>
            <label className="field-label">
              城市名称
              <Input
                {...fieldProps(error, "name")}
                value={form.name}
                maxLength={WORLD_NAME_LIMIT * 2}
                autoComplete="off"
                placeholder="例如：东京"
                onChange={(event) => onChange({ name: event.target.value })}
              />
            </label>

            <fieldset className="world-location">
              <legend>位置</legend>
              <Button type="button" variant="outline" onClick={onPickOnMap} disabled={!canPick} data-action="pick">
                <CrosshairSimple size={17} />
                在地图上选点
              </Button>
              {!canPick && <p className="world-hint">世界地图暂不可用，请直接输入经纬度。</p>}
              <div className="world-coordinates">
                <label className="field-label">
                  纬度（-90 到 90）
                  <Input
                    {...fieldProps(error, "lat")}
                    inputMode="decimal"
                    autoComplete="off"
                    placeholder="35.6764"
                    value={form.lat}
                    onChange={(event) => onChange({ lat: event.target.value })}
                  />
                </label>
                <label className="field-label">
                  经度（-180 到 180）
                  <Input
                    {...fieldProps(error, "lng")}
                    inputMode="decimal"
                    autoComplete="off"
                    placeholder="139.65"
                    value={form.lng}
                    onChange={(event) => onChange({ lng: event.target.value })}
                  />
                </label>
              </div>
              <p className="world-located" role="status" data-located={located.state}>
                {locatedText(located, names)}
              </p>
            </fieldset>

            <div className="world-country-field">
              <label className="field-label">
                所在国家或地区
                <Input
                  type="search"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="搜索：中文、拼音、英文、代码"
                  aria-label="搜索所在国家或地区"
                  onKeyDown={(event) => event.key === "Enter" && event.preventDefault()}
                  autoComplete="off"
                  maxLength={100}
                />
              </label>
              <select
                {...fieldProps(error, "country")}
                aria-label="所在国家或地区"
                value={form.country}
                onChange={(event) => onChooseCountry(event.target.value)}
              >
                <option value="">请选择…</option>
                {options.map((country) => (
                  <option key={country.code} value={country.code}>
                    {country.name} · {country.en}
                    {country.hasShape ? "" : "（仅可从列表选择）"}
                  </option>
                ))}
              </select>
              {differs && (
                <p className="world-note" role="note">
                  这个位置在{names[located.code] ?? located.code}，你选择了{chosenName}。如果位置没有选错，可以保持现状。
                  <Button type="button" variant="outline" size="sm" onClick={() => onChooseCountry(located.code)}>
                    改为{names[located.code] ?? located.code}
                  </Button>
                </p>
              )}
            </div>

            <section className="world-trips" aria-labelledby="world-trips-title">
              <h3 id="world-trips-title">旅行记录 · {form.trips.length} 次</h3>
              <p className="world-hint">日期可以留空；填写时必须是真实存在的日期。</p>
              {form.trips.map((trip, i) => (
                <div className="world-trip" key={trip.id} data-trip={trip.id}>
                  <div className="world-trip-head">
                    <strong>第 {i + 1} 次旅行</strong>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`删除第 ${i + 1} 次旅行`}
                      onClick={() => onChange({ trips: form.trips.filter((other) => other.id !== trip.id) })}
                    >
                      <Trash size={15} />
                      删除
                    </Button>
                  </div>
                  <label className="field-label">
                    旅行日期
                    <span className="sr-only"> 第 {i + 1} 次</span>
                    <Input
                      type="date"
                      value={trip.date}
                      aria-invalid={tripError(error, trip.id, "date") || undefined}
                      aria-describedby={tripError(error, trip.id, "date") ? "world-form-error" : undefined}
                      onChange={(event) => changeTrip(trip.id, { date: event.target.value })}
                    />
                  </label>
                  <label className="field-label">
                    旅行记忆
                    <span className="sr-only"> 第 {i + 1} 次</span> <span>选填</span>
                    <textarea
                      value={trip.note}
                      maxLength={WORLD_NOTE_LIMIT}
                      aria-invalid={tripError(error, trip.id, "note") || undefined}
                      aria-describedby={tripError(error, trip.id, "note") ? "world-form-error" : undefined}
                      placeholder="那天的天气、遇见的人，或一顿念念不忘的美食…"
                      onChange={(event) => changeTrip(trip.id, { note: event.target.value })}
                    />
                  </label>
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  const trip = newTrip();
                  focusTrip.current = trip.id;
                  onChange({ trips: [...form.trips, trip] });
                }}
              >
                <Plus size={16} />
                添加一次旅行
              </Button>
            </section>
          </div>

          {error && (
            <p id="world-form-error" className="world-form-error" role="alert">
              {error.message}
            </p>
          )}
          {asking ? (
            <div className="world-discard" role="alertdialog" aria-labelledby="world-discard-title" aria-describedby="world-discard-text">
              <p id="world-discard-title">
                <strong>放弃未保存的修改？</strong>
              </p>
              <p id="world-discard-text">
                {isNew ? "这座城市还没有保存，" : "这座城市的修改还没有保存，"}关闭后已填写的内容（包括旅行记录）不会保留。
              </p>
              <div className="dialog-actions world-form-actions">
                <Button type="button" variant="outline" ref={keepEditing} onClick={() => setAsking(false)}>
                  继续编辑
                </Button>
                <Button type="button" variant="destructive" onClick={onCancel}>
                  放弃修改
                </Button>
              </div>
            </div>
          ) : (
            <div className="dialog-actions world-form-actions">
              {!isNew && (
                <Button type="button" variant="ghost" className="delete-button" onClick={onDelete}>
                  <Trash size={16} />
                  删除这座城市
                </Button>
              )}
              <Button type="button" variant="outline" onClick={requestClose}>
                取消
              </Button>
              <Button type="submit">{isNew ? "添加城市" : "保存修改"}</Button>
            </div>
          )}
        </form>
      </DialogContent>
    </Dialog>
  );
}
