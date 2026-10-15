package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/tripmap/auth"
	"go.orx.me/apps/trip-map/internal/tripmap/ent"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/notificationprofile"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/notificationsetting"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/pushsubscription"
	"go.orx.me/apps/trip-map/internal/tripmap/notification"
	"go.orx.me/apps/trip-map/internal/tripmap/report"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	emailCodeTTL          = 15 * time.Minute
	emailCodeMaxAttempts  = 5
	maxPushSubscriptions  = 10
	pushFailureLimit      = 5
	notificationReadLimit = 120
	notificationWriteRate = 30
)

// NotificationService implements reminder settings, email verification and push
// subscriptions (F12).
type NotificationService struct {
	tripmapv1connect.UnimplementedNotificationServiceHandler
	src      StoreSource
	notifier *Notifier
}

func NewNotificationService(src StoreSource, notifier *Notifier) *NotificationService {
	return &NotificationService{src: src, notifier: notifier}
}

func (s *NotificationService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("notification service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many requests, try again later"))
	}
	return nil
}

func kindFromWire(k string) (notification.Kind, bool) {
	kind := notification.Kind(k)
	return kind, kind.Valid()
}

func channelFromWire(c string) (notification.Channel, bool) {
	channel := notification.Channel(c)
	return channel, channel.Valid()
}

type settingKey struct {
	kind    notification.Kind
	channel notification.Channel
}

// notificationState is the stored state of one account.
type notificationState struct {
	profile *ent.NotificationProfile // nil until the first write
	enabled map[settingKey]bool
	pushes  int
}

func (st *notificationState) timezone() string {
	if st.profile != nil {
		return st.profile.Timezone
	}
	return report.DefaultTimezone
}

func (st *notificationState) sendHour() int {
	if st.profile != nil {
		return st.profile.SendHour
	}
	return 9
}

func (st *notificationState) showPlaces() bool { return st.profile != nil && st.profile.ShowPlaces }

func (st *notificationState) verifiedEmail() string {
	if st.profile != nil && st.profile.EmailVerifiedAt != nil && st.profile.Email != "" {
		return st.profile.Email
	}
	return ""
}

func loadNotificationState(ctx context.Context, c *ent.Client, tx *ent.Tx, userID string) (*notificationState, error) {
	var (
		profile *ent.NotificationProfile
		rows    []*ent.NotificationSetting
		pushes  int
		err     error
	)
	if tx != nil {
		profile, err = tx.NotificationProfile.Query().Where(notificationprofile.UserID(userID)).Only(ctx)
	} else {
		profile, err = c.NotificationProfile.Query().Where(notificationprofile.UserID(userID)).Only(ctx)
	}
	if err != nil && !ent.IsNotFound(err) {
		return nil, err
	}
	if ent.IsNotFound(err) {
		profile = nil
	}
	if tx != nil {
		rows, err = tx.NotificationSetting.Query().Where(notificationsetting.UserID(userID)).All(ctx)
	} else {
		rows, err = c.NotificationSetting.Query().Where(notificationsetting.UserID(userID)).All(ctx)
	}
	if err != nil {
		return nil, err
	}
	if tx != nil {
		pushes, err = tx.PushSubscription.Query().Where(pushsubscription.UserID(userID)).Count(ctx)
	} else {
		pushes, err = c.PushSubscription.Query().Where(pushsubscription.UserID(userID)).Count(ctx)
	}
	if err != nil {
		return nil, err
	}
	out := &notificationState{profile: profile, enabled: map[settingKey]bool{}, pushes: pushes}
	for _, r := range rows {
		out.enabled[settingKey{notification.Kind(r.Kind), notification.Channel(r.Channel)}] = r.Enabled
	}
	return out, nil
}

func (n *Notifier) settingsMessage(st *notificationState) *tripmapv1.NotificationSettings {
	out := &tripmapv1.NotificationSettings{
		Timezone:              st.timezone(),
		SendHour:              int32(st.sendHour()),
		ShowPlaces:            st.showPlaces(),
		PushSubscriptionCount: int32(st.pushes),
	}
	for _, k := range notification.Kinds {
		for _, c := range notification.Channels {
			out.Preferences = append(out.Preferences, &tripmapv1.NotificationPreference{
				Kind: string(k), Channel: string(c), Enabled: st.enabled[settingKey{k, c}],
			})
		}
	}
	if email := st.verifiedEmail(); email != "" {
		out.EmailMasked = notification.MaskEmail(email)
		out.EmailVerified = true
	}
	if p := st.profile; p != nil && p.PendingEmail != "" && p.CodeExpiresAt != nil && p.CodeExpiresAt.After(n.now()) {
		out.PendingEmailMasked = notification.MaskEmail(p.PendingEmail)
		out.PendingExpiresAt = timestamppb.New(*p.CodeExpiresAt)
	}
	return out
}

func (s *NotificationService) current(ctx context.Context, st *Store, p auth.Principal) (*notificationState, error) {
	state, err := loadNotificationState(ctx, st.DB, nil, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	return state, nil
}

func (s *NotificationService) GetNotificationSettings(ctx context.Context, _ *connect.Request[tripmapv1.GetNotificationSettingsRequest]) (*connect.Response[tripmapv1.GetNotificationSettingsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "notify-read:"+p.UserID, notificationReadLimit, time.Minute); err != nil {
		return nil, err
	}
	state, err := s.current(ctx, st, p)
	if err != nil {
		return nil, err
	}
	res := &tripmapv1.GetNotificationSettingsResponse{
		Settings:       s.notifier.settingsMessage(state),
		EmailAvailable: s.notifier.EmailAvailable(),
		PushAvailable:  s.notifier.PushAvailable(),
	}
	if res.PushAvailable {
		res.VapidPublicKey = s.notifier.Config.WebPush.VAPIDPublicKey
	}
	return connect.NewResponse(res), nil
}

func ensureProfile(ctx context.Context, tx *ent.Tx, userID string) (*ent.NotificationProfile, error) {
	profile, err := tx.NotificationProfile.Query().Where(notificationprofile.UserID(userID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return tx.NotificationProfile.Create().SetUserID(userID).Save(ctx)
	}
	return profile, err
}

func (s *NotificationService) UpdateNotificationSettings(ctx context.Context, req *connect.Request[tripmapv1.UpdateNotificationSettingsRequest]) (*connect.Response[tripmapv1.UpdateNotificationSettingsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if m.Timezone == "" || len(m.Timezone) > 64 {
		return nil, invalid("timezone must be an IANA time zone name")
	}
	if _, err := time.LoadLocation(m.Timezone); err != nil {
		return nil, invalid("timezone must be an IANA time zone name")
	}
	if m.SendHour < 0 || m.SendHour > 23 {
		return nil, invalid("send_hour must be between 0 and 23")
	}
	if len(m.Preferences) > len(notification.Kinds)*len(notification.Channels) {
		return nil, invalid("too many preferences")
	}
	type change struct {
		key     settingKey
		enabled bool
	}
	var changes []change
	seen := map[settingKey]bool{}
	for _, pref := range m.Preferences {
		kind, ok := kindFromWire(pref.Kind)
		if !ok {
			return nil, invalid("unknown notification kind")
		}
		channel, ok := channelFromWire(pref.Channel)
		if !ok {
			return nil, invalid("unknown notification channel")
		}
		key := settingKey{kind, channel}
		if seen[key] {
			return nil, invalid("duplicate preference")
		}
		seen[key] = true
		changes = append(changes, change{key, pref.Enabled})
	}
	if err = s.allow(ctx, st, "notify-write:"+p.UserID, notificationWriteRate, time.Minute); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUser(ctx, tx, p.UserID); err != nil {
		return nil, err
	}
	profile, err := ensureProfile(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	for _, c := range changes {
		if !c.enabled {
			continue
		}
		switch c.key.channel {
		case notification.ChannelEmail:
			if !s.notifier.EmailAvailable() {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("email notifications are not available on this server"))
			}
			if profile.EmailVerifiedAt == nil || profile.Email == "" {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("verify an email address first"))
			}
		case notification.ChannelPush:
			if !s.notifier.PushAvailable() {
				return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("browser push is not available on this server"))
			}
		}
	}
	if err = tx.NotificationProfile.UpdateOneID(profile.ID).SetTimezone(m.Timezone).SetSendHour(int(m.SendHour)).SetShowPlaces(m.ShowPlaces).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	for _, c := range changes {
		err = tx.NotificationSetting.Create().SetUserID(p.UserID).SetKind(notificationsetting.Kind(c.key.kind)).SetChannel(notificationsetting.Channel(c.key.channel)).SetEnabled(c.enabled).
			OnConflictColumns(notificationsetting.FieldUserID, notificationsetting.FieldKind, notificationsetting.FieldChannel).UpdateEnabled().UpdateUpdatedAt().Exec(ctx)
		if err != nil {
			return nil, internalError(err)
		}
	}
	state, err := loadNotificationState(ctx, nil, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.UpdateNotificationSettingsResponse{Settings: s.notifier.settingsMessage(state)}), nil
}

func hashCode(userID, email, code string) string {
	sum := sha256.Sum256([]byte("tripmap-email-code-v1\x00" + userID + "\x00" + email + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func (s *NotificationService) RequestEmailVerification(ctx context.Context, req *connect.Request[tripmapv1.RequestEmailVerificationRequest]) (*connect.Response[tripmapv1.RequestEmailVerificationResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if !s.notifier.EmailAvailable() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("email notifications are not available on this server"))
	}
	email, err := notification.ValidEmail(req.Msg.Email)
	if err != nil {
		return nil, invalid("enter a valid email address")
	}
	// Limits protect the sender's reputation and the recipient: per account, and
	// per address no matter which accounts ask.
	sum := sha256.Sum256([]byte(email))
	if err = s.allow(ctx, st, "notify-email:"+p.UserID, 5, time.Hour); err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "notify-email-addr:"+hex.EncodeToString(sum[:8]), 3, time.Hour); err != nil {
		return nil, err
	}
	code, err := newCode()
	if err != nil {
		return nil, internalError(err)
	}
	expires := s.notifier.now().Add(emailCodeTTL)
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUser(ctx, tx, p.UserID); err != nil {
		return nil, err
	}
	profile, err := ensureProfile(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.NotificationProfile.UpdateOneID(profile.ID).SetPendingEmail(email).SetCodeHash(hashCode(p.UserID, email, code)).SetCodeExpiresAt(expires).SetCodeAttempts(0).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	mail := notification.Mail{
		To:      email,
		Subject: "山海足迹邮箱验证码",
		Text:    fmt.Sprintf("你的山海足迹验证码是 %s，%d 分钟内有效。\n\n如果这不是你本人的操作，请忽略这封邮件，不会有任何改动。\n", code, int(emailCodeTTL.Minutes())),
	}
	if err = s.notifier.sendMail(ctx, mail); err != nil {
		slog.Error("verification email failed", "error", err)
		_ = st.DB.NotificationProfile.UpdateOneID(profile.ID).SetPendingEmail("").SetCodeHash("").ClearCodeExpiresAt().Exec(ctx)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("could not send the verification email, try again later"))
	}
	return connect.NewResponse(&tripmapv1.RequestEmailVerificationResponse{
		PendingEmailMasked: notification.MaskEmail(email), ExpiresAt: timestamppb.New(expires),
	}), nil
}

func (s *NotificationService) ConfirmEmail(ctx context.Context, req *connect.Request[tripmapv1.ConfirmEmailRequest]) (*connect.Response[tripmapv1.ConfirmEmailResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	code := req.Msg.Code
	if len(code) != 6 {
		return nil, invalid("enter the six-digit code")
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return nil, invalid("enter the six-digit code")
		}
	}
	if err = s.allow(ctx, st, "notify-confirm:"+p.UserID, 20, time.Hour); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUser(ctx, tx, p.UserID); err != nil {
		return nil, err
	}
	profile, err := ensureProfile(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	now := s.notifier.now()
	if profile.PendingEmail == "" || profile.CodeExpiresAt == nil || !profile.CodeExpiresAt.After(now) || profile.CodeAttempts >= emailCodeMaxAttempts {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the code expired or was used up, request a new one"))
	}
	want := hashCode(p.UserID, profile.PendingEmail, code)
	if subtle.ConstantTimeCompare([]byte(want), []byte(profile.CodeHash)) != 1 {
		// A wrong guess is recorded even though this response is an error, so it
		// is committed instead of rolled back.
		if err = tx.NotificationProfile.UpdateOneID(profile.ID).AddCodeAttempts(1).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
		if err = tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the code is not correct"))
	}
	if err = tx.NotificationProfile.UpdateOneID(profile.ID).SetEmail(profile.PendingEmail).SetEmailVerifiedAt(now).SetPendingEmail("").SetCodeHash("").ClearCodeExpiresAt().SetCodeAttempts(0).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	state, err := loadNotificationState(ctx, nil, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.ConfirmEmailResponse{Settings: s.notifier.settingsMessage(state)}), nil
}

func (s *NotificationService) RemoveEmail(ctx context.Context, _ *connect.Request[tripmapv1.RemoveEmailRequest]) (*connect.Response[tripmapv1.RemoveEmailResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "notify-write:"+p.UserID, notificationWriteRate, time.Minute); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUser(ctx, tx, p.UserID); err != nil {
		return nil, err
	}
	profile, err := ensureProfile(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.NotificationProfile.UpdateOneID(profile.ID).SetEmail("").ClearEmailVerifiedAt().SetPendingEmail("").SetCodeHash("").ClearCodeExpiresAt().SetCodeAttempts(0).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = tx.NotificationSetting.Update().Where(notificationsetting.UserID(p.UserID), notificationsetting.ChannelEQ(notificationsetting.ChannelEmail)).SetEnabled(false).Save(ctx); err != nil {
		return nil, internalError(err)
	}
	state, err := loadNotificationState(ctx, nil, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RemoveEmailResponse{Settings: s.notifier.settingsMessage(state)}), nil
}

func endpointHash(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:])
}

func (s *NotificationService) RegisterPushSubscription(ctx context.Context, req *connect.Request[tripmapv1.RegisterPushSubscriptionRequest]) (*connect.Response[tripmapv1.RegisterPushSubscriptionResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if !s.notifier.PushAvailable() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("browser push is not available on this server"))
	}
	m := req.Msg
	if err = notification.ValidPushEndpoint(m.Endpoint); err != nil {
		return nil, invalid(err.Error())
	}
	if err = notification.ValidPushKeys(m.P256Dh, m.Auth); err != nil {
		return nil, invalid(err.Error())
	}
	if err = s.allow(ctx, st, "notify-write:"+p.UserID, notificationWriteRate, time.Minute); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUser(ctx, tx, p.UserID); err != nil {
		return nil, err
	}
	hash := endpointHash(m.Endpoint)
	existing, err := tx.PushSubscription.Query().Where(pushsubscription.EndpointHash(hash)).Only(ctx)
	switch {
	case err == nil:
		// Same browser: keys may have rotated, and it may have been used by another
		// account, which then stops receiving here.
		if err = tx.PushSubscription.UpdateOneID(existing.ID).SetUserID(p.UserID).SetP256dh(m.P256Dh).SetAuth(m.Auth).SetFailures(0).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	case ent.IsNotFound(err):
		own, qerr := tx.PushSubscription.Query().Where(pushsubscription.UserID(p.UserID)).Order(ent.Asc(pushsubscription.FieldUpdatedAt)).All(ctx)
		if qerr != nil {
			return nil, internalError(qerr)
		}
		// Keep the newest devices; the oldest make room.
		for len(own) >= maxPushSubscriptions {
			if err = tx.PushSubscription.DeleteOneID(own[0].ID).Exec(ctx); err != nil {
				return nil, internalError(err)
			}
			own = own[1:]
		}
		if err = tx.PushSubscription.Create().SetUserID(p.UserID).SetEndpointHash(hash).SetEndpoint(m.Endpoint).SetP256dh(m.P256Dh).SetAuth(m.Auth).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	default:
		return nil, internalError(err)
	}
	count, err := tx.PushSubscription.Query().Where(pushsubscription.UserID(p.UserID)).Count(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RegisterPushSubscriptionResponse{PushSubscriptionCount: int32(count)}), nil
}

func (s *NotificationService) RemovePushSubscription(ctx context.Context, req *connect.Request[tripmapv1.RemovePushSubscriptionRequest]) (*connect.Response[tripmapv1.RemovePushSubscriptionResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if len(req.Msg.Endpoint) == 0 || len(req.Msg.Endpoint) > 2048 {
		return nil, invalid("push endpoint must be 1-2048 characters")
	}
	if err = s.allow(ctx, st, "notify-write:"+p.UserID, notificationWriteRate, time.Minute); err != nil {
		return nil, err
	}
	// Only the caller's own row can match, so another account's subscription is untouched.
	if _, err = st.DB.PushSubscription.Delete().Where(pushsubscription.UserID(p.UserID), pushsubscription.EndpointHash(endpointHash(req.Msg.Endpoint))).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	count, err := st.DB.PushSubscription.Query().Where(pushsubscription.UserID(p.UserID)).Count(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RemovePushSubscriptionResponse{PushSubscriptionCount: int32(count)}), nil
}

func (s *NotificationService) SendTestNotification(ctx context.Context, req *connect.Request[tripmapv1.SendTestNotificationRequest]) (*connect.Response[tripmapv1.SendTestNotificationResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	channel, ok := channelFromWire(req.Msg.Channel)
	if !ok {
		return nil, invalid("unknown notification channel")
	}
	if err = s.allow(ctx, st, "notify-test:"+p.UserID, 5, time.Hour); err != nil {
		return nil, err
	}
	state, err := s.current(ctx, st, p)
	if err != nil {
		return nil, err
	}
	msg := notification.Message{Title: "山海足迹 · 测试通知", Body: "这是一条测试通知。收到它说明提醒可以送达；它不包含任何足迹内容。"}
	delivered := 0
	switch channel {
	case notification.ChannelEmail:
		if !s.notifier.EmailAvailable() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("email notifications are not available on this server"))
		}
		email := state.verifiedEmail()
		if email == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("verify an email address first"))
		}
		mail := s.notifier.testMail(email, p.UserID, msg)
		if err = s.notifier.sendMail(ctx, mail); err != nil {
			slog.Error("test email failed", "error", err)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("could not send the test email"))
		}
		delivered = 1
	case notification.ChannelPush:
		if !s.notifier.PushAvailable() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("browser push is not available on this server"))
		}
		if state.pushes == 0 {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no browser is subscribed yet"))
		}
		delivered = s.notifier.pushTo(ctx, st, p.UserID, notification.PushPayload{Title: msg.Title, Body: msg.Body, URL: s.notifier.appURL(), Tag: "test"})
		if delivered == 0 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("could not reach any subscribed browser; subscribe again on this device"))
		}
	}
	return connect.NewResponse(&tripmapv1.SendTestNotificationResponse{Delivered: int32(delivered)}), nil
}

// pushTo sends payload to every subscription of the user and returns how many
// accepted it. A subscription the push service reports gone is deleted; one that
// keeps failing is dropped after pushFailureLimit attempts, so dead browsers do
// not accumulate. The error text never reaches clients: endpoints are capabilities.
func (n *Notifier) pushTo(ctx context.Context, st *Store, userID string, payload notification.PushPayload) int {
	subs, err := st.DB.PushSubscription.Query().Where(pushsubscription.UserID(userID)).All(ctx)
	if err != nil {
		slog.Error("load push subscriptions failed", "error", err)
		return 0
	}
	delivered := 0
	for _, sub := range subs {
		err := n.Push.Send(ctx, notification.PushTarget{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, payload)
		switch {
		case err == nil:
			delivered++
			if sub.Failures > 0 {
				_ = st.DB.PushSubscription.UpdateOneID(sub.ID).SetFailures(0).Exec(ctx)
			}
		case errors.Is(err, notification.ErrSubscriptionGone):
			_ = st.DB.PushSubscription.DeleteOneID(sub.ID).Exec(ctx)
		default:
			slog.Warn("push delivery failed", "error", err)
			if sub.Failures+1 >= pushFailureLimit {
				_ = st.DB.PushSubscription.DeleteOneID(sub.ID).Exec(ctx)
			} else {
				_ = st.DB.PushSubscription.UpdateOneID(sub.ID).AddFailures(1).Exec(ctx)
			}
		}
	}
	return delivered
}
