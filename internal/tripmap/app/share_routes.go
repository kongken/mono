package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.orx.me/apps/trip-map/internal/tripmap/application"
)

// shareMethods are the verbs the public share routes answer. HEAD lets link
// checkers and some crawlers probe without a body.
var shareMethods = []string{http.MethodGet, http.MethodHead}

// redactShareTrace keeps the token out of tracing: the otel Gin middleware records
// the raw request path as a span attribute before any handler runs, so the share
// routes replace it with the route pattern. Access logs are handled elsewhere:
// Butterfly discards Gin's request log, and nginx switches access_log off for
// /s/ and /api/share/.
func redactShareTrace(pattern string) gin.HandlerFunc {
	return func(c *gin.Context) {
		span := trace.SpanFromContext(c.Request.Context())
		span.SetAttributes(
			attribute.String("url.path", pattern), attribute.String("http.target", pattern),
			attribute.String("url.full", pattern), attribute.String("http.url", pattern),
			attribute.String("url.query", ""), attribute.String("http.route", pattern),
		)
		c.Next()
	}
}

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
	r.Match(shareMethods, "/s/*rest", redactShareTrace("/s/{token}"), func(c *gin.Context) {
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
	api.Match(shareMethods, "/share/*rest", redactShareTrace("/api/share/{token}"), func(c *gin.Context) {
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
