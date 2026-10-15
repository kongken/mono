import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import {
  emptyPrefs,
  memoryPreview,
  prefKey,
  pushKeysOf,
  pushSupport,
  sameDraft,
  timezoneOptions,
  urlBase64ToBytes,
} from "../src/lib/notification.ts";

test("every reminder starts switched off", () => {
  const prefs = emptyPrefs();
  assert.deepEqual(Object.keys(prefs).sort(), [
    "annual_report/email",
    "annual_report/push",
    "memory/email",
    "memory/push",
  ]);
  assert.ok(Object.values(prefs).every((v) => v === false));
  assert.equal(prefKey("memory", "push"), "memory/push");
});

test("sameDraft treats a missing preference as off", () => {
  const base = { prefs: emptyPrefs(), timezone: "Asia/Shanghai", sendHour: 9, showPlaces: false };
  assert.ok(sameDraft(base, { ...base, prefs: {} }));
  assert.ok(!sameDraft(base, { ...base, prefs: { ...base.prefs, "memory/email": true } }));
  assert.ok(!sameDraft(base, { ...base, sendHour: 10 }));
  assert.ok(!sameDraft(base, { ...base, timezone: "Asia/Tokyo" }));
  assert.ok(!sameDraft(base, { ...base, showPlaces: true }));
});

test("timezone choices keep the saved zone and drop unknown ones", () => {
  const zones = timezoneOptions("Pacific/Auckland", "Not/AZone");
  assert.equal(zones[0], "Pacific/Auckland");
  assert.ok(zones.includes("Asia/Shanghai"));
  assert.ok(!zones.includes("Not/AZone"));
  assert.equal(new Set(zones).size, zones.length);
});

test("the preview only names a place when asked to", () => {
  assert.ok(!memoryPreview(false).body.includes("杭州"));
  assert.ok(memoryPreview(true).body.includes("杭州"));
});

test("push support explains why it is unavailable", () => {
  const ok = { hasServiceWorker: true, hasPushManager: true, hasNotification: true, secure: true, permission: "default" };
  assert.equal(pushSupport(ok), "ok");
  assert.equal(pushSupport({ ...ok, secure: false }), "insecure");
  assert.equal(pushSupport({ ...ok, hasPushManager: false }), "unsupported");
  assert.equal(pushSupport({ ...ok, permission: "denied" }), "denied");
});

test("the application server key and subscription keys are converted to the wire formats", () => {
  assert.deepEqual([...urlBase64ToBytes("_-8")], [0xff, 0xef]);
  const buf = (...n) => new Uint8Array(n).buffer;
  const sub = (endpoint, p, a) => ({ endpoint, getKey: (name) => (name === "p256dh" ? p : a) });
  assert.deepEqual(pushKeysOf(sub("https://p.example/1", buf(251, 255), buf(1, 2))), {
    endpoint: "https://p.example/1",
    p256dh: "-_8",
    auth: "AQI",
  });
  assert.equal(pushKeysOf(sub("https://p.example/1", buf(1), null)), null);
  assert.equal(pushKeysOf(sub("", buf(1), buf(2))), null);
});

// Runs public/sw.js against a minimal service-worker scope.
function loadWorker(origin = "https://app.example") {
  const handlers = {};
  const shown = [];
  const opened = [];
  let windows = [];
  let focused = 0;
  const self = {
    location: { origin },
    addEventListener: (type, fn) => (handlers[type] = fn),
    skipWaiting() {},
    clients: {
      claim: async () => {},
      matchAll: async () => windows,
      openWindow: async (url) => opened.push(url),
    },
    registration: { showNotification: async (title, options) => shown.push({ title, options }) },
  };
  vm.runInNewContext(readFileSync(new URL("../public/sw.js", import.meta.url), "utf8"), { self, URL, JSON });
  return {
    shown,
    opened,
    setWindows: (list) => (windows = list.map((url) => ({ url, focus: async () => focused++ }))),
    focused: () => focused,
    push: async (payload) => {
      const waits = [];
      handlers.push({ data: payload === undefined ? null : { json: () => JSON.parse(payload) }, waitUntil: (p) => waits.push(p) });
      await Promise.all(waits);
    },
    click: async (url) => {
      const waits = [];
      handlers.notificationclick({ notification: { close() {}, data: { url } }, waitUntil: (p) => waits.push(p) });
      await Promise.all(waits);
    },
  };
}

test("the service worker shows only what the server sent", async () => {
  const w = loadWorker();
  await w.push(JSON.stringify({ title: "山海足迹 · 一年前的今天", body: "你有 1 段旅行记忆。", tag: "memory-2026-10-15", url: "/", note: "secret" }));
  assert.equal(w.shown.length, 1);
  assert.equal(w.shown[0].title, "山海足迹 · 一年前的今天");
  assert.equal(w.shown[0].options.body, "你有 1 段旅行记忆。");
  assert.equal(w.shown[0].options.tag, "memory-2026-10-15");
  assert.ok(!JSON.stringify(w.shown[0]).includes("secret"));
});

test("the service worker survives an empty or malformed push", async () => {
  const w = loadWorker();
  await w.push(undefined);
  await w.push("not json");
  assert.equal(w.shown.length, 2);
  assert.ok(w.shown.every((n) => n.title === "山海足迹" && n.options.tag === "tripmap"));
});

test("tapping a notification never opens another origin", async () => {
  const w = loadWorker();
  await w.click("https://evil.example/phish");
  await w.click("javascript:alert(1)");
  await w.click("/?from=reminder");
  assert.deepEqual(w.opened, ["https://app.example/", "https://app.example/", "https://app.example/?from=reminder"]);
  w.setWindows(["https://app.example/map"]);
  await w.click("/");
  assert.equal(w.focused(), 1);
  assert.equal(w.opened.length, 3);
});
