package e2e_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func adminWrite[T any](t *testing.T, b *Browser, msg *T) *connect.Request[T] {
	t.Helper()
	req := connect.NewRequest(msg)
	b.Write(context.Background(), req)
	return req
}

// metro-admin reaches the admin API through the real BFF and the real metro
// service; a signed-in user without the role and an anonymous browser do not.
func TestChainMetroAdminRequiresTheRole(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()

	anon := h.FreshBrowser()
	if _, err := anon.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}

	user := h.FreshBrowser()
	if err := user.LoginWithRoles(ctx, "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := user.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("signed in without the role: %v", err)
	}
	other := h.FreshBrowser()
	if err := other.LoginWithRoles(ctx, "other-admin", "someone-else"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("another role: %v", err)
	}

	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "root", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.MetroAdmin.ListAdminCities(ctx, connect.NewRequest(&bffmetrov1.ListAdminCitiesRequest{})); err != nil {
		t.Fatalf("admin: %v", err)
	}
	// A write without CSRF is refused before it reaches metro.
	if _, err := admin.MetroAdmin.CreateCity(ctx, connect.NewRequest(&bffmetrov1.CreateCityRequest{City: &bffmetrov1.CityInput{Slug: "x", Name: "x"}})); err == nil {
		t.Fatal("a write without a CSRF token was accepted")
	}
}

func TestChainMetroAdminEditsAreVisibleToAnonymousReaders(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "root", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	me, err := admin.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reader := h.FreshBrowser()

	before, err := reader.Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	city, err := admin.MetroAdmin.CreateCity(ctx, adminWrite(t, admin, &bffmetrov1.CreateCityRequest{City: &bffmetrov1.CityInput{Slug: "shanghai", Name: "上海", Region: "east"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.MetroAdmin.CreateLine(ctx, adminWrite(t, admin, &bffmetrov1.CreateLineRequest{Line: &bffmetrov1.LineInput{
		Code: "sh-1", CitySlug: "shanghai", Name: "1号线", Number: 1, Color: "#E4002B", LengthKm: 36.9, Cars: 6,
		VehicleType: "a", Stations: 3, StationNames: []string{"富锦路", "人民广场", "莘庄"},
	}})); err != nil {
		t.Fatal(err)
	}
	// The anonymous reader's cached version is now stale.
	stale, err := reader.Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{KnownVersion: before.Msg.Version}))
	if err != nil || stale.Msg.NotModified || len(stale.Msg.Cities) != 1 || stale.Msg.Cities[0].LineCount != 1 {
		t.Fatalf("reader after the edit: %+v %v", stale, err)
	}
	public, err := reader.Catalog.GetCity(ctx, connect.NewRequest(&bffmetrov1.GetCityRequest{Slug: "shanghai"}))
	if err != nil || public.Msg.City.Region != "east" || public.Msg.Lines[0].VehicleType != "a" || public.Msg.Lines[0].StationNames[1] != "人民广场" {
		t.Fatalf("discovery fields did not cross the BFF and database: %v %v", public, err)
	}

	// Validation failures come back with field paths a form can show.
	_, err = admin.MetroAdmin.CreateLine(ctx, adminWrite(t, admin, &bffmetrov1.CreateLineRequest{Line: &bffmetrov1.LineInput{
		Code: "sh-2", CitySlug: "shanghai", Name: "2号线", Color: "red",
	}}))
	var ce *connect.Error
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !errorAs(err, &ce) {
		t.Fatalf("invalid line: %v", err)
	}
	fields := map[string]bool{}
	for _, d := range ce.Details() {
		v, derr := d.Value()
		if derr != nil {
			t.Fatal(derr)
		}
		if detail, ok := v.(*bffmetrov1.ValidationDetail); ok {
			for _, fe := range detail.Errors {
				fields[fe.Field] = true
			}
		}
	}
	if !fields["color"] {
		t.Fatalf("validation detail did not reach the browser: %v (%v)", fields, err)
	}

	// A stale revision reports the current one.
	_, err = admin.MetroAdmin.UpdateCity(ctx, adminWrite(t, admin, &bffmetrov1.UpdateCityRequest{
		Id: city.Msg.City.Id, ExpectedRevision: 7, City: &bffmetrov1.CityInput{Slug: "shanghai", Name: "改"},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errorAs(err, &ce) || len(ce.Details()) != 1 {
		t.Fatalf("stale revision: %v", err)
	}
	if v, _ := ce.Details()[0].Value(); v.(*bffmetrov1.RevisionConflictDetail).CurrentRevision != 1 {
		t.Fatalf("conflict detail: %v", v)
	}

	// The city is in use.
	_, err = admin.MetroAdmin.DeleteCity(ctx, adminWrite(t, admin, &bffmetrov1.DeleteCityRequest{Id: city.Msg.City.Id, ExpectedRevision: 1}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "1 line") {
		t.Fatalf("delete city in use: %v", err)
	}

	// The audit log names the signed-in administrator and the web channel.
	logs, err := admin.MetroAdmin.ListAuditLogs(ctx, connect.NewRequest(&bffmetrov1.ListAuditLogsRequest{}))
	if err != nil || len(logs.Msg.Logs) != 2 {
		t.Fatalf("audit: %+v %v", logs, err)
	}
	for _, l := range logs.Msg.Logs {
		if l.ActorUserId != me.GetId() || l.Channel != "web" || l.RequestId == "" {
			t.Fatalf("audit row: %+v (admin is %s)", l, me.GetId())
		}
	}
}

func TestChainMetroImportThroughTheBFF(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	ctx := context.Background()
	admin := h.FreshBrowser()
	if err := admin.LoginWithRoles(ctx, "root", "metro-admin"); err != nil {
		t.Fatal(err)
	}
	doc := `{"format":"metro-catalog/1","cities":[{"slug":"suzhou","name":"苏州"}],
	"lines":[{"code":"sz-1","city":"suzhou","name":"1号线","number":1,"cars":6,"stations":24,"length_km":26.6,"color":"#7AB72B"}]}`

	dry, err := admin.MetroAdmin.ImportCatalog(ctx, adminWrite(t, admin, &bffmetrov1.ImportCatalogRequest{CatalogJson: doc, DryRun: true}))
	if err != nil || !dry.Msg.DryRun || dry.Msg.Lines.Created != 1 || dry.Msg.Cities.Created != 1 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if list, _ := h.FreshBrowser().Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{})); len(list.Msg.Cities) != 0 {
		t.Fatal("a dry run wrote data")
	}
	real, err := admin.MetroAdmin.ImportCatalog(ctx, adminWrite(t, admin, &bffmetrov1.ImportCatalogRequest{CatalogJson: doc}))
	if err != nil || real.Msg.Lines.Created != 1 || real.Msg.CatalogVersion < 2 {
		t.Fatalf("import: %+v %v", real, err)
	}
	reader := h.FreshBrowser()
	got, err := reader.Catalog.GetCity(ctx, connect.NewRequest(&bffmetrov1.GetCityRequest{Slug: "suzhou"}))
	if err != nil || got.Msg.TotalLines != 1 {
		t.Fatalf("catalog after import: %+v %v", got, err)
	}
	exported, err := admin.MetroAdmin.ExportCatalog(ctx, connect.NewRequest(&bffmetrov1.ExportCatalogRequest{}))
	if err != nil || !strings.Contains(exported.Msg.CatalogJson, `"sz-1"`) {
		t.Fatalf("export: %v %v", exported, err)
	}
}

func errorAs(err error, target **connect.Error) bool { return errors.As(err, target) }

// metro enforces required_role itself: a credential that names a user but carries
// no metro-admin role is refused even when the BFF is bypassed.
func TestChainMetroRefusesAdminCallsWithoutTheRoleInTheCredential(t *testing.T) {
	h := adminHarness(t, loopback, ChainMetro{})
	client := metrov1.NewAdminServiceClient(h.DirectMetroGRPC())
	call := func(roles ...string) error {
		ctx := rpcauth.WithCall(context.Background(), rpcauth.Call{UserID: "mallory", Roles: roles, RequestID: "direct"})
		_, err := client.ListAdminCities(ctx, &metrov1.ListAdminCitiesRequest{})
		return err
	}
	for name, roles := range map[string][]string{"no roles": nil, "other role": {"metro-viewer"}} {
		if err := call(roles...); status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := call("metro-admin"); err != nil {
		t.Fatalf("with the role: %v", err)
	}
	// An anonymous credential never reaches the admin API.
	_, err := client.ListAdminCities(context.Background(), &metrov1.ListAdminCitiesRequest{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
}
