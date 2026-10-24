/**
 * The page to return to after signing in. Only a same-origin path is accepted, so the
 * sign-in page can never be used to send someone to another site.
 */
export function safeNext(raw: string | null | undefined): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.includes("\\") || /[\u0000-\u001f]/.test(raw)) return "/";
  const path = raw.split(/[?#]/)[0];
  if (path === "/login" || path === "/account" || path.startsWith("/account/")) return "/";
  return raw;
}

/** The sign-in page address that returns to `next` afterwards. */
export function loginPath(next: string): string {
  const target = safeNext(next);
  return target === "/" ? "/login" : `/login?next=${encodeURIComponent(target)}`;
}
