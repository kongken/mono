package app

import (
	"strings"
	"testing"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Every browser procedure that declares a required role is a management procedure and must sit
// behind the admin perimeter; and every procedure of an admin service declares one. A service
// added with a required role and forgotten in adminServices fails here, not in production.
func TestEveryRoleGatedBrowserServiceIsInsideTheAdminPerimeter(t *testing.T) {
	inPerimeter := map[string]bool{}
	for _, s := range adminServices {
		inPerimeter[s] = true
	}
	seen := map[string]int{}
	protoregistry.GlobalFiles.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		services := f.Services()
		for i := 0; i < services.Len(); i++ {
			svc := services.Get(i)
			name := string(svc.FullName())
			if !strings.HasPrefix(name, "bff.") {
				continue
			}
			methods := svc.Methods()
			for j := 0; j < methods.Len(); j++ {
				procedure := "/" + name + "/" + string(methods.Get(j).Name())
				gated := rpcauth.RequiredRole(procedure) != ""
				switch {
				case gated && !inPerimeter[name]:
					t.Errorf("%s declares required_role but %s is not an admin service", procedure, name)
				case inPerimeter[name] && !gated:
					t.Errorf("%s is in the admin perimeter but declares no required_role", procedure)
				}
				if inPerimeter[name] {
					seen[name]++
					if !isAdminProcedure(procedure) || !isAdminPath("/api"+procedure) {
						t.Errorf("%s is not matched by the perimeter", procedure)
					}
				}
			}
		}
		return true
	})
	for _, s := range adminServices {
		if seen[s] == 0 {
			t.Errorf("admin service %s has no procedures in the registry", s)
		}
	}
}

func TestAdminPerimeterMatchesOnlyAdminServices(t *testing.T) {
	for procedure, want := range map[string]bool{
		"/bff.v1.UserAdminService/ListUsers":              true,
		"/bff.metro.v1.AdminService/ListAdminCities":      true,
		"/bff.v1.AccessTokenService/CreateAccessToken":    true,
		"/bff.metro.v1.CatalogService/ListCities":         false,
		"/bff.metro.v1.RiderService/MarkLine":             false,
		"/bff.v1.AuthService/Me":                          false,
		"/bff.v1.UserAdminServiceX/ListUsers":             false,
		"/bff.metro.v1.AdminServiceExtra/ListAdminCities": false,
		"/tripmap.v1.AdminService/Anything":               false,
	} {
		if got := isAdminProcedure(procedure); got != want {
			t.Errorf("%s: %v", procedure, got)
		}
		if got := isAdminPath("/api" + procedure); got != want {
			t.Errorf("/api%s: %v", procedure, got)
		}
	}
}
