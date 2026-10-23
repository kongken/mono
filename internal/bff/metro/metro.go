// Package metro mounts the metro product's browser API on the BFF: the
// bff.metro.v1 Connect services. Accounts, sessions, CSRF and the ingress policy
// stay shared in the parent package tree; this package only adapts one product's
// contract to its internal gRPC service, and does not import any other product.
package metro

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ingress"
	"github.com/kongken/mono/internal/bff/metro/catalog"
	"github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc"
)

// Service is the upstream name in the gateway route table and BFF configuration.
const Service = "metro"

// HeavyProcedures are the metro procedures that get the long request deadline.
// None yet; catalog imports are added with the admin interface.
func HeavyProcedures() map[string]bool { return map[string]bool{} }

// MountRPC registers the metro Connect services on mux. Callers mount it only
// when the session layer can authenticate the browser.
func MountRPC(mux *http.ServeMux, conn *grpc.ClientConn, opts []connect.HandlerOption) {
	mount := func(route string, handler http.Handler) { mux.Handle(route, ingress.ConnectOnly(handler)) }
	mount(bffmetrov1connect.NewCatalogServiceHandler(catalog.New(metrov1.NewCatalogServiceClient(conn)), opts...))
}
