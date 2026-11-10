import { expect, test } from "@playwright/test";
import { entryJson, fakeBackend, mediaJson } from "./fake-backend.ts";

// A real one-pixel PNG, so a preview is an image a browser can actually decode rather than a stub.
const PNG = Buffer.from(
  "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000a49444154789c6360000002000154a24f9c0000000049454e44ae426082",
  "hex",
);

const today = () => {
  const clock = new Date();
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${clock.getFullYear()}-${pad(clock.getMonth() + 1)}-${pad(clock.getDate())}`;
};

/** Opens 记一笔, writes the least a record needs, and hands the drawer one file. */
async function draftWithFile(page: import("@playwright/test").Page, name: string, mimeType: string, buffer: Buffer) {
  await page.getByRole("button", { name: "写手账" }).click();
  const drawer = page.getByRole("dialog", { name: "记一笔" });
  await drawer.getByLabel(/今天发生了什么/).fill("验车票据");
  await drawer.getByLabel("日期").fill(today());
  await drawer.locator("#timeline-attachments").setInputFiles({ name, mimeType, buffer });
  return drawer;
}

test("a chosen file is signed, uploaded straight to the store, completed, and then hangs off the record", async ({ page }) => {
  const fake = await fakeBackend(page, { entries: [] });
  await page.goto("/");

  const drawer = await draftWithFile(page, "receipt.png", "image/png", PNG);
  await expect(drawer.getByText("receipt.png")).toBeVisible();
  await expect(drawer.getByText(/待上传/)).toBeVisible();
  await drawer.getByRole("button", { name: "保存记录" }).click();
  await expect(drawer).not.toBeVisible();

  // Three steps in order, the first and last of them writes with the CSRF header.
  expect(fake.state.calls).toContain("bff.timeline.v1.MediaService/PrepareUpload");
  expect(fake.state.calls).toContain("bff.timeline.v1.MediaService/CompleteUpload");
  const mediaWrites = fake.state.writes.filter((write) => write.procedure.startsWith("bff.timeline.v1.MediaService/"));
  expect(mediaWrites.map((write) => write.procedure)).toEqual([
    "bff.timeline.v1.MediaService/PrepareUpload",
    "bff.timeline.v1.MediaService/CompleteUpload",
  ]);
  for (const write of mediaWrites) expect(write.csrf).toMatch(/^csrf-/);
  // The prepare and the complete are the same intent: the service refuses a complete with another ID.
  expect(mediaWrites[0]?.body.mutationId).toBe(mediaWrites[1]?.body.mutationId);

  // The bytes went to the store, with the type the signature covers, and only once.
  expect(fake.state.uploads).toHaveLength(1);
  expect(fake.state.uploads[0]?.contentType).toBe("image/png");
  expect(fake.state.uploads[0]?.bytes).toBe(PNG.length);

  const chip = page.getByRole("button", { name: "打开附件「receipt.png」" });
  await expect(chip).toBeVisible();
  await expect(page.locator("article", { hasText: "验车票据" })).toContainText("1 个附件");

  // The list is the server's: a reload shows the attachment again, read back through ListMedia.
  await page.reload();
  await expect(page.getByRole("button", { name: "打开附件「receipt.png」" })).toBeVisible();
});

test("shows nonzero byte upload progress while a large attachment is uploading", async ({ page }) => {
  await fakeBackend(page, { entries: [] });
  await page.goto("/");
  const drawer = await draftWithFile(page, "large.png", "image/png", Buffer.alloc(8 * 1024 * 1024, 7));
  await drawer.getByRole("button", { name: "保存记录" }).click();
  await expect(drawer.getByText(/上传中 [1-9][0-9]*%/)).toBeVisible();
  await expect(drawer).not.toBeVisible();
});
test("a refused PUT keeps the record and re-uploads only the file, never the record twice", async ({ page }) => {
  const fake = await fakeBackend(page, { entries: [] });
  fake.state.uploadFailures = 1;
  await page.goto("/");

  const drawer = await draftWithFile(page, "receipt.png", "image/png", PNG);
  await drawer.getByRole("button", { name: "保存记录" }).click();

  // The record exists, the file does not: the drawer stays open with an error that says exactly that.
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText(/记录已经保存，但有 1 个附件没传上去/)).toBeVisible();
  await expect(drawer.getByText(/没传上去：上传失败/)).toBeVisible();
  const creates = () => fake.state.writes.filter((write) => write.procedure === "bff.timeline.v1.TimelineService/CreateEntry");
  expect(creates()).toHaveLength(1);

  await drawer.getByRole("button", { name: /重试上传（1 个）/ }).click();
  await expect(drawer).not.toBeVisible();
  await expect(page.getByRole("button", { name: "打开附件「receipt.png」" })).toBeVisible();
  expect(creates()).toHaveLength(1);
  expect(fake.state.uploads).toHaveLength(2);
});

test("an expired upload link is explained, and retrying signs a fresh one", async ({ page }) => {
  const fake = await fakeBackend(page, { entries: [] });
  fake.state.completeFailures = 1;
  await page.goto("/");

  const drawer = await draftWithFile(page, "receipt.png", "image/png", PNG);
  await drawer.getByRole("button", { name: "保存记录" }).click();
  await expect(drawer.getByText(/记录已经保存，但有 1 个附件没传上去/)).toBeVisible();
  await expect(drawer.getByText(/上传链接已过期/)).toBeVisible();

  await drawer.getByRole("button", { name: /重试上传（1 个）/ }).click();
  await expect(drawer).not.toBeVisible();
  await expect(page.getByRole("button", { name: "打开附件「receipt.png」" })).toBeVisible();
  expect(fake.state.calls.filter((call) => call === "bff.timeline.v1.MediaService/PrepareUpload")).toHaveLength(2);
});

test("an image is read privately into a preview, and a deleted attachment leaves the record alone", async ({ page }) => {
  const entry = entryJson({ id: "e1", title: "验车票据", date: today(), category: "交通" });
  const fake = await fakeBackend(page, {
    entries: [entry],
    media: [mediaJson({ mediaRef: "med_00000000000000000000000001", entryRef: entry.entryRef, filename: "invoice.png", body: PNG.toString("base64") })],
  });
  await page.goto("/");

  await page.getByRole("button", { name: "打开附件「invoice.png」" }).click();
  // The bytes came through ReadMedia with the session, and live only as an object URL in the page.
  const preview = page.locator("article figure img");
  await expect(preview).toHaveAttribute("src", /^blob:/);
  expect(fake.state.calls).toContain("bff.timeline.v1.MediaService/ReadMedia");

  await page.getByRole("button", { name: "删除附件「invoice.png」" }).click();
  await expect(page.getByRole("button", { name: "打开附件「invoice.png」" })).toHaveCount(0);
  await expect(page.getByText("附件已删除，存储空间会在后台回收。")).toBeVisible();
  // The record itself is untouched: only the attachment went away.
  await expect(page.locator("article", { hasText: "验车票据" })).toBeVisible();

  const deletion = fake.state.writes.find((write) => write.procedure === "bff.timeline.v1.MediaService/DeleteMedia");
  expect(deletion?.csrf).toMatch(/^csrf-/);
  expect(deletion?.body).toMatchObject({ mediaRef: "med_00000000000000000000000001", expectedRevision: "1" });
  expect(deletion?.body.mutationId).toBeTruthy();
  expect(fake.state.media[0]?.status).toBe("deleting");
});

test("a file the service would refuse is refused here first, by name", async ({ page }) => {
  const fake = await fakeBackend(page, { entries: [] });
  await page.goto("/");

  const drawer = await draftWithFile(page, "notes.txt", "text/plain", Buffer.from("hello"));
  await expect(drawer.getByText("「notes.txt」不是 JPEG / PNG / WebP / PDF。")).toBeVisible();
  // Nothing was chosen, so nothing is uploaded when the record is saved.
  await expect(drawer.getByText(/待上传/)).toHaveCount(0);
  await drawer.getByRole("button", { name: "保存记录" }).click();
  await expect(drawer).not.toBeVisible();
  await expect(page.locator("article", { hasText: "验车票据" })).toBeVisible();
  expect(fake.state.calls).not.toContain("bff.timeline.v1.MediaService/PrepareUpload");
});
