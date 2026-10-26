import { parseApiBase, parseOrigin } from "@kongken/account/navigation";
import { accountLandingHeaders } from "@kongken/account/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, loadEnv } from "vite";

// Dev-server proxy target: the BFF (make dev). The BFF is the only backend the
// browser talks to.
const backend = process.env.METRO_DEV_BACKEND ?? "http://127.0.0.1:18081";

const proxy = {
  "/api": backend,
  "/ping": backend,
  "/readyz": backend,
};

// A build must name both addresses. The session Cookie belongs to the API host, so the browser calls the
// canonical BFF origin directly (no same-origin `/api` proxy of this site can share that session with OzID), and
// every account page lives in OzID, so its origin is needed to send anybody there. Both are public build settings;
// a missing or malformed one fails the build instead of shipping a product that cannot sign anybody in.
// The port matches the metro origin in the usersrv app configuration.
export default defineConfig(({ command, mode }) => {
  if (command === "build") {
    const env = loadEnv(mode, process.cwd(), "VITE_");
    const api = parseApiBase(env.VITE_API_BASE_URL);
    if (!api.ok) throw new Error(`@kongken/metro cannot be built: ${api.error}. Set it to the BFF's public API base (example: https://api.example.com/api).`);
    const ozid = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
    if (!ozid.ok) throw new Error(`@kongken/metro cannot be built: ${ozid.error}. Set it to the OzID origin (example: https://id.example.com).`);
  }
  return {
    server: { port: 5174, strictPort: true, proxy },
    preview: { proxy },
    plugins: [react(), tailwindcss(), accountLandingHeaders()],
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  };
});
