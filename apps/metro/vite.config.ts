import { accountLandingHeaders } from "@kongken/account/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";

// Dev-server proxy target: the BFF (make dev). The BFF is the only backend the
// browser talks to.
const backend = process.env.METRO_DEV_BACKEND ?? "http://127.0.0.1:18081";

const proxy = {
  "/api": backend,
  "/ping": backend,
  "/readyz": backend,
};

// The port matches the metro origin in the usersrv app configuration.
export default defineConfig({
  server: { port: 5174, strictPort: true, proxy },
  preview: { proxy },
  plugins: [react(), tailwindcss(), accountLandingHeaders()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
});
