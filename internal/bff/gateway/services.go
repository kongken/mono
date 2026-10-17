package gateway

// Service is one business service behind the BFF. Adding a business means a new
// entry here and its address under upstreams in the BFF configuration.
type Service struct {
	Name string
	// GRPC marks a service the BFF calls over internal gRPC; it needs a
	// grpc.services entry in the configuration. Its upstreams entry stays for the
	// health probe and for the routes not migrated yet.
	GRPC bool
	// Packages are the proto packages it serves as Connect procedures at
	// /api/<package>.<Service>/<Method>.
	Packages []string
	// Routes are its plain HTTP paths outside the Connect procedures.
	Routes []Route
	// Headers are the extra request headers its browser client sends; CORS
	// allows them.
	Headers []string
}

// Route is a plain HTTP path owned by a service.
type Route struct {
	// Path matches exactly, or as a prefix when it ends in "/".
	Path string
	// Trace replaces the path and query in tracing spans when they carry a
	// credential. Empty records the request as it is.
	Trace string
}

// Services is the route table. Business RPCs are served as bff.v1 Connect on the
// BFF; upstream entries remain for health probes and internal gRPC only.
var Services = []Service{{
	Name: "tripmap",
	GRPC: true,
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
