package notification

import (
	"html/template"
	"io"
	"net/http"
	"net/url"

	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnsubscribeHTTP serves the signed email-unsubscribe page at the BFF edge.
type UnsubscribeHTTP struct {
	client         tripmapv1.NotificationPublicServiceClient
	clientIPHeader string
}

func NewUnsubscribeHTTP(client tripmapv1.NotificationPublicServiceClient, clientIPHeader string) *UnsubscribeHTTP {
	return &UnsubscribeHTTP{client: client, clientIPHeader: clientIPHeader}
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

func (u *UnsubscribeHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		plain(w, http.StatusMethodNotAllowed, false)
		return
	}
	token := r.URL.Query().Get("t")
	confirm := r.Method == http.MethodPost
	if confirm {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
	}
	ip := httpx.ClientAddr(u.clientIPHeader, r.Header, r.RemoteAddr)
	ctx := rpcauth.WithCall(r.Context(), rpcauth.Call{ClientIP: ip})
	out, err := u.client.ProcessEmailUnsubscribe(ctx, &tripmapv1.ProcessEmailUnsubscribeRequest{Token: token, Confirm: confirm})
	if err != nil {
		switch status.Code(err) {
		case codes.ResourceExhausted:
			plain(w, http.StatusTooManyRequests, true)
		default:
			plain(w, http.StatusServiceUnavailable, false)
		}
		return
	}
	switch out.GetStatus() {
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_INVALID:
		u.page(w, http.StatusNotFound, unsubscribePage{Title: "链接无效", Message: "这个退订链接无效或已失效。可以登录山海足迹，在“账号 → 提醒与通知”中管理提醒。"})
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_PREVIEW:
		u.page(w, http.StatusOK, unsubscribePage{
			Title:   "退订提醒邮件",
			Message: "确认后将不再向你的邮箱发送" + out.GetScopeLabel() + "。之后可随时在山海足迹的“提醒与通知”中重新开启。",
			Action:  template.URL("?t=" + url.QueryEscape(token)),
		})
	case tripmapv1.EmailUnsubscribeStatus_EMAIL_UNSUBSCRIBE_STATUS_COMPLETED:
		u.page(w, http.StatusOK, unsubscribePage{Title: "已退订", Message: "已停止发送" + out.GetScopeLabel() + "。如果想再次接收，可在山海足迹的“提醒与通知”中开启。"})
	default:
		plain(w, http.StatusServiceUnavailable, false)
	}
}

func (u *UnsubscribeHTTP) page(w http.ResponseWriter, code int, data unsubscribePage) {
	h := w.Header()
	secure(h)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(code)
	_ = unsubscribeTemplate.Execute(w, data)
}

func secure(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
}

func plain(w http.ResponseWriter, code int, retryAfter bool) {
	secure(w.Header())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if retryAfter {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(code)
}
