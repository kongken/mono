import { defineConfig } from "@playwright/test";

const port = Number(process.env.E2E_PORT ?? 4175);
const host = "127.0.0.1";
const freePort = `fuser -k ${port}/tcp 2>/dev/null || true`;

// The tests run the built single page app against a fake BFF installed with
// page.route (tests/fake-backend.ts); the real chain is covered by internal/e2e.
export default defineConfig({
  testDir: "tests",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL: `http://${host}:${port}`, trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 900 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: {
    command: `${freePort}; VITE_API_BASE_URL=/api npm run build -w @kongken/metro && npm run preview -w @kongken/metro -- --host ${host} --port ${port}`,
    url: `http://${host}:${port}`,
    reuseExistingServer: false,
    timeout: 180_000,
  },
});
