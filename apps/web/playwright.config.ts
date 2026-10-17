import { defineConfig } from "@playwright/test";

const realBff = process.env.E2E_REAL_BFF === "1";
const backend = process.env.TRIPMAP_DEV_BACKEND ?? "http://127.0.0.1:18081";

export default defineConfig({
  testDir: "tests",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL: "http://127.0.0.1:4174", trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 960 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: realBff
    ? {
        command: `VITE_API_BASE_URL=/api npm run build -w @trip-map/web && TRIPMAP_DEV_BACKEND=${backend} npm run preview -w @trip-map/web -- --host 127.0.0.1 --port 4174`,
        url: "http://127.0.0.1:4174",
        reuseExistingServer: !process.env.CI,
        timeout: 180_000,
      }
    : {
        command: "npm run preview -- --host 127.0.0.1 --port 4174",
        url: "http://127.0.0.1:4174",
        reuseExistingServer: false,
      },
});
