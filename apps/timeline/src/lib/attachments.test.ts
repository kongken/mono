import assert from "node:assert/strict";
import { test } from "node:test";
import { MAX_FILE_BYTES, explainUploadFailure, fileSize, isImage, refuseFiles, uploadStatusText, type UploadItem } from "./attachments.ts";

const fake = (name: string, type: string, bytes: number) => new File([new Uint8Array(bytes)], name, { type });

test("the four types the service accepts are attached, anything else is refused by name", () => {
  for (const type of ["image/jpeg", "image/png", "image/webp", "application/pdf"]) {
    assert.equal(refuseFiles([fake("a", type, 10)], 0), null, type);
  }
  assert.equal(refuseFiles([fake("notes.txt", "text/plain", 10)], 0), "「notes.txt」不是 JPEG / PNG / WebP / PDF。");
  // Browsers report an unknown extension as the empty type: that is a refusal too, not a silent pass.
  assert.equal(refuseFiles([fake("scan.heic", "", 10)], 0), "「scan.heic」不是 JPEG / PNG / WebP / PDF。");
});

test("the ceiling is ten mebibytes per file and twenty per record, counted with what is already attached", () => {
  assert.equal(refuseFiles([fake("ok.png", "image/png", MAX_FILE_BYTES)], 0), null);
  assert.equal(refuseFiles([fake("big.png", "image/png", MAX_FILE_BYTES + 1)], 0), "「big.png」超过 10 MiB。");
  assert.equal(refuseFiles([fake("empty.png", "image/png", 0)], 0), "「empty.png」是空文件。");

  const twenty = Array.from({ length: 20 }, (_value, index) => fake(`${index}.png`, "image/png", 4));
  assert.equal(refuseFiles(twenty, 0), null);
  assert.equal(refuseFiles(twenty.slice(0, 3), 18), "一条记录最多 20 个附件，这次会变成 21 个。");
});

test("only images are shown in place; a PDF is handed to the browser", () => {
  assert.equal(isImage("image/webp"), true);
  assert.equal(isImage("image/jpeg"), true);
  assert.equal(isImage("application/pdf"), false);
});

test("a size reads as a size, and a file says what happened to it", () => {
  assert.equal(fileSize(823), "823 B");
  assert.equal(fileSize(1024), "1 KiB");
  assert.equal(fileSize(1024 * 1024 * 1.4), "1.4 MiB");

  const item = (status: UploadItem["status"], message?: string): UploadItem => ({ file: fake("a.png", "image/png", 2048), status, message });
  assert.equal(uploadStatusText(item("pending")), "2 KiB · 待上传");
  assert.equal(uploadStatusText(item("uploading")), "上传中…");
  assert.equal(uploadStatusText(item("done")), "已附上");
  assert.equal(uploadStatusText(item("failed")), "没传上去");
  assert.equal(uploadStatusText(item("failed", "HTTP 500")), "没传上去：HTTP 500");
});

test("an expired upload link is explained as retryable, an unknown refusal is passed through", () => {
  assert.match(explainUploadFailure("UPLOAD_EXPIRED: the upload was reclaimed"), /上传链接已过期/);
  assert.match(explainUploadFailure('upload denied: signature mismatch'), /上传链接失效/);
  assert.equal(explainUploadFailure("HTTP 500"), "HTTP 500");
});
