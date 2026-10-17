package share

import (
	"context"
	"net/http"
	"strings"

	"github.com/kongken/mono/internal/platform/httpx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var shareMethods = []string{http.MethodGet, http.MethodHead}

// PublicHTTP terminates public share routes at the BFF and calls tripmap gRPC.
type PublicHTTP struct {
	client         tripmapv1.SharePublicServiceClient
	clientIPHeader string
}

func NewPublicHTTP(client tripmapv1.SharePublicServiceClient, clientIPHeader string) *PublicHTTP {
	return &PublicHTTP{client: client, clientIPHeader: clientIPHeader}
}

func (p *PublicHTTP) Methods() []string { return shareMethods }

func (p *PublicHTTP) ServeRoot(w http.ResponseWriter, r *http.Request) {
	p.servePage(w, r, "")
}

func splitSharePath(rest string) (token string, tail []string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", nil
	}
	return parts[0], parts[1:]
}

func (p *PublicHTTP) ServeSharePath(w http.ResponseWriter, r *http.Request, rest string) {
	token, tail := splitSharePath(rest)
	switch {
	case token == "":
		p.servePage(w, r, "")
	case len(tail) == 0:
		p.servePage(w, r, token)
	case len(tail) == 1 && tail[0] == "og.png":
		p.servePreview(w, r, token)
	default:
		p.servePage(w, r, "")
	}
}

func (p *PublicHTTP) ServeAPIPath(w http.ResponseWriter, r *http.Request, rest string) {
	token, tail := splitSharePath(rest)
	switch {
	case token != "" && len(tail) == 1 && tail[0] == "data":
		p.serveData(w, r, token)
	case token != "" && len(tail) == 2 && tail[0] == "photos":
		p.servePhoto(w, r, token, tail[1], r.URL.Query().Get("size"))
	default:
		p.servePage(w, r, "")
	}
}

func (p *PublicHTTP) servePage(w http.ResponseWriter, r *http.Request, token string) {
	p.invoke(w, r, func(ctx context.Context) (*tripmapv1.SharePublicHTTPBody, error) {
		out, err := p.client.GetSharePage(ctx, &tripmapv1.GetSharePageRequest{Token: token})
		if err != nil {
			return nil, err
		}
		return out.GetHttp(), nil
	})
}

func (p *PublicHTTP) serveData(w http.ResponseWriter, r *http.Request, token string) {
	p.invoke(w, r, func(ctx context.Context) (*tripmapv1.SharePublicHTTPBody, error) {
		out, err := p.client.GetShareData(ctx, &tripmapv1.GetShareDataRequest{Token: token})
		if err != nil {
			return nil, err
		}
		return out.GetHttp(), nil
	})
}

func (p *PublicHTTP) servePreview(w http.ResponseWriter, r *http.Request, token string) {
	p.invoke(w, r, func(ctx context.Context) (*tripmapv1.SharePublicHTTPBody, error) {
		out, err := p.client.GetSharePreviewImage(ctx, &tripmapv1.GetSharePreviewImageRequest{Token: token})
		if err != nil {
			return nil, err
		}
		return out.GetHttp(), nil
	})
}

func (p *PublicHTTP) servePhoto(w http.ResponseWriter, r *http.Request, token, handle, size string) {
	p.invoke(w, r, func(ctx context.Context) (*tripmapv1.SharePublicHTTPBody, error) {
		out, err := p.client.GetSharePhoto(ctx, &tripmapv1.GetSharePhotoRequest{Token: token, Handle: handle, Size: size})
		if err != nil {
			return nil, err
		}
		return out.GetHttp(), nil
	})
}

type grpcCall func(context.Context) (*tripmapv1.SharePublicHTTPBody, error)

func (p *PublicHTTP) invoke(w http.ResponseWriter, r *http.Request, call grpcCall) {
	if !contains(shareMethods, r.Method) {
		w.Header().Set("Allow", strings.Join(shareMethods, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	ip := httpx.ClientAddr(p.clientIPHeader, r.Header, r.RemoteAddr)
	ctx := rpcauth.WithCall(r.Context(), rpcauth.Call{ClientIP: ip})
	out, err := call(ctx)
	if err != nil {
		switch status.Code(err) {
		case codes.ResourceExhausted:
			plain(w, http.StatusTooManyRequests, true)
		default:
			plain(w, http.StatusServiceUnavailable, false)
		}
		return
	}
	writePublicHTTP(w, r, out)
}

func writePublicHTTP(w http.ResponseWriter, r *http.Request, out *tripmapv1.SharePublicHTTPBody) {
	for k, v := range out.GetHeaders() {
		w.Header().Set(k, v)
	}
	code := int(out.GetStatus())
	if code == 0 {
		code = http.StatusOK
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(code)
		return
	}
	w.WriteHeader(code)
	if body := out.GetBody(); len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func plain(w http.ResponseWriter, code int, retryAfter bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if retryAfter {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(code)
	_, _ = w.Write([]byte(http.StatusText(code) + "\n"))
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
