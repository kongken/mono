// The pages of the admin console. Pure, so node:test can cover it.

/** Matches what a registry entry for the console allows: "/", "/users" and "/users/*". */
export const RETURN_PATHS_DOC = '["/users", "/users/*"]';

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export type Route =
  | { name: "home" }
  | { name: "users" }
  | { name: "user"; id: string }
  | { name: "not-found" };

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  if (path === "/") return { name: "home" };
  if (path === "/users") return { name: "users" };
  const m = /^\/users\/([^/]+)$/.exec(path);
  if (m && UUID.test(m[1]!)) return { name: "user", id: m[1]! };
  return { name: "not-found" };
}

/** Which role opens a route; "" means any signed-in administrator may see it. */
export function roleOf(route: Route): "ozid-admin" | "" {
  return route.name === "users" || route.name === "user" ? "ozid-admin" : "";
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
  return "/";
}

/** The query of the users list page, from its filter state; empty values are left out. */
export type UserFilters = { email: string; userId: string; nickname: string; status: string; role: string };

export const EMPTY_FILTERS: UserFilters = { email: "", userId: "", nickname: "", status: "", role: "" };

/** Which lookup a filter state stands for: only one of email, id and nickname may be used at a time. */
export function lookupCount(f: UserFilters): number {
  return [f.email, f.userId, f.nickname].filter((v) => v.trim() !== "").length;
}
