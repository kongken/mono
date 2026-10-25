// The fixed paths of the mail landing pages (spec 7.2). Free of DOM types so build tooling can import it.

export const VERIFY_EMAIL_PATH = "/account/verify-email";
export const RESET_PASSWORD_PATH = "/account/reset-password";

export type LandingKind = "verify-email" | "reset-password";

const trimSlash = (s: string) => (s.length > 1 ? s.replace(/\/+$/, "") : s);

/** Which landing page the path is, or null. `base` is the Vite base path the app is served under. */
export function landingKind(pathname: string, base = "/"): LandingKind | null {
  const prefix = trimSlash(base === "" ? "/" : base);
  let path = pathname;
  if (prefix !== "/") {
    if (path !== prefix && !path.startsWith(`${prefix}/`)) return null;
    path = path.slice(prefix.length) || "/";
  }
  path = trimSlash(path);
  if (path === VERIFY_EMAIL_PATH) return "verify-email";
  if (path === RESET_PASSWORD_PATH) return "reset-password";
  return null;
}
