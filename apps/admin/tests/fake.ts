/**
 * A fake BFF for the console's screens (UI only): the shared account fake for `AuthService`
 * plus an in-memory `UserAdminService` (the account audit included: fixed filter patterns, keyset paging, rows added by real changes) that applies the rules the real one applies where a screen
 * can see them: signed in, the platform role, the exact console Origin, a recent password check,
 * the network perimeter, a rate limit, exact email lookup, literal nickname search, filters and
 * stable paging (50 by default, 100 at most) - and, for the four account changes, the CSRF header, the
 * 15-minute password check, the platform-role and deleting-account protections, the version check and
 * per-operator idempotency receipts. It is a fixture, not a second implementation: the real
 * chain is internal/e2e (TestChainUserAdmin*) and the real-stack browser spec.
 */
import type { Page, Route } from "@playwright/test";
import { createFakeAccounts, installFakeAccounts, type FakeAccounts } from "@kongken/account/testing";
import { createFakeMetro } from "./fake-metro";

export const OZID = "https://id.example.com";
export const CONSOLE_ORIGIN = "http://127.0.0.1:4177";

export type FakeUser = {
  id: string;
  email: string;
  displayName: string;
  status: "ACTIVE" | "DISABLED" | "DELETING";
  roles: string[];
  createdAt: string;
  authVersion: number;
};

const CHANGE_METHODS = ["DisableUser", "EnableUser", "GrantMetroAdmin", "RevokeMetroAdmin"];

const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** 125 accounts, newest first by id: three are disabled, two hold the platform role, one is closing. */
export function seedUsers(): FakeUser[] {
  return Array.from({ length: 125 }, (_, i) => {
    const n = i + 1;
    return {
      id: uuid(n),
      email: `user${n}@example.com`,
      displayName: n === 7 ? "100%_literal" : `成员 ${n}`,
      status: n % 40 === 0 ? "DISABLED" : n === 3 ? "DELETING" : "ACTIVE",
      roles: n === 1 || n === 2 ? ["ozid-admin"] : n === 9 ? ["metro-admin"] : [],
      createdAt: new Date(Date.UTC(2026, 0, 1) + n * 3_600_000).toISOString(),
      authVersion: 1,
    };
  });
}

/** One row of the fake account audit, in the shape the BFF's JSON has. */
export type FakeAudit = {
  id: string;
  createdAt: string;
  channel: "WEB" | "CLI" | "SELF" | "SYSTEM";
  operatorUserId: string;
  operatorLabel: string;
  userId: string;
  action: string;
  result: string;
  reason: string;
  requestId: string;
  mutationId: string;
  beforeStatus: string;
  afterStatus: string;
  beforeRoles: string[];
  afterRoles: string[];
  originId: string;
};

/** The administrator of the seeded web rows (the signed-in operator of a spec writes its own rows with its own ID). */
export const SEED_OPERATOR = uuid(9001);

/**
 * 130 rows, newest first: web changes (done, unchanged and refused), a replay that points at its original,
 * command-line operations with a controlled operator name, self-service events and system rows. Every
 * tenth pair shares a timestamp so stable paging is visible.
 */
export function seedAudit(): FakeAudit[] {
  const base = Date.UTC(2026, 5, 1, 12, 0, 0);
  const rows: FakeAudit[] = [];
  const kinds = [
    { channel: "WEB", action: "admin.account.disable", result: "ok", before: "active", after: "disabled", reason: "支持工单 77" },
    { channel: "WEB", action: "admin.account.enable", result: "ok", before: "disabled", after: "active", reason: "支持工单 78" },
    { channel: "WEB", action: "admin.account.disable", result: "unchanged", before: "disabled", after: "disabled", reason: "重复操作" },
    { channel: "WEB", action: "admin.account.disable", result: "refused_target_protected", before: "", after: "", reason: "误操作" },
    { channel: "CLI", action: "role.grant:metro-admin", result: "ok", before: "", after: "", reason: "运维授权" },
    { channel: "SELF", action: "password.change", result: "ok", before: "", after: "", reason: "" },
    { channel: "WEB", action: "admin.users.get", result: "ok", before: "", after: "", reason: "" },
  ] as const;
  for (let i = 0; i < 130; i++) {
    const k = kinds[i % kinds.length]!;
    const n = i + 1;
    const web = k.channel === "WEB";
    rows.push({
      id: uuid(10_000 + n),
      // Oldest first by index; every tenth row shares the timestamp of the previous one.
      createdAt: new Date(base - i * 60_000 + (i % 10 === 9 ? 60_000 : 0)).toISOString(),
      channel: k.channel,
      operatorUserId: web ? SEED_OPERATOR : k.channel === "SELF" ? uuid(((n * 7) % 125) + 1) : "",
      operatorLabel: k.channel === "CLI" ? "ops-cli" : "",
      userId: uuid(((n * 7) % 125) + 1),
      action: k.action,
      result: k.result,
      reason: k.reason,
      requestId: `req-${n}`,
      mutationId: web && k.action.startsWith("admin.account") ? `key-${n}` : "",
      beforeStatus: k.before,
      afterStatus: k.after,
      beforeRoles: [],
      afterRoles: k.channel === "CLI" ? ["metro-admin"] : [],
      originId: "",
    });
  }
  // One replay of row 1's change, with its exact origin; one replay whose origin is no longer kept.
  rows.unshift({ ...rows[0]!, id: uuid(9_999), createdAt: new Date(base + 60_000).toISOString(), result: "replayed", beforeStatus: "", afterStatus: "", originId: rows[0]!.id, requestId: "req-replay" });
  rows.unshift({ ...rows[1]!, id: uuid(9_998), createdAt: new Date(base + 120_000).toISOString(), result: "replayed", beforeStatus: "", afterStatus: "", originId: uuid(1), requestId: "req-purged", mutationId: "key-old" });
  return rows;
}

type Body = Record<string, any>;
type Held = { arrived: Promise<void>; release: () => void };

export function createFakeAdmin(accounts: FakeAccounts, users: FakeUser[] = seedUsers()) {
  const state = {
    users,
    /** Procedures called, in order, with their bodies. */
    calls: [] as { method: string; body: Body; origin: string | undefined }[],
    /** The perimeter refuses every call (network or origin outside the list). */
    perimeterClosed: false,
    /** The password check is older than the admin session limit. */
    reauthNeeded: false,
    /** Calls refused with RESOURCE_EXHAUSTED while > 0. */
    rateLimited: 0,
    /** Calls answered 503 while > 0. */
    unavailable: 0,
    holds: [] as { arrive: () => void; released: Promise<void> }[],
    /** Account changes sent without the CSRF header (must stay empty). */
    writesWithoutCsrf: [] as string[],
    /** Idempotency receipts, per operator: "<operator>|<key>" -> what the first answer said. */
    receipts: new Map<string, { hash: string; outcome: Body; auditId: string }>(),
    /** Runs on the server side just before the next account change is decided (another administrator, the CLI). */
    beforeNextChange: null as null | ((users: FakeUser[]) => void),
    /** The next account change is applied and then its response is lost (the connection fails). */
    loseNextResponse: false,
    /** The account audit, in no particular order; real changes made through the fake add their rows. */
    audit: seedAudit(),
  };

  const json = (route: Route, status: number, body: unknown) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  const out = (u: FakeUser) => ({ userId: u.id, email: u.email, displayName: u.displayName, status: `USER_STATUS_${u.status}`, roles: u.roles, createdAt: u.createdAt, authVersion: String(u.authVersion) });

  function list(b: Body) {
    const email = String(b.email ?? "").trim().toLowerCase();
    const userId = String(b.userId ?? "");
    const nick = String(b.nickname ?? "").toLowerCase();
    if ([email, userId, nick].filter(Boolean).length > 1) return { status: 400, body: { code: "invalid_argument", message: "filter: use at most one of email, user_id and nickname" } };
    const status = String(b.status ?? "").replace("USER_STATUS_", "");
    let rows = [...state.users].sort((a, b2) => (a.createdAt === b2.createdAt ? (a.id < b2.id ? 1 : -1) : a.createdAt < b2.createdAt ? 1 : -1));
    if (email) rows = rows.filter((u) => u.email === email);
    if (userId) rows = rows.filter((u) => u.id === userId);
    if (nick) rows = rows.filter((u) => u.displayName.toLowerCase().includes(nick));
    if (status && status !== "UNSPECIFIED") rows = rows.filter((u) => u.status === status);
    if (b.role) rows = rows.filter((u) => u.roles.includes(b.role));
    const size = Math.min(b.pageSize || 50, 100);
    const start = b.pageToken ? Number(Buffer.from(String(b.pageToken), "base64url").toString()) : 0;
    const page = rows.slice(start, start + size);
    const next = start + size < rows.length ? Buffer.from(String(start + size)).toString("base64url") : "";
    return { status: 200, body: { users: page.map(out), nextPageToken: next } };
  }

  const sortedAudit = () => [...state.audit].sort((a, b) => (a.createdAt === b.createdAt ? (a.id < b.id ? 1 : -1) : a.createdAt < b.createdAt ? 1 : -1));
  const originOut = (id: string) => {
    const o = state.audit.find((x) => x.id === id);
    return o ? { id: o.id, createdAt: o.createdAt, action: o.action, result: o.result, beforeStatus: o.beforeStatus, afterStatus: o.afterStatus, beforeRoles: o.beforeRoles, afterRoles: o.afterRoles } : undefined;
  };

  /** The audit list with the real rules: fixed filter patterns, keyset paging over (time, id), 50 by default and 100 at most. */
  function listAudit(b: Body) {
    const bad = (m: string) => ({ status: 400, body: { code: "invalid_argument", message: m } });
    const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
    if (b.userId && !UUID.test(b.userId)) return bad("user_id: invalid");
    if (b.operatorUserId && !UUID.test(b.operatorUserId)) return bad("operator_user_id: invalid");
    if (b.action && !/^[a-z0-9][a-z0-9._:-]{0,63}$/.test(b.action)) return bad("action: invalid");
    if (b.mutationId && !/^[A-Za-z0-9._-]{1,64}$/.test(b.mutationId)) return bad("mutation_id: invalid");
    const channel = String(b.channel ?? "").replace("AUDIT_CHANNEL_", "");
    let rows = sortedAudit();
    if (b.userId) rows = rows.filter((r) => r.userId === b.userId);
    if (b.operatorUserId) rows = rows.filter((r) => r.operatorUserId === b.operatorUserId);
    if (channel && channel !== "UNSPECIFIED") rows = rows.filter((r) => r.channel === channel);
    if (b.action) rows = rows.filter((r) => r.action === b.action);
    if (b.result) rows = rows.filter((r) => r.result === b.result);
    if (b.mutationId) rows = rows.filter((r) => r.mutationId === b.mutationId);
    if (b.from) rows = rows.filter((r) => r.createdAt >= new Date(b.from).toISOString());
    if (b.to) rows = rows.filter((r) => r.createdAt < new Date(b.to).toISOString());
    const size = Math.min(b.pageSize || 50, 100);
    let start = 0;
    if (b.pageToken) {
      const [at, id] = Buffer.from(String(b.pageToken), "base64url").toString().split("|");
      start = rows.findIndex((r) => r.createdAt < at! || (r.createdAt === at && r.id < id!));
      if (start < 0) start = rows.length;
    }
    const page = rows.slice(start, start + size);
    const last = page[page.length - 1];
    const next = last && start + size < rows.length ? Buffer.from(`${last.createdAt}|${last.id}`).toString("base64url") : "";
    return {
      status: 200,
      body: {
        entries: page.map((r) => ({ ...r, channel: `AUDIT_CHANNEL_${r.channel}`, origin: r.originId ? originOut(r.originId) : undefined })),
        nextPageToken: next,
      },
    };
  }

  async function answer(route: Route) {
    const req = route.request();
    const method = req.url().split("/").pop()!;
    const body = (req.postData() ? JSON.parse(req.postData()!) : {}) as Body;
    state.calls.push({ method, body, origin: req.headers()["origin"] });
    const hold = state.holds.shift();
    // A held call decides from the state at arrival and answers on release.
    const decided = decide(method, body, req.headers()["origin"], req.headers());
    if (hold) {
      hold.arrive();
      await hold.released;
    }
    if (CHANGE_METHODS.includes(method) && state.loseNextResponse && decided.status === 200) {
      state.loseNextResponse = false;
      return route.abort("failed");
    }
    return json(route, decided.status, decided.body);
  }

  const AUDIT_ACTION: Record<string, string> = { DisableUser: "admin.account.disable", EnableUser: "admin.account.enable", GrantMetroAdmin: "admin.role.grant:metro-admin", RevokeMetroAdmin: "admin.role.revoke:metro-admin" };

  /** A change as the real service records it: every decided attempt leaves a row; invalid input does not. */
  function change(method: string, body: Body, operator: string): { status: number; body: unknown } {
    const target = state.users.find((x) => x.id === body.userId);
    const before = target ? { status: target.status.toLowerCase(), roles: [...target.roles] } : null;
    const res = decideChange(method, body, operator);
    const msg = String((res.body as Body)?.message ?? "");
    const code = String((res.body as Body)?.code ?? "");
    let result = "";
    if (res.status === 200) result = (res.body as Body).replayed ? "replayed" : (res.body as Body).changed ? "ok" : "unchanged";
    else if (code === "aborted") result = "refused_version_conflict";
    else if (code === "already_exists") result = "refused_mutation_reuse";
    else if (code === "failed_precondition") result = msg.includes("platform administrators") ? "refused_target_protected" : "refused_not_active";
    if (!result) return res;
    const done = result === "ok";
    const o = res.status === 200 ? (res.body as Body) : null;
    const prior = result === "replayed" ? state.receipts.get(`${operator}|${body.mutationId}`)?.auditId ?? "" : "";
    const id = uuid(20_000 + state.audit.length);
    state.audit.push({
      id,
      createdAt: new Date().toISOString(),
      channel: "WEB",
      operatorUserId: accounts.state.current?.id ?? "",
      operatorLabel: "",
      userId: String(body.userId),
      action: AUDIT_ACTION[method]!,
      result,
      reason: String(body.reason ?? "").trim(),
      requestId: `req-fake-${state.audit.length}`,
      mutationId: String(body.mutationId ?? ""),
      beforeStatus: done || result === "unchanged" ? before?.status ?? "" : "",
      afterStatus: done || result === "unchanged" ? String(o?.status ?? "").replace("USER_STATUS_", "").toLowerCase() : "",
      beforeRoles: done || result === "unchanged" ? before?.roles ?? [] : [],
      afterRoles: done || result === "unchanged" ? ((o?.roles as string[]) ?? []) : [],
      originId: prior,
    });
    if (res.status === 200 && !(res.body as Body).replayed) {
      const r = state.receipts.get(`${operator}|${body.mutationId}`);
      if (r) r.auditId = id;
    }
    return res;
  }

  function decideChange(method: string, body: Body, operator: string): { status: number; body: unknown } {
    state.beforeNextChange?.(state.users);
    state.beforeNextChange = null;
    const refuse = (status: number, code: string, message: string) => ({ status, body: { code, message } });
    const reason = String(body.reason ?? "").trim();
    if (!reason || [...reason].length > 200 || /[^\s@]+@[^\s@]+\.[^\s@]+/.test(reason)) return refuse(400, "invalid_argument", "reason: must be 1-200 characters");
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(String(body.mutationId ?? ""))) return refuse(400, "invalid_argument", "mutation_id: must be 1-64 characters");
    const expected = Number(body.expectedAuthVersion ?? 0);
    if (!(expected >= 1)) return refuse(400, "invalid_argument", "expected_auth_version: must be at least 1");
    const target = state.users.find((x) => x.id === body.userId);
    const receiptKey = `${operator}|${body.mutationId}`;
    const hash = JSON.stringify([method, body.userId, expected, reason]);
    const receipt = state.receipts.get(receiptKey);
    if (receipt) return receipt.hash === hash ? { status: 200, body: { ...receipt.outcome, replayed: true } } : refuse(409, "already_exists", "mutation id used for a different request");
    if (!target) return refuse(404, "not_found", "account not found");
    if (target.roles.includes("ozid-admin")) return refuse(400, "failed_precondition", "platform administrators cannot be changed here");
    if (target.authVersion !== expected) return refuse(409, "aborted", "account changed since it was read");
    if (target.status === "DELETING") return refuse(400, "failed_precondition", "account is not active");
    let changed = false;
    if (method === "DisableUser" && target.status === "ACTIVE") (target.status = "DISABLED"), (changed = true);
    if (method === "EnableUser" && target.status === "DISABLED") (target.status = "ACTIVE"), (changed = true);
    if (method === "GrantMetroAdmin") {
      if (target.status !== "ACTIVE") return refuse(400, "failed_precondition", "account is not active");
      if (!target.roles.includes("metro-admin")) (target.roles = [...target.roles, "metro-admin"].sort()), (changed = true);
    }
    if (method === "RevokeMetroAdmin" && target.roles.includes("metro-admin")) (target.roles = target.roles.filter((r) => r !== "metro-admin")), (changed = true);
    if (changed) target.authVersion += 1;
    const outcome = { userId: target.id, status: `USER_STATUS_${target.status}`, roles: target.roles, authVersion: String(target.authVersion), changed, replayed: false };
    state.receipts.set(receiptKey, { hash, outcome, auditId: "" });
    return { status: 200, body: outcome };
  }

  function decide(method: string, body: Body, origin: string | undefined, headers: Record<string, string> = {}): { status: number; body: unknown } {
    if (state.perimeterClosed || origin !== CONSOLE_ORIGIN) return { status: 403, body: { code: "permission_denied", message: "request rejected" } };
    const me = accounts.state.current;
    if (!me) return { status: 401, body: { code: "unauthenticated", message: "sign in required" } };
    if (!me.roles.includes("ozid-admin")) return { status: 403, body: { code: "permission_denied", message: "required role missing" } };
    if (state.reauthNeeded) return { status: 401, body: { code: "unauthenticated", message: "sign in again to use this feature" } };
    if (state.rateLimited > 0) {
      state.rateLimited -= 1;
      return { status: 429, body: { code: "resource_exhausted", message: "too many requests, try again later" } };
    }
    if (state.unavailable > 0) {
      state.unavailable -= 1;
      return { status: 503, body: { code: "unavailable", message: "account service unavailable" } };
    }
    if (CHANGE_METHODS.includes(method)) {
      if (!headers["x-csrf-token"]) {
        state.writesWithoutCsrf.push(method);
        return { status: 403, body: { code: "permission_denied", message: "request rejected" } };
      }
      if (Date.now() - accounts.state.authenticatedAt > 15 * 60_000)
        return { status: 400, body: { code: "failed_precondition", message: "enter your password again to continue" } };
      return change(method, body, me.email);
    }
    if (method === "ListUsers") return list(body);
    if (method === "ListSecurityAudit") return listAudit(body);
    if (method === "GetUser") {
      const u = state.users.find((x) => x.id === body.userId);
      return u ? { status: 200, body: { user: out(u) } } : { status: 404, body: { code: "not_found", message: "account not found" } };
    }
    return { status: 501, body: { code: "unimplemented", message: method } };
  }

  return {
    state,
    install: (page: Page) => page.route("**/api/bff.v1.UserAdminService/*", answer),
    /** The next call's answer is decided on arrival and delivered on `release()`. */
    holdNext(): Held {
      let release!: () => void;
      let arrive!: () => void;
      const released = new Promise<void>((r) => (release = r));
      const arrived = new Promise<void>((r) => (arrive = r));
      state.holds.push({ arrive, released });
      return { arrived, release };
    },
    userCalls: () => state.calls.filter((c) => c.method === "ListUsers" || c.method === "GetUser"),
    auditCalls: () => state.calls.filter((c) => c.method === "ListSecurityAudit"),
  };
}

export type FakeAdmin = ReturnType<typeof createFakeAdmin>;

/** The console against the fakes, with the OzID origin and any other host recorded instead of reached. */
export async function setup(page: Page, opts: { roles?: string[]; signedIn?: boolean; metro?: "present" | "absent" } = {}) {
  const accounts = createFakeAccounts({
    navigation: {
      ozid: { origin: OZID, paths: ["/security"] },
      admin: { origin: CONSOLE_ORIGIN, paths: ["/users", "/users/*", "/audit/users", "/metro/*"] },
    },
  });
  await installFakeAccounts(page, accounts);
  const admin = createFakeAdmin(accounts);
  await admin.install(page);
  const metro = createFakeMetro(accounts);
  metro.state.absent = opts.metro === "absent";
  await metro.install(page);
  const account = accounts.addAccount({ email: "operator@example.com", password: "correct horse battery", displayName: "运维小李", roles: opts.roles ?? [] });
  if (opts.signedIn !== false) accounts.signIn(account);
  const visited: string[] = [];
  await page.route(`${OZID}/**`, (route) => {
    visited.push(route.request().url());
    return route.fulfill({ status: 200, contentType: "text/html", body: "<title>OzID stand-in</title><p>OzID</p>" });
  });
  const stray: string[] = [];
  await page.route((url) => url.hostname.endsWith("evil.example"), (route) => {
    stray.push(route.request().url());
    return route.abort();
  });
  return { accounts, admin, metro, account, visited, stray };
}
