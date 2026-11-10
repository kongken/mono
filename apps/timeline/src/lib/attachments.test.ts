import assert from "node:assert/strict";
import test from "node:test";
import { MAX_ATTACHMENT_BYTES, MAX_ATTACHMENTS, attachmentKind, formatAttachmentSize, validateAttachment } from "./attachments.ts";

const file = (type: string, size: number, name = "upload.bin") => ({ type, size, name } as File);

test("attachments accept the four supported formats and reject invalid size/type", () => {
  for (const type of ["image/jpeg", "image/png", "image/webp", "application/pdf"]) assert.equal(validateAttachment(file(type, 1)), undefined);
  assert.match(validateAttachment(file("text/plain", 1)) ?? "", /仅支持/);
  assert.match(validateAttachment(file("image/png", MAX_ATTACHMENT_BYTES + 1)) ?? "", /10 MiB/);
  assert.match(validateAttachment(file("image/png", 0)) ?? "", /空文件/);
  assert.equal(MAX_ATTACHMENTS, 20);
});

test("attachment display helpers preserve media type and readable size", () => {
  assert.equal(attachmentKind("application/pdf"), "pdf");
  assert.equal(attachmentKind("image/webp"), "image");
  assert.equal(formatAttachmentSize(1024 * 1024), "1.0 MiB");
});
