import { parseApiBase, parseOrigin } from "@kongken/account/navigation";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";
import { defineConfig, loadEnv } from "vite";

// The console has no same-origin proxy: the browser calls the shared BFF at its canonical public
// origin with credentials, and the session Cookie belongs to that API host. Both addresses are
// public build settings, so a missing or malformed one fails the build instead of shipping a
// console that cannot sign anybody in or send them to OzID.
export default defineConfig(({ command, mode }) => {
  if (command === "build") {
    const env = loadEnv(mode, process.cwd(), "VITE_");
    const api = parseApiBase(env.VITE_API_BASE_URL);
    if (!api.ok) throw new Error(`@kongken/admin cannot be built: ${api.error}. Set it to the BFF's public API base (example: https://api.example.com/api).`);
    const ozid = parseOrigin(env.VITE_OZID_ORIGIN, "VITE_OZID_ORIGIN");
    if (!ozid.ok) throw new Error(`@kongken/admin cannot be built: ${ozid.error}. Set it to the OzID origin (example: https://id.example.com).`);
  }
  // The port matches the console origin in the BFF's navigation registry (config.bff.example.yaml).
  return {
    server: { port: 5176, strictPort: true },
    plugins: [react(), tailwindcss()],
    resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  };
});
