import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { BellRinging, EnvelopeSimple, DeviceMobile } from "@phosphor-icons/react";
import { detachThisDevice, notificationApi, type Capabilities, type ServerSettings } from "@/api/notification";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { describeApiError } from "@/lib/errors";
import {
  NOTIFICATION_CHANNELS,
  NOTIFICATION_KINDS,
  PUSH_SUPPORT_TEXT,
  browserTimezone,
  detectPushSupport,
  memoryPreview,
  prefKey,
  pushKeysOf,
  sameDraft,
  timezoneOptions,
  urlBase64ToBytes,
  type NotificationChannel,
  type NotificationDraft,
} from "@/lib/notification";
import "./notification.css";

type Props = { open: boolean; onOpenChange: (open: boolean) => void };

const draftOf = (s: ServerSettings): NotificationDraft => ({
  prefs: { ...s.prefs },
  timezone: s.timezone,
  sendHour: s.sendHour,
  showPlaces: s.showPlaces,
});

const hours = Array.from({ length: 24 }, (_, h) => h);
const hourLabel = (h: number) => `${String(h).padStart(2, "0")}:00`;

const serviceWorkerUrl = `${import.meta.env.BASE_URL}sw.js`;

/** Reminder settings (F12): what to send, how, and when. Everything is off until switched on. */
export default function NotificationDialog({ open, onOpenChange }: Props) {
  const [server, setServer] = useState<ServerSettings | null>(null);
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [draft, setDraft] = useState<NotificationDraft | null>(null);
  const [loadError, setLoadError] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [deviceSubscribed, setDeviceSubscribed] = useState(false);
  const generation = useRef(0);
  const support = useMemo(() => (open ? detectPushSupport() : "ok"), [open]);

  const refreshDevice = useCallback(async () => {
    try {
      const reg = await navigator.serviceWorker?.getRegistration();
      setDeviceSubscribed(!!(await reg?.pushManager?.getSubscription()));
    } catch {
      setDeviceSubscribed(false);
    }
  }, []);

  const load = useCallback(async () => {
    const id = ++generation.current;
    setLoadError("");
    try {
      const r = await notificationApi.get();
      if (id !== generation.current) return;
      setServer(r.settings);
      setCaps(r.caps);
      setDraft(draftOf(r.settings));
      void refreshDevice();
    } catch (e) {
      if (id === generation.current) setLoadError(describeApiError(e));
    }
  }, [refreshDevice]);

  useEffect(() => {
    if (!open) return;
    setError("");
    setNotice("");
    setEmail("");
    setCode("");
    void load();
    return () => {
      generation.current++;
    };
  }, [open, load]);

  // Runs one action; errors and notices are shown above the buttons.
  const run = async (action: () => Promise<string | void>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const message = await action();
      if (message) setNotice(message);
    } catch (e) {
      setError(e instanceof Error && e.name === "PushSetupError" ? e.message : describeApiError(e));
    } finally {
      setBusy(false);
    }
  };

  const dirty = !!(server && draft && !sameDraft(draft, draftOf(server)));
  const zones = useMemo(() => timezoneOptions(draft?.timezone ?? "", browserTimezone()), [draft?.timezone]);

  const setPref = (kind: string, channel: NotificationChannel, value: boolean) =>
    setDraft((d) => (d ? { ...d, prefs: { ...d.prefs, [`${kind}/${channel}`]: value } } : d));

  const save = () =>
    run(async () => {
      if (!draft) return;
      const next = await notificationApi.update(draft);
      setServer(next);
      setDraft(draftOf(next));
      return "设置已保存。";
    });

  const requestCode = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const r = await notificationApi.requestEmail(email.trim());
      setCode("");
      setServer((s) => (s ? { ...s, pendingEmailMasked: r.masked, pendingExpiresAt: r.expiresAt } : s));
      return `验证码已发送到 ${r.masked}，15 分钟内有效。`;
    });
  };

  const confirm = (e: FormEvent) => {
    e.preventDefault();
    void run(async () => {
      const next = await notificationApi.confirmEmail(code.trim());
      setServer(next);
      setEmail("");
      setCode("");
      return "邮箱已验证。现在可以开启邮件提醒。";
    });
  };

  const removeEmail = () =>
    run(async () => {
      const next = await notificationApi.removeEmail();
      setServer(next);
      setDraft((d) => {
        if (!d) return d;
        const prefs = { ...d.prefs };
        for (const k of NOTIFICATION_KINDS) prefs[prefKey(k.kind, "email")] = false;
        return { ...d, prefs };
      });
      return "已移除邮箱，邮件提醒已关闭。";
    });

  const enableThisDevice = () =>
    run(async () => {
      if (!caps?.vapidPublicKey) return;
      const fail = (message: string) => {
        const err = new Error(message);
        err.name = "PushSetupError";
        return err;
      };
      const reg = await navigator.serviceWorker.register(serviceWorkerUrl);
      await navigator.serviceWorker.ready;
      if ((await Notification.requestPermission()) !== "granted") {
        throw fail("没有获得通知权限。请在浏览器的网站设置中允许通知后重试。");
      }
      const sub =
        (await reg.pushManager.getSubscription()) ??
        (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToBytes(caps.vapidPublicKey) }));
      const keys = pushKeysOf(sub);
      if (!keys) throw fail("浏览器没有返回完整的推送订阅，请重试。");
      const count = await notificationApi.registerPush(keys);
      setServer((s) => (s ? { ...s, pushCount: count } : s));
      setDeviceSubscribed(true);
      return "已在此设备启用推送。记得在上方开启需要的提醒并保存。";
    });

  const disableThisDevice = () =>
    run(async () => {
      await detachThisDevice();
      await load();
      return "已停用此设备的推送。";
    });

  const sendTest = (channel: NotificationChannel) =>
    run(async () => {
      await notificationApi.test(channel);
      return channel === "email" ? "测试邮件已发送，请查看收件箱（也看看垃圾邮件）。" : "测试推送已发出。";
    });

  const preview = memoryPreview(!!draft?.showPlaces);
  const channelReady = (channel: NotificationChannel) =>
    channel === "email" ? !!caps?.emailAvailable && !!server?.emailVerified : !!caps?.pushAvailable;
  const channelHint = (channel: NotificationChannel) =>
    channel === "email"
      ? !caps?.emailAvailable
        ? "服务器未配置邮件"
        : !server?.emailVerified
          ? "先验证邮箱"
          : ""
      : !caps?.pushAvailable
        ? "服务器未配置推送"
        : "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="notify-dialog">
        <DialogHeader>
          <span className="dialog-icon">
            <BellRinging size={22} />
          </span>
          <DialogTitle>提醒与通知</DialogTitle>
          <DialogDescription>
            所有提醒默认关闭，每一类、每个渠道可单独开关。通知不会包含备注或照片。
          </DialogDescription>
        </DialogHeader>

        {loadError && (
          <div role="alert" className="unreadable-note unreadable-error">
            <p>{loadError}</p>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              重试
            </Button>
          </div>
        )}
        {!server && !loadError && <p role="status">正在读取提醒设置…</p>}

        {server && draft && caps && (
          <>
            {NOTIFICATION_KINDS.map(({ kind, label, hint }) => (
              <fieldset key={kind} className="notify-fieldset">
                <legend>{label}</legend>
                <p className="notify-hint">{hint}</p>
                <div className="notify-channels">
                  {NOTIFICATION_CHANNELS.map(({ channel, label: channelLabel }) => {
                    const ready = channelReady(channel);
                    const reason = channelHint(channel);
                    return (
                      <label key={channel} className="notify-option">
                        <input
                          type="checkbox"
                          checked={!!draft.prefs[prefKey(kind, channel)]}
                          disabled={busy || (!ready && !draft.prefs[prefKey(kind, channel)])}
                          onChange={(e) => setPref(kind, channel, e.target.checked)}
                        />
                        <span>
                          {channelLabel}
                          {reason && <small>{reason}</small>}
                        </span>
                      </label>
                    );
                  })}
                </div>
              </fieldset>
            ))}

            <fieldset className="notify-fieldset">
              <legend>发送时间与内容</legend>
              <div className="notify-row">
                <label className="field-label">
                  时区
                  <select
                    className="notify-select"
                    value={draft.timezone}
                    disabled={busy}
                    onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}
                  >
                    {zones.map((z) => (
                      <option key={z} value={z}>
                        {z}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field-label">
                  当天几点后发送
                  <select
                    className="notify-select"
                    value={draft.sendHour}
                    disabled={busy}
                    onChange={(e) => setDraft({ ...draft, sendHour: Number(e.target.value) })}
                  >
                    {hours.map((h) => (
                      <option key={h} value={h}>
                        {hourLabel(h)}
                      </option>
                    ))}
                  </select>
                </label>
              </div>
              <label className="notify-option">
                <input
                  type="checkbox"
                  checked={draft.showPlaces}
                  disabled={busy}
                  onChange={(e) => setDraft({ ...draft, showPlaces: e.target.checked })}
                />
                <span>
                  在通知中显示城市名称
                  <small>关闭时通知只写“你有几段旅行记忆”，锁屏和邮件预览里不会出现地点。</small>
                </span>
              </label>
              <p className="notify-preview" aria-label="通知预览">
                <strong>{preview.title}</strong>
                <span>{preview.body}</span>
              </p>
            </fieldset>

            <div className="notify-actions">
              <Button disabled={busy || !dirty} onClick={() => void save()}>
                保存设置
              </Button>
              {dirty && <span className="notify-hint">有未保存的修改</span>}
            </div>

            <section className="notify-section" aria-labelledby="notify-email-title">
              <h3 id="notify-email-title">
                <EnvelopeSimple size={16} aria-hidden="true" /> 邮件
              </h3>
              {!caps.emailAvailable ? (
                <p className="notify-hint">此服务器尚未配置邮件发送，暂不能使用邮件提醒。</p>
              ) : server.emailVerified ? (
                <div className="notify-inline">
                  <span>
                    已验证：<strong>{server.emailMasked}</strong>
                  </span>
                  <Button variant="outline" size="sm" disabled={busy} onClick={() => void sendTest("email")}>
                    发送测试邮件
                  </Button>
                  <Button variant="outline" size="sm" disabled={busy} onClick={() => void removeEmail()}>
                    移除邮箱
                  </Button>
                </div>
              ) : (
                <>
                  <form className="notify-inline" onSubmit={requestCode}>
                    <label className="field-label">
                      邮箱地址
                      <Input
                        type="email"
                        autoComplete="email"
                        required
                        value={email}
                        disabled={busy}
                        onChange={(e) => setEmail(e.target.value)}
                      />
                    </label>
                    <Button type="submit" variant="outline" disabled={busy || !email.trim()}>
                      发送验证码
                    </Button>
                  </form>
                  {server.pendingEmailMasked && (
                    <form className="notify-inline" onSubmit={confirm}>
                      <label className="field-label">
                        验证码（已发送到 {server.pendingEmailMasked}）
                        <Input
                          inputMode="numeric"
                          autoComplete="one-time-code"
                          maxLength={6}
                          pattern="[0-9]{6}"
                          required
                          value={code}
                          disabled={busy}
                          onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                        />
                      </label>
                      <Button type="submit" disabled={busy || code.length !== 6}>
                        确认
                      </Button>
                    </form>
                  )}
                  <p className="notify-hint">邮件里的退订链接可随时一键停止同类邮件。地址只用于发送你开启的提醒。</p>
                </>
              )}
            </section>

            <section className="notify-section" aria-labelledby="notify-push-title">
              <h3 id="notify-push-title">
                <DeviceMobile size={16} aria-hidden="true" /> 浏览器推送
              </h3>
              {!caps.pushAvailable ? (
                <p className="notify-hint">此服务器尚未配置浏览器推送，暂不能使用。</p>
              ) : support !== "ok" ? (
                <p className="notify-hint">{PUSH_SUPPORT_TEXT[support]}</p>
              ) : (
                <div className="notify-inline">
                  <span>
                    已订阅的设备：<strong>{server.pushCount}</strong>
                    {deviceSubscribed ? "（含此设备）" : ""}
                  </span>
                  {deviceSubscribed ? (
                    <Button variant="outline" size="sm" disabled={busy} onClick={() => void disableThisDevice()}>
                      停用此设备
                    </Button>
                  ) : (
                    <Button variant="outline" size="sm" disabled={busy} onClick={() => void enableThisDevice()}>
                      在此设备启用推送
                    </Button>
                  )}
                  {server.pushCount > 0 && (
                    <Button variant="outline" size="sm" disabled={busy} onClick={() => void sendTest("push")}>
                      发送测试推送
                    </Button>
                  )}
                </div>
              )}
              <p className="notify-hint">退出登录时，此设备会自动停止接收该账号的推送。</p>
            </section>

            {notice && (
              <p role="status" className="account-sync">
                {notice}
              </p>
            )}
            {error && (
              <p role="alert" className="unreadable-note unreadable-error">
                {error}
              </p>
            )}
          </>
        )}

        <div className="dialog-actions">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
