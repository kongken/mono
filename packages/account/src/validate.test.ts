import assert from "node:assert/strict";
import { test } from "node:test";
import {
  checkAvatarUrl,
  checkDisplayName,
  checkEmail,
  checkExistingPassword,
  checkNewPassword,
  checkPasswordConfirmation,
  newMutationId,
  trimEmail,
} from "./validate.ts";

test("emails: one ASCII address, trimmed, no display names or international forms", () => {
  assert.equal(checkEmail("  Alice+tag@Example.com "), null);
  assert.equal(checkEmail("a.lice@example.co.uk"), null);
  for (const bad of ["", "   ", "alice", "alice@", "@example.com", "a@b", "a@@b.com", "a b@example.com", "alice@example.com, bob@example.com", "Alice <a@example.com>", "ａ@example.com", "alice@例え.jp", "a..b@example.com", ".a@example.com", "a@-bad.example.com", "a@exa_mple.com"])
    assert.notEqual(checkEmail(bad), null, bad);
  assert.notEqual(checkEmail("a\r\n@example.com"), null);
  assert.notEqual(checkEmail(`${"a".repeat(65)}@example.com`), null);
  assert.notEqual(checkEmail(`a@${"b".repeat(250)}.com`), null);
  assert.equal(trimEmail("\t a@b.co \t"), "a@b.co");
});

test("new passwords count code points, not bytes, and are never trimmed", () => {
  assert.notEqual(checkNewPassword(""), null);
  assert.notEqual(checkNewPassword("a".repeat(11)), null);
  assert.equal(checkNewPassword("a".repeat(12)), null);
  assert.equal(checkNewPassword(" ".repeat(12)), null, "spaces are characters too");
  assert.equal(checkNewPassword("密".repeat(12)), null, "12 characters of 3 bytes each");
  assert.equal(checkNewPassword("😀".repeat(12)), null, "emoji count once");
  assert.equal(checkNewPassword("a".repeat(128)), null);
  assert.notEqual(checkNewPassword("a".repeat(129)), null);
  // 128 four-byte characters are 512 bytes: still allowed; one more byte is not.
  assert.equal(checkNewPassword("😀".repeat(128)), null);
  assert.notEqual(checkNewPassword("😀".repeat(127) + "abcd"), null, "over 512 bytes");
});

test("an existing password has no minimum, so an older shorter one still reaches the server", () => {
  assert.equal(checkExistingPassword("short"), null);
  assert.notEqual(checkExistingPassword(""), null);
  assert.notEqual(checkExistingPassword("a".repeat(129)), null);
});

test("confirmation must match exactly", () => {
  assert.equal(checkPasswordConfirmation("abc def ghi jkl", "abc def ghi jkl"), null);
  assert.notEqual(checkPasswordConfirmation("abc def ghi jkl", "abc def ghi jkl "), null);
});

test("display names: 1-40 characters, no control characters; empty only where a default exists", () => {
  assert.equal(checkDisplayName("旅行者", false), null);
  assert.notEqual(checkDisplayName("   ", false), null);
  assert.equal(checkDisplayName("   ", true), null);
  assert.equal(checkDisplayName("名".repeat(40), false), null);
  assert.notEqual(checkDisplayName("名".repeat(41), false), null);
  assert.notEqual(checkDisplayName("a\u0007b", false), null);
  assert.notEqual(checkDisplayName("a\u2028b", false), null);
});

test("avatar URLs: empty or https without credentials", () => {
  assert.equal(checkAvatarUrl(""), null);
  assert.equal(checkAvatarUrl("https://cdn.example.com/a.png"), null);
  for (const bad of ["http://cdn.example.com/a.png", "https://user:pw@cdn.example.com/a.png", "https://cdn.example.com/a.png#x", "javascript:alert(1)", "https://cdn.example.com/a b.png", "//cdn.example.com/a.png", `https://e.com/${"a".repeat(500)}`])
    assert.notEqual(checkAvatarUrl(bad), null, bad);
});

test("deletion keys fit the server's 1-64 character alphabet", () => {
  const id = newMutationId();
  assert.match(id, /^[A-Za-z0-9._-]{1,64}$/);
  assert.notEqual(id, newMutationId());
});
