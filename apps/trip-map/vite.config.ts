import { accountLandingHeaders } from "@kongken/account/vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";

// Dev-server proxy target, the BFF (make dev); override when it listens elsewhere.
const backend = process.env.TRIPMAP_DEV_BACKEND ?? "http://127.0.0.1:18081";

const proxy = {
  "/api": backend,
  "/ping": backend,
  "/readyz": backend,
};

// https://vite.dev/config/
export default defineConfig({
  server: {
    // The dev server sends /s/<token> to the backend, which renders it with Open Graph
    // tags, as the front image's Nginx does. `vite preview` serves the built single page
    // app only, so share links there open the same SharePage without those tags.
    proxy: { ...proxy, "/s/": backend },
  },
  preview: { proxy },
  plugins: [react(), tailwindcss(), accountLandingHeaders()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
});
