import { defineConfig } from "@playwright/test";

const port = Number(process.env.E2E_PORT ?? 4175);
const host = "127.0.0.1";
const freePort = `fuser -k ${port}/tcp 2>/dev/null || true`;

// The tests run the built single page app against a fake BFF installed with
// page.route (tests/fake-backend.ts); the real chain is covered by internal/e2e (TestMetroBrowserChain).
// The product calls the canonical BFF by an absolute address (there is no same-origin /api to share the session
// with), and OzID is a reserved example origin the specs never reach: they stub it. The fake BFF answers /api on
// the preview origin itself, so that origin doubles as the API origin here.
const baseURL = `http://${host}:${port}`;
const ozidOrigin = "https://id.example.com";
export default defineConfig({
  testDir: "tests",
  // The real-stack specs have their own config and are started by internal/e2e TestMetroBrowserChain.
  testIgnore: "real-stack/**",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL, trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 900 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: {
    command: `${freePort}; VITE_API_BASE_URL=${baseURL}/api VITE_OZID_ORIGIN=${ozidOrigin} npm run build -w @kongken/metro && npm run preview -w @kongken/metro -- --host ${host} --port ${port}`,
    url: baseURL,
    reuseExistingServer: false,
    timeout: 180_000,
  },
});
