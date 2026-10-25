import test from "node:test";
import assert from "node:assert/strict";
import { Code, ConnectError } from "@connectrpc/connect";
import { csrfRetry } from "../src/lib/csrf-retry.ts";

const denied = () => new ConnectError("request rejected", Code.PermissionDenied);
const call = (interceptor, next, req = {}) => interceptor(next)(req);

test("a refused write is repeated once with a fresh token", async () => {
  let refreshed = 0;
  let sent = 0;
  const reply = await call(
    csrfRetry(async () => void refreshed++),
    async () => {
      if (++sent === 1) throw denied();
      return "ok";
    },
  );
  assert.equal(reply, "ok");
  assert.equal(refreshed, 1);
  assert.equal(sent, 2);
});

test("a second refusal is the answer; nothing is repeated again", async () => {
  let sent = 0;
  await assert.rejects(
    call(csrfRetry(async () => {}), async () => {
      sent++;
      throw denied();
    }),
    (e) => ConnectError.from(e).code === Code.PermissionDenied,
  );
  assert.equal(sent, 2);
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
    call(csrfRetry(async () => { throw new Error("offline"); }), async () => { sent++; throw denied(); }),
    (e) => ConnectError.from(e).code === Code.PermissionDenied && ConnectError.from(e).rawMessage === "request rejected",
  );
  assert.equal(sent, 1);
});

test("streaming calls are not repeated", async () => {
  let sent = 0;
  await assert.rejects(call(csrfRetry(async () => assert.fail("no token")), async () => { sent++; throw denied(); }, { stream: true }));
  assert.equal(sent, 1);
});
