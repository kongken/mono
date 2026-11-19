import assert from "node:assert/strict";
import test from "node:test";
import { loadConfigFromFile } from "vite";

// The build gate in vite.config.ts. A deployment host (CI, Docker, Cloudflare Pages) hands the two public
// addresses over in the process environment, so these tests set them there and load the real config the way
// `vite build` does. The failure message has to name the `VITE_*` variables the build could see: that is what
// tells a variable that never reached the build apart from a misspelled one.

const API = "https://api.example.com/api";
const OZID = "https://id.example.com";
const ADDRESSES = ["VITE_API_BASE_URL", "VITE_OZID_ORIGIN"];

async function load(env: Record<string, string>) {
  const saved = new Map<string, string | undefined>();
  for (const key of [...ADDRESSES, ...Object.keys(env)]) {
    saved.set(key, process.env[key]);
    delete process.env[key];
  }
  Object.assign(process.env, env);
  try {
    return await loadConfigFromFile({ command: "build", mode: "production" }, "vite.config.ts", process.cwd(), "silent");
  } finally {
    for (const [key, value] of saved) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

test("the build gate refuses a build that cannot name the BFF API base", async () => {
  await assert.rejects(load({}), (err: Error) => {
    assert.match(err.message, /cannot be built: VITE_API_BASE_URL is required/);
    assert.match(err.message, /the VITE_\* names this build could see were: none\./);
    return true;
  });
});

test("the build gate refuses a build without OzID's origin", async () => {
  await assert.rejects(load({ VITE_API_BASE_URL: API }), /cannot be built: VITE_OZID_ORIGIN is required/);
});

test("the build gate names the variables a misspelled build setting leaves behind", async () => {
  await assert.rejects(load({ VITE_API_BASE: API }), /the VITE_\* names this build could see were: VITE_API_BASE\./);
});

test("the build gate accepts the two public addresses", async () => {
  const loaded = await load({ VITE_API_BASE_URL: API, VITE_OZID_ORIGIN: OZID });
  assert.ok(loaded?.config);
});
