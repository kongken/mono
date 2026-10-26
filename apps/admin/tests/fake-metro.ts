/**
 * A fake BFF for the metro administration screens of the console (UI only): an in-memory
 * `bff.metro.v1.AdminService` and `bff.v1.AccessTokenService` that apply, where a screen can see them,
 * the rules the real ones apply: the network perimeter and the console's exact Origin on every call
 * (reads too), signed in, the metro role, a recent password check, CSRF on writes, expected revisions,
 * references that forbid deleting, field errors, an atomic import with a dry-run preview, export in the format
 * the import takes back, the audit channels, and the management tokens (secret returned once, a limit of
 * ten, revoke). It is a fixture, not a second implementation: the real chain is internal/e2e
 * (TestChainMetroAdmin*, TestChainAdminPerimeter*, TestAdminBrowserChain) and the real-stack spec.
 */
import type { Page, Route } from "@playwright/test";
import { create, toBinary } from "@bufbuild/protobuf";
import { ReferencedDetailSchema, RevisionConflictDetailSchema, ValidationDetailSchema } from "@kongken/api-client/bff/metro/v1/admin_pb";
import type { FakeAccounts } from "@kongken/account/testing";
import { CONSOLE_ORIGIN } from "./fake";

type Body = Record<string, any>;
type Out = { status: number; body: unknown };

export type City = { id: string; revision: number; slug: string; name: string; headerImages: string[]; mapSvgUrl: string; sortOrder: number };
export type Stock = { id: string; revision: number; code: string; lengthM?: number; widthM?: number; capacity?: number };
export type Line = {
  id: string; revision: number; code: string; citySlug: string; name: string; number: number; status: string; rollingStockCode: string;
  cars: number; carsAddition: number; stations: number; lengthKm: number; color: string; commencedOn: string; extendedOn: string; isLoop: boolean;
};
export type Topic = { id: string; revision: number; slug: string; name: string; rollingStockCodes: string[]; cars: number[] };
export type Token = { id: string; userId: string; name: string; audience: string; scopes: string[]; createdAt: string; expiresAt: string; lastUsedAt?: string; revokedAt?: string };
export type Log = { id: string; actorUserId: string; channel: "web" | "mcp"; tokenId: string; action: string; entityType: string; entityId: string; beforeJson: string; afterJson: string; createdAt: string };

const OPERATING = "LINE_STATUS_OPERATING";
const b64 = (bytes: Uint8Array) => Buffer.from(bytes).toString("base64");
const COLOR = /^#[0-9A-Fa-f]{6}$/;

const failure = (status: number, code: string, message: string, details: { type: string; value: Uint8Array }[] = []): Out => ({
  status,
  body: { code, message, details: details.map((d) => ({ type: d.type, value: b64(d.value) })) },
});

export function createFakeMetro(accounts: FakeAccounts) {
  const state = {
    version: 1,
    seq: 100,
    cities: [
      { id: "c1", revision: 1, slug: "shanghai", name: "上海", headerImages: [], mapSvgUrl: "", sortOrder: 0 },
      { id: "c2", revision: 1, slug: "tokyo", name: "东京", headerImages: [], mapSvgUrl: "", sortOrder: 1 },
    ] as City[],
    stocks: [{ id: "s1", revision: 1, code: "02A02", lengthM: 22.1, widthM: 3, capacity: 310 }] as Stock[],
    lines: [
      line("l1", "sh-1", "shanghai", "1号线", 1, "#E4002B"),
      line("l2", "sh-2", "shanghai", "2号线", 2, "#97D700"),
    ] as Line[],
    topics: [{ id: "t1", revision: 1, slug: "six-cars", name: "6 节编组", rollingStockCodes: ["02A02"], cars: [6] }] as Topic[],
    tokens: [] as Token[],
    secrets: [] as string[],
    audit: [
      { id: "a1", actorUserId: "u-root", channel: "web", tokenId: "", action: "city.create", entityType: "city", entityId: "c1", beforeJson: "", afterJson: '{"slug":"shanghai"}', createdAt: "2026-05-01T08:00:00Z" },
      { id: "a2", actorUserId: "u-root", channel: "mcp", tokenId: "tok-abcdef123456", action: "line.update", entityType: "line", entityId: "l1", beforeJson: '{"name":"一号线"}', afterJson: '{"name":"1号线"}', createdAt: "2026-05-02T08:00:00Z" },
    ] as Log[],
    /** Procedures called, in order, with their bodies and the Origin header. */
    calls: [] as { method: string; body: Body; origin: string | undefined }[],
    /** The upstream is not mounted behind the BFF: every call is answered 404 with no Connect body. */
    absent: false,
    /** The perimeter refuses every call (network outside the allowlist). */
    perimeterClosed: false,
    /** The password check is older than the admin session limit. */
    reauthNeeded: false,
    /** Calls answered 503 while > 0. */
    unavailable: 0,
    /** Writes sent without the CSRF header (must stay empty). */
    writesWithoutCsrf: [] as string[],
    /** Milliseconds a call waits before it is answered, by procedure and body (a slow older answer arriving late). */
    delay: null as null | ((method: string, body: Body) => number),
    /** Runs on the server side just before the next write is decided (another editor). */
    beforeNextWrite: null as null | (() => void),
  };

  function line(id: string, code: string, citySlug: string, name: string, number: number, color: string): Line {
    return { id, revision: 1, code, citySlug, name, number, status: OPERATING, rollingStockCode: "02A02", cars: 6, carsAddition: 0, stations: 28, lengthKm: 36.9, color, commencedOn: "1993-05-28", extendedOn: "", isLoop: false };
  }

  const nextId = (p: string) => `${p}${++state.seq}`;
  const bump = () => (state.version += 1);
  const invalid = (errors: { field: string; reason: string }[]) =>
    failure(400, "invalid_argument", "invalid", [{ type: "bff.metro.v1.ValidationDetail", value: toBinary(ValidationDetailSchema, create(ValidationDetailSchema, { errors })) }]);
  const exists = (field: string) =>
    failure(409, "already_exists", "already exists", [{ type: "bff.metro.v1.ValidationDetail", value: toBinary(ValidationDetailSchema, create(ValidationDetailSchema, { errors: [{ field, reason: "已存在" }] })) }]);
  const conflict = (current: number) =>
    failure(400, "failed_precondition", "stale revision", [{ type: "bff.metro.v1.RevisionConflictDetail", value: toBinary(RevisionConflictDetailSchema, create(RevisionConflictDetailSchema, { currentRevision: BigInt(current) })) }]);
  const referenced = (refs: { entityType: string; count: number }[]) =>
    failure(400, "failed_precondition", "referenced", [{ type: "bff.metro.v1.ReferencedDetail", value: toBinary(ReferencedDetailSchema, create(ReferencedDetailSchema, { references: refs.map((r) => ({ entityType: r.entityType, count: BigInt(r.count) })) })) }]);
  const notFound = () => failure(404, "not_found", "not found");

  const log = (action: string, entityType: string, entityId: string, before: unknown, after: unknown) => {
    const me = accounts.state.current;
    state.audit.unshift({
      id: nextId("a"), actorUserId: me ? me.id : "", channel: "web", tokenId: "", action, entityType, entityId,
      beforeJson: before ? JSON.stringify(before) : "", afterJson: after ? JSON.stringify(after) : "", createdAt: new Date().toISOString(),
    });
  };

  const page = <T>(rows: T[], b: Body) => {
    const size = Math.min(b.pageSize || 50, 100);
    const start = b.pageToken ? Number(Buffer.from(String(b.pageToken), "base64url").toString()) : 0;
    const items = rows.slice(start, start + size);
    return { items, next: start + size < rows.length ? Buffer.from(String(start + size)).toString("base64url") : "", total: String(rows.length) };
  };
  const matches = (q: string, ...fields: string[]) => !q || fields.some((f) => f.toLowerCase().includes(q.toLowerCase()));

  const cityIn = (c: City) => ({ slug: c.slug, name: c.name, headerImages: c.headerImages, mapSvgUrl: c.mapSvgUrl, sortOrder: c.sortOrder });
  const lineIn = (l: Line) => {
    const { id: _i, revision: _r, ...rest } = l;
    return rest;
  };
  const topicIn = (t: Topic) => ({ slug: t.slug, name: t.name, rollingStockCodes: t.rollingStockCodes, cars: t.cars });
  const stockIn = (s: Stock) => ({ code: s.code, lengthM: s.lengthM, widthM: s.widthM, capacity: s.capacity });

  function checkLine(l: Body): Out | null {
    const errors: { field: string; reason: string }[] = [];
    if (!COLOR.test(String(l.color ?? ""))) errors.push({ field: "color", reason: "必须是 #RRGGBB" });
    // The server's own names for these two fields are "city" and "rolling_stock".
    if (!state.cities.some((c) => c.slug === l.citySlug)) errors.push({ field: "city", reason: "城市不存在" });
    if (l.rollingStockCode && !state.stocks.some((s) => s.code === l.rollingStockCode)) errors.push({ field: "rolling_stock", reason: "车型不存在" });
    return errors.length ? invalid(errors) : null;
  }

  function reads(method: string, b: Body): Out | undefined {
    switch (method) {
      case "ListAdminCities": {
        const p = page(state.cities.filter((c) => matches(b.query ?? "", c.slug, c.name)), b);
        return { status: 200, body: { cities: p.items.map((c) => ({ id: c.id, revision: String(c.revision), city: cityIn(c) })), nextPageToken: p.next, total: p.total } };
      }
      case "ListAdminLines": {
        const p = page(state.lines.filter((l) => matches(b.query ?? "", l.code, l.name)), b);
        return { status: 200, body: { lines: p.items.map((l) => ({ id: l.id, revision: String(l.revision), line: lineIn(l) })), nextPageToken: p.next, total: p.total } };
      }
      case "ListAdminRollingStocks": {
        const p = page(state.stocks.filter((s) => matches(b.query ?? "", s.code)), b);
        return { status: 200, body: { rollingStocks: p.items.map((s) => ({ id: s.id, revision: String(s.revision), rollingStock: stockIn(s) })), nextPageToken: p.next, total: p.total } };
      }
      case "ListAdminRollingStockTopics": {
        const p = page(state.topics.filter((t) => matches(b.query ?? "", t.slug, t.name)), b);
        return { status: 200, body: { topics: p.items.map((t) => ({ id: t.id, revision: String(t.revision), topic: topicIn(t) })), nextPageToken: p.next, total: p.total } };
      }
      case "ListAuditLogs": {
        let rows = state.audit.filter((l) => (!b.entityType || l.entityType === b.entityType) && (!b.channel || l.channel === b.channel) && (!b.actorUserId || l.actorUserId === b.actorUserId));
        rows = [...rows].sort((a, c) => (a.createdAt < c.createdAt ? 1 : -1));
        const p = page(rows, b);
        return { status: 200, body: { logs: p.items, nextPageToken: p.next } };
      }
      case "ExportCatalog":
        return { status: 200, body: { catalogJson: JSON.stringify(exportDoc()), catalogVersion: String(state.version) } };
      case "ListAccessTokens":
        return { status: 200, body: { tokens: [...state.tokens].reverse() } };
    }
    return undefined;
  }

  const exportDoc = () => ({
    format: "metro-catalog/1",
    cities: state.cities.map((c) => ({ slug: c.slug, name: c.name, header_images: c.headerImages, map_svg_url: c.mapSvgUrl, sort_order: c.sortOrder })),
    rolling_stocks: state.stocks.map((s) => ({ code: s.code, length_m: s.lengthM, width_m: s.widthM, capacity: s.capacity })),
    lines: state.lines.map(lineDoc),
    topics: state.topics.map(topicIn).map((t) => ({ slug: t.slug, name: t.name, rolling_stock_codes: t.rollingStockCodes, cars: t.cars })),
  });
  // A line as the export document writes it (snake_case keys, natural keys for the city and the rolling stock).
  const lineDoc = (l: Line) => ({
    code: l.code, city: l.citySlug, name: l.name, number: l.number, status: "operating", rolling_stock: l.rollingStockCode, cars: l.cars, cars_addition: l.carsAddition,
    stations: l.stations, length_km: l.lengthKm, color: l.color, commenced_on: l.commencedOn, extended_on: l.extendedOn, is_loop: l.isLoop,
  });

  function importDoc(b: Body): Out {
    let doc: { format?: string; cities?: any[]; rolling_stocks?: any[]; lines?: any[]; topics?: any[] };
    try {
      doc = JSON.parse(b.catalogJson);
    } catch {
      return invalid([{ field: "catalog_json", reason: "不是有效的 JSON" }]);
    }
    if (doc.format !== "metro-catalog/1") return invalid([{ field: "format", reason: "必须是 metro-catalog/1" }]);
    const errors: { field: string; reason: string }[] = [];
    const cities = doc.cities ?? [];
    const lines = doc.lines ?? [];
    const topics = doc.topics ?? [];
    const stocks = doc.rolling_stocks ?? [];
    const citySlugs = new Set([...state.cities.map((c) => c.slug), ...cities.map((c) => c.slug)]);
    lines.forEach((l, i) => {
      if (!COLOR.test(String(l.color ?? ""))) errors.push({ field: `lines[${i}].color`, reason: "必须是 #RRGGBB" });
      if (!citySlugs.has(l.city)) errors.push({ field: `lines[${i}].city`, reason: "城市不存在" });
    });
    const count = <T extends { slug?: string; code?: string }>(incoming: T[], current: { slug?: string; code?: string }[]) => {
      const key = (x: { slug?: string; code?: string }) => x.slug ?? x.code;
      const created = incoming.filter((x) => !current.some((c) => key(c) === key(x))).length;
      return { created, updated: 0, unchanged: incoming.length - created };
    };
    const counts = {
      cities: count(cities, state.cities), rollingStocks: count(stocks, state.stocks), lines: count(lines, state.lines), topics: count(topics, state.topics),
    };
    if (b.dryRun) return { status: 200, body: { dryRun: true, ...counts, errors, catalogVersion: String(state.version) } };
    // Atomic: any problem writes nothing.
    if (errors.length) return invalid(errors);
    for (const c of cities) if (!state.cities.some((x) => x.slug === c.slug)) state.cities.push({ id: nextId("c"), revision: 1, slug: c.slug, name: c.name ?? "", headerImages: c.header_images ?? [], mapSvgUrl: c.map_svg_url ?? "", sortOrder: c.sort_order ?? 0 });
    for (const s of stocks) if (!state.stocks.some((x) => x.code === s.code)) state.stocks.push({ id: nextId("s"), revision: 1, code: s.code, lengthM: s.length_m, widthM: s.width_m, capacity: s.capacity });
    for (const l of lines) if (!state.lines.some((x) => x.code === l.code)) state.lines.push({ ...line(nextId("l"), l.code, l.city, l.name ?? "", l.number ?? 0, l.color), rollingStockCode: l.rolling_stock ?? "" });
    for (const t of topics) if (!state.topics.some((x) => x.slug === t.slug)) state.topics.push({ id: nextId("t"), revision: 1, slug: t.slug, name: t.name ?? "", rollingStockCodes: t.rolling_stock_codes ?? [], cars: t.cars ?? [] });
    const changed = counts.cities.created + counts.rollingStocks.created + counts.lines.created + counts.topics.created;
    if (changed) {
      bump();
      log("catalog.import", "catalog", "", null, { changed });
    }
    return { status: 200, body: { dryRun: false, ...counts, errors: [], catalogVersion: String(state.version) } };
  }

  function writes(method: string, b: Body): Out | undefined {
    state.beforeNextWrite?.();
    state.beforeNextWrite = null;
    const revisionOf = <T extends { id: string; revision: number }>(rows: T[], id: string): T | Out => {
      const r = rows.find((x) => x.id === id);
      if (!r) return notFound();
      return BigInt(b.expectedRevision ?? 0) !== BigInt(r.revision) ? conflict(r.revision) : r;
    };
    const isOut = (x: unknown): x is Out => typeof x === "object" && x !== null && "status" in x && "body" in x;
    const done = () => ({ status: 200, body: { catalogVersion: String(state.version) } });
    switch (method) {
      case "CreateCity": {
        if (state.cities.some((c) => c.slug === b.city.slug)) return exists("slug");
        if (!b.city.slug || !b.city.name) return invalid([{ field: b.city.slug ? "name" : "slug", reason: "必填" }]);
        const c: City = { id: nextId("c"), revision: 1, slug: b.city.slug, name: b.city.name, headerImages: b.city.headerImages ?? [], mapSvgUrl: b.city.mapSvgUrl ?? "", sortOrder: b.city.sortOrder ?? 0 };
        state.cities.push(c);
        bump();
        log("city.create", "city", c.id, null, cityIn(c));
        return { status: 200, body: { city: { id: c.id, revision: "1", city: cityIn(c) }, catalogVersion: String(state.version) } };
      }
      case "UpdateCity": {
        const c = revisionOf(state.cities, b.id);
        if (isOut(c)) return c;
        const before = cityIn(c);
        Object.assign(c, { slug: b.city.slug, name: b.city.name, headerImages: b.city.headerImages ?? [], mapSvgUrl: b.city.mapSvgUrl ?? "", sortOrder: b.city.sortOrder ?? 0, revision: c.revision + 1 });
        bump();
        log("city.update", "city", c.id, before, cityIn(c));
        return { status: 200, body: { city: { id: c.id, revision: String(c.revision), city: cityIn(c) }, catalogVersion: String(state.version) } };
      }
      case "DeleteCity": {
        const c = revisionOf(state.cities, b.id);
        if (isOut(c)) return c;
        const n = state.lines.filter((l) => l.citySlug === c.slug).length;
        if (n > 0) return referenced([{ entityType: "line", count: n }]);
        state.cities = state.cities.filter((x) => x !== c);
        bump();
        log("city.delete", "city", c.id, cityIn(c), null);
        return done();
      }
      case "CreateLine": {
        if (state.lines.some((l) => l.code === b.line.code)) return exists("code");
        const bad = checkLine(b.line);
        if (bad) return bad;
        const l: Line = { ...line(nextId("l"), b.line.code, b.line.citySlug, b.line.name ?? "", b.line.number ?? 0, b.line.color), ...b.line, status: b.line.status ?? OPERATING, id: "" };
        l.id = nextId("l");
        state.lines.push(l);
        bump();
        log("line.create", "line", l.id, null, lineIn(l));
        return { status: 200, body: { line: { id: l.id, revision: "1", line: lineIn(l) }, catalogVersion: String(state.version) } };
      }
      case "UpdateLine": {
        const l = revisionOf(state.lines, b.id);
        if (isOut(l)) return l;
        const bad = checkLine(b.line);
        if (bad) return bad;
        const before = lineIn(l);
        Object.assign(l, b.line, { revision: l.revision + 1 });
        bump();
        log("line.update", "line", l.id, before, lineIn(l));
        return { status: 200, body: { line: { id: l.id, revision: String(l.revision), line: lineIn(l) }, catalogVersion: String(state.version) } };
      }
      case "DeleteLine": {
        const l = revisionOf(state.lines, b.id);
        if (isOut(l)) return l;
        state.lines = state.lines.filter((x) => x !== l);
        bump();
        log("line.delete", "line", l.id, lineIn(l), null);
        return done();
      }
      case "CreateRollingStock": {
        if (state.stocks.some((s) => s.code === b.rollingStock.code)) return exists("code");
        if (!b.rollingStock.code) return invalid([{ field: "code", reason: "必填" }]);
        const s: Stock = { id: nextId("s"), revision: 1, ...b.rollingStock };
        state.stocks.push(s);
        bump();
        log("rolling_stock.create", "rolling_stock", s.id, null, stockIn(s));
        return { status: 200, body: { rollingStock: { id: s.id, revision: "1", rollingStock: stockIn(s) }, catalogVersion: String(state.version) } };
      }
      case "UpdateRollingStock": {
        const s = revisionOf(state.stocks, b.id);
        if (isOut(s)) return s;
        const before = stockIn(s);
        Object.assign(s, { code: b.rollingStock.code, lengthM: b.rollingStock.lengthM, widthM: b.rollingStock.widthM, capacity: b.rollingStock.capacity, revision: s.revision + 1 });
        bump();
        log("rolling_stock.update", "rolling_stock", s.id, before, stockIn(s));
        return { status: 200, body: { rollingStock: { id: s.id, revision: String(s.revision), rollingStock: stockIn(s) }, catalogVersion: String(state.version) } };
      }
      case "DeleteRollingStock": {
        const s = revisionOf(state.stocks, b.id);
        if (isOut(s)) return s;
        const refs = [
          { entityType: "line", count: state.lines.filter((l) => l.rollingStockCode === s.code).length },
          { entityType: "topic", count: state.topics.filter((t) => t.rollingStockCodes.includes(s.code)).length },
        ].filter((r) => r.count > 0);
        if (refs.length) return referenced(refs);
        state.stocks = state.stocks.filter((x) => x !== s);
        bump();
        log("rolling_stock.delete", "rolling_stock", s.id, stockIn(s), null);
        return done();
      }
      case "CreateRollingStockTopic": {
        if (state.topics.some((t) => t.slug === b.topic.slug)) return exists("slug");
        if (!(b.topic.rollingStockCodes?.length || b.topic.cars?.length)) return invalid([{ field: "rolling_stock_codes", reason: "车型与编组至少填一项" }]);
        const t: Topic = { id: nextId("t"), revision: 1, slug: b.topic.slug, name: b.topic.name, rollingStockCodes: b.topic.rollingStockCodes ?? [], cars: b.topic.cars ?? [] };
        state.topics.push(t);
        bump();
        log("topic.create", "topic", t.id, null, topicIn(t));
        return { status: 200, body: { topic: { id: t.id, revision: "1", topic: topicIn(t) }, catalogVersion: String(state.version) } };
      }
      case "UpdateRollingStockTopic": {
        const t = revisionOf(state.topics, b.id);
        if (isOut(t)) return t;
        const before = topicIn(t);
        Object.assign(t, { slug: b.topic.slug, name: b.topic.name, rollingStockCodes: b.topic.rollingStockCodes ?? [], cars: b.topic.cars ?? [], revision: t.revision + 1 });
        bump();
        log("topic.update", "topic", t.id, before, topicIn(t));
        return { status: 200, body: { topic: { id: t.id, revision: String(t.revision), topic: topicIn(t) }, catalogVersion: String(state.version) } };
      }
      case "DeleteRollingStockTopic": {
        const t = revisionOf(state.topics, b.id);
        if (isOut(t)) return t;
        state.topics = state.topics.filter((x) => x !== t);
        bump();
        log("topic.delete", "topic", t.id, topicIn(t), null);
        return done();
      }
      case "ImportCatalog":
        return importDoc(b);
      case "CreateAccessToken": {
        const me = accounts.state.current!;
        if (!b.name || String(b.name).length > 64) return invalid([{ field: "name", reason: "1 到 64 个字符" }]);
        if (![7, 30, 90].includes(Number(b.expiresInDays))) return invalid([{ field: "expires_in_days", reason: "只能是 7、30 或 90 天" }]);
        if (state.tokens.filter((t) => !t.revokedAt && new Date(t.expiresAt).getTime() > Date.now()).length >= 10) return failure(400, "failed_precondition", "too many active tokens");
        const secret = `mcp_${"ab12".repeat(16)}`;
        state.secrets.push(secret);
        const now = new Date();
        const t: Token = { id: nextId("tok"), userId: me.id, name: b.name, audience: "metro", scopes: b.scopes?.length ? b.scopes : ["metro:read"], createdAt: now.toISOString(), expiresAt: new Date(now.getTime() + Number(b.expiresInDays) * 864e5).toISOString() };
        state.tokens.push(t);
        return { status: 200, body: { token: t, secret } };
      }
      case "RevokeAccessToken": {
        const t = state.tokens.find((x) => x.id === b.id);
        if (!t) return notFound();
        t.revokedAt = new Date().toISOString();
        return { status: 200, body: {} };
      }
    }
    return undefined;
  }

  // Same reads as the real read-only list: these need no CSRF token.
  const READS = new Set(["ListAdminCities", "ListAdminLines", "ListAdminRollingStocks", "ListAdminRollingStockTopics", "ListAuditLogs", "ExportCatalog", "ListAccessTokens"]);

  function decide(method: string, body: Body, origin: string | undefined, headers: Record<string, string>): Out {
    if (state.perimeterClosed || origin !== CONSOLE_ORIGIN) return failure(403, "permission_denied", "request rejected");
    const me = accounts.state.current;
    if (!me) return failure(401, "unauthenticated", "sign in required");
    if (!me.roles.includes("metro-admin")) return failure(403, "permission_denied", "required role missing");
    if (state.reauthNeeded) return failure(401, "unauthenticated", "sign in again to use this feature");
    if (state.unavailable > 0) {
      state.unavailable -= 1;
      return failure(503, "unavailable", "metro unavailable");
    }
    if (!READS.has(method)) {
      if (!headers["x-csrf-token"]) {
        state.writesWithoutCsrf.push(method);
        return failure(403, "permission_denied", "request rejected");
      }
      return writes(method, body) ?? failure(501, "unimplemented", method);
    }
    return reads(method, body) ?? failure(501, "unimplemented", method);
  }

  async function answer(route: Route) {
    const req = route.request();
    const method = new URL(req.url()).pathname.split("/").pop()!;
    const body = (req.postDataJSON() ?? {}) as Body;
    state.calls.push({ method, body, origin: req.headers()["origin"] });
    const wait = state.delay?.(method, body) ?? 0;
    if (wait > 0) await new Promise((r) => setTimeout(r, wait));
    if (state.absent) return route.fulfill({ status: 404, contentType: "text/plain", body: "404 page not found" });
    const out = decide(method, body, req.headers()["origin"], req.headers());
    return route.fulfill({ status: out.status, contentType: "application/json", body: JSON.stringify(out.body) });
  }

  return {
    state,
    install: async (page: Page) => {
      await page.route("**/api/bff.metro.v1.AdminService/*", answer);
      await page.route("**/api/bff.v1.AccessTokenService/*", answer);
    },
    /** Someone else changes the city behind the page's back. */
    touchCity(slug: string, name?: string) {
      const c = state.cities.find((x) => x.slug === slug)!;
      c.revision += 1;
      c.name = name ?? `${c.name}（已改）`;
      bump();
    },
    removeCity(slug: string) {
      state.cities = state.cities.filter((c) => c.slug !== slug);
      bump();
    },
    calls: (...methods: string[]) => state.calls.filter((c) => methods.includes(c.method)),
    exportDoc,
  };
}

export type FakeMetro = ReturnType<typeof createFakeMetro>;
