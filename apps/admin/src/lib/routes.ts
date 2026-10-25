// The pages of the admin console. Pure, so node:test can cover it.

/**
 * Matches what a registry entry for the console allows: "/", "/users", "/users/*", "/audit/users" and "/metro/*"
 * (the metro sections; "/metro" itself is allowed by the same entry).
 */
export const RETURN_PATHS_DOC = '["/users", "/users/*", "/audit/users", "/metro/*"]';

/** The sections of the metro catalogue administration, in the order they are offered. */
export const METRO_SECTIONS = ["cities", "lines", "rolling-stocks", "topics", "import", "export", "audit", "tokens"] as const;
export type MetroSection = (typeof METRO_SECTIONS)[number];

export const METRO_TITLES: Record<MetroSection, string> = {
  cities: "城市", lines: "线路", "rolling-stocks": "车型", topics: "专题", import: "导入", export: "导出", audit: "审计日志", tokens: "管理令牌",
};

export const metroPathOf = (section: MetroSection) => `/metro/${section}`;

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export type Route =
  | { name: "home" }
  | { name: "users" }
  | { name: "user"; id: string }
  | { name: "audit-users" }
  | { name: "metro"; section: MetroSection }
  | { name: "not-found" };

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  if (path === "/") return { name: "home" };
  if (path === "/users") return { name: "users" };
  if (path === "/audit/users") return { name: "audit-users" };
  if (path === "/metro") return { name: "metro", section: "cities" };
  const section = /^\/metro\/([^/]+)$/.exec(path)?.[1];
  if (section && (METRO_SECTIONS as readonly string[]).includes(section)) return { name: "metro", section: section as MetroSection };
  const m = /^\/users\/([^/]+)$/.exec(path);
  if (m && UUID.test(m[1]!)) return { name: "user", id: m[1]! };
  return { name: "not-found" };
}

/** Which role opens a route; "" means any signed-in administrator may see it. */
export function roleOf(route: Route): "ozid-admin" | "metro-admin" | "" {
  if (route.name === "metro") return "metro-admin";
  return route.name === "users" || route.name === "user" || route.name === "audit-users" ? "ozid-admin" : "";
}

/**
 * The pathname OzID is asked to return to after sign-in or password confirmation: only a page of
 * the console the BFF registry allows (a registered pair is the BFF's decision, not this
 * function's), and "/" for anything else, so an unknown address never becomes a return target.
 */
export function returnPathOf(pathname: string): string {
  const route = matchRoute(pathname);
  if (route.name === "users") return "/users";
  if (route.name === "user") return `/users/${route.id}`;
  if (route.name === "audit-users") return "/audit/users";
  if (route.name === "metro") return metroPathOf(route.section);
  return "/";
}

/** The query of the users list page, from its filter state; empty values are left out. */
export type UserFilters = { email: string; userId: string; nickname: string; status: string; role: string };

export const EMPTY_FILTERS: UserFilters = { email: "", userId: "", nickname: "", status: "", role: "" };

/** Which lookup a filter state stands for: only one of email, id and nickname may be used at a time. */
export function lookupCount(f: UserFilters): number {
  return [f.email, f.userId, f.nickname].filter((v) => v.trim() !== "").length;
}

/** The filter state of the account audit page; empty values are left out of the query. */
export type AuditFilters = { userId: string; operatorId: string; channel: string; action: string; result: string; mutationId: string; from: string; to: string };

export const EMPTY_AUDIT_FILTERS: AuditFilters = { userId: "", operatorId: "", channel: "", action: "", result: "", mutationId: "", from: "", to: "" };

/** The account ID a link from a detail page carries ("?user=<id>"): a valid ID or nothing, never free text. */
export function userOfSearch(search: string): string {
  const v = new URLSearchParams(search).get("user") ?? "";
  return UUID.test(v) ? v : "";
}

/** Where the audit of one account is: the account's ID is an identifier, not an address or a secret, so it may be in the URL. */
export const auditPathOf = (userId: string) => `/audit/users?user=${userId}`;

const CLASS = /^[a-z0-9][a-z0-9._:-]{0,63}$/;
const KEY = /^[A-Za-z0-9._-]{1,64}$/;

/**
 * What is wrong with a filter, as a sentence for the person, or "" if it can be sent. The server checks the
 * same rules again; this only saves a round trip and never decides anything.
 */
export function auditFilterProblem(f: AuditFilters): string {
  const id = (v: string) => v.trim() === "" || UUID.test(v.trim());
  if (!id(f.userId)) return "账号 ID 的格式不对。";
  if (!id(f.operatorId)) return "操作者 ID 的格式不对。";
  if (f.action.trim() !== "" && !CLASS.test(f.action.trim())) return "操作类型只能是小写字母、数字和 . _ : -。";
  if (f.mutationId.trim() !== "" && !KEY.test(f.mutationId.trim())) return "操作编号只能是字母、数字和 . _ -，最长 64 个字符。";
  if (f.from && f.to && new Date(f.from).getTime() >= new Date(f.to).getTime()) return "开始时间必须早于结束时间。";
  return "";
}
