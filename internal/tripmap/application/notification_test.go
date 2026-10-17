package application_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/ent/notificationdelivery"
	"github.com/kongken/mono/internal/tripmap/ent/notificationprofile"
	"github.com/kongken/mono/internal/tripmap/ent/notificationsetting"
	"github.com/kongken/mono/internal/tripmap/ent/pushsubscription"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/notification"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }
func (c *fakeClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

type fakeMailer struct {
	mu   sync.Mutex
	sent []notification.Mail
	fail bool
}

func (m *fakeMailer) Send(_ context.Context, mail notification.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("smtp down")
	}
	m.sent = append(m.sent, mail)
	return nil
}
func (m *fakeMailer) all() []notification.Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]notification.Mail(nil), m.sent...)
}
func (m *fakeMailer) reset() { m.mu.Lock(); m.sent = nil; m.mu.Unlock() }

type pushed struct {
	endpoint string
	payload  notification.PushPayload
}

type fakePusher struct {
	mu   sync.Mutex
	sent []pushed
	// errs maps an endpoint to the error its next sends return.
	errs map[string]error
	fail bool
}

func (p *fakePusher) Send(_ context.Context, t notification.PushTarget, payload notification.PushPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.errs[t.Endpoint]; err != nil {
		return err
	}
	if p.fail {
		return errors.New("push service down")
	}
	p.sent = append(p.sent, pushed{t.Endpoint, payload})
	return nil
}
func (p *fakePusher) all() []pushed {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pushed(nil), p.sent...)
}
func (p *fakePusher) reset() { p.mu.Lock(); p.sent = nil; p.mu.Unlock() }

// pushKeys returns browser-shaped subscription keys.
func pushKeys(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(key.PublicKey().Bytes()), enc.EncodeToString(secret)
}

func pref(kind, channel string, on bool) *tripmapv1.NotificationPreference {
	return &tripmapv1.NotificationPreference{Kind: kind, Channel: channel, Enabled: on}
}

const (
	kMemory = "memory"
	kAnnual = "annual_report"
	cEmail  = "email"
	cPush   = "push"
)

func (e *env) settings(token string) *tripmapv1.GetNotificationSettingsResponse {
	e.t.Helper()
	res, err := e.notify.GetNotificationSettings(context.Background(), bearer(token, &tripmapv1.GetNotificationSettingsRequest{}))
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Msg
}

func enabledSet(s *tripmapv1.NotificationSettings) map[string]bool {
	out := map[string]bool{}
	for _, p := range s.Preferences {
		out[p.Kind + "/" + p.Channel] = p.Enabled
	}
	return out
}

func (e *env) update(token string, prefs []*tripmapv1.NotificationPreference, tz string, hour int32, places bool) (*tripmapv1.NotificationSettings, error) {
	res, err := e.notify.UpdateNotificationSettings(context.Background(), bearer(token, &tripmapv1.UpdateNotificationSettingsRequest{Preferences: prefs, Timezone: tz, SendHour: hour, ShowPlaces: places}))
	if err != nil {
		return nil, err
	}
	return res.Msg.Settings, nil
}

var codePattern = regexp.MustCompile(`\b(\d{6})\b`)

// verifyEmail runs the full email confirmation for a user.
func (e *env) verifyEmail(token, address string) {
	e.t.Helper()
	e.mailer.reset()
	if _, err := e.notify.RequestEmailVerification(context.Background(), bearer(token, &tripmapv1.RequestEmailVerificationRequest{Email: address})); err != nil {
		e.t.Fatal(err)
	}
	sent := e.mailer.all()
	if len(sent) != 1 || sent[0].To != strings.ToLower(address) {
		e.t.Fatalf("verification mail: %+v", sent)
	}
	if _, err := e.notify.ConfirmEmail(context.Background(), bearer(token, &tripmapv1.ConfirmEmailRequest{Code: codePattern.FindString(sent[0].Text)})); err != nil {
		e.t.Fatal(err)
	}
	e.mailer.reset()
}

func (e *env) subscribe(token, endpoint string) {
	e.t.Helper()
	p256, authKey := pushKeys(e.t)
	if _, err := e.notify.RegisterPushSubscription(context.Background(), bearer(token, &tripmapv1.RegisterPushSubscriptionRequest{Endpoint: endpoint, P256Dh: p256, Auth: authKey})); err != nil {
		e.t.Fatal(err)
	}
}

func TestNotificationSettingsAreOptInValidatedAndPrivate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, err := e.notify.GetNotificationSettings(ctx, connect.NewRequest(&tripmapv1.GetNotificationSettingsRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	alice, bob := e.register("alice_f12"), e.register("bob_f12")

	got := e.settings(alice)
	if !got.EmailAvailable || !got.PushAvailable || got.VapidPublicKey != "vapid-public" {
		t.Fatalf("availability: %+v", got)
	}
	s := got.Settings
	if s.Timezone != "Asia/Shanghai" || s.SendHour != 9 || s.ShowPlaces || s.EmailVerified || len(s.Preferences) != 4 {
		t.Fatalf("defaults: %+v", s)
	}
	for k, on := range enabledSet(s) {
		if on {
			t.Fatalf("%s is on by default", k)
		}
	}

	for name, call := range map[string]func() error{
		"empty zone": func() error { _, err := e.update(alice, nil, "", 9, false); return err },
		"bad zone":   func() error { _, err := e.update(alice, nil, "Mars/Base", 9, false); return err },
		"hour 24":    func() error { _, err := e.update(alice, nil, "Asia/Shanghai", 24, false); return err },
		"hour -1":    func() error { _, err := e.update(alice, nil, "Asia/Shanghai", -1, false); return err },
		"duplicate": func() error {
			_, err := e.update(alice, []*tripmapv1.NotificationPreference{pref(kMemory, cPush, true), pref(kMemory, cPush, false)}, "Asia/Shanghai", 9, false)
			return err
		},
		"unknown kind": func() error {
			_, err := e.update(alice, []*tripmapv1.NotificationPreference{pref("", cPush, true)}, "Asia/Shanghai", 9, false)
			return err
		},
	} {
		wantCode(t, call(), connect.CodeInvalidArgument)
		_ = name
	}
	// Email cannot be switched on before an address is verified.
	_, err = e.update(alice, []*tripmapv1.NotificationPreference{pref(kMemory, cEmail, true)}, "Asia/Shanghai", 9, false)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if e.settings(alice).Settings.Preferences[0].Enabled {
		t.Fatal("a rejected update changed settings")
	}

	saved, err := e.update(alice, []*tripmapv1.NotificationPreference{pref(kMemory, cPush, true)}, "America/Los_Angeles", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Timezone != "America/Los_Angeles" || saved.SendHour != 20 || !saved.ShowPlaces || !enabledSet(saved)["memory/push"] {
		t.Fatalf("saved: %+v", saved)
	}
	// Unlisted switches keep their values; listed ones change.
	saved, err = e.update(alice, []*tripmapv1.NotificationPreference{pref(kAnnual, cPush, true)}, "America/Los_Angeles", 20, true)
	if err != nil {
		t.Fatal(err)
	}
	on := enabledSet(saved)
	if !on["memory/push"] || !on["annual_report/push"] {
		t.Fatalf("partial update: %v", on)
	}
	// Another account sees only its own defaults.
	if b := e.settings(bob).Settings; b.SendHour != 9 || b.ShowPlaces || enabledSet(b)["memory/push"] {
		t.Fatalf("bob inherited settings: %+v", b)
	}

	// A server without channels reports so and refuses to enable them.
	e.notifier.Mail, e.notifier.Push = nil, nil
	got = e.settings(alice)
	if got.EmailAvailable || got.PushAvailable || got.VapidPublicKey != "" {
		t.Fatalf("unavailable channels advertised: %+v", got)
	}
	_, err = e.update(alice, []*tripmapv1.NotificationPreference{pref(kMemory, cPush, true)}, "Asia/Shanghai", 9, false)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestEmailVerificationCodeExpiryAttemptsAndRemoval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.register("alice_mail"), e.register("bob_mail")

	_, err := e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: "not-an-address"}))
	wantCode(t, err, connect.CodeInvalidArgument)
	for _, bad := range []string{"a@b.c\r\nBcc: x@y.z", "Alice <alice@example.com>", "a b@example.com", "a@example"} {
		_, err = e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: bad}))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	if len(e.mailer.all()) != 0 {
		t.Fatal("mail sent for an invalid address")
	}

	res, err := e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: "  Alice@Example.COM "}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.PendingEmailMasked != "a***@example.com" {
		t.Fatalf("masked: %q", res.Msg.PendingEmailMasked)
	}
	mails := e.mailer.all()
	if len(mails) != 1 || mails[0].To != "alice@example.com" || !strings.Contains(mails[0].Subject, "验证码") {
		t.Fatalf("mail: %+v", mails)
	}
	code := codePattern.FindString(mails[0].Text)
	if code == "" {
		t.Fatalf("no code in %q", mails[0].Text)
	}
	// The pending address is shown masked but not yet verified, and the stored code is hashed.
	if s := e.settings(alice).Settings; s.EmailVerified || s.EmailMasked != "" || s.PendingEmailMasked != "a***@example.com" {
		t.Fatalf("pending state: %+v", s)
	}
	profile := e.store.DB.NotificationProfile.Query().Where(notificationprofile.PendingEmail("alice@example.com")).OnlyX(ctx)
	if profile.CodeHash == "" || strings.Contains(profile.CodeHash, code) {
		t.Fatal("verification code is not stored as a hash")
	}

	// Another account cannot confirm Alice's code.
	_, err = e.notify.ConfirmEmail(ctx, bearer(bob, &tripmapv1.ConfirmEmailRequest{Code: code}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.notify.ConfirmEmail(ctx, bearer(alice, &tripmapv1.ConfirmEmailRequest{Code: "12ab56"}))
	wantCode(t, err, connect.CodeInvalidArgument)

	// Five wrong guesses burn the code, even the right one is refused afterwards.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		_, err = e.notify.ConfirmEmail(ctx, bearer(alice, &tripmapv1.ConfirmEmailRequest{Code: wrong}))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	_, err = e.notify.ConfirmEmail(ctx, bearer(alice, &tripmapv1.ConfirmEmailRequest{Code: code}))
	wantCode(t, err, connect.CodeFailedPrecondition)

	// A fresh request starts over and can succeed.
	e.mailer.reset()
	if _, err = e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: "alice@example.com"})); err != nil {
		t.Fatal(err)
	}
	fresh := codePattern.FindString(e.mailer.all()[0].Text)
	// ... unless it expires first.
	e.clock.set(e.clock.now().Add(16 * time.Minute))
	_, err = e.notify.ConfirmEmail(ctx, bearer(alice, &tripmapv1.ConfirmEmailRequest{Code: fresh}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if s := e.settings(alice).Settings; s.PendingEmailMasked != "" || s.EmailVerified {
		t.Fatalf("expired code still pending: %+v", s)
	}

	e.verifyEmail(alice, "alice@example.com")
	s := e.settings(alice).Settings
	if !s.EmailVerified || s.EmailMasked != "a***@example.com" || s.PendingEmailMasked != "" {
		t.Fatalf("verified state: %+v", s)
	}
	if _, err = e.update(alice, []*tripmapv1.NotificationPreference{pref(kMemory, cEmail, true)}, "Asia/Shanghai", 9, false); err != nil {
		t.Fatal(err)
	}
	// Removing the address switches the channel off and forgets it.
	rm, err := e.notify.RemoveEmail(ctx, bearer(alice, &tripmapv1.RemoveEmailRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if rm.Msg.Settings.EmailVerified || rm.Msg.Settings.EmailMasked != "" || enabledSet(rm.Msg.Settings)["memory/email"] {
		t.Fatalf("after removal: %+v", rm.Msg.Settings)
	}

	// A failing mail server leaves nothing pending.
	e.mailer.fail = true
	_, err = e.notify.RequestEmailVerification(ctx, bearer(bob, &tripmapv1.RequestEmailVerificationRequest{Email: "bob@example.com"}))
	wantCode(t, err, connect.CodeUnavailable)
	if s := e.settings(bob).Settings; s.PendingEmailMasked != "" {
		t.Fatalf("failed send left a pending address: %+v", s)
	}
}

func TestEmailVerificationIsRateLimited(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_rl")
	for i := 0; i < 5; i++ {
		if _, err := e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: fmt.Sprintf("target%d@example.com", i)})); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.notify.RequestEmailVerification(ctx, bearer(alice, &tripmapv1.RequestEmailVerificationRequest{Email: "target9@example.com"}))
	wantCode(t, err, connect.CodeResourceExhausted)

	// One address cannot be flooded from several accounts either.
	for i := 0; i < 3; i++ {
		other := e.register(fmt.Sprintf("flood_%d", i))
		if _, err = e.notify.RequestEmailVerification(ctx, bearer(other, &tripmapv1.RequestEmailVerificationRequest{Email: "victim@example.com"})); err != nil {
			t.Fatal(err)
		}
	}
	other := e.register("flood_last")
	_, err = e.notify.RequestEmailVerification(ctx, bearer(other, &tripmapv1.RequestEmailVerificationRequest{Email: "victim@example.com"}))
	wantCode(t, err, connect.CodeResourceExhausted)
}

func TestPushSubscriptionsValidationOwnershipAndDelivery(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice, bob := e.register("alice_push"), e.register("bob_push")
	p256, authKey := pushKeys(t)

	for name, req := range map[string]*tripmapv1.RegisterPushSubscriptionRequest{
		"http":       {Endpoint: "http://push.example.com/x", P256Dh: p256, Auth: authKey},
		"ip literal": {Endpoint: "https://127.0.0.1/x", P256Dh: p256, Auth: authKey},
		"localhost":  {Endpoint: "https://localhost/x", P256Dh: p256, Auth: authKey},
		"odd port":   {Endpoint: "https://push.example.com:8443/x", P256Dh: p256, Auth: authKey},
		"userinfo":   {Endpoint: "https://u:p@push.example.com/x", P256Dh: p256, Auth: authKey},
		"empty":      {Endpoint: "", P256Dh: p256, Auth: authKey},
		"bad key":    {Endpoint: "https://push.example.com/x", P256Dh: "AAAA", Auth: authKey},
		"bad auth":   {Endpoint: "https://push.example.com/x", P256Dh: p256, Auth: "AAAA"},
	} {
		_, err := e.notify.RegisterPushSubscription(ctx, bearer(alice, req))
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: code %v", name, codeOf(err))
		}
	}

	const shared = "https://push.example.com/device-1"
	e.subscribe(alice, shared)
	if e.settings(alice).Settings.PushSubscriptionCount != 1 {
		t.Fatal("alice should have one browser")
	}
	// The endpoint never leaves the server.
	if got := fmt.Sprint(e.settings(alice)); strings.Contains(got, "device-1") {
		t.Fatalf("endpoint leaked: %s", got)
	}

	// The same browser signing in as Bob moves the subscription to him.
	e.subscribe(bob, shared)
	if e.settings(alice).Settings.PushSubscriptionCount != 0 || e.settings(bob).Settings.PushSubscriptionCount != 1 {
		t.Fatal("subscription did not move to the latest account")
	}
	// Alice cannot remove Bob's subscription by knowing its endpoint.
	res, err := e.notify.RemovePushSubscription(ctx, bearer(alice, &tripmapv1.RemovePushSubscriptionRequest{Endpoint: shared}))
	if err != nil || res.Msg.PushSubscriptionCount != 0 {
		t.Fatalf("remove foreign: %v %v", res, err)
	}
	if e.settings(bob).Settings.PushSubscriptionCount != 1 {
		t.Fatal("alice removed bob's subscription")
	}

	// A test push goes to the caller's browsers only.
	_, err = e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: cPush}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	tested, err := e.notify.SendTestNotification(ctx, bearer(bob, &tripmapv1.SendTestNotificationRequest{Channel: cPush}))
	if err != nil || tested.Msg.Delivered != 1 {
		t.Fatalf("test push: %v %v", tested, err)
	}
	sent := e.pusher.all()
	if len(sent) != 1 || sent[0].endpoint != shared || !strings.Contains(sent[0].payload.Title, "测试") || sent[0].payload.URL != "https://trip.example.com/" {
		t.Fatalf("sent: %+v", sent)
	}

	// A subscription the push service reports gone is removed.
	e.pusher.errs = map[string]error{shared: notification.ErrSubscriptionGone}
	_, err = e.notify.SendTestNotification(ctx, bearer(bob, &tripmapv1.SendTestNotificationRequest{Channel: cPush}))
	wantCode(t, err, connect.CodeUnavailable)
	if e.settings(bob).Settings.PushSubscriptionCount != 0 {
		t.Fatal("gone subscription was kept")
	}
	e.pusher.errs = nil

	// The newest ten browsers are kept.
	for i := 0; i < 12; i++ {
		e.subscribe(alice, fmt.Sprintf("https://push.example.com/many-%d", i))
	}
	if n := e.settings(alice).Settings.PushSubscriptionCount; n != 10 {
		t.Fatalf("subscriptions kept: %d", n)
	}
	if e.store.DB.PushSubscription.Query().Where(pushsubscription.EndpointHash(application.EndpointHashForTest("https://push.example.com/many-0"))).ExistX(ctx) {
		t.Fatal("oldest subscription survived")
	}
}

func TestTestEmailNeedsVerifiedAddressAndCarriesNoFootprints(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.register("alice_test")
	_, err := e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: cEmail}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: ""}))
	wantCode(t, err, connect.CodeInvalidArgument)
	e.verifyEmail(alice, "alice@example.com")
	res, err := e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: cEmail}))
	if err != nil || res.Msg.Delivered != 1 {
		t.Fatalf("%v %v", res, err)
	}
	mail := e.mailer.all()[0]
	if mail.To != "alice@example.com" || !strings.Contains(mail.UnsubscribeURL, "/api/notifications/unsubscribe?t=") || !strings.Contains(mail.Text, mail.UnsubscribeURL) {
		t.Fatalf("test mail: %+v", mail)
	}
	for i := 0; i < 4; i++ {
		_, _ = e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: cEmail}))
	}
	_, err = e.notify.SendTestNotification(ctx, bearer(alice, &tripmapv1.SendTestNotificationRequest{Channel: cEmail}))
	wantCode(t, err, connect.CodeResourceExhausted)
}

// --- reminders ---------------------------------------------------------------

func (e *env) runReminders() {
	e.t.Helper()
	idx, err := regions.Default()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := application.RunNotificationMaintenance(context.Background(), e.store, idx, e.notifier); err != nil {
		e.t.Fatal(err)
	}
}

// remindable creates an account that has a verified address and a browser and
// switches the given kinds on for both channels.
func (e *env) remindable(name string, places bool, tz string, hour int32, kinds ...string) string {
	e.t.Helper()
	token := e.register(name)
	e.verifyEmail(token, name+"@example.com")
	e.subscribe(token, "https://push.example.com/"+name)
	var prefs []*tripmapv1.NotificationPreference
	for _, k := range kinds {
		prefs = append(prefs, pref(k, cEmail, true), pref(k, cPush, true))
	}
	if _, err := e.update(token, prefs, tz, hour, places); err != nil {
		e.t.Fatal(err)
	}
	e.pusher.reset()
	e.mailer.reset()
	return token
}

func TestMemoryReminderIsSentOnceWithoutPlacesOrNotes(t *testing.T) {
	e := newEnv(t)
	alice := e.remindable("alice_mem", false, "Asia/Shanghai", 9, kMemory)
	rev := e.mustSync2(alice, "setup-1", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "t-2025", "2025-10-15", "私人备注：不要出现在通知里"),
		upTrip("330100", "t-other-day", "2025-10-17", "x"),
		upTrip("330100", "t-this-year", "2026-10-15", "x"),
		upCity("320100", "南京市", "320000", "#4b937d"),
		upTrip("320100", "t-2024", "2024-10-15", ""),
	)
	_ = rev

	// 10:00 in Shanghai on 15 October.
	e.runReminders()
	mails, pushes := e.mailer.all(), e.pusher.all()
	if len(mails) != 1 || len(pushes) != 1 {
		t.Fatalf("mails %d pushes %d", len(mails), len(pushes))
	}
	for _, text := range []string{mails[0].Subject + mails[0].Text, pushes[0].payload.Title + pushes[0].payload.Body} {
		if !strings.Contains(text, "一年前的今天") || !strings.Contains(text, "另有 1 段更早") {
			t.Fatalf("unexpected wording: %s", text)
		}
		for _, secret := range []string{"杭州", "南京", "私人备注", "t-2025"} {
			if strings.Contains(text, secret) {
				t.Fatalf("notification leaks %q: %s", secret, text)
			}
		}
	}
	if mails[0].To != "alice_mem@example.com" || pushes[0].endpoint != "https://push.example.com/alice_mem" || pushes[0].payload.URL != "https://trip.example.com/" {
		t.Fatalf("destinations: %+v %+v", mails[0], pushes[0])
	}
	if !strings.Contains(mails[0].Text, "不再接收") || mails[0].UnsubscribeURL == "" {
		t.Fatalf("mail lacks unsubscribe: %s", mails[0].Text)
	}

	// Another run, an hour later, still the same local day: nothing more.
	e.clock.set(e.clock.now().Add(time.Hour))
	e.runReminders()
	if len(e.mailer.all()) != 1 || len(e.pusher.all()) != 1 {
		t.Fatal("reminder was repeated")
	}
	// The next day has no anniversary, so there is nothing to send.
	e.clock.set(time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	if len(e.mailer.all()) != 1 || len(e.pusher.all()) != 1 {
		t.Fatal("reminder sent on a day without memories")
	}
	// Records of both channels exist for the day, and a later day records "skipped".
	ctx := context.Background()
	if n := e.store.DB.NotificationDelivery.Query().Where(notificationdelivery.StatusEQ(notificationdelivery.StatusSent)).CountX(ctx); n != 2 {
		t.Fatalf("sent deliveries: %d", n)
	}
	if n := e.store.DB.NotificationDelivery.Query().Where(notificationdelivery.StatusEQ(notificationdelivery.StatusSkipped)).CountX(ctx); n != 2 {
		t.Fatalf("skipped deliveries: %d", n)
	}
}

func TestMemoryReminderNamesPlacesOnlyWhenAllowed(t *testing.T) {
	e := newEnv(t)
	bob := e.remindable("bob_places", true, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(bob, "setup-1", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t1", "2025-10-15", "秘密"),
		upCity("320100", "南京市", "320000", "#4b937d"), upTrip("320100", "t2", "2025-10-15", ""),
	)
	e.runReminders()
	mails := e.mailer.all()
	if len(mails) != 1 || !strings.Contains(mails[0].Text, "南京市、杭州市") || strings.Contains(mails[0].Text, "秘密") {
		t.Fatalf("mail: %+v", mails)
	}
	// Turning the option off takes effect for the next reminder.
	if _, err := e.update(bob, nil, "Asia/Shanghai", 9, false); err != nil {
		t.Fatal(err)
	}
	e.clock.set(time.Date(2027, 10, 15, 2, 0, 0, 0, time.UTC))
	e.mailer.reset()
	e.runReminders()
	if m := e.mailer.all(); len(m) != 1 || strings.Contains(m[0].Text, "杭州") || !strings.Contains(m[0].Text, "2 年前的今天") {
		t.Fatalf("second-year mail: %+v", m)
	}
}

func TestReminderRespectsTimezoneAndSendHour(t *testing.T) {
	e := newEnv(t)
	// 02:00 UTC on 15 October is 19:00 on the 14th in Los Angeles and 10:00 on the 15th in Shanghai.
	la := e.remindable("la_user", false, "America/Los_Angeles", 9, kMemory)
	e.mustSync2(la, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-14", ""))
	late := e.remindable("late_user", false, "Asia/Shanghai", 11, kMemory)
	e.mustSync2(late, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))

	e.runReminders()
	mails := e.mailer.all()
	if len(mails) != 1 || mails[0].To != "la_user@example.com" {
		t.Fatalf("only the Los Angeles user is due: %+v", mails)
	}
	// An hour later 11:00 has arrived in Shanghai.
	e.clock.set(e.clock.now().Add(time.Hour))
	e.runReminders()
	mails = e.mailer.all()
	if len(mails) != 2 || mails[1].To != "late_user@example.com" {
		t.Fatalf("after the send hour: %+v", mails)
	}
}

func TestReminderUsesSchema1Dates(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	token := e.remindable("old_user", true, "Asia/Shanghai", 9, kMemory)
	if _, err := e.foot.UpsertVisit(ctx, bearer(token, &tripmapv1.UpsertVisitRequest{Visit: &tripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d", Date: "2025-10-15"}})); err != nil {
		t.Fatal(err)
	}
	e.runReminders()
	if m := e.mailer.all(); len(m) != 1 || !strings.Contains(m[0].Text, "杭州市") {
		t.Fatalf("schema 1 reminder: %+v", m)
	}
}

func TestLeapDayTripsAreRememberedOnFebruary28(t *testing.T) {
	e := newEnv(t)
	token := e.remindable("leap_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(token, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "leap", "2024-02-29", ""))
	e.clock.set(time.Date(2027, 2, 28, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	if m := e.mailer.all(); len(m) != 1 || !strings.Contains(m[0].Text, "3 年前的今天") {
		t.Fatalf("leap day: %+v", m)
	}
}

func TestAnnualRecapIsSentOnceInJanuaryWithCountsOnly(t *testing.T) {
	e := newEnv(t)
	token := e.remindable("annual_user", true, "Asia/Shanghai", 9, kAnnual)
	e.mustSync2(token, "s", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "a", "2026-03-01", "x"), upTrip("330100", "b", "2026-08-01", ""),
		upCity("320100", "南京市", "320000", "#4b937d"), upTrip("320100", "c", "2026-05-01", ""),
	)
	// Outside the first week of January nothing happens.
	e.clock.set(time.Date(2026, 12, 20, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	e.clock.set(time.Date(2027, 1, 20, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	if len(e.mailer.all())+len(e.pusher.all()) != 0 {
		t.Fatal("recap sent outside its window")
	}
	e.clock.set(time.Date(2027, 1, 2, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	e.clock.set(time.Date(2027, 1, 3, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	mails := e.mailer.all()
	if len(mails) != 1 || len(e.pusher.all()) != 1 {
		for _, r := range e.store.DB.NotificationDelivery.Query().AllX(context.Background()) {
			t.Logf("row %s %s %s %s %d", r.Kind, r.Channel, r.Period, r.Status, r.Attempts)
		}
		for _, m := range mails {
			t.Logf("mail %s %s", m.To, m.Subject)
		}
		t.Fatalf("recaps: %d mails, %d pushes", len(mails), len(e.pusher.all()))
	}
	text := mails[0].Subject + mails[0].Text
	if !strings.Contains(text, "2026 年旅行回顾") || !strings.Contains(text, "3 次旅行") || !strings.Contains(text, "2 座城市") || strings.Contains(text, "杭州") {
		t.Fatalf("recap: %s", text)
	}
}

func TestOnlySwitchedOnKindsAndChannelsAreUsed(t *testing.T) {
	e := newEnv(t)
	token := e.remindable("picky_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(token, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	// Email off for memories: only push is used.
	if _, err := e.update(token, []*tripmapv1.NotificationPreference{pref(kMemory, cEmail, false)}, "Asia/Shanghai", 9, false); err != nil {
		t.Fatal(err)
	}
	e.runReminders()
	if len(e.mailer.all()) != 0 || len(e.pusher.all()) != 1 {
		t.Fatalf("mails %d pushes %d", len(e.mailer.all()), len(e.pusher.all()))
	}
	// Everything off: nothing for the next year's anniversary.
	if _, err := e.update(token, []*tripmapv1.NotificationPreference{pref(kMemory, cPush, false)}, "Asia/Shanghai", 9, false); err != nil {
		t.Fatal(err)
	}
	e.clock.set(time.Date(2027, 10, 15, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	if len(e.mailer.all()) != 0 || len(e.pusher.all()) != 1 {
		t.Fatal("a switched-off channel was used")
	}
}

func TestFailedDeliveriesRetryThreeTimesThenStop(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	token := e.remindable("flaky_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(token, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	e.mailer.fail = true
	e.pusher.fail = true
	for i := 0; i < 5; i++ {
		e.runReminders()
		e.clock.set(e.clock.now().Add(time.Hour))
	}
	rows := e.store.DB.NotificationDelivery.Query().AllX(ctx)
	if len(rows) != 2 {
		t.Fatalf("rows: %d", len(rows))
	}
	for _, r := range rows {
		if r.Status != notificationdelivery.StatusFailed || r.Attempts != 3 {
			t.Fatalf("delivery %+v", r)
		}
	}
	// A third attempt that succeeds ends the retries: use a fresh day.
	e.clock.set(time.Date(2027, 10, 15, 2, 0, 0, 0, time.UTC))
	e.mailer.fail = false
	e.pusher.fail = true
	e.runReminders()
	if len(e.mailer.all()) != 1 || len(e.pusher.all()) != 0 {
		t.Fatalf("partial failure: %d mails %d pushes", len(e.mailer.all()), len(e.pusher.all()))
	}
	e.pusher.fail = false
	e.clock.set(e.clock.now().Add(time.Hour))
	e.runReminders()
	if len(e.mailer.all()) != 1 || len(e.pusher.all()) != 1 {
		t.Fatalf("retry should send only the failed channel: %d mails %d pushes", len(e.mailer.all()), len(e.pusher.all()))
	}
}

func TestRemindersSkipUnusableChannelsAndDisabledAccounts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	token := e.remindable("skip_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(token, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	uid := e.store.DB.User.Query().Where(user.Username("skip_user")).OnlyX(ctx).ID
	// An unverified or removed address and a removed browser are never used.
	e.store.DB.NotificationProfile.Update().Where(notificationprofile.UserID(uid)).ClearEmailVerifiedAt().ExecX(ctx)
	e.store.DB.PushSubscription.Delete().Where(pushsubscription.UserID(uid)).ExecX(ctx)
	e.runReminders()
	if len(e.mailer.all())+len(e.pusher.all()) != 0 {
		t.Fatal("unusable channels were used")
	}
	if n := e.store.DB.NotificationDelivery.Query().CountX(ctx); n != 0 {
		t.Fatalf("deliveries recorded for unusable channels: %d", n)
	}
	// Once usable again the same day's reminder still goes out.
	e.subscribe(token, "https://push.example.com/skip_user")
	e.runReminders()
	if len(e.pusher.all()) != 1 {
		t.Fatal("reminder was not sent after the browser returned")
	}

	// An account whose deletion is pending gets nothing, and deleting it removes every record.
	gone := e.remindable("gone_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(gone, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	gid := e.store.DB.User.Query().Where(user.Username("gone_user")).OnlyX(ctx).ID
	e.mustCleanupAccount(ctx, gid)
	e.pusher.reset()
	e.mailer.reset()
	e.clock.set(time.Date(2027, 10, 15, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	for _, p := range e.pusher.all() {
		if strings.HasSuffix(p.endpoint, "gone_user") {
			t.Fatal("deleted account was notified")
		}
	}
	for _, m := range e.mailer.all() {
		if m.To == "gone_user@example.com" {
			t.Fatal("deleted account was emailed")
		}
	}
	if e.store.DB.NotificationProfile.Query().Where(notificationprofile.UserID(gid)).ExistX(ctx) ||
		e.store.DB.NotificationSetting.Query().Where(notificationsetting.UserID(gid)).ExistX(ctx) ||
		e.store.DB.PushSubscription.Query().Where(pushsubscription.UserID(gid)).ExistX(ctx) ||
		e.store.DB.NotificationDelivery.Query().Where(notificationdelivery.UserID(gid)).ExistX(ctx) {
		t.Fatal("account deletion left notification records behind")
	}
}

func TestConcurrentRunsSendOnce(t *testing.T) {
	e := newEnv(t)
	token := e.remindable("race_user", false, "Asia/Shanghai", 9, kMemory)
	e.mustSync2(token, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.runReminders() }()
	}
	wg.Wait()
	if len(e.mailer.all()) != 1 || len(e.pusher.all()) != 1 {
		t.Fatalf("replicas sent %d mails and %d pushes", len(e.mailer.all()), len(e.pusher.all()))
	}
}

// --- unsubscribe ---------------------------------------------------------------

func (e *env) unsubscribe(method, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/notifications/unsubscribe?t="+url.QueryEscape(token), nil)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Body = http.NoBody
	}
	rec := httptest.NewRecorder()
	e.public.Unsubscribe(rec, req)
	return rec
}

func linkToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil || u.Query().Get("t") == "" || u.Path != "/api/notifications/unsubscribe" {
		t.Fatalf("bad unsubscribe link %q", link)
	}
	return u.Query().Get("t")
}

func TestUnsubscribeLinkNeedsPostAndOnlyAffectsItsScope(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	alice := e.remindable("alice_unsub", false, "Asia/Shanghai", 9, kMemory, kAnnual)
	bob := e.remindable("bob_unsub", false, "Asia/Shanghai", 9, kMemory, kAnnual)
	e.mustSync2(alice, "s", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t", "2025-10-15", ""))
	e.runReminders()
	mails := e.mailer.all()
	if len(mails) != 1 {
		t.Fatalf("mails: %d", len(mails))
	}
	token := linkToken(t, mails[0].UnsubscribeURL)

	// GET shows a confirmation and changes nothing (mail scanners follow links).
	page := e.unsubscribe(http.MethodGet, token)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `method="post"`) || !strings.Contains(page.Body.String(), "回忆提醒") {
		t.Fatalf("GET: %d %s", page.Code, page.Body.String())
	}
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(page.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("headers: %v", page.Header())
	}
	if !enabledSet(e.settings(alice).Settings)["memory/email"] {
		t.Fatal("a GET unsubscribed the account")
	}

	// The one-click POST switches off this kind's email only.
	if rec := e.unsubscribe(http.MethodPost, token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已退订") {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	on := enabledSet(e.settings(alice).Settings)
	if on["memory/email"] || !on["annual_report/email"] || !on["memory/push"] {
		t.Fatalf("scope: %v", on)
	}
	// Repeating it is harmless; Bob is untouched.
	if rec := e.unsubscribe(http.MethodPost, token); rec.Code != http.StatusOK {
		t.Fatalf("repeat: %d", rec.Code)
	}
	if !enabledSet(e.settings(bob).Settings)["memory/email"] {
		t.Fatal("another account was unsubscribed")
	}
	// No further reminder mail the next year.
	e.mailer.reset()
	e.clock.set(time.Date(2027, 10, 15, 2, 0, 0, 0, time.UTC))
	e.runReminders()
	for _, m := range e.mailer.all() {
		if m.To == "alice_unsub@example.com" {
			t.Fatal("mail sent after unsubscribing")
		}
	}

	// The "all" link in the mail turns every email kind off but leaves push alone.
	all := regexp.MustCompile(`https://trip\.example\.com/api/notifications/unsubscribe\?t=\S+`).FindAllString(mails[0].Text, -1)
	if len(all) != 2 {
		t.Fatalf("links in mail: %v", all)
	}
	if rec := e.unsubscribe(http.MethodPost, linkToken(t, all[1])); rec.Code != http.StatusOK {
		t.Fatalf("all: %d", rec.Code)
	}
	on = enabledSet(e.settings(alice).Settings)
	if on["annual_report/email"] || !on["annual_report/push"] {
		t.Fatalf("all scope: %v", on)
	}
	_ = ctx
}

func TestUnsubscribeRejectsForgedTokens(t *testing.T) {
	e := newEnv(t)
	alice := e.remindable("alice_forge", false, "Asia/Shanghai", 9, kMemory)
	uid := e.store.DB.User.Query().Where(user.Username("alice_forge")).OnlyX(context.Background()).ID
	good := notification.SignUnsubscribe(e.notifier.Config.Secret, notification.Unsubscribe{UserID: uid, Kind: notification.AllKinds})
	forged := notification.SignUnsubscribe(strings.Repeat("x", 32), notification.Unsubscribe{UserID: uid, Kind: notification.AllKinds})
	for name, token := range map[string]string{"empty": "", "garbage": "abc", "wrong secret": forged, "truncated": good[:len(good)-3], "swapped parts": strings.Join(reverse(strings.Split(good, ".")), ".")} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if rec := e.unsubscribe(method, token); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", name, method, rec.Code)
			}
		}
	}
	if !enabledSet(e.settings(alice).Settings)["memory/email"] {
		t.Fatal("a forged token changed settings")
	}
	// Without a configured secret the route is inert.
	e.notifier.Config.Secret = ""
	if rec := e.unsubscribe(http.MethodPost, good); rec.Code != http.StatusNotFound {
		t.Fatalf("no secret: %d", rec.Code)
	}
}

func reverse(s []string) []string {
	out := append([]string(nil), s...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
