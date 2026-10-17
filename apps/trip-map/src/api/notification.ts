import { createClient } from "@connectrpc/connect";
import { timestampMs } from "@bufbuild/protobuf/wkt";
import { NotificationService, type NotificationSettings as WireSettings } from "@kongken/api-client/bff/tripmap/v1/notification_pb";
import {
  DEFAULT_TIMEZONE,
  NOTIFICATION_CHANNELS,
  NOTIFICATION_KINDS,
  emptyPrefs,
  prefKey,
  type NotificationChannel,
  type NotificationDraft,
  type PushKeys,
} from "@/lib/notification";
import { transport } from "./transport";

const raw = transport ? createClient(NotificationService, transport) : null;
// Only reachable once a session exists, which requires a configured backend.
const client = () => {
  if (!raw) throw new Error("backend not configured");
  return raw;
};

export type ServerSettings = NotificationDraft & {
  emailMasked: string;
  emailVerified: boolean;
  pendingEmailMasked: string;
  pendingExpiresAt: number | null;
  pushCount: number;
};

export type Capabilities = { emailAvailable: boolean; pushAvailable: boolean; vapidPublicKey: string };

function settings(s?: WireSettings): ServerSettings {
  const prefs = emptyPrefs();
  for (const p of s?.preferences ?? []) {
    const kind = NOTIFICATION_KINDS.find((k) => k.kind === p.kind)?.kind;
    const channel = NOTIFICATION_CHANNELS.find((c) => c.channel === p.channel)?.channel;
    if (kind && channel) prefs[prefKey(kind, channel)] = p.enabled;
  }
  return {
    prefs,
    timezone: s?.timezone || DEFAULT_TIMEZONE,
    sendHour: s?.sendHour ?? 9,
    showPlaces: s?.showPlaces ?? false,
    emailMasked: s?.emailMasked ?? "",
    emailVerified: s?.emailVerified ?? false,
    pendingEmailMasked: s?.pendingEmailMasked ?? "",
    pendingExpiresAt: s?.pendingExpiresAt ? timestampMs(s.pendingExpiresAt) : null,
    pushCount: s?.pushSubscriptionCount ?? 0,
  };
}

export const notificationApi = {
  get: async () => {
    const r = await client().getNotificationSettings({});
    const caps: Capabilities = {
      emailAvailable: r.emailAvailable,
      pushAvailable: r.pushAvailable,
      vapidPublicKey: r.vapidPublicKey,
    };
    return { settings: settings(r.settings), caps };
  },
  update: async (draft: NotificationDraft) => {
    const preferences = NOTIFICATION_KINDS.flatMap((k) =>
      NOTIFICATION_CHANNELS.map((c) => ({
        kind: k.kind,
        channel: c.channel,
        enabled: !!draft.prefs[prefKey(k.kind, c.channel)],
      })),
    );
    const r = await client().updateNotificationSettings({
      preferences,
      timezone: draft.timezone,
      sendHour: draft.sendHour,
      showPlaces: draft.showPlaces,
    });
    return settings(r.settings);
  },
  requestEmail: async (email: string) => {
    const r = await client().requestEmailVerification({ email });
    return { masked: r.pendingEmailMasked, expiresAt: r.expiresAt ? timestampMs(r.expiresAt) : null };
  },
  confirmEmail: async (code: string) => settings((await client().confirmEmail({ code })).settings),
  removeEmail: async () => settings((await client().removeEmail({})).settings),
  registerPush: async (keys: PushKeys) => {
    const r = await client().registerPushSubscription({ endpoint: keys.endpoint, p256dh: keys.p256dh, auth: keys.auth });
    return r.pushSubscriptionCount;
  },
  removePush: async (endpoint: string) => (await client().removePushSubscription({ endpoint })).pushSubscriptionCount,
  test: async (channel: NotificationChannel) => (await client().sendTestNotification({ channel })).delivered,
};

/**
 * Forgets this browser's push subscription on the server and in the browser, so a
 * signed-out device stops receiving the previous account's reminders. Best effort
 * and bounded in time: signing out never waits on it for long or fails because of it.
 */
export async function detachThisDevice(): Promise<void> {
  if (!raw || typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  const work = (async () => {
    const reg = await navigator.serviceWorker.getRegistration();
    const sub = await reg?.pushManager?.getSubscription();
    if (!sub) return;
    await notificationApi.removePush(sub.endpoint);
    await sub.unsubscribe();
  })();
  await Promise.race([work.catch(() => undefined), new Promise<void>((resolve) => setTimeout(resolve, 3000))]);
}
