package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/testutil"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type seed struct {
	db *ent.Client
}

func (s seed) city(t *testing.T, slug, name string, order int) *ent.City {
	t.Helper()
	c, err := s.db.City.Create().SetSlug(slug).SetName(name).SetSortOrder(order).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (s seed) stock(t *testing.T, code string) *ent.RollingStock {
	t.Helper()
	r, err := s.db.RollingStock.Create().SetCode(code).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type lineOpts struct {
	number, cars, carsAddition int
	km                         float64
	status                     string
	stock                      *ent.RollingStock
	commenced                  *time.Time
}

func (s seed) line(t *testing.T, c *ent.City, code string, o lineOpts) *ent.Line {
	t.Helper()
	if o.status == "" {
		o.status = "operating"
	}
	create := s.db.Line.Create().SetCode(code).SetCityID(c.ID).SetName(code).SetNumber(o.number).
		SetStatus(statusOf(o.status)).SetCars(o.cars).SetCarsAddition(o.carsAddition).SetLengthKm(o.km).SetColor("#E4002B")
	if o.stock != nil {
		create.SetRollingStockID(o.stock.ID)
	}
	if o.commenced != nil {
		create.SetCommencedOn(*o.commenced)
	}
	l, err := create.Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestGetCityOrdersLinesAndSumsLength(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	sh := s.city(t, "shanghai", "上海", 0)
	other := s.city(t, "beijing", "北京", 1)
	stock := s.stock(t, "02A02")
	day := time.Date(1993, 5, 28, 0, 0, 0, 0, time.UTC)
	s.line(t, sh, "sh-pujiang", lineOpts{number: 0, km: 6.7, cars: 4})
	s.line(t, sh, "sh-2", lineOpts{number: 2, km: 64.2, cars: 8})
	s.line(t, sh, "sh-1", lineOpts{number: 1, km: 36.9, cars: 6, stock: stock, commenced: &day})
	s.line(t, sh, "sh-18", lineOpts{number: 18, km: 0.1, cars: 6, status: "under_construction"})
	s.line(t, other, "bj-1", lineOpts{number: 1, km: 31})

	svc := application.NewCatalogService(db)
	res, err := svc.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	var lineCodes []string
	for _, l := range res.Lines {
		lineCodes = append(lineCodes, l.Code)
	}
	if want := []string{"sh-1", "sh-2", "sh-pujiang"}; !equal(lineCodes, want) {
		t.Fatalf("line order %v, want %v (numbered first, unnumbered last, operating only)", lineCodes, want)
	}
	if res.TotalLines != 3 || res.TotalLengthKm != 107.8 || res.City.LineCount != 3 || res.City.TotalLengthKm != 107.8 {
		t.Fatalf("totals: lines=%d km=%v city=%+v", res.TotalLines, res.TotalLengthKm, res.City)
	}
	first := res.Lines[0]
	if first.RollingStockCode != "02A02" || first.CommencedOn != "1993-05-28" || first.ExtendedOn != "" ||
		first.Status != metrov1.LineStatus_LINE_STATUS_OPERATING || first.CityName != "上海" || first.Color != "#E4002B" {
		t.Fatalf("line message: %+v", first)
	}

	// Asking for another status shows those lines instead of the default.
	res, err = svc.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "shanghai", Statuses: []metrov1.LineStatus{metrov1.LineStatus_LINE_STATUS_UNDER_CONSTRUCTION}})
	if err != nil || len(res.Lines) != 1 || res.Lines[0].Code != "sh-18" {
		t.Fatalf("under construction: %v %v", res, err)
	}
	if _, err = svc.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "nowhere"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown city: %v", err)
	}
	if _, err = svc.GetCity(context.Background(), &metrov1.GetCityRequest{Slug: "shanghai", Statuses: []metrov1.LineStatus{99}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown status: %v", err)
	}
}

func equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListCitiesKnownVersion(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	s := seed{db}
	s.city(t, "shanghai", "上海", 2)
	s.city(t, "beijing", "北京", 1)
	c := s.city(t, "chengdu", "成都", 1)
	s.line(t, c, "cd-1", lineOpts{number: 1, km: 10})
	s.line(t, c, "cd-2", lineOpts{number: 2, km: 5.5, status: "planned"})
	svc := application.NewCatalogService(db)

	res, err := svc.ListCities(ctx, &metrov1.ListCitiesRequest{})
	if err != nil || res.NotModified || res.Version != application.InitialVersion || len(res.Cities) != 3 {
		t.Fatalf("first read: %+v %v", res, err)
	}
	// sort_order, then slug: beijing and chengdu share 1.
	if got := []string{res.Cities[0].Slug, res.Cities[1].Slug, res.Cities[2].Slug}; !equal(got, []string{"beijing", "chengdu", "shanghai"}) {
		t.Fatalf("city order %v", got)
	}
	if cd := res.Cities[1]; cd.LineCount != 1 || cd.TotalLengthKm != 10 {
		t.Fatalf("chengdu counts only operating lines: %+v", cd)
	}

	// Same version: nothing to send.
	cached, err := svc.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: res.Version})
	if err != nil || !cached.NotModified || len(cached.Cities) != 0 || cached.Version != res.Version {
		t.Fatalf("known version: %+v %v", cached, err)
	}

	// A catalog write bumps the version in its own transaction; the old version now gets the data again.
	tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, err := application.BumpVersion(ctx, tx)
	if err != nil || next != res.Version+1 {
		t.Fatalf("bump: %d %v", next, err)
	}
	// Uncommitted: readers still see the old version.
	if mid, _ := svc.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: res.Version}); !mid.NotModified {
		t.Fatal("an uncommitted write was visible")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: res.Version})
	if err != nil || fresh.NotModified || fresh.Version != next || len(fresh.Cities) != 3 {
		t.Fatalf("after write: %+v %v", fresh, err)
	}
	if again, _ := svc.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: next}); !again.NotModified {
		t.Fatal("new version not accepted as known")
	}
	// An unknown (larger) version from a client is just a mismatch.
	if odd, _ := svc.ListCities(ctx, &metrov1.ListCitiesRequest{KnownVersion: next + 10}); odd.NotModified {
		t.Fatal("a version the server never issued was accepted")
	}
}

func TestEnsureCatalogStateIsIdempotentAndBumpCreatesMissingRow(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	for i := 0; i < 2; i++ {
		if err := application.EnsureCatalogState(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	if n := db.CatalogState.Query().CountX(ctx); n != 1 {
		t.Fatalf("rows: %d", n)
	}
	db.CatalogState.Delete().ExecX(ctx)
	tx, _ := db.Tx(ctx)
	v, err := application.BumpVersion(ctx, tx)
	if err != nil || v != application.InitialVersion+1 {
		t.Fatalf("bump on a missing row: %d %v", v, err)
	}
	_ = tx.Commit()
}

func TestListLinesFiltersByCityAndStatus(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	s := seed{db}
	sh, bj := s.city(t, "shanghai", "上海", 2), s.city(t, "beijing", "北京", 1)
	s.line(t, sh, "sh-1", lineOpts{number: 1})
	s.line(t, bj, "bj-2", lineOpts{number: 2})
	s.line(t, bj, "bj-1", lineOpts{number: 1})
	s.line(t, bj, "bj-old", lineOpts{number: 9, status: "retired"})
	svc := application.NewCatalogService(db)

	res, err := svc.ListLines(ctx, &metrov1.ListLinesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range res.Lines {
		got = append(got, l.Code)
	}
	if !equal(got, []string{"bj-1", "bj-2", "sh-1"}) {
		t.Fatalf("all cities, operating: %v", got)
	}
	res, err = svc.ListLines(ctx, &metrov1.ListLinesRequest{CitySlug: "beijing", Statuses: []metrov1.LineStatus{metrov1.LineStatus_LINE_STATUS_RETIRED, metrov1.LineStatus_LINE_STATUS_OPERATING}})
	if err != nil || len(res.Lines) != 3 {
		t.Fatalf("beijing with retired: %v %v", res, err)
	}
	if _, err = svc.ListLines(ctx, &metrov1.ListLinesRequest{CitySlug: "nowhere"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown city: %v", err)
	}
}

func TestRollingStockTopicSelectsOperatingLinesByCityGroup(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	s := seed{db}
	sh, bj := s.city(t, "shanghai", "上海", 2), s.city(t, "beijing", "北京", 1)
	a, b := s.stock(t, "A型"), s.stock(t, "B型")
	s.line(t, sh, "sh-1", lineOpts{number: 1, cars: 6, km: 36.9, stock: a})
	s.line(t, sh, "sh-2", lineOpts{number: 2, cars: 8, carsAddition: 6, km: 64.2, stock: b})
	s.line(t, sh, "sh-3", lineOpts{number: 3, cars: 6, km: 40, stock: b, status: "suspended"})
	s.line(t, bj, "bj-1", lineOpts{number: 1, cars: 6, km: 31, stock: a})
	s.line(t, bj, "bj-2", lineOpts{number: 2, cars: 4, km: 20, stock: a})
	db.RollingStockTopic.Create().SetSlug("six-cars").SetName("6 节编组").SetCars([]int{6}).SaveX(ctx)
	db.RollingStockTopic.Create().SetSlug("a-six").SetName("A 型 6 节").SetCars([]int{6}).SetRollingStockCodes([]string{"A型"}).SaveX(ctx)
	svc := application.NewCatalogService(db)

	list, err := svc.ListRollingStockTopics(ctx, &metrov1.ListRollingStockTopicsRequest{})
	if err != nil || len(list.Topics) != 2 || list.Topics[0].Slug != "a-six" {
		t.Fatalf("list: %v %v", list, err)
	}

	// Cars match on cars or cars_addition; suspended lines never count.
	res, err := svc.GetRollingStockTopic(ctx, &metrov1.GetRollingStockTopicRequest{Slug: "six-cars"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range res.Lines {
		got = append(got, l.Code)
	}
	if !equal(got, []string{"bj-1", "sh-1", "sh-2"}) || res.TotalLines != 3 || res.TotalLengthKm != 132.1 {
		t.Fatalf("six cars: lines %v total %d / %v", got, res.TotalLines, res.TotalLengthKm)
	}
	if len(res.Cities) != 2 || res.Cities[0].CitySlug != "beijing" || res.Cities[0].LineCount != 1 || res.Cities[0].TotalLengthKm != 31 ||
		res.Cities[1].CitySlug != "shanghai" || res.Cities[1].LineCount != 2 || res.Cities[1].TotalLengthKm != 101.1 {
		t.Fatalf("city groups: %v", res.Cities)
	}

	// With rolling stock codes, the line's code must also be listed.
	res, err = svc.GetRollingStockTopic(ctx, &metrov1.GetRollingStockTopicRequest{Slug: "a-six"})
	if err != nil || res.TotalLines != 2 || res.Lines[0].Code != "bj-1" || res.Lines[1].Code != "sh-1" {
		t.Fatalf("a-six: %v %v", res, err)
	}
	if _, err = svc.GetRollingStockTopic(ctx, &metrov1.GetRollingStockTopicRequest{Slug: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown topic: %v", err)
	}
}

func TestDatabaseRefusesWhatTheServiceWouldRefuse(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB(t)
	s := seed{db}
	c := s.city(t, "shanghai", "上海", 0)
	stock := s.stock(t, "A型")
	l := s.line(t, c, "sh-1", lineOpts{number: 1, stock: stock})

	if _, err := db.City.Create().SetSlug("Bad Slug").SetName("x").Save(ctx); err == nil {
		t.Error("city slug with spaces and capitals stored")
	}
	if _, err := db.City.Create().SetSlug("shanghai").SetName("dup").Save(ctx); err == nil {
		t.Error("duplicate city slug stored")
	}
	if _, err := db.Line.Create().SetCode("sh-9").SetCityID(c.ID).SetName("9").SetColor("E4002B").Save(ctx); err == nil {
		t.Error("color without # stored")
	}
	if _, err := db.Line.Create().SetCode("sh-8").SetCityID(c.ID).SetName("8").SetColor("#E4002B").SetStations(-1).Save(ctx); err == nil {
		t.Error("negative station count stored")
	}
	// Foreign keys restrict deletion: nothing is cascaded or nulled.
	if err := db.City.DeleteOne(c).Exec(ctx); err == nil {
		t.Error("city with lines deleted")
	}
	if err := db.RollingStock.DeleteOne(stock).Exec(ctx); err == nil {
		t.Error("rolling stock used by a line deleted")
	}
	if got := db.Line.GetX(ctx, l.ID); got.RollingStockID == nil {
		t.Error("line lost its rolling stock")
	}
	// Single catalog_state row.
	if _, err := db.CatalogState.Create().SetID(2).Save(ctx); err == nil {
		t.Error("second catalog_state row stored")
	}
}

func statusOf(s string) line.Status { return line.Status(s) }
