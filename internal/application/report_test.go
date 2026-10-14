package application_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/application"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
)

func TestReportServiceRequiresAuthenticationAndIsolatesUsers(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.report.GetReport(ctx, connect.NewRequest(&tripmapv1.GetReportRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)

	alice := e.register("alice_f8")
	bob := e.register("bob_f8")

	e.mustSync2(alice, "mut-alice-1", 0,
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "trip-hz-1", "2026-04-12", "春游西湖苏堤与龙井茶园漫步"),
	)

	bobReport, err := e.report.GetReport(ctx, bearer(bob, &tripmapv1.GetReportRequest{Timezone: "Asia/Shanghai"}))
	if err != nil {
		t.Fatalf("bob GetReport: %v", err)
	}
	if bobReport.Msg.Overview.VisitedCities != 0 || bobReport.Msg.Overview.TotalTrips != 0 {
		t.Fatalf("expected bob report to be empty, got %+v", bobReport.Msg.Overview)
	}
}

func TestReportServiceTimezoneCacheInvalidationAndScheduledMaintenance(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	fixedNow := time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC)
	e.reportSvc.SetNow(func() time.Time { return fixedNow })

	alice := e.register("alice_f8_tz")
	e.mustSync2(alice, "mut-alice-seed", 0,
		&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: "530000", Color: "#4b937d"}}},
		upCity("330100", "杭州市", "330000", "#4b937d"),
		upTrip("330100", "trip-hz-2025", "2025-05-01", "第一次在西湖看断桥残雪与保俶塔"),
		upTrip("330100", "trip-hz-2026a", "2026-04-02", "第二次来杭州九溪十八涧徒步看春茶"),
		upTrip("330100", "trip-hz-2026b", "2026-10-05", "第三次来杭州满觉陇赏桂花喝桂花拿铁"),
	)

	// First call in Asia/Shanghai (UTC+8 => 2027-01-01, currentYear=2027) computes and caches.
	r1, err := e.report.GetReport(ctx, bearer(alice, &tripmapv1.GetReportRequest{Timezone: "Asia/Shanghai"}))
	if err != nil {
		t.Fatalf("GetReport 1: %v", err)
	}
	if r1.Msg.FromCache {
		t.Fatalf("expected first GetReport call not to be from cache")
	}
	if r1.Msg.GeneratedOn != "2027-01-01" || r1.Msg.CurrentYear != 2027 || r1.Msg.SelectedYear != 2026 {
		t.Fatalf("unexpected Shanghai dates: %+v", r1.Msg)
	}
	if r1.Msg.Overview.ExploredProvinces != 2 || r1.Msg.Overview.VisitedCities != 1 || r1.Msg.Overview.TotalTrips != 3 {
		t.Fatalf("unexpected overview: %+v", r1.Msg.Overview)
	}

	// Second call with the same revision and timezone hits the Redis cache and allows switching selected_year.
	r2, err := e.report.GetReport(ctx, bearer(alice, &tripmapv1.GetReportRequest{Timezone: "Asia/Shanghai", Year: 2025}))
	if err != nil {
		t.Fatalf("GetReport 2: %v", err)
	}
	if !r2.Msg.FromCache || r2.Msg.SelectedYear != 2025 {
		t.Fatalf("expected cache hit with selected_year=2025, got from_cache=%v selected_year=%d", r2.Msg.FromCache, r2.Msg.SelectedYear)
	}

	// Calling in America/New_York (UTC-5 => 2026-12-31, currentYear=2026) evaluates in New York timezone.
	rNY, err := e.report.GetReport(ctx, bearer(alice, &tripmapv1.GetReportRequest{Timezone: "America/New_York"}))
	if err != nil {
		t.Fatalf("GetReport NY: %v", err)
	}
	if rNY.Msg.GeneratedOn != "2026-12-31" || rNY.Msg.CurrentYear != 2026 {
		t.Fatalf("unexpected New York dates: %+v", rNY.Msg)
	}

	// Mutating footprints advances revision and immediately invalidates the old revision cache.
	e.mustSync2(alice, "mut-alice-add-xm", r1.Msg.Revision,
		upCity("350200", "厦门市", "350000", "#e2af5c"),
		upTrip("350200", "trip-xm-2026", "2026-11-12", "鼓浪屿环岛漫步与沙坡尾日落"),
	)

	// Run scheduled maintenance job to prewarm the cache for the new revision.
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := application.RunReportMaintenance(ctx, e.store, idx, fixedNow); err != nil {
		t.Fatalf("RunReportMaintenance: %v", err)
	}

	// Next GetReport in Asia/Shanghai is served directly from the prewarmed maintenance cache with updated numbers.
	r3, err := e.report.GetReport(ctx, bearer(alice, &tripmapv1.GetReportRequest{Timezone: "Asia/Shanghai"}))
	if err != nil {
		t.Fatalf("GetReport 3: %v", err)
	}
	if !r3.Msg.FromCache {
		t.Fatalf("expected GetReport 3 to hit cache prewarmed by RunReportMaintenance")
	}
	if r3.Msg.Overview.ExploredProvinces != 3 || r3.Msg.Overview.VisitedCities != 2 || r3.Msg.Overview.TotalTrips != 4 {
		t.Fatalf("expected updated overview after mutation, got %+v", r3.Msg.Overview)
	}
}
