import { resolveApiConfig } from "@/lib/backend";
import { parseShareContent, type ShareContent } from "@/lib/share";

// The public share page reads the anonymous HTTP routes only. It deliberately does not
// import the RPC transport: no session, no token and no Authorization header.
export const publicApi = resolveApiConfig({
  VITE_API_BASE_URL: import.meta.env.VITE_API_BASE_URL as string | undefined,
});

export class ShareNotFound extends Error {}

const url = (token: string, tail: string) =>
  publicApi.enabled ? `${publicApi.baseUrl}/share/${token}/${tail}` : "";

export const sharePhotoUrl = (token: string, handle: string, size: "thumb" | "full" = "thumb") =>
  url(token, `photos/${handle}?size=${size}`);

/** Throws ShareNotFound for a link that is unknown or revoked; any other failure is retryable. */
export async function fetchPublicShare(token: string, signal?: AbortSignal): Promise<ShareContent> {
  if (!publicApi.enabled) throw new Error("backend not configured");
  const response = await fetch(url(token, "data"), {
    signal,
    credentials: "omit",
    cache: "no-store",
    referrerPolicy: "no-referrer",
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) throw new ShareNotFound();
  if (!response.ok) throw new Error(`share request failed: ${response.status}`);
  return parseShareContent(await response.json());
}
