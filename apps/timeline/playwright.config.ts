import { defineConfig, devices } from "@playwright/test";
export default defineConfig({ testDir: "./tests", use: { baseURL: "http://127.0.0.1:4178", ...devices["Desktop Chrome"] }, webServer: { command: "npm run dev -- --host 127.0.0.1 --port 4178", port: 4178, reuseExistingServer: !process.env.CI } });
