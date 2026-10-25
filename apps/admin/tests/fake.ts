/**
 * A fake BFF for the console's screens (UI only): the shared account fake for `AuthService`
 * plus an in-memory `UserAdminService` that applies the rules the real one applies where a screen
 * can see them: signed in, the platform role, the exact console Origin, a recent password check,
 * the network perimeter, a rate limit, exact email lookup, literal nickname search, filters and
 * stable paging (50 by default, 100 at most) - and, for the four account changes, the CSRF header, the
 * 15-minute password check, the platform-role and deleting-account protections, the version check and
 * per-operator idempotency receipts. It is a fixture, not a second implementation: the real
 * chain is internal/e2e (TestChainUserAdmin*) and the real-stack browser spec.
 */
import type { Page, Route } from "@playwright/test";
import { createFakeAccounts, installFakeAccounts, type FakeAccounts } from "@kongken/account/testing";

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
    receipts: new Map<string, { hash: string; outcome: Body }>(),
    /** Runs on the server side just before the next account change is decided (another administrator, the CLI). */
    beforeNextChange: null as null | ((users: FakeUser[]) => void),
    /** The next account change is applied and then its response is lost (the connection fails). */
    loseNextResponse: false,
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

  function change(method: string, body: Body, operator: string): { status: number; body: unknown } {
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
    state.receipts.set(receiptKey, { hash, outcome });
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
  };
}

export type FakeAdmin = ReturnType<typeof createFakeAdmin>;

/** The console against the fakes, with the OzID origin and any other host recorded instead of reached. */
export async function setup(page: Page, opts: { roles?: string[]; signedIn?: boolean } = {}) {
  const accounts = createFakeAccounts({
    navigation: {
      ozid: { origin: OZID, paths: ["/security"] },
      admin: { origin: CONSOLE_ORIGIN, paths: ["/users", "/users/*"] },
    },
  });
  await installFakeAccounts(page, accounts);
  const admin = createFakeAdmin(accounts);
  await admin.install(page);
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
  return { accounts, admin, account, visited, stray };
}
