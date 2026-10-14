// Pure helpers for reminder settings (F12). Nothing here touches the DOM or the
// network, so the rules can be tested with `npm test`.

export type NotificationKind = "memory" | "annual_report";
export type NotificationChannel = "email" | "push";

export const NOTIFICATION_KINDS: { kind: NotificationKind; label: string; hint: string }[] = [
  { kind: "memory", label: "回忆提醒", hint: "“一年前的今天”：往年同一天有旅行记录时提醒。" },
  { kind: "annual_report", label: "年度旅行回顾", hint: "每年 1 月第一周，提醒查看上一年的旅行回顾。" },
];

export const NOTIFICATION_CHANNELS: { channel: NotificationChannel; label: string }[] = [
  { channel: "email", label: "邮件" },
  { channel: "push", label: "浏览器推送" },
];

export const prefKey = (kind: NotificationKind, channel: NotificationChannel) => `${kind}/${channel}`;

export type NotificationDraft = {
  /** Every kind/channel pair, keyed by prefKey. */
  prefs: Record<string, boolean>;
  timezone: string;
  sendHour: number;
  showPlaces: boolean;
};

export const DEFAULT_TIMEZONE = "Asia/Shanghai";

export function emptyPrefs(): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const k of NOTIFICATION_KINDS) for (const c of NOTIFICATION_CHANNELS) out[prefKey(k.kind, c.channel)] = false;
  return out;
}

export function sameDraft(a: NotificationDraft, b: NotificationDraft) {
  return (
    a.timezone === b.timezone &&
    a.sendHour === b.sendHour &&
    a.showPlaces === b.showPlaces &&
    Object.keys({ ...a.prefs, ...b.prefs }).every((k) => !!a.prefs[k] === !!b.prefs[k])
  );
}

const COMMON_ZONES = [
  "Asia/Shanghai",
  "Asia/Hong_Kong",
  "Asia/Taipei",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Europe/London",
  "Europe/Paris",
  "America/New_York",
  "America/Los_Angeles",
  "Australia/Sydney",
];

/** Time zones offered in the picker: the common ones plus the saved and browser zones. */
export function timezoneOptions(saved: string, browser = ""): string[] {
  const set = new Set<string>([saved, browser, ...COMMON_ZONES].filter(Boolean));
  return [...set].filter((z) => {
    try {
      new Intl.DateTimeFormat("zh-CN", { timeZone: z });
      return true;
    } catch {
      return false;
    }
  });
}

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {
    return "";
  }
}

/** What a memory reminder says, for the preview next to the "show places" switch. */
export function memoryPreview(showPlaces: boolean): { title: string; body: string } {
  return {
    title: "山海足迹 · 一年前的今天",
    body: showPlaces
      ? "一年前的今天，你在杭州市留下了足迹。打开山海足迹回顾。"
      : "一年前的今天，你有 1 段旅行记忆。打开山海足迹回顾。",
  };
}

/** Converts the base64url application server key to the bytes pushManager.subscribe wants. */
export function urlBase64ToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (value.length % 4)) % 4);
  const raw = atob(padded);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

export type PushKeys = { endpoint: string; p256dh: string; auth: string };

const b64url = (buffer: ArrayBuffer | null) => {
  if (!buffer) return "";
  let s = "";
  for (const byte of new Uint8Array(buffer)) s += String.fromCharCode(byte);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

/** Reads the endpoint and keys the server needs; null when the subscription is incomplete. */
export function pushKeysOf(sub: { endpoint: string; getKey(name: "p256dh" | "auth"): ArrayBuffer | null }): PushKeys | null {
  const p256dh = b64url(sub.getKey("p256dh"));
  const auth = b64url(sub.getKey("auth"));
  return sub.endpoint && p256dh && auth ? { endpoint: sub.endpoint, p256dh, auth } : null;
}

export type PushSupport = "ok" | "unsupported" | "insecure" | "denied";

/** Whether this browser can subscribe to push, and why not if it can't. */
export function pushSupport(env: {
  hasServiceWorker: boolean;
  hasPushManager: boolean;
  hasNotification: boolean;
  secure: boolean;
  permission: string;
}): PushSupport {
  if (!env.secure) return "insecure";
  if (!env.hasServiceWorker || !env.hasPushManager || !env.hasNotification) return "unsupported";
  if (env.permission === "denied") return "denied";
  return "ok";
}

export function detectPushSupport(): PushSupport {
  if (typeof window === "undefined") return "unsupported";
  return pushSupport({
    hasServiceWorker: "serviceWorker" in navigator,
    hasPushManager: "PushManager" in window,
    hasNotification: "Notification" in window,
    secure: window.isSecureContext,
    permission: "Notification" in window ? Notification.permission : "default",
  });
}

export const PUSH_SUPPORT_TEXT: Record<Exclude<PushSupport, "ok">, string> = {
  unsupported: "此浏览器不支持网页推送。可改用邮件提醒。",
  insecure: "网页推送需要 HTTPS 访问（本机 localhost 除外）。",
  denied: "此浏览器已拒绝本站的通知权限。请在浏览器的网站设置中允许通知后再启用。",
};
