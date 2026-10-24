import assert from "node:assert/strict";
import { test } from "node:test";
import { decodeQuotedPrintable, readMessage } from "./smtp.ts";

test("quoted-printable soft breaks and escapes decode back to the original text", () => {
  assert.equal(
    decodeQuotedPrintable("http://127.0.0.1:4174/account/verify-email#token=3Dabc=\r\ndef =E4=BD=A0=E5=A5=BD"),
    "http://127.0.0.1:4174/account/verify-email#token=abcdef 你好",
  );
  assert.equal(decodeQuotedPrintable("a=3Db"), "a=b");
});

test("a multipart message yields its plain-text part, whatever its transfer encoding", () => {
  const raw = [
    "Subject: =?UTF-8?B?x?=",
    'Content-Type: multipart/alternative; boundary="b1"',
    "",
    "--b1",
    "Content-Type: text/plain; charset=utf-8",
    "Content-Transfer-Encoding: quoted-printable",
    "",
    "open http://x/account/verify-email#token=3Dabc",
    "--b1",
    "Content-Type: text/html; charset=utf-8",
    "",
    "<p>ignored</p>",
    "--b1--",
    "",
  ].join("\r\n");
  const { text } = readMessage(raw);
  assert.match(text, /verify-email#token=abc/);
  assert.doesNotMatch(text, /ignored/);
});
