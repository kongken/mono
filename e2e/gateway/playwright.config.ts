import { defineConfig } from "@playwright/test";

// The gateway scenario's browser run, started by internal/e2e TestGatewayDomainSets (which starts the
// BFF, usersrv, the real Nginx containers' configuration and the real Caddy example, and passes the
// addresses in GATEWAY_STACK). There is no web server to start here and nothing to run by hand.
//
// Chromium resolves every name of the scenario's reserved site (*.test) to one gateway port on the
// loopback, and accepts the gateway's internal CA; nothing else about the browser is relaxed. The
// "outside" project resolves the console host to a second gateway instance that has the shipped
// example's own source rule, which admits no real network: the console is closed for it, while every
// other name goes to the same gateway as in the inside projects.
type Stack = { site: string; adminHost: string; mainPort: number; shippedPort: number };
if (!process.env.GATEWAY_STACK) throw new Error("GATEWAY_STACK is set by internal/e2e TestGatewayDomainSets; run that test instead");
const stack: Stack = JSON.parse(process.env.GATEWAY_STACK);

const inside = `MAP *.${stack.site} 127.0.0.1:${stack.mainPort}`;
const outside = `MAP ${stack.adminHost} 127.0.0.1:${stack.shippedPort}, ${inside}`;
const launch = (rules: string) => ({ args: [`--host-resolver-rules=${rules}`] });

export default defineConfig({
  testDir: ".",
  timeout: 90_000,
  fullyParallel: false,
  workers: 1,
  forbidOnly: true,
  retries: 0,
  use: {
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    { name: "inside-desktop", testIgnore: /outside\.spec/, use: { viewport: { width: 1280, height: 900 }, launchOptions: launch(inside) } },
    { name: "inside-mobile", testIgnore: /outside\.spec/, use: { viewport: { width: 390, height: 844 }, launchOptions: launch(inside) } },
    { name: "outside-desktop", testMatch: /outside\.spec/, use: { viewport: { width: 1280, height: 900 }, launchOptions: launch(outside) } },
  ],
});
