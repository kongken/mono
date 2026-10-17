import { test, expect } from "@playwright/test";
import { fakeBackend, login } from "./fake-backend";

test("private media uploads, retries confirmation, downloads and deletes on desktop and mobile", async ({
  page,
}) => {
  const backend = fakeBackend(page, { visits: [] });
  await backend.install();
  const files = new Map<string, Record<string, unknown>>();
  const uploadIds: string[] = [];
  let confirmations = 0,
    putBearer: string | undefined;
  const pixel =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jJ1sAAAAASUVORK5CYII=";
  await page.route("**/upload-test", async (route) => {
    putBearer = route.request().headers().authorization;
    await route.fulfill({ status: 200, body: "" });
  });
  await page.route("**/api/bff.tripmap.v1.MediaService/**", async (route) => {
    const method = route.request().url().split("/").pop(),
      body = route.request().postDataJSON();
    let result: unknown = {};
    if (method === "ListMedia")
      result = {
        media: [...files.values()].filter(
          (m) => !body.regionCode || m.regionCode === body.regionCode,
        ),
      };
    if (method === "PrepareUpload") {
      uploadIds.push(body.uploadId);
      const media = files.get("one") ?? {
        id: "one",
        regionCode: body.regionCode,
        filename: body.filename,
        contentType: body.contentType,
        sizeBytes: body.sizeBytes,
        status: "pending",
        width: 0,
        height: 0,
      };
      files.set("one", media);
      result = {
        media,
        uploadUrl: "http://127.0.0.1:4174/upload-test",
        headers: { "Content-Type": "image/png" },
      };
    }
    if (method === "CompleteUpload") {
      confirmations++;
      // The first response is lost after the server has committed.
      files.set("one", {
        ...files.get("one"),
        status: "ready",
        filename: "西湖.jpg",
        contentType: "image/jpeg",
        width: 1,
        height: 1,
      });
      if (confirmations === 1) {
        await route.fulfill({
          status: 503,
          json: { code: "unavailable", message: "response lost" },
        });
        return;
      }
      result = { media: files.get("one") };
    }
    if (method === "ReadMedia")
      result = { data: pixel, contentType: "image/png", filename: "西湖.jpg" };
    if (method === "DeleteMedia") files.delete(body.id);
    await route.fulfill({ status: 200, json: result });
  });
  await login(page);
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "照片与附件", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "照片与附件" });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("媒体目的地").selectOption("330100");
  const input = dialog.getByLabel("上传照片或附件");
  await input.setInputFiles({
    name: "bad.svg",
    mimeType: "image/svg+xml",
    buffer: Buffer.from("<svg/>"),
  });
  await expect(dialog.getByRole("alert")).toContainText("请选择 10 MB 以内");
  expect(uploadIds).toHaveLength(0);
  await input.setInputFiles({
    name: "西湖.png",
    mimeType: "image/png",
    buffer: Buffer.from(pixel, "base64"),
  });
  await expect(dialog.getByRole("button", { name: "重试上传" })).toBeVisible();
  await dialog.getByRole("button", { name: "重试上传" }).click();
  await expect(dialog.getByText("已保存，照片的元数据已移除")).toBeVisible();
  expect(new Set(uploadIds).size).toBe(1);
  expect(files.size).toBe(1);
  expect(putBearer).toBeUndefined();
  await expect(dialog.getByRole("img", { name: "西湖.jpg" })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "下载 西湖.jpg" }).click();
  expect((await download).suggestedFilename()).toBe("西湖.jpg");
  await dialog.getByRole("button", { name: "删除 西湖.jpg" }).click();
  await dialog.getByRole("button", { name: "保留", exact: true }).click();
  expect(files.size).toBe(1);
  await dialog.getByRole("button", { name: "删除 西湖.jpg" }).click();
  await dialog.getByRole("button", { name: "确认删除媒体" }).click();
  await expect(
    dialog.getByText("还没有照片或附件，把旅途中的记忆留在这里。"),
  ).toBeVisible();
  expect(files.size).toBe(0);
});
