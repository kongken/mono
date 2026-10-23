import type { Page, Route } from "@playwright/test";
import { create, toBinary } from "@bufbuild/protobuf";
import { RevisionConflictDetailSchema, ValidationDetailSchema } from "@kongken/api-client/bff/metro/v1/admin_pb";

type Role = "user" | "admin";
type City = { id: string; revision: number; slug: string; name: string; headerImages: string[]; mapSvgUrl: string; sortOrder: number };
type Line = { id: string; revision: number; code: string; citySlug: string; name: string; number: number; status: string; rollingStockCode: string; cars: number; carsAddition: number; stations: number; lengthKm: number; color: string; commencedOn: string; extendedOn: string; isLoop: boolean };
type Token = { id: string; userId: string; name: string; audience: string; scopes: string[]; createdAt: string; expiresAt: string; lastUsedAt?: string; revokedAt?: string };

const OPERATING = "LINE_STATUS_OPERATING";

const b64 = (bytes: Uint8Array) => Buffer.from(bytes).toString("base64");

/** Connect error with typed details, as the BFF relays them. */
function failure(status: number, code: string, message: string, details: { type: string; value: Uint8Array }[] = []) {
  return { status, body: { code, message, details: details.map((d) => ({ type: d.type, value: b64(d.value) })) } };
}

/**
 * A small in-memory BFF for the metro application: enough of the catalogue,
 * rider, admin and token contracts (revision checks, dry runs, CSRF, roles) to
 * drive the screens. It is installed with page.route; nothing leaves the test.
 */
export function fakeBackend(page: Page, opts: { signedIn?: Role | false } = {}) {
  const state = {
    user: opts.signedIn ? { id: "u1", username: "rider", displayName: "骑行者", roles: opts.signedIn === "admin" ? ["metro-admin"] : [] } : null as null | { id: string; username: string; displayName: string; roles: string[] },
    version: 1,
    seq: 10,
    cities: [{ id: "c1", revision: 1, slug: "shanghai", name: "上海", headerImages: [], mapSvgUrl: "", sortOrder: 0 }] as City[],
    lines: [
      line("l1", "sh-1", "shanghai", "1号线", 1, "#E4002B", 28, 36.9),
      line("l2", "sh-2", "shanghai", "2号线", 2, "#97D700", 30, 64.0),
      { ...line("l3", "sh-17", "shanghai", "17号线", 17, "#BC79A9", 13, 35.3), status: "LINE_STATUS_PLANNED" },
    ] as Line[],
    topics: [{ id: "t1", revision: 1, slug: "six-cars", name: "6 节编组", rollingStockCodes: [] as string[], cars: [6] }],
    stocks: [] as { id: string; revision: number; code: string }[],
    rides: new Map<string, number>(),
    profile: { handle: "", displayName: "", subtitle: "", bio: "", location: "", headerImages: [] as string[], twitter: "", instagram: "", weibo: "", facebook: "", github: "", isPublic: false },
    tokens: [] as Token[],
    csrfIssued: 0,
    /** Procedures that were called with a CSRF header, to check writes carry one. */
    writesWithoutCsrf: [] as string[],
    loginRequests: [] as string[],
    calls: [] as string[],
    secrets: [] as string[],
  };

  function line(id: string, code: string, citySlug: string, name: string, number: number, color: string, stations: number, km: number): Line {
    return { id, revision: 1, code, citySlug, name, number, status: OPERATING, rollingStockCode: "02A02", cars: 6, carsAddition: 0, stations, lengthKm: km, color, commencedOn: "1993-05-28", extendedOn: "", isLoop: false };
  }

  const operating = (l: Line) => l.status === OPERATING;
  const cityJson = (c: City) => ({
    slug: c.slug, name: c.name, headerImages: c.headerImages, mapSvgUrl: c.mapSvgUrl, sortOrder: c.sortOrder,
    lineCount: state.lines.filter((l) => l.citySlug === c.slug && operating(l)).length,
    totalLengthKm: state.lines.filter((l) => l.citySlug === c.slug && operating(l)).reduce((s, l) => s + l.lengthKm, 0),
  });
  const lineJson = (l: Line) => ({ ...l, cityName: state.cities.find((c) => c.slug === l.citySlug)?.name ?? "" });
  const lineInput = (l: Line) => { const { id: _i, revision: _r, ...rest } = l; return rest; };
  const needAdmin = () => !state.user ? failure(401, "unauthenticated", "sign in required") : !state.user.roles.includes("metro-admin") ? failure(403, "permission_denied", "required role missing") : null;
  const needUser = () => (state.user ? null : failure(401, "unauthenticated", "sign in required"));
  const bump = () => { state.version += 1; };
  const nextId = (p: string) => `${p}${++state.seq}`;
  const conflict = (current: number) => failure(400, "failed_precondition", "stale revision", [
    { type: "bff.metro.v1.RevisionConflictDetail", value: toBinary(RevisionConflictDetailSchema, create(RevisionConflictDetailSchema, { currentRevision: BigInt(current) })) },
  ]);
  const invalid = (errors: { field: string; reason: string }[]) => failure(400, "invalid_argument", "invalid", [
    { type: "bff.metro.v1.ValidationDetail", value: toBinary(ValidationDetailSchema, create(ValidationDetailSchema, { errors })) },
  ]);
  const colorOk = (c: string) => /^#[0-9A-Fa-f]{6}$/.test(c);

  type Body = Record<string, any>;
  const handlers: Record<string, (b: Body) => { status?: number; body: unknown }> = {
    "bff.v1.AuthService/Me": () => needUser() ?? { body: { user: { id: state.user!.id, username: state.user!.username, displayName: state.user!.displayName }, roles: state.user!.roles } },
    "bff.v1.AuthService/IssueCSRFToken": () => ({ body: { token: `csrf-${++state.csrfIssued}` } }),
    "bff.v1.AuthService/Logout": () => { state.user = null; return { body: {} }; },
    "bff.v1.AuthService/DeleteAccount": () => needUser() ?? { body: {} },

    "bff.metro.v1.CatalogService/ListCities": (b) =>
      b.knownVersion && BigInt(b.knownVersion) === BigInt(state.version)
        ? { body: { version: String(state.version), notModified: true } }
        : { body: { version: String(state.version), cities: [...state.cities].sort((a, c) => a.sortOrder - c.sortOrder).map(cityJson) } },
    "bff.metro.v1.CatalogService/GetCity": (b) => {
      const c = state.cities.find((x) => x.slug === b.slug);
      if (!c) return failure(404, "not_found", "city not found");
      const ls = state.lines.filter((l) => l.citySlug === c.slug && operating(l)).sort((a, z) => (a.number || 1e9) - (z.number || 1e9));
      return { body: { version: String(state.version), city: cityJson(c), lines: ls.map(lineJson), totalLines: ls.length, totalLengthKm: ls.reduce((s, l) => s + l.lengthKm, 0) } };
    },
    "bff.metro.v1.CatalogService/ListRollingStockTopics": () => ({ body: { version: String(state.version), topics: state.topics } }),
    "bff.metro.v1.CatalogService/GetRollingStockTopic": (b) => {
      const t = state.topics.find((x) => x.slug === b.slug);
      if (!t) return failure(404, "not_found", "topic not found");
      const ls = state.lines.filter((l) => operating(l) && (t.cars.includes(l.cars) || t.rollingStockCodes.includes(l.rollingStockCode)));
      const byCity = [...new Set(ls.map((l) => l.citySlug))].map((slug) => {
        const mine = ls.filter((l) => l.citySlug === slug);
        return { citySlug: slug, cityName: state.cities.find((c) => c.slug === slug)?.name ?? slug, lineCount: mine.length, totalLengthKm: mine.reduce((s, l) => s + l.lengthKm, 0) };
      });
      return { body: { version: String(state.version), topic: t, lines: ls.map(lineJson), cities: byCity, totalLines: ls.length, totalLengthKm: ls.reduce((s, l) => s + l.lengthKm, 0) } };
    },

    "bff.metro.v1.RiderService/ListMyLines": () => needUser() ?? { body: ridden() },
    "bff.metro.v1.RiderService/MarkLine": (b) => {
      const denied = needUser();
      if (denied) return denied;
      const l = state.lines.find((x) => x.code === b.lineCode);
      if (!l) return failure(404, "not_found", "line not found");
      state.rides.set(l.code, b.riddenYear ?? 0);
      return { body: { line: { lineCode: l.code, lineName: l.name, color: l.color, status: l.status, lengthKm: l.lengthKm, riddenYear: b.riddenYear ?? 0 } } };
    },
    "bff.metro.v1.RiderService/UnmarkLine": (b) => needUser() ?? (state.rides.delete(b.lineCode), { body: {} }),
    "bff.metro.v1.RiderService/GetMyProfile": () => needUser() ?? { body: { profile: state.profile, exists: Boolean(state.profile.handle) } },
    "bff.metro.v1.RiderService/UpdateMyProfile": (b) => {
      const denied = needUser();
      if (denied) return denied;
      if (b.profile.isPublic && !b.profile.handle) return invalid([{ field: "handle", reason: "公开主页需要 handle" }]);
      state.profile = { ...state.profile, ...b.profile };
      return { body: { profile: state.profile } };
    },
    "bff.metro.v1.RiderService/GetPublicProfile": (b) =>
      state.profile.isPublic && state.profile.handle === b.handle
        ? { body: { profile: { ...state.profile }, cities: ridden().cities, stats: ridden().stats } }
        : failure(404, "not_found", "profile not found"),

    "bff.metro.v1.AdminService/ListAdminCities": (b) => needAdmin() ?? { body: { cities: state.cities.filter((c) => !b.query || c.slug.includes(b.query) || c.name.includes(b.query)).map((c) => ({ id: c.id, revision: String(c.revision), city: { slug: c.slug, name: c.name, headerImages: c.headerImages, mapSvgUrl: c.mapSvgUrl, sortOrder: c.sortOrder } })), nextPageToken: "", total: String(state.cities.length) } },
    "bff.metro.v1.AdminService/CreateCity": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      if (state.cities.some((c) => c.slug === b.city.slug)) return failure(409, "already_exists", "slug exists", [{ type: "bff.metro.v1.ValidationDetail", value: toBinary(ValidationDetailSchema, create(ValidationDetailSchema, { errors: [{ field: "slug", reason: "已存在" }] })) }]);
      const c: City = { id: nextId("c"), revision: 1, slug: b.city.slug, name: b.city.name, headerImages: b.city.headerImages ?? [], mapSvgUrl: b.city.mapSvgUrl ?? "", sortOrder: b.city.sortOrder ?? 0 };
      state.cities.push(c);
      bump();
      return { body: { city: { id: c.id, revision: "1", city: b.city }, catalogVersion: String(state.version) } };
    },
    "bff.metro.v1.AdminService/UpdateCity": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      const c = state.cities.find((x) => x.id === b.id);
      if (!c) return failure(404, "not_found", "city not found");
      if (BigInt(b.expectedRevision ?? 0) !== BigInt(c.revision)) return conflict(c.revision);
      Object.assign(c, { slug: b.city.slug, name: b.city.name, headerImages: b.city.headerImages ?? [], mapSvgUrl: b.city.mapSvgUrl ?? "", sortOrder: b.city.sortOrder ?? 0, revision: c.revision + 1 });
      bump();
      return { body: { city: { id: c.id, revision: String(c.revision), city: b.city }, catalogVersion: String(state.version) } };
    },
    "bff.metro.v1.AdminService/DeleteCity": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      state.cities = state.cities.filter((c) => c.id !== b.id);
      bump();
      return { body: { catalogVersion: String(state.version) } };
    },
    "bff.metro.v1.AdminService/ListAdminLines": () => needAdmin() ?? { body: { lines: state.lines.map((l) => ({ id: l.id, revision: String(l.revision), line: lineInput(l) })), nextPageToken: "", total: String(state.lines.length) } },
    "bff.metro.v1.AdminService/CreateLine": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      if (!colorOk(b.line.color)) return invalid([{ field: "color", reason: "必须是 #RRGGBB" }]);
      const l: Line = { id: nextId("l"), revision: 1, status: OPERATING, rollingStockCode: "", cars: 0, carsAddition: 0, stations: 0, lengthKm: 0, commencedOn: "", extendedOn: "", isLoop: false, number: 0, name: "", code: "", citySlug: "", color: "", ...b.line };
      state.lines.push(l);
      bump();
      return { body: { line: { id: l.id, revision: "1", line: b.line }, catalogVersion: String(state.version) } };
    },
    "bff.metro.v1.AdminService/ListAdminRollingStocks": () => needAdmin() ?? { body: { rollingStocks: [], nextPageToken: "", total: "0" } },
    "bff.metro.v1.AdminService/ListAdminRollingStockTopics": () => needAdmin() ?? { body: { topics: state.topics.map((t) => ({ id: t.id, revision: String(t.revision), topic: t })), nextPageToken: "", total: String(state.topics.length) } },
    "bff.metro.v1.AdminService/ListAuditLogs": () => needAdmin() ?? { body: { logs: [], nextPageToken: "" } },
    "bff.metro.v1.AdminService/ExportCatalog": () => needAdmin() ?? { body: { catalogJson: JSON.stringify({ format: "metro-catalog/1", cities: state.cities.map((c) => ({ slug: c.slug, name: c.name })) }), catalogVersion: String(state.version) } },
    "bff.metro.v1.AdminService/ImportCatalog": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      let doc: { cities?: { slug: string; name: string }[]; lines?: { color: string }[] };
      try {
        doc = JSON.parse(b.catalogJson);
      } catch {
        return invalid([{ field: "catalog_json", reason: "不是有效的 JSON" }]);
      }
      const errors = (doc.lines ?? []).flatMap((l, i) => (colorOk(l.color) ? [] : [{ field: `lines[${i}].color`, reason: "必须是 #RRGGBB" }]));
      const fresh = (doc.cities ?? []).filter((c) => !state.cities.some((x) => x.slug === c.slug));
      const counts = { cities: { created: fresh.length, updated: 0, unchanged: (doc.cities?.length ?? 0) - fresh.length }, rollingStocks: {}, lines: { created: doc.lines?.length ?? 0 }, topics: {} };
      if (b.dryRun) return { body: { dryRun: true, ...counts, errors, catalogVersion: String(state.version) } };
      if (errors.length) return invalid(errors);
      for (const c of fresh) state.cities.push({ id: nextId("c"), revision: 1, slug: c.slug, name: c.name, headerImages: [], mapSvgUrl: "", sortOrder: 0 });
      if (fresh.length) bump();
      return { body: { dryRun: false, ...counts, errors: [], catalogVersion: String(state.version) } };
    },

    "bff.v1.AccessTokenService/CreateAccessToken": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      const secret = `mcp_${"ab12".repeat(16)}`;
      state.secrets.push(secret);
      const now = new Date();
      const t: Token = { id: nextId("tok"), userId: state.user!.id, name: b.name, audience: "metro", scopes: b.scopes?.length ? b.scopes : ["metro:read"], createdAt: now.toISOString(), expiresAt: new Date(now.getTime() + b.expiresInDays * 864e5).toISOString() };
      state.tokens.push(t);
      return { body: { token: t, secret } };
    },
    "bff.v1.AccessTokenService/ListAccessTokens": () => needAdmin() ?? { body: { tokens: [...state.tokens].reverse() } },
    "bff.v1.AccessTokenService/RevokeAccessToken": (b) => {
      const denied = needAdmin();
      if (denied) return denied;
      const t = state.tokens.find((x) => x.id === b.id);
      if (t) t.revokedAt = new Date().toISOString();
      return { body: {} };
    },
  };

  const readOnly = new Set(["Me", "IssueCSRFToken", "ListCities", "GetCity", "ListRollingStockTopics", "GetRollingStockTopic", "GetPublicProfile", "GetMyProfile", "ListMyLines", "ListAdminCities", "ListAdminLines", "ListAdminRollingStocks", "ListAdminRollingStockTopics", "ListAuditLogs", "ExportCatalog", "ListAccessTokens"]);

  function ridden() {
    const byCity = new Map<string, { citySlug: string; cityName: string; lines: object[] }>();
    let km = 0;
    for (const [code, year] of state.rides) {
      const l = state.lines.find((x) => x.code === code)!;
      const c = state.cities.find((x) => x.slug === l.citySlug)!;
      const g = byCity.get(c.slug) ?? { citySlug: c.slug, cityName: c.name, lines: [] };
      g.lines.push({ lineCode: l.code, lineName: l.name, color: l.color, status: l.status, lengthKm: l.lengthKm, riddenYear: year });
      byCity.set(c.slug, g);
      km += l.lengthKm;
    }
    return { cities: [...byCity.values()], stats: { cityCount: byCity.size, lineCount: state.rides.size, totalLengthKm: km } };
  }

  async function handle(route: Route) {
    const req = route.request();
    const url = new URL(req.url());
    const match = /^\/api\/([\w.]+)\/(\w+)$/.exec(url.pathname);
    if (!match) return route.fulfill({ status: 404, body: "not found" });
    const [, service, method] = match;
    const key = `${service}/${method}`;
    state.calls.push(key);
    if (!readOnly.has(method) && !req.headers()["x-csrf-token"]) state.writesWithoutCsrf.push(key);
    const h = handlers[key];
    if (!h) return route.fulfill({ status: 501, contentType: "application/json", body: JSON.stringify({ code: "unimplemented", message: key }) });
    const out = h((req.postDataJSON() ?? {}) as Body);
    return route.fulfill({ status: out.status ?? 200, contentType: "application/json", body: JSON.stringify(out.body) });
  }

  const install = async () => {
    await page.route("**/api/**", handle);
    // The login redirect: the BFF would send the browser to Logto and back; here it signs in at once.
    await page.route("**/auth/login**", (route) => {
      state.loginRequests.push(route.request().url());
      state.user ??= { id: "u1", username: "rider", displayName: "骑行者", roles: [] };
      return route.fulfill({ status: 303, headers: { location: "/" } });
    });
  };

  return {
    state,
    install,
    /** Someone else edits the city behind the page's back. */
    touchCity(slug: string) {
      const c = state.cities.find((x) => x.slug === slug)!;
      c.revision += 1;
      c.name = `${c.name}（已改）`;
      bump();
    },
    signInAs(role: Role) {
      state.user = { id: "u1", username: "rider", displayName: "骑行者", roles: role === "admin" ? ["metro-admin"] : [] };
    },
  };
}

export type Fake = ReturnType<typeof fakeBackend>;
