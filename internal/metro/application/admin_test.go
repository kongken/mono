package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/testutil"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
)

func adminCtx() context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{
		UserID: "admin-1", RequestID: "req-1", Channel: rpcauth.ChannelWeb,
	})
}

func mcpCtx() context.Context {
	return rpcauth.WithCall(context.Background(), rpcauth.Call{
		UserID: "admin-2", RequestID: "req-2", Channel: rpcauth.ChannelMCP, TokenID: "tok-9",
	})
}

// detail returns the first detail of type T carried by err.
func detail[T proto.Message](t *testing.T, err error, into T) T {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("not a status error: %v", err)
	}
	for _, d := range st.Details() {
		if m, ok := d.(protoadapt.MessageV1); ok {
			if got, ok := protoadapt.MessageV2Of(m).(T); ok {
				return got
			}
		}
	}
	t.Fatalf("no %T detail in %v", into, err)
	return into
}

func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code %v, want InvalidArgument: %v", status.Code(err), err)
	}
	out := map[string]string{}
	for _, e := range detail(t, err, &metrov1.ValidationDetail{}).Errors {
		out[e.Field] = e.Reason
	}
	return out
}

func version(t *testing.T, db *ent.Client) int64 {
	t.Helper()
	row, err := db.CatalogState.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return row.Version
}

func auditCount(t *testing.T, db *ent.Client) int {
	t.Helper()
	n, err := db.AdminAuditLog.Query().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func goodLine() *metrov1.LineInput {
	return &metrov1.LineInput{
		Code: "sh-1", CitySlug: "shanghai", Name: "1号线", Number: 1, Cars: 6, Stations: 28, LengthKm: 36.9,
		Color: "#E4002B", CommencedOn: "1993-05-28",
	}
}

func TestCityLifecycleRevisionVersionAndAudit(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	v0 := version(t, db)

	created, err := svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{
		Slug: "shanghai", Name: "上海", HeaderImages: []string{"https://img.example/a.jpg"}, SortOrder: 3}})
	if err != nil {
		t.Fatal(err)
	}
	c := created.City
	if c.Revision != 1 || created.CatalogVersion != v0+1 || c.City.Slug != "shanghai" || c.Id == "" {
		t.Fatalf("created: %+v version=%d (was %d)", c, created.CatalogVersion, v0)
	}
	if _, err = svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "重复"}}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate slug: %v", err)
	}

	renamed := &metrov1.CityInput{Slug: "shanghai", Name: "上海市", SortOrder: 3, HeaderImages: []string{"https://img.example/a.jpg"}}
	updated, err := svc.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: c.Id, ExpectedRevision: 1, City: renamed})
	if err != nil || updated.City.Revision != 2 || updated.City.City.Name != "上海市" || updated.CatalogVersion != v0+2 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	// A stale revision is refused with the current one.
	_, err = svc.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: c.Id, ExpectedRevision: 1, City: &metrov1.CityInput{Slug: "shanghai", Name: "旧"}})
	if status.Code(err) != codes.FailedPrecondition || detail(t, err, &metrov1.RevisionConflictDetail{}).CurrentRevision != 2 {
		t.Fatalf("stale update: %v", err)
	}
	// Sending the stored values again changes nothing: no revision, version or audit.
	auditBefore := auditCount(t, db)
	same, err := svc.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: c.Id, ExpectedRevision: 2, City: renamed})
	if err != nil || same.City.Revision != 2 || same.CatalogVersion != v0+2 || auditCount(t, db) != auditBefore {
		t.Fatalf("no-op update: %+v %v", same, err)
	}
	// Missing revision and unknown ids.
	if _, err = svc.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: c.Id, City: renamed}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no revision: %v", err)
	}
	if _, err = svc.DeleteCity(ctx, &metrov1.DeleteCityRequest{Id: "nope", ExpectedRevision: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown id: %v", err)
	}

	del, err := svc.DeleteCity(ctx, &metrov1.DeleteCityRequest{Id: c.Id, ExpectedRevision: 2})
	if err != nil || del.CatalogVersion != v0+3 {
		t.Fatalf("delete: %+v %v", del, err)
	}
	logs, err := svc.ListAuditLogs(ctx, &metrov1.ListAuditLogsRequest{EntityId: c.Id})
	if err != nil || len(logs.Logs) != 3 {
		t.Fatalf("audit: %+v %v", logs, err)
	}
	actions := []string{logs.Logs[0].Action, logs.Logs[1].Action, logs.Logs[2].Action}
	if strings.Join(actions, ",") != "city.delete,city.update,city.create" {
		t.Fatalf("audit order %v", actions)
	}
	first := logs.Logs[2]
	if first.ActorUserId != "admin-1" || first.Channel != "web" || first.RequestId != "req-1" || first.BeforeJson != "" ||
		!strings.Contains(first.AfterJson, `"slug":"shanghai"`) || first.TokenId != "" {
		t.Fatalf("create audit row: %+v", first)
	}
	if upd := logs.Logs[1]; !strings.Contains(upd.BeforeJson, `"name":"上海"`) || !strings.Contains(upd.AfterJson, `"name":"上海市"`) {
		t.Fatalf("update snapshots: %+v", upd)
	}
	if logs.Logs[0].AfterJson != "" || logs.Logs[0].BeforeJson == "" {
		t.Fatalf("delete snapshots: %+v", logs.Logs[0])
	}
}

func TestAuditRecordsMCPChannelAndToken(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	if _, err := svc.CreateRollingStock(mcpCtx(), &metrov1.CreateRollingStockRequest{RollingStock: &metrov1.RollingStockInput{Code: "02A02"}}); err != nil {
		t.Fatal(err)
	}
	logs, err := svc.ListAuditLogs(adminCtx(), &metrov1.ListAuditLogsRequest{Channel: "mcp"})
	if err != nil || len(logs.Logs) != 1 || logs.Logs[0].TokenId != "tok-9" || logs.Logs[0].ActorUserId != "admin-2" {
		t.Fatalf("mcp audit: %+v %v", logs, err)
	}
	if logs, _ = svc.ListAuditLogs(adminCtx(), &metrov1.ListAuditLogsRequest{Channel: "web"}); len(logs.Logs) != 0 {
		t.Fatalf("web filter: %+v", logs)
	}
	if _, err = svc.ListAuditLogs(adminCtx(), &metrov1.ListAuditLogsRequest{Channel: "fax"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad channel: %v", err)
	}
	// Without an identity the service refuses to write.
	if _, err = svc.CreateRollingStock(context.Background(), &metrov1.CreateRollingStockRequest{RollingStock: &metrov1.RollingStockInput{Code: "x"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("no identity: %v", err)
	}
}

func TestLineValidationReportsEveryField(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	if _, err := svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海"}}); err != nil {
		t.Fatal(err)
	}
	v := version(t, db)
	bad := &metrov1.LineInput{
		Code: "Sh_1", CitySlug: "atlantis", Name: " 1号线", Number: -1, RollingStockCode: "ghost", Cars: 120,
		LengthKm: 1.234, Color: "E4002B", CommencedOn: "1993/05/28", ExtendedOn: "1990-01-01",
		Status: metrov1.LineStatus(42),
	}
	_, err := svc.CreateLine(ctx, &metrov1.CreateLineRequest{Line: bad})
	got := fieldErrors(t, err)
	for _, field := range []string{"code", "name", "number", "status", "cars", "length_km", "color", "commenced_on"} {
		if got[field] == "" {
			t.Errorf("no error for %s: %v", field, got)
		}
	}
	// The references are checked against the database only once the rest is valid.
	bad = goodLine()
	bad.CitySlug, bad.RollingStockCode = "atlantis", "ghost"
	got = fieldErrors(t, mustFail(t, svc, ctx, bad))
	if got["city"] == "" || got["rolling_stock"] == "" {
		t.Fatalf("reference errors: %v", got)
	}
	if v2 := version(t, db); v2 != v || auditCount(t, db) != 1 {
		t.Fatalf("a refused write changed the version (%d -> %d) or audit", v, v2)
	}

	bad = goodLine()
	bad.ExtendedOn = "1990-01-01"
	if got = fieldErrors(t, mustFail(t, svc, ctx, bad)); got["extended_on"] == "" {
		t.Fatalf("extension before opening: %v", got)
	}
	if _, err = svc.CreateLine(ctx, &metrov1.CreateLineRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing line: %v", err)
	}
}

func mustFail(t *testing.T, svc *application.AdminService, ctx context.Context, in *metrov1.LineInput) error {
	t.Helper()
	_, err := svc.CreateLine(ctx, &metrov1.CreateLineRequest{Line: in})
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func TestLineCodeIsImmutableAndCityMayChange(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	for _, slug := range []string{"shanghai", "suzhou"} {
		if _, err := svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: slug, Name: slug}}); err != nil {
			t.Fatal(err)
		}
	}
	created, err := svc.CreateLine(ctx, &metrov1.CreateLineRequest{Line: goodLine()})
	if err != nil {
		t.Fatal(err)
	}
	l := created.Line
	if l.Revision != 1 || l.Line.Status != metrov1.LineStatus_LINE_STATUS_OPERATING || l.Line.CommencedOn != "1993-05-28" {
		t.Fatalf("created line: %+v", l)
	}
	if _, err = svc.CreateLine(ctx, &metrov1.CreateLineRequest{Line: goodLine()}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate code: %v", err)
	}

	edit := goodLine()
	edit.Code = "sh-9"
	if got := fieldErrors(t, errOf(svc.UpdateLine(ctx, &metrov1.UpdateLineRequest{Id: l.Id, ExpectedRevision: 1, Line: edit}))); got["code"] == "" {
		t.Fatalf("code change: %v", got)
	}
	edit = goodLine()
	edit.CitySlug, edit.Status, edit.ExtendedOn, edit.CommencedOn = "suzhou", metrov1.LineStatus_LINE_STATUS_RETIRED, "", ""
	res, err := svc.UpdateLine(ctx, &metrov1.UpdateLineRequest{Id: l.Id, ExpectedRevision: 1, Line: edit})
	if err != nil || res.Line.Revision != 2 || res.Line.Line.CitySlug != "suzhou" ||
		res.Line.Line.Status != metrov1.LineStatus_LINE_STATUS_RETIRED || res.Line.Line.CommencedOn != "" {
		t.Fatalf("update: %+v %v", res, err)
	}
}

func errOf[T any](_ T, err error) error { return err }

func TestReferencedRecordsCannotBeDeleted(t *testing.T) {
	db := testutil.DB(t)
	var riders int64
	guard := func(context.Context, *ent.Tx, string) (string, int64, error) { return "rider_line", riders, nil }
	svc := application.NewAdminService(db, guard)
	ctx := adminCtx()
	city, _ := svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海"}})
	stock, _ := svc.CreateRollingStock(ctx, &metrov1.CreateRollingStockRequest{RollingStock: &metrov1.RollingStockInput{Code: "02A02"}})
	in := goodLine()
	in.RollingStockCode = "02A02"
	ln, err := svc.CreateLine(ctx, &metrov1.CreateLineRequest{Line: in})
	if err != nil {
		t.Fatal(err)
	}
	v := version(t, db)

	_, err = svc.DeleteCity(ctx, &metrov1.DeleteCityRequest{Id: city.City.Id, ExpectedRevision: 1})
	refs := detail(t, err, &metrov1.ReferencedDetail{}).References
	if status.Code(err) != codes.FailedPrecondition || len(refs) != 1 || refs[0].EntityType != "line" || refs[0].Count != 1 {
		t.Fatalf("city in use: %v", err)
	}
	if _, err = svc.DeleteRollingStock(ctx, &metrov1.DeleteRollingStockRequest{Id: stock.RollingStock.Id, ExpectedRevision: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stock in use: %v", err)
	}
	riders = 3
	_, err = svc.DeleteLine(ctx, &metrov1.DeleteLineRequest{Id: ln.Line.Id, ExpectedRevision: 1})
	refs = detail(t, err, &metrov1.ReferencedDetail{}).References
	if status.Code(err) != codes.FailedPrecondition || len(refs) != 1 || refs[0].EntityType != "rider_line" || refs[0].Count != 3 {
		t.Fatalf("line with riders: %v", err)
	}
	if version(t, db) != v {
		t.Fatal("refused deletes raised the version")
	}
	riders = 0
	for _, step := range []func() error{
		func() error {
			return errOf(svc.DeleteLine(ctx, &metrov1.DeleteLineRequest{Id: ln.Line.Id, ExpectedRevision: 1}))
		},
		func() error {
			return errOf(svc.DeleteCity(ctx, &metrov1.DeleteCityRequest{Id: city.City.Id, ExpectedRevision: 1}))
		},
		func() error {
			return errOf(svc.DeleteRollingStock(ctx, &metrov1.DeleteRollingStockRequest{Id: stock.RollingStock.Id, ExpectedRevision: 1}))
		},
	} {
		if err = step(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTopicRules(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	_, err := svc.CreateRollingStockTopic(ctx, &metrov1.CreateRollingStockTopicRequest{Topic: &metrov1.RollingStockTopicInput{Slug: "x", Name: "无条件"}})
	if got := fieldErrors(t, err); got["cars"] == "" {
		t.Fatalf("a topic without criteria must be refused: %v", got)
	}
	_, err = svc.CreateRollingStockTopic(ctx, &metrov1.CreateRollingStockTopicRequest{Topic: &metrov1.RollingStockTopicInput{
		Slug: "Six", Name: "n", Cars: []int32{6, 6, 0}, RollingStockCodes: []string{"a", "a"}}})
	got := fieldErrors(t, err)
	for _, f := range []string{"slug", "cars[1]", "cars[2]", "rolling_stock_codes[1]"} {
		if got[f] == "" {
			t.Errorf("no error for %s: %v", f, got)
		}
	}
	res, err := svc.CreateRollingStockTopic(ctx, &metrov1.CreateRollingStockTopicRequest{Topic: &metrov1.RollingStockTopicInput{Slug: "six-cars", Name: "6 节编组", Cars: []int32{6}}})
	if err != nil || res.Topic.Revision != 1 {
		t.Fatal(res, err)
	}
	if _, err = svc.DeleteRollingStockTopic(ctx, &metrov1.DeleteRollingStockTopicRequest{Id: res.Topic.Id, ExpectedRevision: 1}); err != nil {
		t.Fatalf("topics have no reference limit: %v", err)
	}
}

func TestListsPageAndFilter(t *testing.T) {
	db := testutil.DB(t)
	s := seed{db}
	a := s.city(t, "alpha", "甲", 0)
	b := s.city(t, "beta", "乙", 1)
	for i, code := range []string{"al-1", "al-2", "al-3"} {
		s.line(t, a, code, lineOpts{number: i + 1})
	}
	s.line(t, b, "be-1", lineOpts{number: 1, status: "retired"})
	svc := application.NewAdminService(db)
	ctx := adminCtx()

	first, err := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{PageSize: 3})
	if err != nil || len(first.Lines) != 3 || first.Total != 4 || first.NextPageToken == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{PageSize: 3, PageToken: first.NextPageToken})
	if err != nil || len(second.Lines) != 1 || second.NextPageToken != "" || second.Lines[0].Line.Code != "be-1" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	// The administrator view shows every status, with the revision.
	retired, err := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{Statuses: []metrov1.LineStatus{metrov1.LineStatus_LINE_STATUS_RETIRED}})
	if err != nil || len(retired.Lines) != 1 || retired.Lines[0].Revision != 1 || retired.Lines[0].Line.CitySlug != "beta" {
		t.Fatalf("retired: %+v %v", retired, err)
	}
	if byCity, _ := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{CitySlug: "alpha"}); len(byCity.Lines) != 3 {
		t.Fatalf("by city: %+v", byCity)
	}
	if byQuery, _ := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{Query: "AL-2"}); len(byQuery.Lines) != 1 {
		t.Fatalf("by query: %+v", byQuery)
	}
	if _, err = svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{PageToken: "garbage"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad token: %v", err)
	}
	if big, err := svc.ListAdminLines(ctx, &metrov1.ListAdminLinesRequest{PageSize: 100000}); err != nil || len(big.Lines) != 4 {
		t.Fatalf("clamped page: %v %v", big, err)
	}
	cities, err := svc.ListAdminCities(ctx, &metrov1.ListAdminCitiesRequest{})
	if err != nil || len(cities.Cities) != 2 || cities.Cities[0].City.Slug != "alpha" {
		t.Fatalf("cities: %+v %v", cities, err)
	}
}

func TestConcurrentUpdatesWithTheSameRevisionAllowExactlyOne(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	created, err := svc.CreateCity(ctx, &metrov1.CreateCityRequest{City: &metrov1.CityInput{Slug: "shanghai", Name: "上海"}})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = svc.UpdateCity(ctx, &metrov1.UpdateCityRequest{Id: created.City.Id, ExpectedRevision: 1,
				City: &metrov1.CityInput{Slug: "shanghai", Name: "名" + string(rune('A'+i))}})
		}()
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, err := range results {
		switch status.Code(err) {
		case codes.OK:
			ok++
		case codes.FailedPrecondition:
			conflicts++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || conflicts != workers-1 {
		t.Fatalf("%d updates won, %d conflicted; want exactly one winner", ok, conflicts)
	}
	row, _ := db.City.Get(context.Background(), created.City.Id)
	if row.Revision != 2 {
		t.Fatalf("revision %d, want 2", row.Revision)
	}
}

const sampleCatalog = `{
  "format": "metro-catalog/1",
  "cities": [{"slug": "shanghai", "name": "上海", "header_images": [], "map_svg_url": "", "sort_order": 0}],
  "rolling_stocks": [{"code": "02A02", "length_m": 22.0, "width_m": 3.0, "capacity": 310}],
  "lines": [
    {"code": "sh-1", "city": "shanghai", "name": "1号线", "number": 1, "status": "operating", "rolling_stock": "02A02", "cars": 6, "cars_addition": 0, "stations": 28, "length_km": 36.9, "color": "#E4002B", "commenced_on": "1993-05-28", "is_loop": false},
    {"code": "sh-4", "city": "shanghai", "name": "4号线", "number": 4, "cars": 6, "stations": 26, "length_km": 33.7, "color": "#5F259F", "is_loop": true}
  ],
  "topics": [{"slug": "six-cars", "name": "6 节编组", "rolling_stock_codes": [], "cars": [6]}]
}`

func TestImportDryRunWritesNothing(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	v := version(t, db)
	res, err := svc.ImportCatalog(adminCtx(), &metrov1.ImportCatalogRequest{CatalogJson: sampleCatalog, DryRun: true})
	if err != nil || !res.DryRun || len(res.Errors) != 0 || res.Cities.Created != 1 || res.RollingStocks.Created != 1 ||
		res.Lines.Created != 2 || res.Topics.Created != 1 || res.CatalogVersion != v {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if n, _ := db.City.Query().Count(context.Background()); n != 0 || version(t, db) != v || auditCount(t, db) != 0 {
		t.Fatal("a dry run changed the catalog")
	}
}

func TestImportTwiceChangesNothingTheSecondTime(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	first, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: sampleCatalog})
	if err != nil || first.Lines.Created != 2 || first.CatalogVersion != version(t, db) {
		t.Fatalf("first import: %+v %v", first, err)
	}
	v, audits := version(t, db), auditCount(t, db)
	if audits != 5 {
		t.Fatalf("audit rows %d, want one per record (5)", audits)
	}
	again, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: sampleCatalog})
	if err != nil || again.Cities.Unchanged != 1 || again.RollingStocks.Unchanged != 1 || again.Lines.Unchanged != 2 ||
		again.Topics.Unchanged != 1 || again.Lines.Created+again.Lines.Updated != 0 {
		t.Fatalf("second import: %+v %v", again, err)
	}
	if version(t, db) != v || auditCount(t, db) != audits {
		t.Fatal("an import that changed nothing raised the version or wrote audit rows")
	}
	rows, _ := db.Line.Query().All(context.Background())
	for _, l := range rows {
		if l.Revision != 1 {
			t.Fatalf("%s revision %d, want 1", l.Code, l.Revision)
		}
	}

	// Changing one line updates just that line: revision, version and one audit row move.
	changed := strings.Replace(sampleCatalog, `"stations": 26`, `"stations": 27`, 1)
	res, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: changed})
	if err != nil || res.Lines.Updated != 1 || res.Lines.Unchanged != 1 || res.CatalogVersion != v+1 || auditCount(t, db) != audits+1 {
		t.Fatalf("partial change: %+v %v", res, err)
	}
	l4, _ := db.Line.Query().Where(line.Code("sh-4")).Only(context.Background())
	if l4.Revision != 2 || l4.Stations != 27 || !l4.IsLoop {
		t.Fatalf("sh-4: %+v", l4)
	}
}

func TestImportIsAllOrNothing(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	v := version(t, db)
	broken := strings.Replace(sampleCatalog, `"#5F259F"`, `"purple"`, 1)
	broken = strings.Replace(broken, `"city": "shanghai", "name": "1号线"`, `"city": "atlantis", "name": "1号线"`, 1)

	// A dry run reports every problem and still succeeds.
	res, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: broken, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, e := range res.Errors {
		paths[e.Field] = true
	}
	if !paths["lines[1].color"] || len(res.Errors) != 1 {
		// The reference check runs only after the per-record rules pass.
		t.Fatalf("dry run errors: %+v", res.Errors)
	}
	fixedColor := strings.Replace(broken, `"purple"`, `"#5F259F"`, 1)
	res, err = svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: fixedColor, DryRun: true})
	if err != nil || len(res.Errors) != 1 || res.Errors[0].Field != "lines[0].city" || res.Lines.Created != 0 {
		t.Fatalf("reference error: %+v %v", res, err)
	}

	// A real import with a bad record is refused whole.
	_, err = svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: broken})
	if got := fieldErrors(t, err); got["lines[1].color"] == "" {
		t.Fatalf("real import: %v", got)
	}
	_, err = svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: fixedColor})
	if got := fieldErrors(t, err); got["lines[0].city"] == "" {
		t.Fatalf("reference: %v", got)
	}
	if n, _ := db.City.Query().Count(context.Background()); n != 0 || version(t, db) != v || auditCount(t, db) != 0 {
		t.Fatal("a refused import left something behind")
	}
}

func TestImportRejectsMalformedDocuments(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	for name, tc := range map[string]struct{ doc, field string }{
		"not json":      {`{`, "catalog_json"},
		"empty":         {``, "catalog_json"},
		"wrong format":  {`{"format":"metro-catalog/2"}`, "format"},
		"unknown field": {`{"format":"metro-catalog/1","cities":[{"slug":"a","name":"A","colour":"red"}]}`, "catalog_json"},
		"wrong type":    {`{"format":"metro-catalog/1","lines":[{"code":"ab","cars":"six"}]}`, "lines.cars"},
		"trailing data": {`{"format":"metro-catalog/1"} {}`, "catalog_json"},
		"duplicate key": {`{"format":"metro-catalog/1","cities":[{"slug":"a","name":"A"},{"slug":"a","name":"B"}]}`, "cities[1]"},
	} {
		res, err := svc.ImportCatalog(adminCtx(), &metrov1.ImportCatalogRequest{CatalogJson: tc.doc, DryRun: true})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		found := false
		for _, e := range res.Errors {
			found = found || e.Field == tc.field
		}
		if !found {
			t.Errorf("%s: want an error on %s, got %+v", name, tc.field, res.Errors)
		}
	}
	big := `{"format":"metro-catalog/1","cities":[` + strings.Repeat(`{"slug":"a","name":"A"},`, 2000) + `{"slug":"b","name":"B"}]}`
	if res, _ := svc.ImportCatalog(adminCtx(), &metrov1.ImportCatalogRequest{CatalogJson: big, DryRun: true}); len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Reason, "2001") {
		t.Fatalf("too many records: %+v", res.Errors)
	}
}

func TestExportRoundTripsThroughImport(t *testing.T) {
	db := testutil.DB(t)
	svc := application.NewAdminService(db)
	ctx := adminCtx()
	if _, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: sampleCatalog}); err != nil {
		t.Fatal(err)
	}
	out, err := svc.ExportCatalog(ctx, &metrov1.ExportCatalogRequest{})
	if err != nil || out.CatalogVersion != version(t, db) {
		t.Fatalf("export: %v %v", out, err)
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(out.CatalogJson), &doc); err != nil || doc["format"] != "metro-catalog/1" {
		t.Fatalf("exported document: %v %v", doc, err)
	}
	v := version(t, db)
	res, err := svc.ImportCatalog(ctx, &metrov1.ImportCatalogRequest{CatalogJson: out.CatalogJson})
	if err != nil || res.Lines.Unchanged != 2 || res.Cities.Unchanged != 1 || res.RollingStocks.Unchanged != 1 ||
		res.Topics.Unchanged != 1 || version(t, db) != v {
		t.Fatalf("re-import of an export: %+v %v", res, err)
	}
	// An empty catalog exports valid, empty lists.
	empty := application.NewAdminService(testutil.DB(t))
	e, err := empty.ExportCatalog(ctx, &metrov1.ExportCatalogRequest{})
	if err != nil || !strings.Contains(e.CatalogJson, `"lines": []`) {
		t.Fatalf("empty export: %v %v", e, err)
	}
}
