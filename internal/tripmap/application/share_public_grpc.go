package application

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/kongken/mono/internal/platform/rpcauth"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type sharePublicGRPC struct {
	tripmapv1.UnimplementedSharePublicServiceServer
	*SharePublic
}

// SharePublicGRPC serves token-gated public share responses over gRPC.
func SharePublicGRPC(p *SharePublic) tripmapv1.SharePublicServiceServer {
	return sharePublicGRPC{SharePublic: p}
}

func (p sharePublicGRPC) GetSharePage(ctx context.Context, req *tripmapv1.GetSharePageRequest) (*tripmapv1.GetSharePageResponse, error) {
	return &tripmapv1.GetSharePageResponse{Http: p.capture(ctx, func(w http.ResponseWriter, r *http.Request) {
		p.SharePublic.Page(w, r, req.GetToken())
	})}, nil
}

func (p sharePublicGRPC) GetShareData(ctx context.Context, req *tripmapv1.GetShareDataRequest) (*tripmapv1.GetShareDataResponse, error) {
	return &tripmapv1.GetShareDataResponse{Http: p.capture(ctx, func(w http.ResponseWriter, r *http.Request) {
		p.SharePublic.Data(w, r, req.GetToken())
	})}, nil
}

func (p sharePublicGRPC) GetSharePreviewImage(ctx context.Context, req *tripmapv1.GetSharePreviewImageRequest) (*tripmapv1.GetSharePreviewImageResponse, error) {
	return &tripmapv1.GetSharePreviewImageResponse{Http: p.capture(ctx, func(w http.ResponseWriter, r *http.Request) {
		p.SharePublic.Image(w, r, req.GetToken())
	})}, nil
}

func (p sharePublicGRPC) GetSharePhoto(ctx context.Context, req *tripmapv1.GetSharePhotoRequest) (*tripmapv1.GetSharePhotoResponse, error) {
	return &tripmapv1.GetSharePhotoResponse{Http: p.capture(ctx, func(w http.ResponseWriter, r *http.Request) {
		r.URL.RawQuery = "size=" + req.GetSize()
		p.SharePublic.Photo(w, r, req.GetToken(), req.GetHandle())
	})}, nil
}

func (p sharePublicGRPC) capture(ctx context.Context, fn func(http.ResponseWriter, *http.Request)) *tripmapv1.SharePublicHTTPBody {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://share.internal/", nil).WithContext(ctx)
	if ip := rpcauth.CallFrom(ctx).ClientIP; ip != "" {
		req.RemoteAddr = ip + ":0"
	}
	fn(rec, req)
	out := &tripmapv1.SharePublicHTTPBody{
		Status: int32(rec.Code),
		Body:   rec.Body.Bytes(),
	}
	if out.Status == 0 {
		out.Status = http.StatusOK
	}
	out.Headers = map[string]string{}
	for k, vals := range rec.Header() {
		if len(vals) > 0 {
			out.Headers[k] = vals[0]
		}
	}
	return out
}
