import { resolveOzid } from "@/lib/ozid";
import { backendEnabled } from "./transport";

/**
 * Where the product's account pages are. Disabled without a backend (local mode has no account center) and, in
 * a development build that has a backend but no `VITE_OZID_ORIGIN`, with the reason shown instead of a link.
 */
export const ozid = resolveOzid(
  { VITE_OZID_ORIGIN: import.meta.env.VITE_OZID_ORIGIN as string | undefined },
  backendEnabled,
  import.meta.env.BASE_URL,
);
