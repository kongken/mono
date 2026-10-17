import { execSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { defineConfig } from "@playwright/test";

const portStamp = join(tmpdir(), "trip-map-playwright-port");

const realBff = process.env.E2E_REAL_BFF === "1";
const backend = process.env.TRIPMAP_DEV_BACKEND ?? "http://127.0.0.1:18081";

/** Bind probe: exit 0 when the port is free on 127.0.0.1. */
function pickPort(preferred = 4174, attempts = 32): number {
  for (let p = preferred; p < preferred + attempts; p++) {
    try {
      execSync(
        `node -e "require('net').createServer().once('error',()=>process.exit(1)).listen(${p},'127.0.0.1',()=>process.exit(0))"`,
        { stdio: "ignore", timeout: 3000 },
      );
      return p;
    } catch {
      /* port in use */
    }
  }
  throw new Error(`no free port in ${preferred}..${preferred + attempts - 1}`);
}

// Runner and workers each reload this file; share one picked port via stamp (or E2E_PORT).
function resolvePort(): number {
  if (process.env.E2E_PORT) return Number(process.env.E2E_PORT);
  if (existsSync(portStamp)) return Number(readFileSync(portStamp, "utf8"));
  const chosen = pickPort(4174);
  writeFileSync(portStamp, String(chosen));
  return chosen;
}
const port = resolvePort();
const host = "127.0.0.1";
const baseURL = `http://${host}:${port}`;
const preview = `npm run preview -w @trip-map/web -- --host ${host} --port ${port}`;
const build = "VITE_API_BASE_URL=/api npm run build -w @trip-map/web";
const freePort = `fuser -k ${port}/tcp 2>/dev/null || true`;

export default defineConfig({
  testDir: "tests",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  use: { baseURL, trace: "retain-on-failure" },
  projects: [
    { name: "desktop", use: { viewport: { width: 1280, height: 960 } } },
    { name: "mobile", use: { viewport: { width: 390, height: 844 } } },
  ],
  webServer: realBff
    ? {
        command: `${freePort}; ${build} && TRIPMAP_DEV_BACKEND=${backend} ${preview}`,
        url: baseURL,
        reuseExistingServer: false,
        timeout: 180_000,
      }
    : {
        command: `${freePort}; ${build} && ${preview}`,
        url: baseURL,
        reuseExistingServer: false,
        timeout: 180_000,
      },
});
