import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vite";

// Dev-server proxy target; override when the backend listens elsewhere.
const backend = process.env.TRIPMAP_DEV_BACKEND ?? "http://127.0.0.1:18081";

// https://vite.dev/config/
export default defineConfig({
  server: {
    proxy: {
      "/api": backend,
      "/ping": backend,
      "/readyz": backend,
    },
  },
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
});
