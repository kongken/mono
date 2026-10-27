package application_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/testutil"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/protobuf/proto"
)

func TestDiscoveryFieldsSurviveCatalogWritesReadsAndImport(t *testing.T) {
	db := testutil.DB(t)
	admin := application.NewAdminService(db)
	reader := application.NewCatalogService(db)
	ctx := adminCtx()
	c, err := admin.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海", Region: "east"}})
	if err != nil {
		t.Fatal(err)
	}
	in := goodLine()
	in.VehicleType, in.Stations = "a", 3
	in.StationNames = []string{"富锦路", "人民广场", "莘庄"}
	l, err := admin.CreateLine(ctx, &metrov1.CreateLineRequest{Line: in})
	if err != nil {
		t.Fatal(err)
	}
	city, err := reader.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if city.City.Region != "east" || city.Lines[0].VehicleType != "a" || strings.Join(city.Lines[0].StationNames, ",") != "富锦路,人民广场,莘庄" {
		t.Fatalf("public city lost discovery fields: %v", city)
	}
	all, err := reader.ListLines(context.Background(), &metrov1.ListLinesRequest{})
	if err != nil || !proto.Equal(all.Lines[0], city.Lines[0]) {
		t.Fatalf("list lines: %v %v", all, err)
	}
	exported, err := admin.ExportCatalog(ctx, &metrov1.ExportCatalogRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`"region": "east"`, `"vehicle_type": "a"`, `"人民广场"`} {
		if !strings.Contains(exported.CatalogJson, text) {
			t.Fatalf("export lost %s: %s", text, exported.CatalogJson)
		}
	}
	imported, err := admin.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: exported.CatalogJson})
	if err != nil || imported.GetCatalogVersion() != exported.CatalogVersion {
		t.Fatalf("unchanged import: %v %v", imported, err)
	}
	// Updating only a new field must participate in revision, version, audit and subsequent exports.
	updated := proto.Clone(c.City.City).(*metrov1.CityInput)
	updated.Region = "north"
	changed, err := admin.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: c.City.Id, ExpectedRevision: 1, City: updated})
	if err != nil || changed.GetCity().GetRevision() != 2 {
		t.Fatalf("region update: %v %v", changed, err)
	}
	in.VehicleType, in.StationNames[1] = "b", "徐家汇"
	changedLine, err := admin.UpdateLine(ctx, &metrov1.UpdateLineRequest{Id: l.Line.Id, ExpectedRevision: 1, Line: in})
	if err != nil || changedLine.GetLine().GetRevision() != 2 {
		t.Fatalf("line update: %v %v", changedLine, err)
	}
	// Importing the previous document restores all the new fields, rather than silently discarding them.
	if _, err = admin.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: exported.CatalogJson}); err != nil {
		t.Fatal(err)
	}
	again, err := reader.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "shanghai"})
	if err != nil || !proto.Equal(city.City, again.GetCity()) || !proto.Equal(city.Lines[0], again.Lines[0]) {
		t.Fatalf("import did not restore fields: %v %v", again, err)
	}
}

func TestDiscoveryFieldsAreValidatedBeforeWrites(t *testing.T) {
	db := testutil.DB(t)
	admin := application.NewAdminService(db)
	ctx := adminCtx()
	if _, err := admin.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海", Region: "anywhere"}}); fieldErrors(t, err)["region"] == "" {
		t.Fatal("invalid region accepted")
	}
	if _, err := admin.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*metrov1.LineInput)
	}{
		{"vehicle", "vehicle_type", func(l *metrov1.LineInput) { l.VehicleType = "model-code" }},
		{"station count", "station_names", func(l *metrov1.LineInput) { l.StationNames = []string{"起点"} }},
		{"station text", "station_names[1]", func(l *metrov1.LineInput) { l.Stations = 2; l.StationNames = []string{"起点", "\n"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := goodLine()
			tc.mutate(in)
			before := version(t, db)
			_, err := admin.CreateLine(ctx, &metrov1.CreateLineRequest{Line: in})
			if fieldErrors(t, err)[tc.field] == "" {
				t.Fatalf("missing %s: %v", tc.field, err)
			}
			if version(t, db) != before {
				t.Fatal("validation failure changed catalog")
			}
		})
	}
}
