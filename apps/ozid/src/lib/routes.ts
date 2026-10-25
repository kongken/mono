// The pages of OzID. Pure, so node:test can cover it.

export type Route = "overview" | "login" | "security" | "not-found";

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  switch (path) {
    case "/":
      return "overview";
    case "/login":
      return "login";
    case "/security":
      return "security";
    default:
      return "not-found";
  }
}
