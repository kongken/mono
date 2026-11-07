package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/entry"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var timeRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$`)

// TimelineService owns all record operations. The caller identity always comes
// from the internal auth context; request messages never contain a user id.
type TimelineService struct {
	timelinev1.UnimplementedTimelineServiceServer
	db *ent.Client
}

func NewTimelineService(db *ent.Client) *TimelineService { return &TimelineService{db: db} }

func user(ctx context.Context) (string, error) {
	id := rpcauth.CallFrom(ctx).UserID
	if id == "" {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	return id, nil
}
func validate(in *timelinev1.Entry, requireID bool) error {
	if in == nil || strings.TrimSpace(in.Title) == "" {
		return status.Error(codes.InvalidArgument, "title is required")
	}
	if !dateRE.MatchString(in.Date) {
		return status.Error(codes.InvalidArgument, "date must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return status.Error(codes.InvalidArgument, "invalid date")
	}
	if in.Time != "" && !timeRE.MatchString(in.Time) {
		return status.Error(codes.InvalidArgument, "invalid time")
	}
	if in.Timezone == "" {
		return status.Error(codes.InvalidArgument, "timezone is required")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return status.Error(codes.InvalidArgument, "invalid timezone")
	}
	if len([]rune(in.Title)) > 200 || len([]rune(in.Note)) > 10000 || len([]rune(in.Location)) > 300 {
		return status.Error(codes.InvalidArgument, "field is too long")
	}
	if in.AmountMinor < 0 || (in.Currency != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(in.Currency)) {
		return status.Error(codes.InvalidArgument, "invalid amount or currency")
	}
	if in.Url != "" {
		u, err := url.Parse(in.Url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.IndexFunc(in.Url, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return status.Error(codes.InvalidArgument, "invalid URL")
		}
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			return status.Error(codes.InvalidArgument, "private URL is not allowed")
		}
	}
	if requireID && in.Id == "" {
		return status.Error(codes.InvalidArgument, "id is required")
	}
	return nil
}
func occurred(in *timelinev1.Entry) time.Time {
	loc, _ := time.LoadLocation(in.Timezone)
	value := in.Date
	if in.Time != "" {
		value += "T" + in.Time
	}
	layout := "2006-01-02"
	if in.Time != "" {
		layout = "2006-01-02T15:04:05"
		if len(in.Time) == 5 {
			layout = "2006-01-02T15:04"
		}
	}
	t, _ := time.ParseInLocation(layout, value, loc)
	return t.UTC()
}
func mapEntry(e *ent.Entry) *timelinev1.Entry {
	out := &timelinev1.Entry{Id: e.ID, Title: e.Title, Date: e.OccurredDate, Time: e.OccurredTime, Timezone: e.Timezone, Category: e.Category, Location: e.Location, AmountMinor: e.AmountMinor, Currency: e.Currency, Note: e.Body, Url: e.URL, UrlTitle: e.URLTitle, UrlNote: e.URLNote, Revision: int64(e.Revision), CreatedAt: timestamppb.New(e.CreatedAt), UpdatedAt: timestamppb.New(e.UpdatedAt)}
	if e.DeletedAt != nil {
		out.DeletedAt = timestamppb.New(*e.DeletedAt)
	}
	return out
}
func (s *TimelineService) ListEntries(ctx context.Context, req *timelinev1.ListEntriesRequest) (*timelinev1.ListEntriesResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	q := s.db.Entry.Query().Where(entry.UserID(uid))
	if !req.IncludeDeleted {
		q = q.Where(entry.DeletedAtIsNil())
	}
	if req.Category != "" {
		q = q.Where(entry.CategoryEQ(req.Category))
	}
	if req.Query != "" {
		q = q.Where(entry.Or(entry.TitleContainsFold(req.Query), entry.BodyContainsFold(req.Query), entry.LocationContainsFold(req.Query)))
	}
	if req.FromDate != "" {
		q = q.Where(entry.OccurredDateGTE(req.FromDate))
	}
	if req.ToDate != "" {
		q = q.Where(entry.OccurredDateLTE(req.ToDate))
	}
	rows, err := q.Order(ent.Desc(entry.FieldOccurredAt), ent.Asc(entry.FieldID)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	out := &timelinev1.ListEntriesResponse{}
	for _, row := range rows {
		out.Entries = append(out.Entries, mapEntry(row))
	}
	return out, nil
}
func (s *TimelineService) CreateEntry(ctx context.Context, req *timelinev1.CreateEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validate(req.Entry, false); err != nil {
		return nil, err
	}
	if req.MutationId != "" {
		if old, e := s.db.Entry.Query().Where(entry.UserID(uid), entry.MutationIDEQ(req.MutationId)).Only(ctx); e == nil {
			return &timelinev1.EntryResponse{Entry: mapEntry(old)}, nil
		}
	}
	c := s.db.Entry.Create().SetUserID(uid).SetTitle(strings.TrimSpace(req.Entry.Title)).SetBody(req.Entry.Note).SetOccurredDate(req.Entry.Date).SetOccurredTime(req.Entry.Time).SetTimezone(req.Entry.Timezone).SetCategory(req.Entry.Category).SetLocation(req.Entry.Location).SetAmountMinor(req.Entry.AmountMinor).SetCurrency(req.Entry.Currency).SetURL(req.Entry.Url).SetURLTitle(req.Entry.UrlTitle).SetURLNote(req.Entry.UrlNote).SetOccurredAt(occurred(req.Entry))
	if req.MutationId != "" {
		c.SetMutationID(req.MutationId)
	}
	row, err := c.Save(ctx)
	if err != nil {
		return nil, err
	}
	return &timelinev1.EntryResponse{Entry: mapEntry(row)}, nil
}
func (s *TimelineService) UpdateEntry(ctx context.Context, req *timelinev1.UpdateEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if err = validate(req.Entry, true); err != nil {
		return nil, err
	}
	row, err := s.db.Entry.Query().Where(entry.ID(req.Entry.Id), entry.UserID(uid), entry.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	u := row.Update().SetTitle(strings.TrimSpace(req.Entry.Title)).SetBody(req.Entry.Note).SetOccurredDate(req.Entry.Date).SetOccurredTime(req.Entry.Time).SetTimezone(req.Entry.Timezone).SetCategory(req.Entry.Category).SetLocation(req.Entry.Location).SetAmountMinor(req.Entry.AmountMinor).SetCurrency(req.Entry.Currency).SetURL(req.Entry.Url).SetURLTitle(req.Entry.UrlTitle).SetURLNote(req.Entry.UrlNote).SetOccurredAt(occurred(req.Entry)).SetRevision(row.Revision + 1)
	if req.MutationId != "" {
		u.SetMutationID(req.MutationId)
	}
	row, err = u.Save(ctx)
	if err != nil {
		return nil, err
	}
	return &timelinev1.EntryResponse{Entry: mapEntry(row)}, nil
}
func (s *TimelineService) DeleteEntry(ctx context.Context, req *timelinev1.DeleteEntryRequest) (*timelinev1.Empty, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.db.Entry.Query().Where(entry.ID(req.Id), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	if req.Permanent {
		if _, err = s.db.Entry.Delete().Where(entry.ID(row.ID), entry.UserID(uid)).Exec(ctx); err != nil {
			return nil, err
		}
	} else {
		if _, err = row.Update().SetDeletedAt(time.Now().UTC()).SetRevision(row.Revision + 1).Save(ctx); err != nil {
			return nil, err
		}
	}
	return &timelinev1.Empty{}, nil
}
func (s *TimelineService) RestoreEntry(ctx context.Context, req *timelinev1.RestoreEntryRequest) (*timelinev1.EntryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.db.Entry.Query().Where(entry.ID(req.Id), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if row.DeletedAt == nil {
		return &timelinev1.EntryResponse{Entry: mapEntry(row)}, nil
	}
	row, err = row.Update().ClearDeletedAt().SetRevision(row.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	return &timelinev1.EntryResponse{Entry: mapEntry(row)}, nil
}
func (s *TimelineService) PurgeEntry(ctx context.Context, req *timelinev1.PurgeEntryRequest) (*timelinev1.Empty, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.db.Entry.Query().Where(entry.ID(req.Id), entry.UserID(uid)).Only(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	if req.ExpectedRevision != 0 && int64(row.Revision) != req.ExpectedRevision {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	if _, err := s.db.Entry.Delete().Where(entry.ID(row.ID), entry.UserID(uid)).Exec(ctx); err != nil {
		return nil, err
	}
	return &timelinev1.Empty{}, nil
}

var _ = errors.New
var _ = fmt.Sprintf
