package app

import (
	"strings"

	"github.com/kongken/mono/pkg/proto/bff/metro/v1/bffmetrov1connect"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

// adminServices are the browser services whose every procedure is a management procedure of the
// unified console: user management, the metro catalogue administration and the management tokens.
// The admin perimeter (source allowlist and the console's exact Origin) applies to each of their
// procedures and public paths, whatever the method, so a procedure added to one of them later is
// covered without being listed anywhere, and a service is added here by name (a test fails when a
// service whose methods declare required_role is missing). The MCP endpoint is not a browser
// service: it has its own machine allowlist.
var adminServices = []string{
	bffv1connect.UserAdminServiceName,
	bffmetrov1connect.AdminServiceName,
	bffv1connect.AccessTokenServiceName,
}

// isAdminProcedure reports whether procedure ("/bff.v1.UserAdminService/ListUsers") belongs to an
// admin service.
func isAdminProcedure(procedure string) bool {
	for _, name := range adminServices {
		if strings.HasPrefix(procedure, "/"+name+"/") {
			return true
		}
	}
	return false
}

// isAdminPath reports whether the public HTTP path of a request ("/api/bff.v1.UserAdminService/...")
// is under an admin service.
func isAdminPath(path string) bool {
	for _, name := range adminServices {
		if strings.HasPrefix(path, "/api/"+name+"/") {
			return true
		}
	}
	return false
}
