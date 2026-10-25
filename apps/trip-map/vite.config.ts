import { parseApiBase, parseOrigin } from "@kongken/account/navigation";
import { accountLandingHeaders } from "@kongken/account/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, loadEnv } from "vite";

// Dev-server proxy target, the BFF (make dev); override when it listens elsewhere.
const backend = process.env.TRIPMAP_DEV_BACKEND ?? "http://127.0.0.1:18081";

const proxy = {
  "/api": backend,
  "/ping": backend,
  "/readyz": backend,
};

// A build that talks to a backend must name both addresses. The session Cookie belongs to the API host, so
// the browser calls the canonical BFF origin directly (no same-origin `/api` proxy can share that session),
// and every account page lives in OzID, so its origin is needed to send anybody there. Both are public build
// settings; a missing or malformed one fails the build instead of shipping a product that cannot sign anybody
// in. Without VITE_API_BASE_URL the build is the standalone, local-only site and needs neither.
// https://vite.dev/config/
export default defineConfig(({ command, mode }) => {
  if (command === "build") {
    const env = loadEnv(mode, process.cwd(), "VITE_");
    if ((env.VITE_API_BASE_URL ?? "").trim() !== "") {
      const api = parseApiBase(env.VITE_API_BASE_URL);
      if (!api.ok) throw new Error(`@kongken/trip-map cannot be built: ${api.error}. Set it to the BFF's public API base (example: https://api.example.com/api).`);
      const ozid = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
      if (!ozid.ok) throw new Error(`@kongken/trip-map cannot be built: ${ozid.error}. Set it to the OzID origin (example: https://id.example.com).`);
    }
  }
  return {
    server: {
      // The dev server sends /s/<token> to the backend, which renders it with Open Graph
      // tags, as the front image's Nginx does. `vite preview` serves the built single page
      // app only, so share links there open the same SharePage without those tags.
      proxy: { ...proxy, "/s/": backend },
    },
    preview: { proxy },
    plugins: [react(), tailwindcss(), accountLandingHeaders()],
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  };
});
