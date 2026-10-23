package application

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql/sqlgraph"
	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/riderline"
	"github.com/kongken/mono/internal/metro/ent/riderprofile"
	"github.com/kongken/mono/internal/platform/rpcauth"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RiderService keeps riders' profiles and ride records. Apart from the public
// profile it acts only on the caller named by the verified credential.
type RiderService struct {
	metrov1.UnimplementedRiderServiceServer
	db  *ent.Client
	now func() time.Time
}

// NewRiderService returns the rider API over db.
func NewRiderService(db *ent.Client) *RiderService {
	return &RiderService{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// caller returns the signed-in user; the interceptor guarantees one for every
// non-anonymous method, this is the second line.
func caller(ctx context.Context) (string, error) {
	id, ok := rpcauth.UserIDFrom(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "sign in required")
	}
	return id, nil
}

// ---- profile ----

func (s *RiderService) GetMyProfile(ctx context.Context, _ *metrov1.GetMyProfileRequest) (*metrov1.GetMyProfileResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.db.RiderProfile.Get(ctx, user)
	switch {
	case ent.IsNotFound(err):
		return &metrov1.GetMyProfileResponse{Profile: profileInputOf(profileDoc{HeaderImages: []string{}})}, nil
	case err != nil:
		return nil, internalError("rider get profile", err)
	}
	return &metrov1.GetMyProfileResponse{Profile: profileInputOf(profileDocOf(p)), Exists: true}, nil
}

func (s *RiderService) UpdateMyProfile(ctx context.Context, req *metrov1.UpdateMyProfileRequest) (*metrov1.UpdateMyProfileResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetProfile() == nil {
		return nil, invalidField("profile", "is required")
	}
	d := profileFromInput(req.GetProfile())
	var errs errList
	d.validate(&errs)
	if errs.any() {
		return nil, invalid(errs.errs)
	}
	create := s.db.RiderProfile.Create().SetID(user).
		SetDisplayName(d.DisplayName).SetSubtitle(d.Subtitle).SetBio(d.Bio).SetLocation(d.Location).
		SetHeaderImages(append([]string{}, d.HeaderImages...)).SetTwitter(d.Twitter).SetInstagram(d.Instagram).
		SetWeibo(d.Weibo).SetFacebook(d.Facebook).SetGithub(d.Github).SetIsPublic(d.IsPublic)
	if d.Handle != "" {
		create.SetHandle(d.Handle)
	}
	// The first write creates the profile; later ones replace every field. The
	// conflict target is the user, so two first writes by the same user cannot
	// fail each other.
	err = create.OnConflictColumns(riderprofile.FieldID).Update(func(u *ent.RiderProfileUpsert) {
		u.UpdateDisplayName().UpdateSubtitle().UpdateBio().UpdateLocation().UpdateHeaderImages().
			UpdateTwitter().UpdateInstagram().UpdateWeibo().UpdateFacebook().UpdateGithub().UpdateIsPublic().UpdateUpdatedAt()
		if d.Handle != "" {
			u.UpdateHandle()
		} else {
			u.ClearHandle()
		}
	}).Exec(ctx)
	if err != nil {
		// The user is the conflict target, so a unique violation here is the handle.
		if sqlgraph.IsUniqueConstraintError(err) {
			return nil, alreadyExists("handle")
		}
		return nil, internalError("rider update profile", err)
	}
	p, err := s.db.RiderProfile.Get(ctx, user)
	if err != nil {
		return nil, internalError("rider read profile", err)
	}
	return &metrov1.UpdateMyProfileResponse{Profile: profileInputOf(profileDocOf(p))}, nil
}

// GetPublicProfile answers NOT_FOUND for a missing profile and for one that is
// not public alike, so the response does not reveal whether a handle is taken.
func (s *RiderService) GetPublicProfile(ctx context.Context, req *metrov1.GetPublicProfileRequest) (*metrov1.GetPublicProfileResponse, error) {
	handle := normalizeHandle(req.GetHandle())
	notFound := status.Error(codes.NotFound, "profile not found")
	if !handlePattern.MatchString(handle) {
		return nil, notFound
	}
	p, err := s.db.RiderProfile.Query().Where(riderprofile.Handle(handle), riderprofile.IsPublic(true)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		return nil, notFound
	case err != nil:
		return nil, internalError("rider public profile", err)
	}
	cities, stats, err := s.records(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return &metrov1.GetPublicProfileResponse{Profile: publicProfileOf(profileDocOf(p)), Cities: cities, Stats: stats}, nil
}

// ---- ride records ----

// records returns a rider's lines grouped by city, with totals.
func (s *RiderService) records(ctx context.Context, user string) ([]*metrov1.RiddenCity, *metrov1.RiderStats, error) {
	rows, err := s.db.RiderLine.Query().Where(riderline.UserID(user)).
		WithLine(func(q *ent.LineQuery) { q.WithCity() }).All(ctx)
	if err != nil {
		return nil, nil, internalError("rider records", err)
	}
	type group struct {
		city  *ent.City
		lines []*ent.Line
	}
	groups := map[string]*group{}
	var km float64
	years := map[string]int{}
	for _, r := range rows {
		l := r.Edges.Line
		if l == nil || l.Edges.City == nil {
			continue
		}
		g := groups[l.Edges.City.ID]
		if g == nil {
			g = &group{city: l.Edges.City}
			groups[l.Edges.City.ID] = g
		}
		g.lines = append(g.lines, l)
		km += l.LengthKm
		if r.RiddenYear != nil {
			years[l.ID] = *r.RiddenYear
		}
	}
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		sortLines(g.lines)
		ordered = append(ordered, g)
	}
	slices.SortFunc(ordered, func(a, b *group) int {
		return cmp.Or(cmp.Compare(a.city.SortOrder, b.city.SortOrder), strings.Compare(a.city.Slug, b.city.Slug))
	})
	cities := make([]*metrov1.RiddenCity, 0, len(ordered))
	lineCount := 0
	for _, g := range ordered {
		c := &metrov1.RiddenCity{CitySlug: g.city.Slug, CityName: g.city.Name}
		for _, l := range g.lines {
			c.Lines = append(c.Lines, riddenLine(l, years[l.ID]))
			lineCount++
		}
		cities = append(cities, c)
	}
	return cities, &metrov1.RiderStats{CityCount: int32(len(cities)), LineCount: int32(lineCount), TotalLengthKm: roundKm(km)}, nil
}

func riddenLine(l *ent.Line, year int) *metrov1.RiddenLine {
	return &metrov1.RiddenLine{
		LineCode: l.Code, LineName: l.Name, Color: l.Color, Status: statusEnum(l.Status),
		LengthKm: l.LengthKm, RiddenYear: int32(year),
	}
}

func (s *RiderService) ListMyLines(ctx context.Context, _ *metrov1.ListMyLinesRequest) (*metrov1.ListMyLinesResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	cities, stats, err := s.records(ctx, user)
	if err != nil {
		return nil, err
	}
	return &metrov1.ListMyLinesResponse{Cities: cities, Stats: stats}, nil
}

// lineByCode finds a line for a ride record; a missing one is NOT_FOUND.
func (s *RiderService) lineByCode(ctx context.Context, code string) (*ent.Line, error) {
	if code == "" {
		return nil, invalidField("line_code", "is required")
	}
	l, err := s.db.Line.Query().Where(line.Code(code)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		return nil, status.Error(codes.NotFound, "line not found")
	case err != nil:
		return nil, internalError("rider line lookup", err)
	}
	return l, nil
}

func (s *RiderService) MarkLine(ctx context.Context, req *metrov1.MarkLineRequest) (*metrov1.MarkLineResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	var year *int
	if req.RiddenYear != nil {
		y := int(req.GetRiddenYear())
		if y < 1900 || y > s.now().Year() {
			return nil, invalidField("ridden_year", "must be between 1900 and the current year")
		}
		year = &y
	}
	l, err := s.lineByCode(ctx, req.GetLineCode())
	if err != nil {
		return nil, err
	}
	existing, err := s.db.RiderLine.Query().Where(riderline.UserID(user), riderline.LineID(l.ID)).Exist(ctx)
	if err != nil {
		return nil, internalError("rider mark lookup", err)
	}
	// A record that already exists may be kept and re-dated; only new records are
	// refused for lines nobody can ride (yet or any more).
	if !existing && (l.Status == line.StatusRetired || l.Status == line.StatusPlanned) {
		return nil, status.Errorf(codes.FailedPrecondition, "a %s line cannot be marked as ridden", strings.ReplaceAll(string(l.Status), "_", " "))
	}
	err = s.db.RiderLine.Create().SetUserID(user).SetLineID(l.ID).SetNillableRiddenYear(year).
		OnConflictColumns(riderline.FieldUserID, riderline.FieldLineID).
		Update(func(u *ent.RiderLineUpsert) {
			if year != nil {
				u.UpdateRiddenYear()
			} else {
				u.ClearRiddenYear()
			}
			u.UpdateUpdatedAt()
		}).Exec(ctx)
	if err != nil {
		return nil, internalError("rider mark", err)
	}
	y := 0
	if year != nil {
		y = *year
	}
	return &metrov1.MarkLineResponse{Line: riddenLine(l, y)}, nil
}

func (s *RiderService) UnmarkLine(ctx context.Context, req *metrov1.UnmarkLineRequest) (*metrov1.UnmarkLineResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	l, err := s.lineByCode(ctx, req.GetLineCode())
	if err != nil {
		return nil, err
	}
	if _, err = s.db.RiderLine.Delete().Where(riderline.UserID(user), riderline.LineID(l.ID)).Exec(ctx); err != nil {
		return nil, internalError("rider unmark", err)
	}
	return &metrov1.UnmarkLineResponse{}, nil
}

// RiderLineGuard keeps a line that riders have recorded from being deleted.
func RiderLineGuard(ctx context.Context, tx *ent.Tx, lineID string) (string, int64, error) {
	n, err := tx.RiderLine.Query().Where(riderline.LineID(lineID)).Count(ctx)
	return "rider_line", int64(n), err
}

// ---- account cleanup ----

// AccountCleanupService removes one user's data when the account is deleted.
type AccountCleanupService struct {
	metrov1.UnimplementedAccountCleanupServiceServer
	db *ent.Client
}

// NewAccountCleanupService returns the cleanup API over db.
func NewAccountCleanupService(db *ent.Client) *AccountCleanupService {
	return &AccountCleanupService{db: db}
}

// CleanupUserData deletes the caller's ride records and profile. Deleting
// nothing is success, so a retried account deletion is harmless.
func (s *AccountCleanupService) CleanupUserData(ctx context.Context, req *metrov1.CleanupUserDataRequest) (*metrov1.CleanupUserDataResponse, error) {
	user, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if other := strings.TrimSpace(req.GetUserId()); other != "" && other != user {
		return nil, status.Error(codes.PermissionDenied, "user mismatch")
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, internalError("cleanup begin", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.RiderLine.Delete().Where(riderline.UserID(user)).Exec(ctx); err != nil {
		return nil, internalError("cleanup lines", err)
	}
	if err = tx.RiderProfile.DeleteOneID(user).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return nil, internalError("cleanup profile", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError("cleanup commit", err)
	}
	return &metrov1.CleanupUserDataResponse{}, nil
}
