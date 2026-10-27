package application_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/testutil"
)

func TestCatalogDiscoveryMigrationPreservesExistingRows(t *testing.T) {
	const directory = "../../../migrations/metro"
	dir, err := migrate.NewLocalDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Validate(dir); err != nil {
		t.Fatal(err)
	}
	db := testutil.Schema(t)
	ctx := context.Background()
	files, err := filepath.Glob(filepath.Join(directory, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		if strings.HasSuffix(file, "_catalog_discovery.sql") {
			// These rows predate all of the new fields, including a saved ride.
			_, err = db.ExecContext(ctx, `INSERT INTO cities (id, slug, name, header_images, created_at, updated_at) VALUES ('c1','shanghai','上海','[]',now(),now());
			INSERT INTO lines (id, city_id, code, name, color, stations, created_at, updated_at) VALUES ('l1','c1','sh-1','1号线','#E4002B',28,now(),now());
			INSERT INTO rider_lines (id, user_id, line_id, ridden_year, created_at, updated_at) VALUES ('r1','u1','l1',2019,now(),now())`)
			if err != nil {
				t.Fatal(err)
			}
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", file, err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	c, err := client.City.Get(ctx, "c1")
	if err != nil || c.Region != "" || c.Name != "上海" {
		t.Fatalf("city: %v %v", c, err)
	}
	l, err := client.Line.Get(ctx, "l1")
	if err != nil || l.VehicleType != "" || len(l.StationNames) != 0 || l.Stations != 28 {
		t.Fatalf("line: %v %v", l, err)
	}
	r, err := client.RiderLine.Get(ctx, "r1")
	if err != nil || r.RiddenYear == nil || *r.RiddenYear != 2019 {
		t.Fatalf("ride: %v %v", r, err)
	}
	var pending bytes.Buffer
	if err = client.Schema.WriteTo(ctx, &pending); err != nil {
		t.Fatal(err)
	}
	var reference bytes.Buffer
	if err = testutil.DB(t).Schema.WriteTo(ctx, &reference); err != nil {
		t.Fatal(err)
	}
	if pending.String() != reference.String() {
		t.Fatalf("migration differs from Ent:\n%s\nreference:\n%s", pending.String(), reference.String())
	}
}
