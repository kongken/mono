import { LineStatus } from "@kongken/api-client/bff/metro/v1/catalog_pb";
import type { LineInput } from "@kongken/api-client/bff/metro/v1/admin_pb";
import { formatKm, lines } from "@kongken/metro-core/format";
import { statusLabel } from "@kongken/metro-core/status";
import { metroAdmin as admin, write } from "@/lib/rpc";
import type { FieldDef, Form } from "@/lib/metro-form";

export type { FieldDef, Form };

export type Row = { id: string; revision: bigint; key: string; cells: string[]; form: Form };
export type Page = { rows: Row[]; next: string; total: bigint };

/** One kind of catalogue record, as the administration screens see it. */
export type Entity = {
  id: string;
  title: string;
  noun: string;
  columns: string[];
  fields: FieldDef[];
  list(query: string, pageToken: string): Promise<Page>;
  emptyForm(): Form;
  create(form: Form): Promise<void>;
  update(id: string, revision: bigint, form: Form): Promise<void>;
  remove(id: string, revision: bigint): Promise<void>;
};

const PAGE = 50;
const str = (f: Form, k: string) => String(f[k] ?? "");
const int = (f: Form, k: string) => Math.trunc(Number(str(f, k) || 0));
const dec = (f: Form, k: string) => Number(str(f, k) || 0);
const optNum = (f: Form, k: string) => (str(f, k).trim() === "" ? undefined : Number(str(f, k)));
const bool = (f: Form, k: string) => f[k] === true;
const show = (n: number | undefined) => (n === undefined ? "" : String(n));

const statusOptions = [LineStatus.OPERATING, LineStatus.UNDER_CONSTRUCTION, LineStatus.PLANNED, LineStatus.SUSPENDED, LineStatus.RETIRED].map((s) => ({
  value: String(s),
  label: statusLabel(s),
}));

export const cities: Entity = {
  id: "cities",
  title: "城市",
  noun: "城市",
  columns: ["标识", "名称", "排序"],
  fields: [
    { key: "slug", label: "标识 slug", kind: "text", hint: "小写字母、数字、连字符，最多 32 位，用于网址" },
    { key: "name", label: "名称", kind: "text" },
    { key: "headerImages", label: "头图（每行一个 https 地址，最多 8 个）", kind: "textarea" },
    { key: "mapSvgUrl", label: "线路图 SVG 地址（https）", kind: "text", hint: "只会作为图片显示" },
    { key: "sortOrder", label: "排序", kind: "number" },
  ],
  async list(query, pageToken) {
    const r = await admin.listAdminCities({ query, pageSize: PAGE, pageToken });
    return {
      rows: r.cities.map((c) => ({
        id: c.id, revision: c.revision, key: c.city?.slug ?? "",
        cells: [c.city?.slug ?? "", c.city?.name ?? "", String(c.city?.sortOrder ?? 0)],
        form: { slug: c.city?.slug ?? "", name: c.city?.name ?? "", headerImages: (c.city?.headerImages ?? []).join("\n"), mapSvgUrl: c.city?.mapSvgUrl ?? "", sortOrder: String(c.city?.sortOrder ?? 0) },
      })),
      next: r.nextPageToken,
      total: r.total,
    };
  },
  emptyForm: () => ({ slug: "", name: "", headerImages: "", mapSvgUrl: "", sortOrder: "0" }),
  create: (f) => write(() => admin.createCity({ city: cityInput(f) })).then(() => undefined),
  update: (id, rev, f) => write(() => admin.updateCity({ id, expectedRevision: rev, city: cityInput(f) })).then(() => undefined),
  remove: (id, rev) => write(() => admin.deleteCity({ id, expectedRevision: rev })).then(() => undefined),
};
const cityInput = (f: Form) => ({ slug: str(f, "slug"), name: str(f, "name"), headerImages: lines(str(f, "headerImages")), mapSvgUrl: str(f, "mapSvgUrl"), sortOrder: int(f, "sortOrder") });

export const rollingStocks: Entity = {
  id: "rolling-stocks",
  title: "车型",
  noun: "车型",
  columns: ["代码", "长度 m", "宽度 m", "载客量"],
  fields: [
    { key: "code", label: "代码", kind: "text", hint: "最多 32 个字符" },
    { key: "lengthM", label: "车长（米，可空）", kind: "decimal" },
    { key: "widthM", label: "车宽（米，可空）", kind: "decimal" },
    { key: "capacity", label: "载客量（可空）", kind: "number" },
  ],
  async list(query, pageToken) {
    const r = await admin.listAdminRollingStocks({ query, pageSize: PAGE, pageToken });
    return {
      rows: r.rollingStocks.map((s) => ({
        id: s.id, revision: s.revision, key: s.rollingStock?.code ?? "",
        cells: [s.rollingStock?.code ?? "", show(s.rollingStock?.lengthM), show(s.rollingStock?.widthM), show(s.rollingStock?.capacity)],
        form: { code: s.rollingStock?.code ?? "", lengthM: show(s.rollingStock?.lengthM), widthM: show(s.rollingStock?.widthM), capacity: show(s.rollingStock?.capacity) },
      })),
      next: r.nextPageToken, total: r.total,
    };
  },
  emptyForm: () => ({ code: "", lengthM: "", widthM: "", capacity: "" }),
  create: (f) => write(() => admin.createRollingStock({ rollingStock: stockInput(f) })).then(() => undefined),
  update: (id, rev, f) => write(() => admin.updateRollingStock({ id, expectedRevision: rev, rollingStock: stockInput(f) })).then(() => undefined),
  remove: (id, rev) => write(() => admin.deleteRollingStock({ id, expectedRevision: rev })).then(() => undefined),
};
const stockInput = (f: Form) => ({ code: str(f, "code"), lengthM: optNum(f, "lengthM"), widthM: optNum(f, "widthM"), capacity: optNum(f, "capacity") });

export const linesEntity: Entity = {
  id: "lines",
  title: "线路",
  noun: "线路",
  columns: ["代码", "城市", "名称", "状态", "里程"],
  fields: [
    { key: "code", label: "代码", kind: "text", hint: "小写字母、数字、连字符，创建后不能修改", lockedOnEdit: true },
    { key: "citySlug", label: "所属城市 slug", kind: "text", serverKey: "city" },
    { key: "name", label: "名称", kind: "text" },
    { key: "number", label: "序号（0 = 未编号，排在最后）", kind: "number" },
    { key: "status", label: "状态", kind: "select", options: statusOptions },
    { key: "rollingStockCode", label: "车型代码（可空）", kind: "text", serverKey: "rolling_stock" },
    { key: "cars", label: "编组（节）", kind: "number" },
    { key: "carsAddition", label: "加开节数", kind: "number" },
    { key: "stations", label: "站数", kind: "number" },
    { key: "lengthKm", label: "里程（km，最多两位小数）", kind: "decimal" },
    { key: "color", label: "颜色", kind: "text", hint: "#RRGGBB" },
    { key: "commencedOn", label: "开通日期（YYYY-MM-DD，可空）", kind: "text" },
    { key: "extendedOn", label: "延伸日期（YYYY-MM-DD，可空）", kind: "text" },
    { key: "isLoop", label: "环线", kind: "checkbox" },
  ],
  async list(query, pageToken) {
    const r = await admin.listAdminLines({ query, statuses: [], citySlug: "", pageSize: PAGE, pageToken });
    return {
      rows: r.lines.map((l) => ({
        id: l.id, revision: l.revision, key: l.line?.code ?? "",
        cells: [l.line?.code ?? "", l.line?.citySlug ?? "", l.line?.name ?? "", statusLabel(l.line?.status ?? LineStatus.UNSPECIFIED), formatKm(l.line?.lengthKm ?? 0)],
        form: lineForm(l.line),
      })),
      next: r.nextPageToken, total: r.total,
    };
  },
  emptyForm: () => ({
    code: "", citySlug: "", name: "", number: "0", status: String(LineStatus.OPERATING), rollingStockCode: "", cars: "6", carsAddition: "0",
    stations: "0", lengthKm: "0", color: "#", commencedOn: "", extendedOn: "", isLoop: false,
  }),
  create: (f) => write(() => admin.createLine({ line: lineInput(f) })).then(() => undefined),
  update: (id, rev, f) => write(() => admin.updateLine({ id, expectedRevision: rev, line: lineInput(f) })).then(() => undefined),
  remove: (id, rev) => write(() => admin.deleteLine({ id, expectedRevision: rev })).then(() => undefined),
};
const lineForm = (x?: LineInput): Form => ({
  code: x?.code ?? "", citySlug: x?.citySlug ?? "", name: x?.name ?? "", number: String(x?.number ?? 0), status: String(x?.status ?? LineStatus.OPERATING),
  rollingStockCode: x?.rollingStockCode ?? "", cars: String(x?.cars ?? 0), carsAddition: String(x?.carsAddition ?? 0), stations: String(x?.stations ?? 0),
  lengthKm: String(x?.lengthKm ?? 0), color: x?.color ?? "", commencedOn: x?.commencedOn ?? "", extendedOn: x?.extendedOn ?? "", isLoop: x?.isLoop ?? false,
});
const lineInput = (f: Form) => ({
  code: str(f, "code"), citySlug: str(f, "citySlug"), name: str(f, "name"), number: int(f, "number"), status: Number(str(f, "status")) as LineStatus,
  rollingStockCode: str(f, "rollingStockCode"), cars: int(f, "cars"), carsAddition: int(f, "carsAddition"), stations: int(f, "stations"),
  lengthKm: dec(f, "lengthKm"), color: str(f, "color"), commencedOn: str(f, "commencedOn"), extendedOn: str(f, "extendedOn"), isLoop: bool(f, "isLoop"),
});

export const topics: Entity = {
  id: "topics",
  title: "专题",
  noun: "专题",
  columns: ["标识", "名称", "车型", "编组"],
  fields: [
    { key: "slug", label: "标识 slug", kind: "text" },
    { key: "name", label: "名称", kind: "text" },
    { key: "rollingStockCodes", label: "车型代码（每行一个）", kind: "textarea", hint: "车型与编组至少填一项" },
    { key: "cars", label: "编组节数（每行一个数字）", kind: "textarea" },
  ],
  async list(query, pageToken) {
    const r = await admin.listAdminRollingStockTopics({ query, pageSize: PAGE, pageToken });
    return {
      rows: r.topics.map((t) => ({
        id: t.id, revision: t.revision, key: t.topic?.slug ?? "",
        cells: [t.topic?.slug ?? "", t.topic?.name ?? "", (t.topic?.rollingStockCodes ?? []).join("、"), (t.topic?.cars ?? []).join("、")],
        form: { slug: t.topic?.slug ?? "", name: t.topic?.name ?? "", rollingStockCodes: (t.topic?.rollingStockCodes ?? []).join("\n"), cars: (t.topic?.cars ?? []).join("\n") },
      })),
      next: r.nextPageToken, total: r.total,
    };
  },
  emptyForm: () => ({ slug: "", name: "", rollingStockCodes: "", cars: "" }),
  create: (f) => write(() => admin.createRollingStockTopic({ topic: topicInput(f) })).then(() => undefined),
  update: (id, rev, f) => write(() => admin.updateRollingStockTopic({ id, expectedRevision: rev, topic: topicInput(f) })).then(() => undefined),
  remove: (id, rev) => write(() => admin.deleteRollingStockTopic({ id, expectedRevision: rev })).then(() => undefined),
};
const topicInput = (f: Form) => ({ slug: str(f, "slug"), name: str(f, "name"), rollingStockCodes: lines(str(f, "rollingStockCodes")), cars: lines(str(f, "cars")).map(Number) });

export const entities: Entity[] = [cities, linesEntity, rollingStocks, topics];

