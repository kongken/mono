/** Routes of the application. Pure so it can be unit-tested. */
export type Route =
  | { name: "home" }
  | { name: "city"; slug: string }
  | { name: "topic"; slug: string }
  | { name: "profile"; handle: string }
  | { name: "me" }
  | { name: "admin"; section: string }
  | { name: "notfound" };

export const adminSections = ["cities", "lines", "rolling-stocks", "topics", "import", "export", "audit", "tokens"] as const;
export type AdminSection = (typeof adminSections)[number];

export function matchRoute(pathname: string): Route {
  const parts = pathname.split("/").filter(Boolean).map((p) => {
    try {
      return decodeURIComponent(p);
    } catch {
      return "";
    }
  });
  if (parts.some((p) => p === "")) return parts.length === 0 ? { name: "home" } : { name: "notfound" };
  if (parts.length === 0) return { name: "home" };
  const [a, b, c] = parts;
  if (parts.length === 2 && a === "cities") return { name: "city", slug: b };
  if (parts.length === 2 && a === "topics") return { name: "topic", slug: b };
  if (parts.length === 2 && a === "u") return { name: "profile", handle: b };
  if (parts.length === 1 && a === "me") return { name: "me" };
  if (a === "admin" && parts.length <= 2 && c === undefined) {
    if (b === undefined) return { name: "admin", section: "cities" };
    if ((adminSections as readonly string[]).includes(b)) return { name: "admin", section: b };
  }
  return { name: "notfound" };
}

/** Paths the BFF accepts as a login return target (auth.oauth_apps.metro.allowed_paths). */
export const loginReturnPaths = ["/", "/me", "/admin"];
