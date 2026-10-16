package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.orx.me/apps/trip-map/internal/platform/httpx"
	"go.orx.me/apps/trip-map/internal/tripmap/application"
)

// shareMethods are the verbs the public share routes answer. HEAD lets link
// checkers and some crawlers probe without a body.
var shareMethods = []string{http.MethodGet, http.MethodHead}

// The share tokens stay out of tracing (httpx.RedactTrace) and out of access logs:
// Butterfly discards Gin's request log, and nginx switches access_log off for
// /s/ and /api/share/.

// mountShare registers the public, unauthenticated share routes:
//
//	GET /s/<token>                         HTML with Open Graph tags
//	GET /s/<token>/og.png                  preview image
//	GET /api/share/<token>/data            public JSON
//	GET /api/share/<token>/photos/<handle> a chosen photo
//
// Every other path below those prefixes is the same uniform 404, so nothing under
// /s/ can fall through to a personal map.
func mountShare(r *gin.Engine, api *gin.RouterGroup, public *application.SharePublic) {
	r.Match(shareMethods, "/s", func(c *gin.Context) { public.NotFound(c.Writer) })
	r.Match(shareMethods, "/s/*rest", httpx.RedactTrace("/s/{token}"), func(c *gin.Context) {
		token, tail := application.SplitSharePath(c.Param("rest"))
		switch {
		case token == "":
			public.NotFound(c.Writer)
		case len(tail) == 0:
			public.Page(c.Writer, c.Request, token)
		case len(tail) == 1 && tail[0] == "og.png":
			public.Image(c.Writer, c.Request, token)
		default:
			public.NotFound(c.Writer)
		}
	})
	api.Match(shareMethods, "/share/*rest", httpx.RedactTrace("/api/share/{token}"), func(c *gin.Context) {
		token, tail := application.SplitSharePath(c.Param("rest"))
		switch {
		case token != "" && len(tail) == 1 && tail[0] == "data":
			public.Data(c.Writer, c.Request, token)
		case token != "" && len(tail) == 2 && tail[0] == "photos":
			public.Photo(c.Writer, c.Request, token, tail[1])
		default:
			public.NotFound(c.Writer)
		}
	})
}
