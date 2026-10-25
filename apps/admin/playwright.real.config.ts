import { defineConfig } from "@playwright/test";

// The real-stack specs (tests/real-stack) are started by internal/e2e TestAdminBrowserChain,
// which runs the real BFF and usersrv behind an HTTPS front on reserved test host names and
// passes the addresses in ADMIN_REAL_STACK. There is no web server to start here.
// Chromium resolves the reserved *.ozid-e2e.test names to the local front, and accepts its
// throwaway certificate; nothing else about the browser is relaxed.
export default defineConfig({
  testDir: "tests/real-stack",
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  use: {
    ignoreHTTPSErrors: true,
    launchOptions: { args: ["--host-resolver-rules=MAP *.ozid-e2e.test 127.0.0.1"] },
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 900 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
});
