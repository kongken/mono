package application

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/tripmap/ent"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/user"
	"go.orx.me/apps/trip-map/internal/tripmap/regions"
	"go.orx.me/apps/trip-map/internal/tripmap/report"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"go.orx.me/apps/trip-map/pkg/proto/tripmap/v1/tripmapv1connect"
	"google.golang.org/protobuf/proto"
)

const (
	reportCacheTTL         = 24 * time.Hour
	reportMaintenanceBatch = 200
)

type ReportService struct {
	tripmapv1connect.UnimplementedReportServiceHandler
	src     StoreSource
	regions *regions.Index

	mu  sync.RWMutex
	now func() time.Time
}

func NewReportService(src StoreSource, idx *regions.Index) *ReportService {
	return &ReportService{
		src:     src,
		regions: idx,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// SetNow overrides the clock used by GetReport; only tests use it.
func (s *ReportService) SetNow(fn func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = fn
}

func (s *ReportService) currentTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.now()
}

func reportCacheKey(st *Store, userID string, revision int64, tzName, localDate, regionVersion string) string {
	return st.prefix() + "report:v2:" + userID + "|" + strconv.FormatInt(revision, 10) + "|" + tzName + "|" + localDate + "|" + regionVersion
}

func applyRequestedYear(res *tripmapv1.GetReportResponse, requestedYear int32) {
	if requestedYear > 0 && slices.Contains(res.AvailableYears, requestedYear) {
		res.SelectedYear = requestedYear
		return
	}
	res.SelectedYear = res.CurrentYear
	for _, ar := range res.AnnualReports {
		if ar.TripCount > 0 {
			res.SelectedYear = ar.Year
			return
		}
	}
}

func (s *ReportService) GetReport(ctx context.Context, req *connect.Request[tripmapv1.GetReportRequest]) (*connect.Response[tripmapv1.GetReportResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	now := s.currentTime()
	loc, tzName := report.ResolveTimezone(req.Msg.GetTimezone())
	localDate := now.In(loc).Format("2006-01-02")

	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	u, err := tx.User.Get(ctx, p.UserID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauthenticated
		}
		return nil, internalError(err)
	}
	if u.Disabled {
		return nil, errUnauthenticated
	}

	key := reportCacheKey(st, u.ID, u.Revision, tzName, localDate, s.regions.Version())
	if raw, rerr := st.Redis.Get(ctx, key).Bytes(); rerr == nil && len(raw) > 0 {
		var cached tripmapv1.GetReportResponse
		if uerr := proto.Unmarshal(raw, &cached); uerr == nil {
			applyRequestedYear(&cached, req.Msg.GetYear())
			cached.FromCache = true
			return connect.NewResponse(&cached), nil
		}
	}

	src, err := loadShareSource(ctx, tx, u, nil)
	if err != nil {
		return nil, shareSourceError(err)
	}
	res := report.Build(s.regions, src, report.Options{
		Timezone: tzName,
		Year:     0,
		Now:      now,
		Revision: u.Revision,
	})
	if raw, merr := proto.Marshal(res); merr == nil {
		_ = st.Redis.Set(ctx, key, raw, reportCacheTTL).Err()
	}
	applyRequestedYear(res, req.Msg.GetYear())
	return connect.NewResponse(res), nil
}

// RunReportMaintenance prewarms Redis report caches in the default timezone
// for recently active accounts whose current footprint revision is not cached.
func RunReportMaintenance(ctx context.Context, st *Store, idx *regions.Index, now time.Time) error {
	if st == nil || st.DB == nil || st.Redis == nil || idx == nil {
		return nil
	}
	loc, tzName := report.ResolveTimezone(report.DefaultTimezone)
	localDate := now.In(loc).Format("2006-01-02")

	users, err := st.DB.User.Query().
		Where(user.DisabledEQ(false)).
		Order(ent.Desc(user.FieldUpdatedAt)).
		Limit(reportMaintenanceBatch).
		All(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range users {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := reportCacheKey(st, candidate.ID, candidate.Revision, tzName, localDate, idx.Version())
		if exists, err := st.Redis.Exists(ctx, key).Result(); err == nil && exists > 0 {
			continue
		}
		tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			continue
		}
		u, err := tx.User.Get(ctx, candidate.ID)
		if err != nil || u.Disabled {
			_ = tx.Rollback()
			continue
		}
		src, err := loadShareSource(ctx, tx, u, nil)
		_ = tx.Rollback()
		if err != nil {
			continue
		}
		res := report.Build(idx, src, report.Options{
			Timezone: tzName,
			Now:      now,
			Revision: u.Revision,
		})
		key = reportCacheKey(st, u.ID, u.Revision, tzName, localDate, idx.Version())
		if raw, merr := proto.Marshal(res); merr == nil {
			_ = st.Redis.Set(ctx, key, raw, reportCacheTTL).Err()
		}
	}
	return nil
}
