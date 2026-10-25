// The pages of OzID. Pure, so node:test can cover it.

export type Route = "overview" | "login" | "register" | "forgot-password" | "security" | "not-found";

export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  switch (path) {
    case "/":
      return "overview";
    case "/login":
      return "login";
    case "/register":
      return "register";
    case "/forgot-password":
      return "forgot-password";
    case "/security":
      return "security";
    default:
      return "not-found";
  }
}

/**
 * The page that serves a form of the account flow. The query (app ID and return path)
 * travels along unchanged, so switching between sign-in, registration and password reset
 * keeps where the user came from.
 */
export function pageOf(mode: "login" | "register" | "forgot", search: string): string {
  return `${mode === "login" ? "/login" : mode === "register" ? "/register" : "/forgot-password"}${search}`;
}
