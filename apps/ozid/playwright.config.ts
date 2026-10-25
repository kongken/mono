import { defineConfig } from "@playwright/test";

const port = Number(process.env.E2E_PORT ?? 4176);
const host = "127.0.0.1";
const freePort = `fuser -k ${port}/tcp 2>/dev/null || true`;

// These specs run the built single page app against a fake BFF installed with page.route
// (UI behaviour only). The build points the API base at the preview server itself so the
// fake sees every request. The real cross-origin chain (BFF, usersrv, Cookie, CORS over
// HTTPS subdomains) is exercised by internal/e2e (TestOzidBrowserChain) with its own
// specs in tests/real-stack.
export default defineConfig({
  testDir: "tests",
  testIgnore: "real-stack/**",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL: `http://${host}:${port}`, trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 900 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: {
    command: `${freePort}; VITE_API_BASE_URL=http://${host}:${port}/api npm run build -w @kongken/ozid && npm run preview -w @kongken/ozid -- --host ${host} --port ${port}`,
    url: `http://${host}:${port}`,
    reuseExistingServer: false,
    timeout: 180_000,
  },
});
