package application

import (
	"context"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent/notificationsetting"
	"github.com/kongken/mono/internal/tripmap/notification"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const unsubscribeRate = 30

// NotificationPublic serves the unauthenticated unsubscribe page that the links
// in reminder emails lead to. The signed token is the credential: it only allows
// switching the email channel off for the account that received the message.
//
//	GET  /api/notifications/unsubscribe?t=<token>  asks for confirmation
//	POST /api/notifications/unsubscribe?t=<token>  switches it off (also RFC 8058 one-click)
//
// A GET never changes anything, so mail scanners and link previews that fetch
// every link cannot unsubscribe anyone by accident.
type NotificationPublic struct {
	cfg      *config.AppConfig
	src      StoreSource
	notifier *Notifier
}

func NewNotificationPublic(cfg *config.AppConfig, src StoreSource, notifier *Notifier) *NotificationPublic {
	return &NotificationPublic{cfg: cfg, src: src, notifier: notifier}
}

var unsubscribeTemplate = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow"><title>{{.Title}} · 山海足迹</title>
<style>body{font:16px/1.7 system-ui,"PingFang SC","Noto Sans SC",sans-serif;color:#2b3a33;background:#f4f1e8;margin:0;display:grid;place-items:center;min-height:100vh;padding:16px;box-sizing:border-box}
main{background:#fff;border:1px solid #d9d3c0;border-radius:14px;padding:28px;max-width:420px;width:100%}
h1{font-size:20px;margin:0 0 12px}p{margin:0 0 16px}
button{font:inherit;background:#4b937d;color:#fff;border:0;border-radius:8px;padding:10px 18px;cursor:pointer}
button:focus-visible{outline:3px solid #e2af5c;outline-offset:2px}</style></head>
<body><main><h1>{{.Title}}</h1><p>{{.Message}}</p>
{{if .Action}}<form method="post" action="{{.Action}}"><button type="submit">确认退订</button></form>{{end}}
</main></body></html>`))

type unsubscribePage struct {
	Title, Message string
	Action         template.URL
}

func (p *NotificationPublic) page(w http.ResponseWriter, code int, data unsubscribePage) {
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(code)
	_ = unsubscribeTemplate.Execute(w, data)
}

func (p *NotificationPublic) invalid(w http.ResponseWriter) {
	p.page(w, http.StatusNotFound, unsubscribePage{Title: "链接无效", Message: "这个退订链接无效或已失效。可以登录山海足迹，在“账号 → 提醒与通知”中管理提醒。"})
}

func scopeLabel(kind string) string {
	if kind == notification.AllKinds {
		return "全部提醒邮件"
	}
	return "“" + notification.Kind(kind).Label() + "”邮件"
}

type emailUnsubscribeOutcome struct {
	Status     tripmapv1.EmailUnsubscribeStatus
	ScopeLabel string
}

func (p *NotificationPublic) processEmailUnsubscribe(ctx context.Context, clientIP, token string, confirm bool) (emailUnsubscribeOutcome, error) {
	if p.notifier == nil || p.notifier.Config.Secret == "" {
		return emailUnsubscribeOutcome{Status: tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_INVALID}, nil
	}
	st := p.src.Store()
	if st == nil {
		return emailUnsubscribeOutcome{}, unavailableUnsubscribe()
	}
	ok, err := NewLimiter(st.Redis, st.prefix()).Allow(ctx, "unsubscribe:"+clientIP, unsubscribeRate, time.Minute)
	if err != nil {
		slog.Error("unsubscribe rate limiter unavailable", "error", err)
		return emailUnsubscribeOutcome{}, unavailableUnsubscribe()
	}
	if !ok {
		return emailUnsubscribeOutcome{}, status.Error(codes.ResourceExhausted, "too many requests")
	}
	target, err := notification.VerifyUnsubscribe(p.notifier.Config.Secret, token)
	if err != nil {
		return emailUnsubscribeOutcome{Status: tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_INVALID}, nil
	}
	label := scopeLabel(target.Kind)
	if !confirm {
		return emailUnsubscribeOutcome{Status: tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_PREVIEW, ScopeLabel: label}, nil
	}
	q := st.DB.NotificationSetting.Update().Where(notificationsetting.UserID(target.UserID), notificationsetting.ChannelEQ(notificationsetting.ChannelEmail))
	if target.Kind != notification.AllKinds {
		q = q.Where(notificationsetting.KindEQ(notificationsetting.Kind(target.Kind)))
	}
	if _, err = q.SetEnabled(false).Save(ctx); err != nil {
		slog.Error("unsubscribe failed", "error", err)
		return emailUnsubscribeOutcome{}, status.Error(codes.Internal, "unsubscribe failed")
	}
	return emailUnsubscribeOutcome{Status: tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_COMPLETED, ScopeLabel: label}, nil
}

// Unsubscribe handles both verbs of the unsubscribe route (application tests).
func (p *NotificationPublic) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		plain(w, http.StatusMethodNotAllowed, false)
		return
	}
	ip := httpx.ClientAddr(p.cfg.Auth.ClientIPHeader, r.Header, r.RemoteAddr)
	token := r.URL.Query().Get("t")
	confirm := r.Method == http.MethodPost
	if confirm {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
	}
	out, err := p.processEmailUnsubscribe(r.Context(), ip, token, confirm)
	if err != nil {
		if status.Code(err) == codes.ResourceExhausted {
			plain(w, http.StatusTooManyRequests, true)
			return
		}
		plain(w, http.StatusServiceUnavailable, false)
		return
	}
	switch out.Status {
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_INVALID:
		p.invalid(w)
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_PREVIEW:
		p.page(w, http.StatusOK, unsubscribePage{
			Title:   "退订提醒邮件",
			Message: "确认后将不再向你的邮箱发送" + out.ScopeLabel + "。之后可随时在山海足迹的“提醒与通知”中重新开启。",
			Action:  template.URL("?t=" + url.QueryEscape(token)),
		})
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_COMPLETED:
		p.page(w, http.StatusOK, unsubscribePage{Title: "已退订", Message: "已停止发送" + out.ScopeLabel + "。如果想再次接收，可在山海足迹的“提醒与通知”中开启。"})
	}
}
