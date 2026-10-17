package gateway

// Service is one business service behind the BFF. Adding a business means a new
// entry here and its address under upstreams in the BFF configuration.
type Service struct {
	Name string
	// GRPC marks a service the BFF calls over internal gRPC; it needs a
	// grpc.services entry in the configuration. Its upstreams entry stays for the
	// health probe and for the routes not migrated yet.
	GRPC bool
	// Packages is a legacy field for tests that still model extra upstreams.
	Packages []string
	// Routes are plain HTTP paths forwarded to the service's upstreams address.
	Routes  []Route
	Headers []string
}

// Route is a plain HTTP path owned by a service.
type Route struct {
	// Path matches exactly, or as a prefix when it ends in "/".
	Path string
	// Trace replaces the path and query in tracing spans when they carry a
	// credential. Empty records the request as it is.
	Trace string
	// Anonymous marks a path that needs no browser credential: Cookie, Authorization
	// and every other client header except Accept and User-Agent are dropped before
	// the request is forwarded.
	Anonymous bool
}

// Services is the route table. Business RPCs are served as bff.* Connect on the
// BFF; upstream entries serve health probes, internal gRPC and the plain HTTP
// Routes listed here.
var Services = []Service{{
	Name: "tripmap",
	GRPC: true,
	Routes: []Route{
		// Mapbox basemap tiles: GET /api/tiles/{z}/{x}/{y}.png. tripmap holds the Mapbox
		// token and the limits; the BFF only forwards. The path carries coordinates, not
		// credentials, so it is traced as it is.
		{Path: "/api/tiles/", Anonymous: true},
	},
}}

// Names lists the services in the route table.
func Names(services []Service) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.Name
	}
	return names
}

// GRPCNames lists the services called over gRPC.
func GRPCNames(services []Service) []string {
	var names []string
	for _, s := range services {
		if s.GRPC {
			names = append(names, s.Name)
		}
	}
	return names
}
