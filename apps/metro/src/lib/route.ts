/** Routes of the application. Pure so it can be unit-tested. */
export type Route =
  | { name: "home" }
  | { name: "city"; slug: string }
  | { name: "topic"; slug: string }
  | { name: "profile"; handle: string }
  | { name: "me" }
  | { name: "login" }
  | { name: "account" }
  | { name: "notfound" };

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
  const [a, b] = parts;
  if (parts.length === 2 && a === "cities") return { name: "city", slug: b };
  if (parts.length === 2 && a === "topics") return { name: "topic", slug: b };
  if (parts.length === 2 && a === "u") return { name: "profile", handle: b };
  if (parts.length === 1 && a === "me") return { name: "me" };
  if (parts.length === 1 && a === "login") return { name: "login" };
  if (parts.length === 1 && a === "account") return { name: "account" };
  return { name: "notfound" };
}

