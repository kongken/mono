import assert from "node:assert/strict";
import { test } from "node:test";
import { Code, ConnectError } from "@connectrpc/connect";
import { classifyAccountError, LOGIN_FAILURE } from "./errors.ts";

const err = (code: Code, message = "") => new ConnectError(message, code);

test("every login failure reads the same and never hints at the account", () => {
  const f = classifyAccountError(err(Code.Unauthenticated, LOGIN_FAILURE), "login");
  assert.deepEqual(f, { kind: "credentials", message: LOGIN_FAILURE });
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "whatever"), "login").message, LOGIN_FAILURE);
});

test("a wrong current password is told apart from a lost session", () => {
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "current password is incorrect"), "change-password").kind, "wrong_password");
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "current password is incorrect"), "reauthenticate").kind, "wrong_password");
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "sign in required"), "change-password").kind, "not_signed_in");
});

test("unusable mailed links collapse into one message, whatever the cause", () => {
  assert.equal(classifyAccountError(err(Code.FailedPrecondition, "link is not usable"), "register-complete").kind, "link_invalid");
  assert.equal(classifyAccountError(err(Code.FailedPrecondition, "link is not usable"), "reset-complete").kind, "link_invalid");
  assert.equal(classifyAccountError(err(Code.AlreadyExists, "email already registered"), "register-complete").kind, "link_invalid");
  assert.equal(classifyAccountError(err(Code.InvalidArgument, "token: invalid format"), "reset-complete").kind, "link_invalid");
});

test("closed sign-ups, recent-password checks and rate limits have their own kinds", () => {
  assert.equal(classifyAccountError(err(Code.FailedPrecondition, "registration is closed"), "register-request").kind, "registration_closed");
  assert.equal(classifyAccountError(err(Code.FailedPrecondition, "enter your password again to continue"), "delete").kind, "reauth_required");
  assert.equal(classifyAccountError(err(Code.ResourceExhausted, "too many requests, try again later"), "login").kind, "rate_limited");
});

test("field problems point at their field", () => {
  assert.deepEqual(classifyAccountError(err(Code.InvalidArgument, "email: invalid format"), "register-request"), { kind: "validation", field: "email", message: "邮箱地址格式不正确" });
  assert.equal(classifyAccountError(err(Code.InvalidArgument, "new_password: must be at least 12 characters"), "change-password").field, "newPassword");
  assert.equal(classifyAccountError(err(Code.InvalidArgument, "display_name: too long"), "profile").field, "displayName");
  assert.equal(classifyAccountError(err(Code.InvalidArgument, "avatar_url: invalid format"), "profile").field, "avatarUrl");
  assert.equal(classifyAccountError(err(Code.InvalidArgument, "something else"), "profile").field, undefined);
});

test("no connection, an unavailable service and a lost deletion answer are different", () => {
  assert.equal(classifyAccountError(new TypeError("Failed to fetch"), "login").kind, "network");
  assert.equal(classifyAccountError(err(Code.Unavailable, "account service unavailable"), "login").kind, "unavailable");
  assert.equal(classifyAccountError(err(Code.Internal, "internal error"), "login").kind, "unavailable");
  const lost = classifyAccountError(err(Code.Unavailable, "cleanup incomplete"), "delete");
  assert.equal(lost.kind, "unavailable");
  assert.match(lost.message, /重试注销/);
  assert.match(classifyAccountError(new TypeError("Failed to fetch"), "delete").message, /重试注销/);
});

test("management refusals keep their meaning: lost session, old password check, missing role, closed perimeter", () => {
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "sign in required")).kind, "not_signed_in");
  assert.equal(classifyAccountError(err(Code.Unauthenticated, "sign in again to use this feature")).kind, "reauth_required");
  assert.equal(classifyAccountError(err(Code.PermissionDenied, "required role missing")).kind, "forbidden");
  assert.equal(classifyAccountError(err(Code.PermissionDenied, "permission denied")).kind, "forbidden");
  assert.equal(classifyAccountError(err(Code.PermissionDenied, "request rejected")).kind, "not_allowed");
  assert.equal(classifyAccountError(err(Code.PermissionDenied, "403 Forbidden")).kind, "not_allowed");
  assert.equal(classifyAccountError(err(Code.PermissionDenied, "something else")).kind, "other");
  assert.equal(classifyAccountError(err(Code.ResourceExhausted, "too many requests, try again later")).kind, "rate_limited");
});

test("a platform administrator is told why the web cannot close the account, and it is not a lost answer", () => {
  const f = classifyAccountError(err(Code.FailedPrecondition, "platform administrator accounts cannot be closed here; an operator must revoke the role first"), "delete");
  assert.equal(f.kind, "platform_admin");
  assert.match(f.message, /平台管理员/);
});

test("an administrator's change keeps every refusal apart and never hides an unconfirmed write", () => {
  const kind = (c: Code, m: string) => classifyAccountError(err(c, m), "admin-change").kind;
  assert.equal(kind(Code.Aborted, "account changed since it was read"), "conflict");
  assert.equal(kind(Code.FailedPrecondition, "platform administrators cannot be changed here"), "target_protected");
  assert.equal(kind(Code.FailedPrecondition, "account is not active"), "target_state");
  assert.equal(kind(Code.AlreadyExists, "mutation id used for a different request"), "mutation_reused");
  assert.equal(kind(Code.FailedPrecondition, "enter your password again to continue"), "reauth_required");
  assert.equal(kind(Code.Unauthenticated, "sign in again to use this feature"), "reauth_required");
  assert.equal(kind(Code.PermissionDenied, "required role missing"), "forbidden");
  assert.equal(kind(Code.PermissionDenied, "request rejected"), "not_allowed");
  assert.equal(kind(Code.ResourceExhausted, "too many requests, try again later"), "rate_limited");
  assert.equal(kind(Code.InvalidArgument, "reason: must be 1-200 characters"), "validation");
  // Without an answer the change may have happened: the text says so and offers the same-ID retry.
  for (const code of [Code.DeadlineExceeded, Code.Unavailable, Code.Unknown]) {
    const f = classifyAccountError(err(code, "x"), "admin-change");
    assert.match(f.message, /不一定已经生效/);
    assert.match(f.message, /同一个操作编号/);
  }
  // The same texts mean nothing outside this action.
  assert.notEqual(classifyAccountError(err(Code.Aborted, "account changed since it was read")).kind, "conflict");
  assert.notEqual(classifyAccountError(err(Code.FailedPrecondition, "platform administrators cannot be changed here")).kind, "target_protected");
});
