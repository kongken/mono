import { defineConfig } from "@playwright/test";

const port = Number(process.env.E2E_PORT ?? 4178);
const host = "127.0.0.1";
const freePort = `fuser -k ${port}/tcp 2>/dev/null || true`;

// The tests run the built single page app against a fake BFF installed with page.route (tests/fake-backend.ts).
// Timeline calls the BFF at its canonical public origin (a same-origin /api proxy cannot share the session Cookie),
// and OzID is a reserved example origin the specs never reach. The fake BFF answers /api on the preview origin
// itself, so that origin doubles as the API origin here.
const baseURL = `http://${host}:${port}`;
const ozidOrigin = "https://id.example.com";
export default defineConfig({
  testDir: "tests",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL, trace: "retain-on-failure" },
  projects: [{ name: "desktop", use: { viewport: { width: 1440, height: 960 } } }],
  webServer: {
    command: `${freePort}; VITE_API_BASE_URL=${baseURL}/api VITE_OZID_ORIGIN=${ozidOrigin} npm run build -w @kongken/timeline && npm run preview -w @kongken/timeline -- --host ${host} --port ${port}`,
    url: baseURL,
    reuseExistingServer: false,
    timeout: 180_000,
  },
});
