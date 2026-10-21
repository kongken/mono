import test from "node:test";
import { stampRegions } from "../src/lib/region-catalog.ts";
import assert from "node:assert/strict";
import {
  SESSION_KEY,
  cacheKey,
  legacyCacheSeenKey,
  clearAccountData,
  clearOtherCaches,
  loadCloudCache,
  loadSession,
  saveCloudCache,
  saveSession,
} from "../src/lib/session.ts";
import {
  oauthLoginURL,
} from "../src/lib/oauth.ts";

function memory(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    data,
    get length() {
      return data.size;
    },
    key: (i) => [...data.keys()][i] ?? null,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, String(v)),
    removeItem: (k) => void data.delete(k),
  };
}
const user = { id: "u1", username: "ann", displayName: "Ann", avatarUrl: "" };
const empty = { version: 2, visits: [], provinces: {} };

test("session round trips and expires", () => {
  const s = memory();
  assert.ok(saveSession(s, { expiresAt: 2000, user }));
  assert.equal(loadSession(s, 1000)?.user.id, "u1");
  assert.equal(loadSession(s, 2000), null);
  assert.equal(s.getItem(SESSION_KEY), null, "expired session is removed");
});

test("malformed session is dropped", () => {
  for (const raw of [
    "{",
    "null",
    '{"expiresAt":9e15,"user":{"id":""}}',
    '{"user":{}}',
  ]) {
    const s = memory({ [SESSION_KEY]: raw });
    assert.equal(loadSession(s, 0), null);
  }
});

test("cloud cache validates and preserves unreadable data", () => {
  const s = memory();
  saveCloudCache(s, "u1", { saved: empty, revision: 4 });
  assert.equal(loadCloudCache(s, "u1", (d) => d)?.revision, 4);
  const raw = s.getItem(cacheKey("u1"));
  assert.throws(
    () =>
      loadCloudCache(s, "u1", () => {
        throw new Error("bad");
      }),
    /原始数据/,
  );
  assert.equal(s.getItem(cacheKey("u1")), raw);
});

test("logout clears every account cache but not local footprints", () => {
  const s = memory({
    "shanhai-travel-v1": "{}",
    "shanhai-travel-v2": "{}",
    "shanhai-travel-v1-unreadable-1": "x",
  });
  saveSession(s, { token: "t", expiresAt: 9e15, user });
  saveCloudCache(s, "u1", { saved: empty, revision: 1 });
  saveCloudCache(s, "u2", { saved: empty, revision: 1 });
  clearAccountData(s);
  assert.deepEqual([...s.data.keys()].sort(), [
    "shanhai-travel-v1",
    "shanhai-travel-v1-unreadable-1",
    "shanhai-travel-v2",
  ]);
});

test("switching accounts keeps only the new user's cache", () => {
  const s = memory();
  saveCloudCache(s, "u1", { saved: empty, revision: 1 });
  saveCloudCache(s, "u2", { saved: empty, revision: 1 });
  clearOtherCaches(s, "u2");
  assert.deepEqual([...s.data.keys()].sort(), [cacheKey("u2"), legacyCacheSeenKey("u2")].sort());
});

test("OAuth navigation uses BFF host and an app identifier", () => {
  assert.equal(oauthLoginURL("https://api.example.com/api", "https://a.example.com", "trip-map", "logto"), "https://api.example.com/auth/login?app=trip-map&provider=logto");
  assert.equal(oauthLoginURL("/api", "http://localhost:5173", "trip-map", "github"), "http://localhost:5173/auth/login?app=trip-map&provider=github");
});


import { Code, ConnectError } from "@connectrpc/connect";
import { describeApiError, isUnauthenticated } from "../src/lib/errors.ts";

test("API errors are described in Chinese, never with raw server text", () => {
  const e = (code, msg) => new ConnectError(msg, code);
  assert.equal(
    describeApiError(e(Code.Unauthenticated, "invalid username or password")),
    "用户名或密码不正确",
  );
  assert.equal(
    describeApiError(e(Code.AlreadyExists, "username is taken")),
    "该用户名已被使用",
  );
  assert.equal(
    describeApiError(e(Code.PermissionDenied, "registration is closed")),
    "当前未开放注册",
  );
  assert.match(
    describeApiError(e(Code.InvalidArgument, "something odd")),
    /不符合要求/,
  );
  assert.match(describeApiError(e(Code.Unavailable, "x")), /无法连接/);
  assert.match(
    describeApiError(new TypeError("Failed to fetch")),
    /无法连接|暂时不可用/,
  );
  for (const code of [
    Code.Internal,
    Code.Unavailable,
    Code.InvalidArgument,
    Code.ResourceExhausted,
  ])
    assert.ok(
      !/[a-zA-Z]{4}/.test(describeApiError(e(code, "english message"))),
    );
  assert.equal(isUnauthenticated(e(Code.Unauthenticated, "")), true);
  assert.equal(isUnauthenticated(e(Code.Internal, "")), false);
});

test("new cache keeps the offline desired state and confirmed ancestor", () => {
  const s = memory();
  const desired = { ...empty, provinces: { 330000: "#4b937d" } };
  const cache = { saved: desired, confirmed: empty, revision: 0 };
  assert.equal(saveCloudCache(s, "u1", cache), true);
  assert.deepEqual(
    loadCloudCache(s, "u1", (d) => d),
    { schema: 2, ...cache, saved: stampRegions(desired), confirmed: stampRegions(empty), legacyDigest: "none" },
  );
  clearOtherCaches(s, "u2");
  assert.notEqual(
    s.getItem(cacheKey("u1")),
    null,
    "account switching must not discard unsent edits",
  );
  clearAccountData(s, "u2");
  assert.notEqual(
    s.getItem(cacheKey("u1")),
    null,
    "another user's logout must not discard them",
  );
  clearAccountData(s, "u1");
  assert.equal(
    s.getItem(cacheKey("u1")),
    null,
    "explicit owner logout clears their data",
  );
});

test("invalid ancestor or revision preserves raw cache", () => {
  for (const revision of [-1, 0.5, null]) {
    const raw = JSON.stringify({ saved: empty, confirmed: empty, revision });
    const s = memory({ [cacheKey("u1")]: raw });
    assert.throws(() => loadCloudCache(s, "u1", (d) => d));
    assert.equal(s.getItem(cacheKey("u1")), raw);
  }
  const s = memory({ [cacheKey("u1")]: "{broken" });
  clearOtherCaches(s, "u2");
  assert.equal(s.getItem(cacheKey("u1")), "{broken");
});

test("storage refusal is observable and does not overwrite the previous cache", () => {
  const s = memory();
  saveCloudCache(s, "u1", { saved: empty, revision: 0 });
  const raw = s.getItem(cacheKey("u1"));
  s.setItem = () => {
    throw new Error("quota");
  };
  assert.equal(
    saveCloudCache(s, "u1", {
      saved: { ...empty, provinces: { 330000: "#4b937d" } },
      confirmed: empty,
      revision: 0,
    }),
    false,
  );
  assert.equal(s.getItem(cacheKey("u1")), raw);
});
