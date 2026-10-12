export type ApiConfig =
  | { enabled: false }
  | { enabled: true; baseUrl: string };

type ApiEnv = { VITE_API_BASE_URL?: string };

/**
 * Resolve the optional backend address from build-time env. Without a valid
 * value the app runs in local-only mode and must not issue backend requests.
 * Accepts a same-origin path such as `/api` or an absolute http(s) URL.
 */
export function resolveApiConfig(env: ApiEnv = {}): ApiConfig {
  const raw = env.VITE_API_BASE_URL?.trim().replace(/\/+$/, "");
  if (!raw) return { enabled: false };
  if (raw.startsWith("/") && !raw.startsWith("//"))
    return { enabled: true, baseUrl: raw };
  try {
    const url = new URL(raw);
    if (url.protocol === "http:" || url.protocol === "https:")
      return { enabled: true, baseUrl: raw };
  } catch {
    // Fall through: an invalid address keeps the app in local-only mode.
  }
  return { enabled: false };
}
