/** Top-level navigation to the configured BFF host (RPC base includes /api). */
export function oauthLoginURL(apiBase: string, appOrigin: string, app: string, provider: string): string {
  const url = new URL("/auth/login", new URL(apiBase, appOrigin));
  url.searchParams.set("app", app);
  url.searchParams.set("provider", provider);
  return url.toString();
}
