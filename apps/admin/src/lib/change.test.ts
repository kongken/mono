import assert from "node:assert/strict";
import { test } from "node:test";
import { CHANGES, checkReason, newMutationId, offerFor } from "./change.ts";

test("a platform administrator is never offered a change; a closing account is read-only", () => {
  assert.deepEqual(offerFor({ status: "active", roles: ["ozid-admin"] }), { kind: "protected" });
  assert.deepEqual(offerFor({ status: "disabled", roles: ["ozid-admin", "metro-admin"] }), { kind: "protected" });
  assert.deepEqual(offerFor({ status: "deleting", roles: [] }), { kind: "closing" });
  assert.deepEqual(offerFor({ status: "deleting", roles: ["metro-admin"] }), { kind: "closing" });
  assert.deepEqual(offerFor({ status: "unknown", roles: [] }), { kind: "unknown" });
});

test("an ordinary account gets the opposite of its state, and a role only when in good standing", () => {
  assert.deepEqual(offerFor({ status: "active", roles: [] }), { kind: "actions", status: "disable", role: "grant", roleBlocked: null });
  assert.deepEqual(offerFor({ status: "active", roles: ["metro-admin"] }), { kind: "actions", status: "disable", role: "revoke", roleBlocked: null });
  const disabled = offerFor({ status: "disabled", roles: [] });
  assert.equal(disabled.kind, "actions");
  if (disabled.kind === "actions") {
    assert.equal(disabled.status, "enable");
    assert.match(disabled.roleBlocked ?? "", /先启用/);
  }
  // A disabled metro-admin may still lose the role.
  assert.deepEqual(offerFor({ status: "disabled", roles: ["metro-admin"] }), { kind: "actions", status: "enable", role: "revoke", roleBlocked: null });
});

test("every change says what it does to the sessions and tokens", () => {
  for (const c of Object.values(CHANGES)) {
    assert.ok(c.impact.length > 0 && c.label && c.confirm && c.done);
    assert.match(c.impact.join(""), /重新登录|令牌/);
  }
  assert.match(CHANGES.enable.impact.join(""), /不会恢复/);
  assert.match(CHANGES.disable.after({ status: "active", roles: ["metro-admin"] }), /已禁用.*地铁目录管理员/);
  assert.match(CHANGES.revoke.after({ status: "active", roles: ["metro-admin"] }), /角色：无/);
  assert.match(CHANGES.grant.after({ status: "active", roles: [] }), /地铁目录管理员/);
});

test("the reason follows the server's rules", () => {
  assert.equal(checkReason("工单 4521：用户申请"), null);
  assert.equal(checkReason("  ok  "), null);
  assert.equal(checkReason("字".repeat(200)), null);
  for (const bad of ["", "   ", "字".repeat(201), "a\nb", "a\u0000b", "联系 bob@example.com", `key ${"a1".repeat(20)}`, "used mcp_abc"]) {
    assert.notEqual(checkReason(bad), null, JSON.stringify(bad));
  }
});

test("operation IDs fit the server's alphabet and differ every time", () => {
  const ids = new Set(Array.from({ length: 50 }, () => newMutationId()));
  assert.equal(ids.size, 50);
  for (const id of ids) assert.match(id, /^[A-Za-z0-9._-]{1,64}$/);
  assert.match(newMutationId(() => "a b/c:d"), /^abcd$/);
  assert.equal(newMutationId(() => "x".repeat(100)).length, 64);
});
