import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeFriendSettings,
  canEditSharedTrip,
  canManageSharedTripMembers,
  roleLabel,
  friendLabel,
  provinceNameByCode,
  friendComparisonColors,
  indexActiveSharedTripsByTripId,
} from "../src/lib/collaboration.ts";
import { Code, ConnectError } from "@connectrpc/connect";
import { describeApiError } from "../src/lib/errors.ts";

test("normalizeFriendSettings clears dates and notes when cities are hidden", () => {
  assert.deepEqual(
    normalizeFriendSettings({ showCities: false, showDates: true, showNotes: true }),
    { showCities: false, showDates: false, showNotes: false },
  );
  assert.deepEqual(
    normalizeFriendSettings({ showCities: true, showDates: true, showNotes: false }),
    { showCities: true, showDates: true, showNotes: false },
  );
});

test("shared trip permission helpers enforce owner, editor and viewer boundaries", () => {
  assert.equal(canEditSharedTrip("owner", "active"), true);
  assert.equal(canEditSharedTrip("editor", "active"), true);
  assert.equal(canEditSharedTrip("viewer", "active"), false);
  assert.equal(canEditSharedTrip("editor", "invited"), false);

  assert.equal(canManageSharedTripMembers("owner", "active"), true);
  assert.equal(canManageSharedTripMembers("editor", "active"), false);
  assert.equal(canManageSharedTripMembers("viewer", "active"), false);
  assert.equal(canManageSharedTripMembers("owner", "invited"), false);
});

test("labels, province names, comparison colors and active trip index work consistently", () => {
  assert.equal(roleLabel("owner"), "创建者");
  assert.equal(roleLabel("editor"), "共同编辑");
  assert.equal(roleLabel("viewer"), "只读成员");
  assert.equal(friendLabel({ displayName: "阿青" }), "阿青");
  assert.equal(friendLabel({ displayName: "", id: "u-1" }), "u-1");
  assert.equal(provinceNameByCode("330000"), "浙江省");

  const colors = friendComparisonColors({
    commonProvinces: ["330000"],
    onlyMeProvinces: ["320000"],
    onlyFriendProvinces: ["510000"],
  });
  assert.deepEqual(colors, {
    "330000": "#4b937d",
    "320000": "#719fc6",
    "510000": "#e2af5c",
  });

  const idx = indexActiveSharedTripsByTripId([
    { id: "st-1", regionCode: "330100", tripId: "t1", myRole: "viewer", myStatus: "active" },
    { id: "st-2", regionCode: "330200", tripId: "t2", myRole: "editor", myStatus: "invited" },
  ]);
  assert.equal(idx.get("330100:t1")?.id, "st-1");
  assert.equal(idx.has("330200:t2"), false);
});

test("collaboration API errors map to clear Chinese descriptions", () => {
  assert.equal(
    describeApiError(new ConnectError("user not found", Code.NotFound)),
    "未找到该用户，请检查用户名或好友邀请码",
  );
  assert.equal(
    describeApiError(new ConnectError("cannot add yourself as a friend", Code.InvalidArgument)),
    "不能添加自己为好友",
  );
  assert.equal(
    describeApiError(new ConnectError("shared trip not found", Code.NotFound)),
    "共同旅行不存在或已解散",
  );
});

