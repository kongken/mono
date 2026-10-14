package application

import (
	"context"
	"net/url"
	"time"

	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/notification"
)

// Notifier bundles the outgoing channels and configuration shared by the
// notification RPCs, the public unsubscribe page and the reminder job. A nil
// channel means that channel is unavailable on this server.
type Notifier struct {
	Config config.NotificationConfig
	Mail   notification.Mailer
	Push   notification.PushSender
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// NewNotifier connects every channel the configuration enables.
func NewNotifier(cfg config.NotificationConfig) *Notifier {
	n := &Notifier{Config: cfg, Now: func() time.Time { return time.Now().UTC() }}
	if cfg.EmailEnabled() {
		n.Mail = &notification.SMTP{
			Host: cfg.SMTP.Host, Port: cfg.SMTP.Port, Username: cfg.SMTP.Username, Password: cfg.SMTP.Password,
			From: cfg.SMTP.From, Security: cfg.SMTPSecurity(),
		}
	}
	if cfg.PushEnabled() {
		n.Push = notification.NewWebPush(cfg.WebPush.VAPIDPublicKey, cfg.WebPush.VAPIDPrivateKey, cfg.WebPush.Subject)
	}
	return n
}

// EmailAvailable reports whether reminder and verification emails can be sent.
func (n *Notifier) EmailAvailable() bool {
	return n != nil && n.Mail != nil && n.Config.EmailEnabled()
}

// PushAvailable reports whether browser push can be sent.
func (n *Notifier) PushAvailable() bool {
	return n != nil && n.Push != nil && n.Config.PushEnabled()
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now().UTC()
}

// appURL is where tapping a notification or the email's link leads.
func (n *Notifier) appURL() string { return n.Config.BaseURL() + "/" }

func (n *Notifier) unsubscribeURL(userID, kind string) string {
	token := notification.SignUnsubscribe(n.Config.Secret, notification.Unsubscribe{UserID: userID, Kind: kind})
	return n.Config.BaseURL() + "/api/notifications/unsubscribe?t=" + url.QueryEscape(token)
}

// reminderMail builds the email for a kind's message with its unsubscribe links.
func (n *Notifier) reminderMail(to, userID string, kind notification.Kind, msg notification.Message) notification.Mail {
	one := n.unsubscribeURL(userID, string(kind))
	all := n.unsubscribeURL(userID, notification.AllKinds)
	text := msg.Body + "\n\n打开山海足迹：" + n.appURL() + "\n\n" +
		"你收到这封邮件，是因为在山海足迹中开启了“" + kind.Label() + "”的邮件通知。\n" +
		"不再接收这类邮件：" + one + "\n" +
		"不再接收任何提醒邮件：" + all + "\n"
	return notification.Mail{To: to, Subject: msg.Title, Text: text, UnsubscribeURL: one}
}

// testMail is the message of SendTestNotification: generic, with one link that
// unsubscribes from every reminder email.
func (n *Notifier) testMail(to, userID string, msg notification.Message) notification.Mail {
	all := n.unsubscribeURL(userID, notification.AllKinds)
	return notification.Mail{
		To: to, Subject: msg.Title, UnsubscribeURL: all,
		Text: msg.Body + "\n\n打开山海足迹：" + n.appURL() + "\n\n不再接收任何提醒邮件：" + all + "\n",
	}
}

func (n *Notifier) sendMail(ctx context.Context, m notification.Mail) error {
	return n.Mail.Send(ctx, m)
}
