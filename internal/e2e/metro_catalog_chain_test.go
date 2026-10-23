package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	metroapplication "github.com/kongken/mono/internal/metro/application"
	bffmetrov1 "github.com/kongken/mono/pkg/proto/bff/metro/v1"
)

// An anonymous browser reads the catalog through the real BFF and the real metro
// gRPC server: acceptance scenarios 1 and 8 of docs/metro/spec.md.
func TestChainMetroAnonymousCatalog(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	ctx := context.Background()
	city := h.MetroDB.City.Create().SetSlug("shanghai").SetName("上海").SaveX(ctx)
	for _, l := range []struct {
		code   string
		number int
		km     float64
	}{{"sh-pujiang", 0, 6.7}, {"sh-2", 2, 64.2}, {"sh-1", 1, 36.9}} {
		h.MetroDB.Line.Create().SetCode(l.code).SetCityID(city.ID).SetName(l.code).SetNumber(l.number).
			SetLengthKm(l.km).SetColor("#E4002B").SetCars(6).SaveX(ctx)
	}
	anon := h.FreshBrowser() // no session at all

	got, err := anon.Catalog.GetCity(ctx, connect.NewRequest(&bffmetrov1.GetCityRequest{Slug: "shanghai"}))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, l := range got.Msg.Lines {
		order = append(order, l.Code)
	}
	if len(order) != 3 || order[0] != "sh-1" || order[1] != "sh-2" || order[2] != "sh-pujiang" {
		t.Fatalf("unnumbered lines must come last: %v", order)
	}
	if got.Msg.TotalLines != 3 || got.Msg.TotalLengthKm != 107.8 || got.Msg.Lines[0].Status != bffmetrov1.LineStatus_LINE_STATUS_OPERATING {
		t.Fatalf("totals: %+v", got.Msg)
	}
	if _, err = anon.Catalog.GetCity(ctx, connect.NewRequest(&bffmetrov1.GetCityRequest{Slug: "nowhere"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown city: %v", err)
	}
	if _, err = anon.Catalog.ListLines(ctx, connect.NewRequest(&bffmetrov1.ListLinesRequest{Statuses: []bffmetrov1.LineStatus{99}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown status: %v", err)
	}

	first, err := anon.Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{}))
	if err != nil || first.Msg.NotModified || len(first.Msg.Cities) != 1 || first.Msg.Cities[0].LineCount != 3 {
		t.Fatalf("list cities: %+v %v", first, err)
	}
	same, err := anon.Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{KnownVersion: first.Msg.Version}))
	if err != nil || !same.Msg.NotModified || len(same.Msg.Cities) != 0 {
		t.Fatalf("known version: %+v %v", same, err)
	}

	// A catalog write (as the admin API will do it) moves the version.
	tx, err := h.MetroDB.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx.City.Create().SetSlug("beijing").SetName("北京").SaveX(ctx)
	if _, err = metroapplication.BumpVersion(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fresh, err := anon.Catalog.ListCities(ctx, connect.NewRequest(&bffmetrov1.ListCitiesRequest{KnownVersion: first.Msg.Version}))
	if err != nil || fresh.Msg.NotModified || len(fresh.Msg.Cities) != 2 || fresh.Msg.Version != first.Msg.Version+1 {
		t.Fatalf("after write: %+v %v", fresh, err)
	}
}

// metro joins the BFF readiness report as metro_grpc and as the HTTP probe.
func TestChainMetroReadiness(t *testing.T) {
	h := NewChainHarness(t, ChainMetro{})
	res, err := http.Get(h.BFFURL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Dependencies []struct {
			Name  string
			Ready bool
		}
	}
	if err = json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	ready := map[string]bool{}
	for _, d := range body.Dependencies {
		ready[d.Name] = d.Ready
	}
	for _, name := range []string{"metro", "metro_grpc", "tripmap", "tripmap_grpc"} {
		if !ready[name] {
			t.Errorf("%s not reported ready: %v", name, ready)
		}
	}
}

// Without upstreams.metro the product is simply not part of the BFF.
func TestChainWithoutMetroHasNoMetroRoutes(t *testing.T) {
	h := NewChainHarness(t)
	res, err := http.Post(h.BFFURL+"/api/bff.metro.v1.CatalogService/ListCities", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.StatusCode)
	}
}
