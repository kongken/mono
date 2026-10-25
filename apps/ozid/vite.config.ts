import { parseApiBase } from "@kongken/account/navigation";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, loadEnv } from "vite";

// OzID has no same-origin proxy: the browser calls the shared BFF at its canonical public
// origin with credentials, and the session Cookie belongs to that API host. The address is
// a public build setting, so a missing or malformed one fails the build instead of
// shipping a page that cannot sign anybody in.
export default defineConfig(({ command, mode }) => {
  if (command === "build") {
    const env = loadEnv(mode, process.cwd(), "VITE_");
    const api = parseApiBase(env.VITE_API_BASE_URL);
    if (!api.ok) throw new Error(`@kongken/ozid cannot be built: ${api.error}. Set it to the BFF's public API base (example: https://api.example.com/api).`);
  }
  // The port matches the OzID origin in the BFF's navigation registry (config.bff.example.yaml).
  return {
    server: { port: 5175, strictPort: true },
    plugins: [react(), tailwindcss()],
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  };
});
