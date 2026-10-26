import test from "node:test";
import assert from "node:assert/strict";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { FootprintService } from "@kongken/api-client/bff/tripmap/v1/footprint_pb";
import { csrfRetry, isCsrfRefusal, REJECTION_HEADER } from "../src/lib/csrf-retry.ts";
import { createApiTransport } from "../src/api/transport-core.ts";

// ---- the interceptor on its own -------------------------------------------------------------------------------

const marked = () => new ConnectError("request rejected", Code.PermissionDenied, { [REJECTION_HEADER]: "csrf" });
const unmarked = () => new ConnectError("request rejected", Code.PermissionDenied);
const call = (interceptor, next, req = {}) => interceptor(next)(req);

test("a refusal marked as a CSRF mismatch is repeated once, after a fresh token", async () => {
  let refreshed = 0;
  let sent = 0;
  const reply = await call(
    csrfRetry(async () => void refreshed++),
    async () => {
      if (++sent === 1) throw marked();
      return "ok";
    },
  );
  assert.equal(reply, "ok");
  assert.equal(refreshed, 1);
  assert.equal(sent, 2);
});

test("the same refusal without the marker is never repeated: Origin, perimeter, role and business refusals look alike", async () => {
  let refreshed = 0;
  let sent = 0;
  await assert.rejects(
    call(csrfRetry(async () => void refreshed++), async () => {
      sent++;
      throw unmarked();
    }),
    (e) => ConnectError.from(e).code === Code.PermissionDenied && ConnectError.from(e).rawMessage === "request rejected",
  );
  assert.equal(sent, 1);
  assert.equal(refreshed, 0);
  // Any other marker value is not the CSRF one either.
  await assert.rejects(
    call(csrfRetry(async () => assert.fail("no token")), async () => {
      throw new ConnectError("request rejected", Code.PermissionDenied, { [REJECTION_HEADER]: "origin" });
    }),
  );
});

test("a marker on another status is not a CSRF refusal", () => {
  for (const code of [Code.Unauthenticated, Code.Unavailable, Code.FailedPrecondition, Code.Internal, Code.Aborted]) {
    assert.equal(isCsrfRefusal(new ConnectError("x", code, { [REJECTION_HEADER]: "csrf" })), false, Code[code]);
  }
});

test("a second marked refusal is the answer; nothing is repeated again", async () => {
  let sent = 0;
  let refreshed = 0;
  await assert.rejects(
    call(csrfRetry(async () => void refreshed++), async () => {
      sent++;
      throw marked();
    }),
    (e) => isCsrfRefusal(e),
  );
  assert.equal(sent, 2);
  assert.equal(refreshed, 1);
});

test("other failures are never repeated and fetch no token", async () => {
  for (const code of [Code.Unauthenticated, Code.Unavailable, Code.Aborted, Code.FailedPrecondition, Code.Internal]) {
    let refreshed = 0;
    let sent = 0;
    await assert.rejects(
      call(csrfRetry(async () => void refreshed++), async () => {
        sent++;
        throw new ConnectError("x", code);
      }),
      (e) => ConnectError.from(e).code === code,
    );
    assert.equal(sent, 1, Code[code]);
    assert.equal(refreshed, 0, Code[code]);
  }
  // A network error (not a ConnectError) is not a refusal either.
  let sent = 0;
  await assert.rejects(call(csrfRetry(async () => assert.fail("no token")), async () => { sent++; throw new TypeError("network"); }));
  assert.equal(sent, 1);
});

test("a token that cannot be fetched leaves the original refusal", async () => {
  let sent = 0;
  await assert.rejects(
    call(csrfRetry(async () => { throw new Error("offline"); }), async () => { sent++; throw marked(); }),
    (e) => isCsrfRefusal(e),
  );
  assert.equal(sent, 1);
});

test("streaming calls are not repeated, even when marked", async () => {
  let sent = 0;
  await assert.rejects(call(csrfRetry(async () => assert.fail("no token")), async () => { sent++; throw marked(); }, { stream: true }));
  assert.equal(sent, 1);
});

// ---- the real request structure ---------------------------------------------------------------------------------

const ACCOUNT_A = "11111111-1111-4111-8111-111111111111";
const ACCOUNT_B = "22222222-2222-4222-8222-222222222222";
const BASE = "https://api.example.com/api";

/** A fetch that records every request exactly as the transport sent it and answers from a script. */
function scripted(answers) {
  const sent = [];
  const fetch = async (input, init) => {
    const request = new Request(input, init);
    sent.push({ url: request.url, headers: Object.fromEntries(request.headers), body: await request.text() });
    const answer = answers[sent.length - 1];
    if (!answer) throw new Error(`unexpected request ${sent.length}`);
    return answer();
  };
  return { fetch, sent };
}
const json = (status, body, headers = {}) => () =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });
const refused = (marker) =>
  json(403, { code: "permission_denied", message: "request rejected" }, marker ? { "x-bff-rejection": marker } : {});
const applied = json(200, { revision: "8", changedCount: 1, replayed: false });

function client(parts) {
  let token = "token-1";
  const refreshes = [];
  const transport = createApiTransport({
    baseUrl: BASE,
    csrf: () => token,
    refresh: async () => {
      refreshes.push(token);
      token = "token-2";
    },
    account: () => ACCOUNT_A,
    fixed: () => ({ "x-tripmap-schema": "2" }),
    origin: () => "https://trip.example.com",
    ...parts,
  });
  return { api: createClient(FootprintService, transport), refreshes };
}

const sync = (api, userId, mutationId = "6f1a9c3e-0000-4000-8000-00000000a001") =>
  api.syncChanges(
    { mutationId, baseRevision: 7n, mutations: [] },
    userId ? { headers: { "X-Kongken-User": userId } } : undefined,
  );

test("the repeat carries the same body, mutation ID and account header; only the token changes", async () => {
  const { fetch, sent } = scripted([refused("csrf"), applied]);
  const { api, refreshes } = client({ fetch });
  const reply = await sync(api, ACCOUNT_A);
  assert.equal(reply.revision, 8n);
  assert.equal(sent.length, 2);
  const [first, second] = sent;
  assert.equal(first.body, second.body, "the same message, byte for byte");
  assert.match(first.body, /6f1a9c3e-0000-4000-8000-00000000a001/);
  assert.equal(first.url, second.url);
  assert.equal(first.headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(second.headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(first.headers["x-csrf-token"], "token-1");
  assert.equal(second.headers["x-csrf-token"], "token-2");
  assert.equal(second.headers["x-tripmap-schema"], "2");
  assert.equal(second.headers.origin, "https://trip.example.com");
  assert.deepEqual(refreshes, ["token-1"]);
});

test("the account of the page may change while the token is being fetched: the repeat still names the original", async () => {
  let current = ACCOUNT_A;
  const { fetch, sent } = scripted([refused("csrf"), refused(null)]);
  const { api } = client({
    fetch,
    account: () => current,
    refresh: async () => {
      current = ACCOUNT_B; // another tab signed in as B while the new token was being issued
    },
  });
  // A call that does not name its account itself: the page's account at the time it was sent is used and kept.
  await assert.rejects(api.syncChanges({ mutationId: "6f1a9c3e-0000-4000-8000-00000000a002", baseRevision: 1n, mutations: [] }));
  assert.equal(sent[0].headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(sent[1].headers["x-kongken-user"], ACCOUNT_A, "never rewritten to B");
});

test("an unmarked refusal sends exactly one request and asks for no token", async () => {
  const { fetch, sent } = scripted([refused(null)]);
  const { api, refreshes } = client({ fetch });
  await assert.rejects(sync(api, ACCOUNT_A), (e) => ConnectError.from(e).code === Code.PermissionDenied);
  assert.equal(sent.length, 1);
  assert.deepEqual(refreshes, []);
});

test("a read refused without the marker is not repeated either", async () => {
  const { fetch, sent } = scripted([refused(null)]);
  const { api, refreshes } = client({ fetch });
  await assert.rejects(api.getFootprints({}, { headers: { "X-Kongken-User": ACCOUNT_A } }));
  assert.equal(sent.length, 1);
  assert.deepEqual(refreshes, []);
});

test("the marker survives the browser's header handling: it is read from the response headers", async () => {
  const { fetch } = scripted([refused("csrf"), refused("csrf")]);
  const { api } = client({ fetch });
  await assert.rejects(sync(api, ACCOUNT_A), (e) => {
    const error = ConnectError.from(e);
    return error.code === Code.PermissionDenied && error.metadata.get("x-bff-rejection") === "csrf";
  });
});
