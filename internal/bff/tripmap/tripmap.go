// Package tripmap mounts the tripmap product's browser API on the BFF: the
// bff.tripmap.v1 Connect services and the public HTTP paths (share pages and
// email unsubscribe). Everything shared by products (accounts, sessions, CSRF,
// ingress policy) stays in the parent package tree; a new product gets a sibling
// package with the same shape.
package tripmap

import (
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/publichttp"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/tripmap/footprint"
	"github.com/kongken/mono/internal/bff/tripmap/friends"
	"github.com/kongken/mono/internal/bff/tripmap/itinerary"
	"github.com/kongken/mono/internal/bff/tripmap/media"
	"github.com/kongken/mono/internal/bff/tripmap/notification"
	"github.com/kongken/mono/internal/bff/tripmap/region"
	"github.com/kongken/mono/internal/bff/tripmap/report"
	"github.com/kongken/mono/internal/bff/tripmap/search"
	"github.com/kongken/mono/internal/bff/tripmap/share"
	"github.com/kongken/mono/internal/bff/tripmap/sharedtrip"
	"github.com/kongken/mono/internal/bff/tripmap/snapshot"
	"github.com/kongken/mono/internal/bff/tripmap/status"
	"github.com/kongken/mono/pkg/proto/bff/tripmap/v1/bfftripmapv1connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
)

// Service is the upstream name in the gateway route table and BFF configuration.
const Service = "tripmap"

// HeavyProcedures are the tripmap procedures that get the long request deadline.
func HeavyProcedures() map[string]bool {
	return map[string]bool{
		bfftripmapv1connect.FootprintServiceSyncChangesProcedure:          true,
		bfftripmapv1connect.SnapshotServiceRestoreSnapshotProcedure:       true,
		bfftripmapv1connect.MediaServicePrepareUploadProcedure:            true,
		bfftripmapv1connect.MediaServiceCompleteUploadProcedure:           true,
		bfftripmapv1connect.SharedTripServiceReadSharedTripMediaProcedure: true,
	}
}

// MountStatus registers the anonymous readiness report.
func MountStatus(mux *http.ServeMux, conn *grpc.ClientConn, opts []connect.HandlerOption) {
	route, handler := bfftripmapv1connect.NewStatusServiceHandler(status.New(tripmapv1.NewSystemServiceClient(conn)), opts...)
	mux.Handle(route, ingress.ConnectOnly(handler))
}

// MountRPC registers the signed-in services. Callers mount it only when the
// session layer can authenticate the browser.
func MountRPC(mux *http.ServeMux, conn *grpc.ClientConn, opts []connect.HandlerOption) {
	mount := func(route string, handler http.Handler) { mux.Handle(route, ingress.ConnectOnly(handler)) }
	collab := tripmapv1.NewCollaborationServiceClient(conn)
	mount(bfftripmapv1connect.NewFootprintServiceHandler(footprint.New(tripmapv1.NewFootprintServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewSnapshotServiceHandler(snapshot.New(tripmapv1.NewSnapshotServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewNotificationServiceHandler(notification.New(tripmapv1.NewNotificationServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewFriendServiceHandler(friends.New(collab), opts...))
	mount(bfftripmapv1connect.NewSharedTripServiceHandler(sharedtrip.New(collab), opts...))
	mount(bfftripmapv1connect.NewItineraryServiceHandler(itinerary.New(tripmapv1.NewItineraryServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewMediaServiceHandler(media.New(tripmapv1.NewMediaServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewShareServiceHandler(share.New(tripmapv1.NewShareServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewReportServiceHandler(report.New(tripmapv1.NewReportServiceClient(conn)), opts...))
	mount(bfftripmapv1connect.NewSearchServiceHandler(search.New(tripmapv1.NewSearchServiceClient(conn)), append(opts, connect.WithReadMaxBytes(4096))...))
	mount(bfftripmapv1connect.NewRegionServiceHandler(region.New(tripmapv1.NewRegionServiceClient(conn)), opts...))
}

// MountPublicHTTP registers the credential-free HTTP paths: email unsubscribe
// and the public share page, data and preview image.
func MountPublicHTTP(r *gin.Engine, conn *grpc.ClientConn, limiter *ratelimit.Limiter, clientIPHeader string) {
	reg := publichttp.New(limiter, clientIPHeader)
	unsub := notification.NewUnsubscribeHTTP(tripmapv1.NewNotificationPublicServiceClient(conn), clientIPHeader)
	reg.Mount(r, publichttp.Route{
		Methods:      []string{http.MethodGet, http.MethodHead, http.MethodPost},
		Path:         "/api/notifications/unsubscribe",
		TracePattern: "/api/notifications/unsubscribe",
		Limit:        &publichttp.Limit{KeyPrefix: "unsubscribe:", Max: 30, Window: time.Minute},
		Handler:      unsub,
	})
	sharePub := share.NewPublicHTTP(tripmapv1.NewSharePublicServiceClient(conn), clientIPHeader)
	shareLimit := func(prefix string, max int64) *publichttp.Limit {
		return &publichttp.Limit{KeyPrefix: prefix, Max: max, Window: time.Minute}
	}
	reg.Mount(r, publichttp.Route{
		Methods: sharePub.Methods(), Path: "/s", TracePattern: "/s", Limit: shareLimit("share-page:", 120),
		Handler: http.HandlerFunc(sharePub.ServeRoot),
	})
	reg.MountParam(r, publichttp.Route{
		Methods: sharePub.Methods(), TracePattern: "/s/{token}", Limit: shareLimit("share-page:", 120),
	}, "/s/*rest", "rest", sharePub.ServeSharePath)
	reg.MountParam(r, publichttp.Route{
		Methods: sharePub.Methods(), TracePattern: "/api/share/{token}", Limit: shareLimit("share-page:", 120),
	}, "/api/share/*rest", "rest", sharePub.ServeAPIPath)
}
