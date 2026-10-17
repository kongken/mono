import test from "node:test";
import assert from "node:assert/strict";
import { dependencyLabel, isStatusPath } from "../src/lib/status.ts";

test("only <base>status is the status page", () => {
  assert.equal(isStatusPath("/status"), true);
  assert.equal(isStatusPath("/status/"), true);
  for (const path of ["/", "/statuses", "/status/x", "/s/status", "/api/status", "/Status"]) assert.equal(isStatusPath(path), false, path);
  assert.equal(isStatusPath("/trip/status", "/trip/"), true);
  assert.equal(isStatusPath("/trip/status", "/trip"), true);
  assert.equal(isStatusPath("/status", "/trip/"), false);
});

test("known dependencies get readable names and unknown ones are shown as reported", () => {
  assert.equal(dependencyLabel("postgres"), "数据库");
  assert.equal(dependencyLabel("redis"), "缓存");
  assert.equal(dependencyLabel("s3"), "对象存储");
  assert.equal(dependencyLabel("custom"), "custom");
});
