package notification

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

// PushTarget is one browser subscription.
type PushTarget struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// PushPayload is what the service worker receives.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is opened when the notification is tapped.
	URL string `json:"url"`
	// Tag replaces an earlier notification of the same kind on the device.
	Tag string `json:"tag"`
}

// ErrSubscriptionGone means the push service says the subscription no longer
// exists (HTTP 404/410): the caller should delete it.
var ErrSubscriptionGone = errors.New("push subscription is gone")

// PushSender delivers one payload to one subscription.
type PushSender interface {
	Send(ctx context.Context, target PushTarget, payload PushPayload) error
}

// ValidPushKeys checks the browser-supplied key material: p256dh is an
// uncompressed P-256 point (65 bytes) and auth a 16-byte secret, both base64url.
func ValidPushKeys(p256dh, auth string) error {
	key, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil || len(key) != 65 || key[0] != 4 {
		return errors.New("invalid push p256dh key")
	}
	secret, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil || len(secret) != 16 {
		return errors.New("invalid push auth secret")
	}
	return nil
}

// WebPush sends VAPID-signed, encrypted Web Push messages.
type WebPush struct {
	PublicKey, PrivateKey, Subject string
	client                         *http.Client
	// TTL is how long a push service holds a message for an offline device.
	TTL time.Duration
}

// NewWebPush builds a sender that only reaches public hosts.
func NewWebPush(publicKey, privateKey, subject string) *WebPush {
	return &WebPush{PublicKey: publicKey, PrivateKey: privateKey, Subject: subject, client: guardedClient(15 * time.Second), TTL: 12 * time.Hour}
}

func (w *WebPush) Send(ctx context.Context, target PushTarget, payload PushPayload) error {
	if err := ValidPushEndpoint(target.Endpoint); err != nil {
		return err
	}
	return w.send(ctx, target, payload)
}

func (w *WebPush) send(ctx context.Context, target PushTarget, payload PushPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: target.Endpoint,
		Keys:     webpush.Keys{P256dh: target.P256dh, Auth: target.Auth},
	}, &webpush.Options{
		HTTPClient:      w.client,
		Subscriber:      w.Subject,
		VAPIDPublicKey:  w.PublicKey,
		VAPIDPrivateKey: w.PrivateKey,
		TTL:             int(w.TTL.Seconds()),
		Urgency:         webpush.UrgencyLow,
		Topic:           payload.Tag,
	})
	if err != nil {
		return fmt.Errorf("push request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrSubscriptionGone
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	}
	return fmt.Errorf("push service answered %d", resp.StatusCode)
}
