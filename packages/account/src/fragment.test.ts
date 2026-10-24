import assert from "node:assert/strict";
import { beforeEach, test } from "node:test";
import { captureLandingToken, heldToken, landingKind, parseFragmentToken, releaseToken, resetCapturedForTest, watchLandingFragment } from "./fragment.ts";

const TOKEN = "Zm9vYmFyYmF6cXV4MTIzNDU2Nzg5MC1fQUJDREVGRw";

test("landing paths are fixed and respect the base path", () => {
  assert.equal(landingKind("/account/verify-email"), "verify-email");
  assert.equal(landingKind("/account/reset-password/"), "reset-password");
  assert.equal(landingKind("/account/verify-email", "/app/"), null);
  assert.equal(landingKind("/app/account/verify-email", "/app/"), "verify-email");
  for (const other of ["/", "/account", "/account/verify-email/x", "/accounts/verify-email", "/auth/login"]) assert.equal(landingKind(other), null, other);
});

test("only a well-formed token fragment is accepted", () => {
  assert.equal(parseFragmentToken(`#token=${TOKEN}`), TOKEN);
  assert.equal(parseFragmentToken(`token=${TOKEN}&x=1`), TOKEN);
  assert.equal(parseFragmentToken(`#x=1&token=${TOKEN}`), TOKEN);
  for (const bad of ["", "#", "#token=", "#token=short", "#token=has space/xxxxxxxxxxxxxxxxxx", `#tok=${TOKEN}`, `#${TOKEN}`]) assert.equal(parseFragmentToken(bad), null, bad);
});

function env(pathname: string, hash: string) {
  const replaced: string[] = [];
  const metas: { name: string; content: string }[] = [];
  return {
    replaced,
    metas,
    env: {
      location: { pathname, search: "?a=1", hash },
      history: { replaceState: (_s: unknown, _u: string, url?: string | URL | null) => void replaced.push(String(url)) },
      document: {
        head: { appendChild: (n: unknown) => void metas.push(n as { name: string; content: string }) },
        createElement: () => ({ name: "", content: "" }),
        querySelector: () => (metas.length ? {} : null),
      },
    },
  };
}

beforeEach(() => resetCapturedForTest());

test("the landing page keeps the token in memory, clears the fragment at once and stops referrers", () => {
  const e = env("/account/reset-password", `#token=${TOKEN}`);
  assert.deepEqual(captureLandingToken(e.env), { kind: "reset-password", token: TOKEN });
  assert.deepEqual(e.replaced, ["/account/reset-password?a=1"], "the URL keeps the path and query but not the fragment");
  assert.deepEqual(e.metas, [{ name: "referrer", content: "no-referrer" }]);
  assert.equal(heldToken(), TOKEN);
  // A second call (React's double render) gets the same answer and touches nothing more.
  assert.equal(captureLandingToken(env("/account/reset-password", "").env)?.token, TOKEN);
  assert.equal(e.replaced.length, 1);
  releaseToken();
  assert.equal(heldToken(), null);
});

test("a landing page without a usable token still clears whatever the fragment held", () => {
  const e = env("/account/verify-email", "#token=nope");
  assert.deepEqual(captureLandingToken(e.env), { kind: "verify-email", token: null });
  assert.equal(e.replaced.length, 1);
});

test("other pages are never touched", () => {
  const e = env("/", `#token=${TOKEN}`);
  assert.equal(captureLandingToken(e.env), null);
  assert.deepEqual(e.replaced, []);
  assert.deepEqual(e.metas, []);
});

test("a second link opened in the same tab replaces the held token and is cleared too", () => {
  const e = env("/account/reset-password", `#token=${TOKEN}`);
  captureLandingToken(e.env);
  const location = { pathname: "/account/reset-password", search: "", hash: "" };
  const replaced: string[] = [];
  let listener: (() => void) | undefined;
  let notified = 0;
  const stop = watchLandingFragment(() => void notified++, {
    location,
    history: { replaceState: (_s: unknown, _u: string, url?: string | URL | null) => void replaced.push(String(url)) },
    addEventListener: (_t, fn) => void (listener = fn),
    removeEventListener: () => void (listener = undefined),
  });
  // Clearing our own fragment fires hashchange with an empty hash: nothing to do.
  listener!();
  assert.equal(notified, 0);
  const SECOND = "c2Vjb25kdG9rZW4tMTIzNDU2Nzg5MC1fQUJDREVGRw";
  location.hash = `#token=${SECOND}`;
  listener!();
  assert.equal(heldToken(), SECOND);
  assert.deepEqual(replaced, ["/account/reset-password"]);
  assert.equal(notified, 1);
  stop();
  assert.equal(listener, undefined);
});
