import assert from "node:assert/strict";
import { test } from "node:test";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { AuthService } from "@kongken/api-client/bff/v1/auth_pb";
import { RiderService } from "@kongken/api-client/bff/metro/v1/rider_pb";
import { isCsrfRefusal } from "@kongken/account/csrf-retry";
import { createApiTransport } from "./transport.ts";

// The request structure of the product's transport, asserted on what is really sent: the shared CSRF replay
// (the boundary trip-map verified in T10) and the account guard, with a real Connect transport and a scripted fetch.

const ACCOUNT_A = "11111111-1111-4111-8111-111111111111";
const ACCOUNT_B = "22222222-2222-4222-8222-222222222222";
const BASE = "https://api.example.com/api";

type Sent = { url: string; headers: Record<string, string>; body: string };

/** A fetch that records every request exactly as the transport sent it and answers from a script. */
function scripted(answers: Array<() => Response>) {
  const sent: Sent[] = [];
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    sent.push({ url: request.url, headers: Object.fromEntries(request.headers), body: await request.text() });
    const answer = answers[sent.length - 1];
    if (!answer) throw new Error(`unexpected request ${sent.length}`);
    return answer();
  }) as typeof globalThis.fetch;
  return { fetch, sent };
}
const json = (status: number, body: unknown, headers: Record<string, string> = {}) => () =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...headers } });
const refused = (marker?: string) => json(403, { code: "permission_denied", message: "request rejected" }, marker ? { "x-bff-rejection": marker } : {});
const marked = refused("csrf");
const unmarked = refused();
const unauthenticated = json(401, { code: "unauthenticated", message: "sign in required" });
const marked_ride = json(200, { line: { lineCode: "sh-1", lineName: "1号线", riddenYear: 2020 } });

function setup(parts: Partial<Parameters<typeof createApiTransport>[0]> & { fetch: typeof globalThis.fetch }) {
  let token = "token-1";
  const refreshes: string[] = [];
  const transport = createApiTransport({
    baseUrl: BASE,
    csrf: () => token,
    refresh: async () => {
      refreshes.push(token);
      token = "token-2";
    },
    account: () => ACCOUNT_A,
    ...parts,
  });
  return { rider: createClient(RiderService, transport), auth: createClient(AuthService, transport), refreshes };
}

test("a marked CSRF refusal of a write is repeated once: same body, same account, only the token changes", async () => {
  const { fetch, sent } = scripted([marked, marked_ride]);
  const { rider, refreshes } = setup({ fetch });
  const reply = await rider.markLine({ lineCode: "sh-1", riddenYear: 2020 });
  assert.equal(reply.line?.lineCode, "sh-1");
  assert.equal(sent.length, 2);
  const [first, second] = sent;
  assert.equal(first.url, `${BASE}/bff.metro.v1.RiderService/MarkLine`);
  assert.equal(first.body, second.body, "the same message, byte for byte");
  assert.match(first.body, /sh-1/);
  assert.equal(first.headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(second.headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(first.headers["x-csrf-token"], "token-1");
  assert.equal(second.headers["x-csrf-token"], "token-2");
  assert.deepEqual(refreshes, ["token-1"]);
});

test("the page's account may change while the token is being fetched: the repeat still names the original", async () => {
  let current = ACCOUNT_A;
  const { fetch, sent } = scripted([marked, unauthenticated]);
  const { rider } = setup({
    fetch,
    account: () => current,
    refresh: async () => {
      current = ACCOUNT_B; // another tab signed in as B while the new token was being issued
    },
  });
  await assert.rejects(rider.markLine({ lineCode: "sh-1" }), (e) => ConnectError.from(e).code === Code.Unauthenticated);
  assert.equal(sent[0].headers["x-kongken-user"], ACCOUNT_A);
  assert.equal(sent[1].headers["x-kongken-user"], ACCOUNT_A, "never rewritten to B");
});

test("a refusal without the marker is one request and no token: Origin, perimeter, role and business refusals look alike", async () => {
  const { fetch, sent } = scripted([unmarked]);
  const { rider, refreshes } = setup({ fetch });
  await assert.rejects(rider.markLine({ lineCode: "sh-1" }), (e) => ConnectError.from(e).code === Code.PermissionDenied && !isCsrfRefusal(e));
  assert.equal(sent.length, 1);
  assert.deepEqual(refreshes, []);
});

test("a second marked refusal is the answer", async () => {
  const { fetch, sent } = scripted([marked, marked]);
  const { rider, refreshes } = setup({ fetch });
  await assert.rejects(rider.markLine({ lineCode: "sh-1" }), (e) => isCsrfRefusal(e));
  assert.equal(sent.length, 2);
  assert.equal(refreshes.length, 1);
});

test("a read refused without the marker is not repeated either", async () => {
  const { fetch, sent } = scripted([unmarked]);
  const { rider, refreshes } = setup({ fetch });
  await assert.rejects(rider.listMyLines({}));
  assert.equal(sent.length, 1);
  assert.deepEqual(refreshes, []);
});

test("Me does not carry the account guard: it is how the page learns who it is", async () => {
  const { fetch, sent } = scripted([json(200, { user: { id: ACCOUNT_B, displayName: "B" }, roles: [] })]);
  const { auth } = setup({ fetch });
  const me = await auth.me({});
  assert.equal(me.user?.id, ACCOUNT_B);
  assert.equal(sent[0].headers["x-kongken-user"], undefined);
});

test("every other call names the account the page believes it is, and nobody when signed out", async () => {
  const signedIn = scripted([json(200, { cities: [], stats: {} })]);
  await setup({ fetch: signedIn.fetch }).rider.listMyLines({});
  assert.equal(signedIn.sent[0].headers["x-kongken-user"], ACCOUNT_A);
  const signedOut = scripted([json(200, { cities: [], stats: {} })]);
  await setup({ fetch: signedOut.fetch, account: () => undefined }).rider.listMyLines({});
  assert.equal(signedOut.sent[0].headers["x-kongken-user"], undefined);
});

test("a call that names its own account keeps it", async () => {
  const { fetch, sent } = scripted([json(200, { cities: [], stats: {} })]);
  await setup({ fetch }).rider.listMyLines({}, { headers: { "X-Kongken-User": ACCOUNT_B } });
  assert.equal(sent[0].headers["x-kongken-user"], ACCOUNT_B);
});
