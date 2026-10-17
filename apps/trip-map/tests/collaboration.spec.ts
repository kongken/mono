import { test, expect, type Page } from "@playwright/test";
import { dongcheng, fakeBackend, green, gold, legacyId, login, tianjin, user, xicheng } from "./fake-backend";

const noOverflow = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

const friendBob = { id: "user-bob", username: "bob_traveler", displayName: "阿博" };
const friendCarol = { id: "user-carol", username: "carol_wander", displayName: "卡萝" };

type FriendSettings = {
  showCities: boolean;
  showDates: boolean;
  showNotes: boolean;
};

type FakeCollabOptions = {
  backend: ReturnType<typeof fakeBackend>;
};

function fakeCollaboration(page: Page, { backend }: FakeCollabOptions) {
  const state = {
    myFriendCode: "fc_ci_user_8899",
    friendSettings: {
      showCities: true,
      showDates: false,
      showNotes: false,
    } as FriendSettings,
    bobSettings: {
      showCities: true,
      showDates: true,
      showNotes: false,
    } as FriendSettings,
    friends: [
      {
        id: "fr-pending-in",
        friend: friendCarol,
        status: "pending",
        direction: "incoming",
        createdAt: "2026-10-14T01:00:00Z",
        updatedAt: "2026-10-14T01:00:00Z",
      },
    ] as Array<{
      id: string;
      friend: { id: string; username: string; displayName: string };
      status: string;
      direction: string;
      createdAt: string;
      updatedAt: string;
    }>,
    sharedTrips: [
      {
        id: "st-hangzhou-invite",
        tripId: "trip-hz-shared",
        regionCode: "330100",
        regionName: "杭州市",
        provinceCode: "330000",
        date: "2026-09-15",
        note: "西湖秋游",
        owner: friendBob,
        myRole: "editor",
        myStatus: "invited",
        members: [
          { user: friendBob, role: "owner", status: "active", joinedAt: "2026-10-14T01:00:00Z" },
          { user, role: "editor", status: "invited" },
        ],
        createdAt: "2026-10-14T01:00:00Z",
        updatedAt: "2026-10-14T01:00:00Z",
      },
    ] as Array<{
      id: string;
      tripId: string;
      regionCode: string;
      regionName: string;
      provinceCode: string;
      date: string;
      note: string;
      owner: { id: string; username: string; displayName: string };
      myRole: string;
      myStatus: string;
      members: Array<{
        user: { id: string; username: string; displayName: string };
        role: string;
        status: string;
        joinedAt?: string;
      }>;
      createdAt: string;
      updatedAt: string;
    }>,
    mediaBySharedTrip: new Map<string, Array<{
      media: {
        id: string;
        regionCode: string;
        tripId: string;
        filename: string;
        contentType: string;
        sizeBytes: string;
        width: number;
        height: number;
        status: string;
      };
      uploader: { id: string; username: string; displayName: string };
    }>>([
      [
        "st-hangzhou-invite",
        [
          {
            media: {
              id: "media-bob-1",
              regionCode: "330100",
              tripId: "trip-hz-shared",
              filename: "断桥残雪.jpg",
              contentType: "image/jpeg",
              sizeBytes: "1024",
              width: 100,
              height: 80,
              status: "ready",
            },
            uploader: friendBob,
          },
        ],
      ],
    ]),
  };

  // Minimal 1x1 PNG for thumbnail preview
  const onePixelPng = Buffer.from(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
    "base64",
  );

  return {
    state,
    install: async () => {
      await page.route("**/upload-target/**", (route) =>
        route.fulfill({ status: 200, body: "" }),
      );
      await page.route("**/api/bff.tripmap.v1.MediaService/**", async (route) => {
        const method = route.request().url().split("/").pop();
        const body = route.request().postDataJSON?.() ?? {};
        if (method === "PrepareUpload") {
          return route.fulfill({
            status: 200,
            json: {
              media: {
                id: "media-me-new",
                regionCode: body.regionCode ?? "330100",
                tripId: body.tripId ?? "trip-hz-shared",
                filename: body.filename ?? "雷峰塔.jpg",
                contentType: body.contentType ?? "image/jpeg",
                sizeBytes: String(body.sizeBytes ?? 1024),
                width: 100,
                height: 80,
                status: "pending",
              },
              uploadUrl: "http://localhost:4173/upload-target/media-me-new",
              headers: {},
            },
          });
        }
        if (method === "CompleteUpload") {
          const list = state.mediaBySharedTrip.get("st-hangzhou-invite") ?? [];
          list.push({
            media: {
              id: "media-me-new",
              regionCode: "330100",
              tripId: "trip-hz-shared",
              filename: "雷峰塔.jpg",
              contentType: "image/jpeg",
              sizeBytes: "1024",
              width: 100,
              height: 80,
              status: "ready",
            },
            uploader: user,
          });
          state.mediaBySharedTrip.set("st-hangzhou-invite", list);
          return route.fulfill({
            status: 200,
            json: {
              media: list[list.length - 1].media,
            },
          });
        }
        if (method === "ListMedia") {
          return route.fulfill({ status: 200, json: { media: [] } });
        }
        return route.fulfill({ status: 200, json: {} });
      });

      const handleFriendRpc = async (
        route: import("@playwright/test").Route,
        method: string | undefined,
        body: Record<string, unknown>,
      ) => {
        const ok = (json: unknown) => route.fulfill({ status: 200, json });
        switch (method) {
          case "GetCollaborationOverview":
            return ok({
              myFriendCode: state.myFriendCode,
              friendSettings: state.friendSettings,
              friends: state.friends,
              sharedTrips: state.sharedTrips,
            });
          case "UpdateFriendSettings": {
            const s = (body.settings ?? {}) as Record<string, unknown>;
            const showCities = Boolean(s.showCities);
            state.friendSettings = {
              showCities,
              showDates: showCities && Boolean(s.showDates),
              showNotes: showCities && Boolean(s.showNotes),
            };
            return ok({ settings: state.friendSettings });
          }
          case "SendFriendRequest": {
            const target = String(body.target ?? "").trim();
            if (target === "bob_traveler" || target === "fc_bob_001") {
              state.friends.push({
                id: "fr-bob",
                friend: friendBob,
                status: "accepted",
                direction: "outgoing",
                createdAt: "2026-10-14T02:00:00Z",
                updatedAt: "2026-10-14T02:00:00Z",
              });
              return ok({ friendship: state.friends[state.friends.length - 1] });
            }
            return route.fulfill({
              status: 404,
              json: { code: "not_found", message: "user not found" },
            });
          }
          case "RespondFriendRequest": {
            const idx = state.friends.findIndex((f) => f.id === body.friendshipId);
            if (idx < 0) {
              return route.fulfill({ status: 404, json: { code: "not_found", message: "friendship not found" } });
            }
            if (body.accept) {
              state.friends[idx] = { ...state.friends[idx], status: "accepted" };
              return ok({ friendship: state.friends[idx] });
            }
            state.friends.splice(idx, 1);
            return ok({});
          }
          case "RemoveFriend": {
            const idx = state.friends.findIndex((f) => f.id === body.friendshipId);
            if (idx >= 0) state.friends.splice(idx, 1);
            return ok({});
          }
          case "GetFriendFootprints": {
            const friendEntry = state.friends.find(
              (f) => f.friend.id === body.friendUserId && f.status === "accepted",
            );
            if (!friendEntry) {
              return route.fulfill({
                status: 403,
                json: { code: "permission_denied", message: "only confirmed friends can view footprints" },
              });
            }
            const bothCities = state.friendSettings.showCities && state.bobSettings.showCities;
            const content = {
              v: 1,
              published_on: "2026-10-14",
              fields: {
                cities: state.bobSettings.showCities,
                dates: state.bobSettings.showCities && state.bobSettings.showDates,
                notes: state.bobSettings.showCities && state.bobSettings.showNotes,
                photos: false,
              },
              stats: {
                explored_provinces: 2,
                total_provinces: 34,
                cities: state.bobSettings.showCities ? 2 : 0,
                total_cities: 391,
                trips: state.bobSettings.showCities ? 2 : 0,
                by_province: [
                  { code: "110000", cities: 1, total: 16 },
                  { code: "330000", cities: 1, total: 11 },
                ],
              },
              provinces: ["110000", "330000"],
              ...(state.bobSettings.showCities
                ? {
                    cities: [
                      {
                        code: "110101",
                        name: "东城区",
                        province: "110000",
                        color: gold,
                        trips: [
                          {
                            ...(state.bobSettings.showDates ? { date: "2026-05-02" } : {}),
                            ...(state.bobSettings.showNotes ? { note: "阿博的故宫笔记" } : {}),
                          },
                        ],
                      },
                      {
                        code: "330100",
                        name: "杭州市",
                        province: "330000",
                        color: green,
                        trips: [
                          {
                            ...(state.bobSettings.showDates ? { date: "2026-09-15" } : {}),
                          },
                        ],
                      },
                    ],
                  }
                : {}),
            };
            return ok({
              friend: friendEntry.friend,
              friendSettings: state.bobSettings,
              contentJson: JSON.stringify(content),
              comparison: {
                commonProvinces: ["110000"],
                onlyMeProvinces: ["120000"],
                onlyFriendProvinces: ["330000"],
                cityComparisonAvailable: bothCities,
                commonCities: bothCities
                  ? [{ code: "110101", name: "东城区", province: "110000", myColor: green, friendColor: gold }]
                  : [],
                myOnlyCityCount: bothCities ? 2 : 0,
                friendOnlyCityCount: bothCities ? 1 : 0,
              },
            });
          }
          default:
            return route.fulfill({ status: 501, json: { code: "unimplemented", message: method } });
        }
      };

      await page.route("**/api/bff.tripmap.v1.FriendService/**", async (route) => {
        const method = route.request().url().split("/").pop();
        const body = route.request().postDataJSON?.() ?? {};
        await handleFriendRpc(route, method, body);
      });

      await page.route("**/api/bff.tripmap.v1.SharedTripService/**", async (route) => {
        const method = route.request().url().split("/").pop();
        const body = route.request().postDataJSON?.() ?? {};
        const ok = (json: unknown) => route.fulfill({ status: 200, json });

        switch (method) {
          case "InviteToSharedTrip": {
            const friendEntry = state.friends.find(
              (f) => f.friend.id === body.friendUserId && f.status === "accepted",
            );
            if (!friendEntry) {
              return route.fulfill({
                status: 403,
                json: { code: "permission_denied", message: "can only invite confirmed friends" },
              });
            }
            let st = state.sharedTrips.find(
              (t) => t.regionCode === body.regionCode && t.tripId === body.tripId,
            );
            const tripRecord = backend.state.trips.get(body.tripId);
            const visitRecord = backend.state.visits.get(body.regionCode);
            if (!st) {
              st = {
                id: `st-${body.tripId}`,
                tripId: body.tripId,
                regionCode: body.regionCode,
                regionName: visitRecord?.name ?? "东城区",
                provinceCode: visitRecord?.province ?? "110000",
                date: tripRecord?.date ?? "2026-05-01",
                note: tripRecord?.note ?? "",
                owner: user,
                myRole: "owner",
                myStatus: "active",
                members: [
                  { user, role: "owner", status: "active", joinedAt: "2026-10-14T02:00:00Z" },
                ],
                createdAt: "2026-10-14T02:00:00Z",
                updatedAt: "2026-10-14T02:00:00Z",
              };
              state.sharedTrips.push(st);
            }
            const existingMember = st.members.find((m) => m.user.id === friendEntry.friend.id);
            if (!existingMember) {
              st.members.push({
                user: friendEntry.friend,
                role: body.role || "editor",
                status: "active",
                joinedAt: "2026-10-14T02:01:00Z",
              });
            }
            return ok({ sharedTrip: st });
          }

          case "RespondSharedTripInvite": {
            const st = state.sharedTrips.find((t) => t.id === body.sharedTripId);
            if (!st) {
              return route.fulfill({ status: 404, json: { code: "not_found", message: "shared trip not found" } });
            }
            if (!body.accept) {
              state.sharedTrips = state.sharedTrips.filter((t) => t.id !== body.sharedTripId);
              return ok({ footprintRevision: String(backend.state.revision) });
            }
            st.myStatus = "active";
            st.members = st.members.map((m) =>
              m.user.id === user.id ? { ...m, status: "active", joinedAt: "2026-10-14T02:05:00Z" } : m,
            );
            if (!backend.state.visits.has(st.regionCode)) {
              backend.otherDeviceCity({
                code: st.regionCode,
                name: st.regionName,
                province: st.provinceCode,
                color: green,
              });
            }
            backend.otherDeviceTrip({
              id: st.tripId,
              code: st.regionCode,
              date: st.date,
              note: st.note,
            });
            return ok({
              sharedTrip: st,
              footprintRevision: String(backend.state.revision),
            });
          }

          case "UpdateSharedTrip": {
            const st = state.sharedTrips.find((t) => t.id === body.sharedTripId);
            if (!st) {
              return route.fulfill({ status: 404, json: { code: "not_found", message: "shared trip not found" } });
            }
            if (st.myRole === "viewer") {
              return route.fulfill({
                status: 403,
                json: { code: "permission_denied", message: "shared trip viewer is read-only" },
              });
            }
            st.date = body.date ?? "";
            st.note = body.note ?? "";
            backend.otherDeviceTrip({
              id: st.tripId,
              code: st.regionCode,
              date: st.date,
              note: st.note,
            });
            return ok({
              sharedTrip: st,
              footprintRevision: String(backend.state.revision),
            });
          }

          case "SetSharedTripMemberRole": {
            const st = state.sharedTrips.find((t) => t.id === body.sharedTripId);
            if (!st) {
              return route.fulfill({ status: 404, json: { code: "not_found", message: "shared trip not found" } });
            }
            st.members = st.members.map((m) =>
              m.user.id === body.memberUserId ? { ...m, role: body.role } : m,
            );
            return ok({ sharedTrip: st });
          }

          case "RemoveSharedTripMember": {
            const st = state.sharedTrips.find((t) => t.id === body.sharedTripId);
            if (!st) {
              return route.fulfill({ status: 404, json: { code: "not_found", message: "shared trip not found" } });
            }
            st.members = st.members.filter((m) => m.user.id !== body.memberUserId);
            return ok({ sharedTrip: st });
          }

          case "LeaveSharedTrip": {
            const st = state.sharedTrips.find((t) => t.id === body.sharedTripId);
            if (st) {
              state.sharedTrips = state.sharedTrips.filter((t) => t.id !== body.sharedTripId);
              // Leaving removes only the shared trip record; the city check-in and any other trips remain intact!
              backend.otherDeviceDeleteTrip(st.tripId);
            }
            return ok({ footprintRevision: String(backend.state.revision) });
          }

          case "DeleteSharedTrip": {
            state.sharedTrips = state.sharedTrips.filter((t) => t.id !== body.sharedTripId);
            return ok({});
          }

          case "ListSharedTripMedia": {
            const list = state.mediaBySharedTrip.get(body.sharedTripId) ?? [];
            return ok({ media: list });
          }

          case "ReadSharedTripMedia": {
            return ok({
              data: onePixelPng.toString("base64"),
              contentType: "image/png",
              filename: "断桥残雪.jpg",
            });
          }

          default:
            return route.fulfill({ status: 404, json: { code: "unimplemented" } });
        }
      });
    },
  };
}

async function openCollaborationDialog(page: Page) {
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "好友与共同旅行" }).click();
  const dialog = page.getByRole("dialog", { name: "好友与共同旅行" });
  await expect(dialog).toBeVisible();
  return dialog;
}

test("friends lifecycle, mutual privacy controls, footprint comparison, and unfriending", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [dongcheng({ note: "天安门" }), xicheng(), tianjin()],
  });
  await backend.install();
  const collab = fakeCollaboration(page, { backend });
  await collab.install();
  await login(page);

  const dialog = await openCollaborationDialog(page);
  expect(await noOverflow(page)).toBe(true);

  // Verify invite code & default privacy checkboxes
  await expect(dialog.getByRole("textbox", { name: "我的好友邀请码" })).toHaveValue("fc_ci_user_8899");
  const showCitiesBox = dialog.getByRole("checkbox", { name: /对好友公开打卡城市与颜色/ });
  const showDatesBox = dialog.getByRole("checkbox", { name: "对好友公开旅行日期" });
  const showNotesBox = dialog.getByRole("checkbox", { name: "对好友公开旅行记忆（备注）" });
  await expect(showCitiesBox).toBeChecked();
  await expect(showDatesBox).not.toBeChecked();
  await expect(showNotesBox).not.toBeChecked();

  // Accept incoming friend request from Carol
  await expect(dialog.getByText("卡萝")).toBeVisible();
  await dialog.getByRole("button", { name: "接受好友申请" }).click();
  await expect(dialog.getByText("已与 卡萝 成为好友")).toBeVisible();

  // Send friend request to Bob by username
  await dialog.getByRole("textbox", { name: "好友用户名或邀请码" }).fill("bob_traveler");
  await dialog.getByRole("button", { name: "发送好友申请" }).click();
  await expect(dialog.getByText("好友申请已发送")).toBeVisible();

  // View Bob's footprint comparison
  const bobItem = dialog.locator(".collab-item", { hasText: "阿博" });
  await bobItem.getByRole("button", { name: "查看地图与对比" }).click();

  const compRegion = dialog.getByRole("region", { name: "阿博的足迹对比" });
  await expect(compRegion).toBeVisible();
  await expect(compRegion).toContainText("对方已探索 2 / 34 个省级区域 · 2 座城市 · 2 次旅行");
  await expect(compRegion).toContainText("共同探索省份（1）");
  await expect(compRegion).toContainText("北京");
  await expect(compRegion).toContainText("仅我探索省份（1）");
  await expect(compRegion).toContainText("天津");
  await expect(compRegion).toContainText("仅对方探索省份（1）");
  await expect(compRegion).toContainText("浙江");
  await expect(compRegion).toContainText("共同打卡城市（1）");
  await expect(compRegion).toContainText("东城区");
  // Bob enabled showDates but disabled showNotes
  await expect(compRegion).toContainText("2026-05-02");
  await expect(compRegion).not.toContainText("阿博的故宫笔记");

  // When I uncheck "公开打卡城市", city comparison becomes unavailable and dates/notes checkboxes disable
  await showCitiesBox.uncheck();
  await expect(showDatesBox).toBeDisabled();
  await expect(showNotesBox).toBeDisabled();
  await bobItem.getByRole("button", { name: "查看地图与对比" }).click();
  await expect(compRegion).toContainText("需要双方均开启“公开打卡城市”后才能查看共同打卡城市对比。");

  // Unfriending asks for confirmation and immediately closes comparison
  await bobItem.getByRole("button", { name: "解除好友" }).click();
  await bobItem.getByRole("button", { name: "确认解除好友" }).click();
  await expect(dialog.getByText("已与 阿博 解除好友关系")).toBeVisible();
  await expect(compRegion).toHaveCount(0);
  expect(await noOverflow(page)).toBe(true);
});

test("shared trip invite accept, collaborative edit, photo upload, leaving preserves city & other trips, and owner management", async ({ page }) => {
  const backend = fakeBackend(page, {
    visits: [dongcheng({ note: "独自初访" })],
    trips: [
      // Pre-existing personal trip in Hangzhou so we can verify leaving a shared trip in Hangzhou preserves the city & personal trip!
      { id: "trip-hz-personal", code: "330100", date: "2025-04-10", note: "我自己的杭州春游" },
    ],
  });
  backend.state.visits.set("330100", { code: "330100", name: "杭州市", province: "330000", color: green });
  await backend.install();
  const collab = fakeCollaboration(page, { backend });
  // Seed Bob as an accepted friend
  collab.state.friends.push({
    id: "fr-bob",
    friend: friendBob,
    status: "accepted",
    direction: "outgoing",
    createdAt: "2026-10-14T01:00:00Z",
    updatedAt: "2026-10-14T01:00:00Z",
  });
  await collab.install();
  await login(page);

  const dialog = await openCollaborationDialog(page);
  await dialog.getByRole("tab", { name: /共同旅行/ }).click();

  // 1. Accept incoming shared trip invite in Hangzhou from Bob
  const inviteSection = dialog.getByRole("region", { name: "待确认的共同旅行邀请" });
  await expect(inviteSection).toContainText("杭州市 · 来自 阿博 的邀请");
  await expect(inviteSection).toContainText("西湖秋游");
  await inviteSection.getByRole("button", { name: "接受并加入" }).click();
  await expect(dialog.getByText("已加入 杭州市 的共同旅行，足迹已同步到你的地图")).toBeVisible();

  // 2. Edit shared trip date & note as co-editor
  const hzCard = dialog.locator('[data-shared-trip-id="st-hangzhou-invite"]');
  await expect(hzCard).toBeVisible();
  await hzCard.getByLabel("共同旅行日期（杭州市）").fill("2026-09-16");
  await hzCard.getByLabel("共同旅行记忆（杭州市）").fill("西湖秋游 · 满觉陇闻桂花");
  await hzCard.getByRole("button", { name: "保存共同旅行修改" }).click();
  await expect(dialog.getByText("已同步更新 杭州市 的共同旅行")).toBeVisible();

  // 3. View & upload shared trip photos
  await hzCard.getByRole("button", { name: "查看共同旅行照片" }).click();
  await expect(hzCard).toContainText("断桥残雪.jpg");
  await expect(hzCard).toContainText("由 阿博 上传");
  await hzCard.getByLabel("上传共同旅行照片（杭州市）").setInputFiles({
    name: "雷峰塔.jpg",
    mimeType: "image/jpeg",
    buffer: Buffer.from("fake-jpeg"),
  });
  await expect(dialog.getByText("共同旅行照片已上传")).toBeVisible();
  await expect(hzCard).toContainText("雷峰塔.jpg");

  // 4. Leave the shared trip in Hangzhou: verify Hangzhou city check-in and personal trip remain!
  await hzCard.getByRole("button", { name: "退出共同旅行" }).click();
  await expect(hzCard).toContainText("退出后仅移除本次共同旅行记录，你在该城市的打卡状态、其他旅行及他人的记录均会保留。");
  await hzCard.getByRole("button", { name: "确认退出共同旅行" }).click();
  await expect(dialog.getByText("已退出 杭州市 的共同旅行，你的城市打卡与其他记录已保留")).toBeVisible();
  expect(backend.state.visits.has("330100")).toBe(true);
  expect(backend.state.trips.has("trip-hz-personal")).toBe(true);
  expect(backend.state.trips.has("trip-hz-shared")).toBe(false);

  // 5. Invite Bob from the city editor ("邀请共同旅行" button) and manage member roles & dissolve
  await dialog.getByRole("button", { name: "关闭", exact: true }).first().click();
  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card", { hasText: "东城区" }).first().click();
  const editorDialog = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
  await expect(editorDialog).toBeVisible();
  await editorDialog.getByRole("button", { name: "这次旅行的共同旅行（第 1 次）" }).click();

  const collabAgain = page.getByRole("dialog", { name: "好友与共同旅行" });
  await expect(collabAgain).toBeVisible();
  await expect(collabAgain.getByLabel("选择要共享的旅行")).toHaveValue(`110101:${legacyId("110101")}`);
  await collabAgain.getByRole("button", { name: "发送共同旅行邀请" }).click();
  await expect(collabAgain.getByText("已邀请好友加入 东城区 的共同旅行")).toBeVisible();

  const dcCard = collabAgain.locator(`[data-shared-trip-id="st-${legacyId("110101")}"]`);
  await expect(dcCard).toBeVisible();
  await expect(dcCard).toContainText("创建者");

  // Change Bob's role to viewer, then remove Bob, then dissolve shared trip
  await dcCard.getByLabel("阿博的权限").selectOption("viewer");
  await expect(collabAgain.getByText("已更新 阿博 的成员权限")).toBeVisible();
  await dcCard.getByRole("button", { name: "移除成员" }).click();
  await expect(collabAgain.getByText("已将 阿博 移出共同旅行")).toBeVisible();

  await dcCard.getByRole("button", { name: "解散共同旅行" }).click();
  await dcCard.getByRole("button", { name: "确认解散共同旅行" }).click();
  await expect(collabAgain.getByText("已解散共同旅行")).toBeVisible();
  // Owner's own Dongcheng record remains
  expect(backend.state.visits.has("110101")).toBe(true);
});

test("viewer role in shared trip is read-only in the city editor and responsive on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 667 });
  const backend = fakeBackend(page, {
    visits: [dongcheng({ note: "只读同行记录" })],
  });
  await backend.install();
  const collab = fakeCollaboration(page, { backend });
  collab.state.sharedTrips = [
    {
      id: "st-dongcheng-viewer",
      tripId: legacyId("110101"),
      regionCode: "110101",
      regionName: "东城区",
      provinceCode: "110000",
      date: "2026-05-01",
      note: "只读同行记录",
      owner: friendBob,
      myRole: "viewer",
      myStatus: "active",
      members: [
        { user: friendBob, role: "owner", status: "active" },
        { user, role: "viewer", status: "active" },
      ],
      createdAt: "2026-10-14T01:00:00Z",
      updatedAt: "2026-10-14T01:00:00Z",
    },
  ];
  await collab.install();
  await login(page);

  await page.getByRole("button", { name: "旅行足迹" }).first().click();
  await page.locator(".visit-card", { hasText: "东城区" }).first().click();
  const editorDialog = page.getByRole("dialog").filter({ has: page.getByRole("button", { name: "保存足迹" }) });
  await expect(editorDialog).toBeVisible();
  await expect(editorDialog).toContainText("第 1 次旅行（共同旅行 · 只读成员）");
  await expect(editorDialog).toContainText("你在这次共同旅行中为只读成员");
  await expect(editorDialog.getByRole("button", { name: "删除第 1 次旅行" })).toBeDisabled();
  await expect(editorDialog.getByLabel(/旅行日期.*第 1 次/)).toBeDisabled();
  await expect(editorDialog.getByLabel(/旅行记忆.*第 1 次/)).toBeDisabled();
  expect(await noOverflow(page)).toBe(true);

  // Open CollaborationDialog from the mobile editor and check layout overflow
  await editorDialog.getByRole("button", { name: "这次旅行的共同旅行（第 1 次）" }).click();
  const collabDialog = page.getByRole("dialog", { name: "好友与共同旅行" });
  await expect(collabDialog).toBeVisible();
  await expect(collabDialog).toContainText("你是只读成员，可查看共同旅行内容与照片，不能修改记录或上传照片。");
  expect(await noOverflow(page)).toBe(true);
});
