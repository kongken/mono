/**
 * An in-memory stand-in for the account half of the BFF (`bff.v1.AuthService` and
 * `bff.v1.UserService`) for the Playwright specs of both apps. It speaks the Connect JSON
 * protocol and keeps just enough of the real behaviour to drive the screens: one text for
 * every login failure, neutral answers to mail requests, single-use mailed tokens, a
 * recent-password rule for deletion, a deletion that can fail and be retried, and the
 * CSRF header check on state-changing calls.
 *
 * It is a UI fixture only. The real chain (usersrv, BFF, mail) is covered by
 * `internal/e2e` and, from the browser, by the real-mail specs of the apps.
 */
import type { Page, Route } from "@playwright/test";

export type FakeFailure = { status: number; code: string; message: string };

type FakeAccount = { id: string; email: string; password: string; displayName: string; avatarUrl: string; roles: string[] };

/** The return context bound to a mailed token: what the BFF validated when the mail was requested. */
export type FakeReturn = { appId: string; returnPath: string };

export type FakeMail = { kind: "registration" | "reset"; email: string; token: string; link: string; bound: FakeReturn | null };

type Body = Record<string, any>;
type Reply = { status?: number; body: unknown };

export const failure = (status: number, code: string, message: string): Reply => ({ status, body: { code, message } });
export const FAILURES = {
  unavailable: { status: 503, code: "unavailable", message: "account service unavailable" },
  rateLimited: { status: 429, code: "resource_exhausted", message: "too many requests, try again later" },
  internal: { status: 500, code: "internal", message: "internal error" },
} satisfies Record<string, FakeFailure>;

const b64url = (n: number) => Buffer.from(Array.from({ length: n }, () => Math.floor(Math.random() * 256))).toString("base64url");
const READ_ONLY = new Set(["Me", "ListSessions", "IssueCSRFToken", "GetAccountDeletion", "ResolveNavigation"]);
const PRODUCTS = ["tripmap", "metro"];

/** A registered app of the fake BFF's navigation registry; the same rules as internal/bff/navigation. */
export type FakeNavApp = { origin: string; defaultPath?: string; paths?: string[] };

/** The canonical return pathname rule of the BFF: ASCII letters, digits and -._~/, no //, no dot segments. */
function canonicalPath(p: string) {
  return p.length > 0 && p.length <= 256 && /^\/[A-Za-z0-9._~/-]*$/.test(p) && !p.includes("//") && !p.split("/").some((seg) => seg === "." || seg === "..");
}

function pathAllowed(app: FakeNavApp, p: string) {
  if (!canonicalPath(p)) return false;
  if (p === (app.defaultPath ?? "/")) return true;
  return (app.paths ?? []).some((rule) => {
    if (rule === "/*") return true;
    if (rule.endsWith("/*")) {
      const base = rule.slice(0, -2);
      return p === base || p.startsWith(`${base}/`);
    }
    return p === rule;
  });
}

export function createFakeAccounts(
  options: {
    /** Told whenever the signed-in account changes (sign-in, sign-out, deletion), so an app's fake can follow. */
    onSession?: (account: { id: string; displayName: string; roles: string[] } | null) => void;
    /** The fake's app registry for ResolveNavigation; without it every target is refused. */
    navigation?: Record<string, FakeNavApp>;
  } = {},
) {
  const state = {
    accounts: [] as FakeAccount[],
    current: null as FakeAccount | null,
    authenticatedAt: 0,
    otherSessions: 0,
    mails: [] as FakeMail[],
    /** Mailed tokens: kind, mailbox and whether they were used. */
    tokens: new Map<string, { kind: "registration" | "reset"; email: string; used: boolean; expired: boolean; bound: FakeReturn | null }>(),
    csrfIssued: 0,
    calls: [] as string[],
    /** Bodies of the calls, by method, to assert what was (not) sent. */
    bodies: new Map<string, Body[]>(),
    writesWithoutCsrf: [] as string[],
    /** One-shot failures per method, taken in order. */
    failures: new Map<string, FakeFailure[]>(),
    /** The mail send requests are refused with this until cleared (rate limit, closed sign-ups ...). */
    mailRefusal: null as FakeFailure | null,
    registrationOpen: true,
    /** How many DeleteAccount / RetryAccountDeletion calls still fail with `unavailable` after the account closed. */
    deletionFailures: 0,
    deletion: null as null | { mutationId: string; account: FakeAccount },
    csrfRequired: true,
    nextId: 1,
  };

  const set = (account: FakeAccount | null) => {
    state.current = account;
    options.onSession?.(account ? { id: account.id, displayName: account.displayName, roles: account.roles } : null);
  };
  const userJson = (a: FakeAccount) => ({ id: a.id, displayName: a.displayName, avatarUrl: a.avatarUrl });
  const needUser = () => (state.current ? null : failure(401, "unauthenticated", "sign in required"));
  const policy = (field: string, value: string) =>
    Array.from(value).length < 12 ? failure(400, "invalid_argument", `${field}: must be at least 12 characters`) : null;
  const iso = (ms: number) => new Date(ms).toISOString();
  const completed = (mutationId: string) => ({ deletion: { state: "ACCOUNT_DELETION_STATE_COMPLETED", mutationId, products: PRODUCTS, completedProducts: PRODUCTS } });

  const mail = (kind: FakeMail["kind"], email: string, bound: FakeReturn | null): FakeMail => {
    const token = b64url(32);
    state.tokens.set(token, { kind, email, used: false, expired: false, bound });
    const path = kind === "registration" ? "/account/verify-email" : "/account/reset-password";
    // The link never carries the context: it travels with the challenge, server side.
    const m = { kind, email, token, link: `${path}#token=${token}`, bound };
    state.mails.push(m);
    return m;
  };
  /**
   * The BFF's rule for a mail request: no context, a registered one, or a refusal: "unconfigured"
   * (FAILED_PRECONDITION: a context reached a BFF without a registry) or "refused" (INVALID_ARGUMENT).
   */
  const bind = (b: Body): FakeReturn | null | "unconfigured" | "refused" => {
    const appId = b.appId ?? "";
    const returnPath = b.returnPath ?? "";
    if (!appId && !returnPath) return null;
    const registry = options.navigation;
    if (!registry) return "unconfigured";
    const app = registry[appId];
    const path = returnPath || app?.defaultPath || "/";
    if (!appId || !app || !pathAllowed(app, path)) return "refused";
    return { appId, returnPath: path };
  };
  const refusal = (ctx: ReturnType<typeof bind>) =>
    ctx === "unconfigured"
      ? failure(400, "failed_precondition", "navigation is not configured")
      : ctx === "refused"
        ? failure(400, "invalid_argument", "navigation target not allowed")
        : null;
  /** The BFF's rule on the way out: re-validated now, dropped (never an error) when no longer allowed. */
  const bound = (t: { bound: FakeReturn | null }) => {
    const registry = options.navigation;
    const ctx = t.bound;
    const app = ctx && registry?.[ctx.appId];
    return ctx && app && pathAllowed(app, ctx.returnPath) ? { appId: ctx.appId, returnPath: ctx.returnPath } : {};
  };
  const findAccount = (email: string) => state.accounts.find((a) => a.email === email.trim().toLowerCase());
  const consumeToken = (token: string, kind: FakeMail["kind"]) => {
    const t = state.tokens.get(token);
    if (!t || t.used || t.expired || t.kind !== kind) return null;
    return t;
  };

  const finishDeletion = (d: NonNullable<typeof state.deletion>) => {
    state.accounts = state.accounts.filter((a) => a !== d.account);
    state.deletion = null;
    return { body: completed(d.mutationId) };
  };

  const handlers: Record<string, (b: Body) => Reply> = {
    IssueCSRFToken: () => ({ body: { token: `csrf-${++state.csrfIssued}` } }),

    ResolveNavigation: (b) => {
      const registry = options.navigation;
      if (!registry) return failure(400, "failed_precondition", "navigation is not configured");
      const app = registry[b.appId ?? ""];
      const path = b.returnPath || app?.defaultPath || "/";
      if (!app || !pathAllowed(app, path)) return failure(400, "invalid_argument", "navigation target not allowed");
      const ozid = registry["ozid"];
      return { body: { appId: b.appId, returnPath: path, returnUrl: app.origin + path, ozidOrigin: ozid?.origin ?? "" } };
    },

    RequestRegistration: (b) => {
      if (!state.registrationOpen) return failure(400, "failed_precondition", "registration is closed");
      if (state.mailRefusal) return { status: state.mailRefusal.status, body: state.mailRefusal };
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(b.email ?? "")) return failure(400, "invalid_argument", "email: invalid format");
      // The same answer whether or not the address can register; mail goes out only when it can.
      const ctx = bind(b);
      if (typeof ctx === "string") return refusal(ctx)!;
      if (!findAccount(b.email)) mail("registration", b.email.trim().toLowerCase(), ctx);
      return { body: { message: "ok" } };
    },
    CompleteRegistration: (b) => {
      if (!state.registrationOpen) return failure(400, "failed_precondition", "registration is closed");
      const invalidPassword = policy("password", b.password ?? "");
      if (invalidPassword) return invalidPassword;
      const t = consumeToken(b.token, "registration");
      if (!t || findAccount(t.email)) return failure(400, "failed_precondition", "link is not usable");
      t.used = true;
      state.accounts.push({ id: `user-${state.nextId++}`, email: t.email, password: b.password, displayName: (b.displayName ?? "").trim() || "旅行者", avatarUrl: "", roles: [] });
      // Signs nobody in and leaves the browser's own session alone; only the bound context comes back.
      return { body: bound(t) };
    },
    LoginWithEmail: (b) => {
      const a = findAccount(b.email ?? "");
      if (!a || a.password !== b.password || state.deletion?.account === a) return failure(401, "unauthenticated", "邮箱或密码错误，或账号不可用");
      set(a);
      state.authenticatedAt = Date.now();
      return { body: { user: userJson(a), expiresAt: iso(Date.now() + 7 * 864e5) } };
    },
    RequestPasswordReset: (b) => {
      if (state.mailRefusal) return { status: state.mailRefusal.status, body: state.mailRefusal };
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(b.email ?? "")) return failure(400, "invalid_argument", "email: invalid format");
      const ctx = bind(b);
      if (typeof ctx === "string") return refusal(ctx)!;
      if (findAccount(b.email)) mail("reset", b.email.trim().toLowerCase(), ctx);
      return { body: { message: "ok" } };
    },
    ResetPassword: (b) => {
      const invalidPassword = policy("new_password", b.newPassword ?? "");
      if (invalidPassword) return invalidPassword;
      const t = consumeToken(b.token, "reset");
      const a = t && findAccount(t.email);
      if (!t || !a) return failure(400, "failed_precondition", "link is not usable");
      t.used = true;
      a.password = b.newPassword;
      if (state.current === a) set(null);
      return { body: bound(t) };
    },

    Me: () =>
      needUser() ?? {
        body: {
          user: userJson(state.current!),
          roles: state.current!.roles,
          email: state.current!.email,
          authenticatedAt: iso(state.authenticatedAt),
          sessionExpiresAt: iso(Date.now() + 7 * 864e5),
        },
      },
    ListSessions: () =>
      needUser() ?? {
        body: {
          sessions: [
            { id: "s-current", current: true, createdAt: iso(Date.now() - 36e5), lastUsedAt: iso(Date.now()), expiresAt: iso(Date.now() + 6 * 864e5) },
            ...Array.from({ length: state.otherSessions }, (_, i) => ({ id: `s-other-${i}`, current: false, createdAt: iso(Date.now() - 864e5), lastUsedAt: iso(Date.now() - 36e5), expiresAt: iso(Date.now() + 5 * 864e5) })),
          ],
        },
      },
    Logout: () => (set(null), { body: {} }),
    RevokeSession: (b) => {
      const denied = needUser();
      if (denied) return denied;
      if (String(b.id).startsWith("s-other-") && state.otherSessions > 0) state.otherSessions -= 1;
      return { body: {} };
    },
    Reauthenticate: (b) => {
      const denied = needUser();
      if (denied) return denied;
      if (state.current!.password !== b.password) return failure(401, "unauthenticated", "current password is incorrect");
      state.authenticatedAt = Date.now();
      return { body: { authenticatedAt: iso(state.authenticatedAt) } };
    },
    ChangePassword: (b) => {
      const denied = needUser();
      if (denied) return denied;
      if (state.current!.password !== b.currentPassword) return failure(401, "unauthenticated", "current password is incorrect");
      const invalidPassword = policy("new_password", b.newPassword ?? "");
      if (invalidPassword) return invalidPassword;
      state.current!.password = b.newPassword;
      // Every session of the account stops working, this one included.
      set(null);
      return { body: {} };
    },
    UpdateMyProfile: (b) => {
      const denied = needUser();
      if (denied) return denied;
      const name = String(b.displayName ?? "").trim();
      if (!name || Array.from(name).length > 40) return failure(400, "invalid_argument", "display_name: too long");
      if (b.avatarUrl && !/^https:\/\/[^\s/@]+(\/\S*)?$/.test(b.avatarUrl)) return failure(400, "invalid_argument", "avatar_url: must be an https URL without credentials");
      state.current!.displayName = name;
      state.current!.avatarUrl = b.avatarUrl ?? "";
      return { body: { user: userJson(state.current!) } };
    },

    DeleteAccount: (b) => {
      const resuming = state.deletion && state.deletion.mutationId === b.mutationId;
      if (!resuming) {
        const denied = needUser();
        if (denied) return denied;
        if (b.confirmation !== "DELETE") return failure(400, "invalid_argument", "explicit DELETE confirmation is required");
        if (!/^[A-Za-z0-9._-]{1,64}$/.test(b.mutationId ?? "")) return failure(400, "invalid_argument", "mutation_id must be 1-64 characters of A-Z a-z 0-9 . _ -");
        if (Date.now() - state.authenticatedAt > 15 * 60_000) return failure(400, "failed_precondition", "enter your password again to continue");
        state.deletion = { mutationId: b.mutationId, account: state.current! };
        set(null);
      }
      if (state.deletionFailures > 0) {
        state.deletionFailures -= 1;
        return failure(503, "unavailable", "cleanup incomplete");
      }
      return finishDeletion(state.deletion!);
    },
    GetAccountDeletion: () =>
      state.deletion
        ? { body: { deletion: { state: "ACCOUNT_DELETION_STATE_PENDING", mutationId: state.deletion.mutationId, products: PRODUCTS, completedProducts: [] } } }
        : failure(404, "not_found", "deletion not found"),
    RetryAccountDeletion: () => {
      if (!state.deletion) return failure(404, "not_found", "deletion not found");
      if (state.deletionFailures > 0) {
        state.deletionFailures -= 1;
        return failure(503, "unavailable", "cleanup incomplete");
      }
      return finishDeletion(state.deletion);
    },
  };

  /** Answers an account RPC. `method` is the bare method name; returns undefined for anything else. */
  function handle(method: string, body: Body, headers: Record<string, string>): Reply | undefined {
    const run = handlers[method];
    if (!run) return undefined;
    state.calls.push(method);
    state.bodies.set(method, [...(state.bodies.get(method) ?? []), body]);
    if (!READ_ONLY.has(method) && state.csrfRequired && !headers["x-csrf-token"]) state.writesWithoutCsrf.push(method);
    const queued = state.failures.get(method)?.shift();
    if (queued) return { status: queued.status, body: { code: queued.code, message: queued.message } };
    return run(body);
  }

  return {
    state,
    handle,
    /** Registers a confirmed account without going through the mail. */
    addAccount(a: { email: string; password: string; displayName?: string; roles?: string[] }) {
      const account: FakeAccount = { id: `user-${state.nextId++}`, email: a.email.toLowerCase(), password: a.password, displayName: a.displayName ?? "旅行者", avatarUrl: "", roles: a.roles ?? [] };
      state.accounts.push(account);
      return account;
    },
    /** A signed-in browser: the Cookie session of this account exists. */
    signIn(account: FakeAccount) {
      set(account);
      state.authenticatedAt = Date.now();
    },
    signOut: () => set(null),
    /** Makes the next calls of `method` fail. */
    failNext(method: string, f: FakeFailure, times = 1) {
      state.failures.set(method, [...(state.failures.get(method) ?? []), ...Array.from({ length: times }, () => f)]);
    },
    /** Pretends the last password check on this session is older than 15 minutes. */
    ageAuthentication: () => void (state.authenticatedAt = Date.now() - 16 * 60_000),
    /** The mailed link is past its lifetime. */
    expireToken(token: string) {
      const t = state.tokens.get(token);
      if (t) t.expired = true;
    },
    lastMail: (kind?: FakeMail["kind"]) => [...state.mails].reverse().find((m) => !kind || m.kind === kind),
  };
}

export type FakeAccounts = ReturnType<typeof createFakeAccounts>;

/**
 * Routes `bff.v1.AuthService/*` and `bff.v1.UserService/*` of the page to the fake. Install
 * it after the app's own fake backend so it takes precedence for these services.
 */
export async function installFakeAccounts(page: Page, fake: FakeAccounts) {
  const answer = async (route: Route) => {
    const req = route.request();
    const method = req.url().split("/").pop()!;
    const raw = req.postData();
    const reply = fake.handle(method, raw ? (JSON.parse(raw) as Body) : {}, req.headers());
    if (!reply) return route.fulfill({ status: 501, contentType: "application/json", body: JSON.stringify({ code: "unimplemented", message: method }) });
    return route.fulfill({ status: reply.status ?? 200, contentType: "application/json", body: JSON.stringify(reply.body) });
  };
  await page.route("**/api/bff.v1.AuthService/*", answer);
  await page.route("**/api/bff.v1.UserService/*", answer);
}
