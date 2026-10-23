import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";

// Dev-server proxy target: the BFF (make dev). The BFF is the only backend the
// browser talks to; /auth/ carries the login redirect and callback.
const backend = process.env.METRO_DEV_BACKEND ?? "http://127.0.0.1:18081";

const proxy = {
  "/api": backend,
  "/auth/": backend,
  "/ping": backend,
  "/readyz": backend,
};

// The port matches auth.oauth_apps.metro.origin in config.bff.example.yaml.
export default defineConfig({
  server: { port: 5174, strictPort: true, proxy },
  preview: { proxy },
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
});
