package gateway

// Service is one business service behind the BFF. Adding a business means a new
// entry here and its address under upstreams in the BFF configuration.
type Service struct {
	Name string
	// GRPC marks a service the BFF calls over internal gRPC; it needs a
	// grpc.services entry in the configuration. Its upstreams entry stays for the
	// health probe and for the routes not migrated yet.
	GRPC bool
	// Optional marks a product that not every deployment runs. It takes part in the
	// BFF only when upstreams.<name> is configured; without it the service is left
	// out of readiness, routing and account cleanup, so adding a product to the
	// route table does not force every environment to deploy it.
	Optional bool
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
	// Method is the HTTP method a BFF-owned route answers; empty means GET (the
	// browser-redirect routes of the login flow).
	Method string
	// Anonymous marks a path that needs no browser credential: Cookie, Authorization
	// and every other client header except Accept and User-Agent are dropped before
	// the request is forwarded.
	Anonymous bool
}

// Services is the route table. Business RPCs are served as bff.* Connect on the
// BFF; upstream entries serve health probes, internal gRPC and the plain HTTP
// Routes listed here.
var Services = []Service{{
	// usersrv is the account service (email accounts, passwords, roles, account mail).
	// It has no browser routes and no HTTP routes: the BFF reaches it over internal gRPC
	// only, for every sign-in and for the re-check before each protected request. It is
	// required: without it nobody can sign in, so the BFF refuses to start without it.
	Name: "usersrv",
	GRPC: true,
}, {
	Name: "tripmap",
	GRPC: true,
	Routes: []Route{
		// Mapbox basemap tiles: GET /api/tiles/{z}/{x}/{y}.png. tripmap holds the Mapbox
		// token and the limits; the BFF only forwards. The path carries coordinates, not
		// credentials, so it is traced as it is.
		{Path: "/api/tiles/", Anonymous: true},
	},
}, {
	// The metro product. Its browser API is bff.metro.v1 over Connect; its MCP
	// endpoint is a BFF-owned route (BFFOwnedRoutes).
	Name:     "metro",
	GRPC:     true,
	Optional: true,
}}

// Active returns the services of the route table that take part in this
// deployment: every required service, and the optional ones that have an
// upstream address configured.
func Active(services []Service, upstreams map[string]string) []Service {
	var out []Service
	for _, s := range services {
		if s.Optional && upstreams[s.Name] == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

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

// BFFOwnedRoutes inventories credential-bearing HTTP routes handled by the BFF
// itself. They are mounted locally, never forwarded to a business upstream. Account
// flows are Connect RPCs (bff.v1.AuthService), not HTTP routes: there is no browser
// login redirect and no OAuth callback.
var BFFOwnedRoutes = []Route{
	// The metro MCP endpoint (MCP Streamable HTTP, stateless). It takes a Bearer
	// management token only and ignores Cookies; the handler is internal/bff/metro/mcp.
	{Path: "/mcp/metro", Method: "POST"},
}

// BrowserHeaders supplements the shared Connect headers for BFF clients.
func BrowserHeaders() []string {
	return []string{"X-CSRF-Token", "X-Kongken-User", "X-Tripmap-Schema", "X-Tripmap-Regions"}
}
