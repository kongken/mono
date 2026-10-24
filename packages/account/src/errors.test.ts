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
