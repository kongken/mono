package application

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/friendship"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	"github.com/kongken/mono/internal/tripmap/ent/sharedtrip"
	"github.com/kongken/mono/internal/tripmap/ent/trip"
	"github.com/kongken/mono/internal/tripmap/ent/tripmember"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/share"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	collabWriteLimit  = 60
	collabWriteWindow = time.Minute
	collabReadLimit   = 120
	collabReadWindow  = time.Minute
	friendCodeCharset = "23456789abcdefghjkmnpqrstuvwxyz"
	defaultCityColor  = "#4b937d"
)

var errCollabForbidden = connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))

// CollaborationService implements friends, friend footprint comparison and shared trips (F7).
type CollaborationService struct {
	tripmapv1connect.UnimplementedCollaborationServiceHandler
	src     StoreSource
	regions *regions.Index
}

func NewCollaborationService(src StoreSource, idx *regions.Index) *CollaborationService {
	return &CollaborationService{src: src, regions: idx}
}

func (s *CollaborationService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many collaboration requests, slow down"))
	}
	return nil
}

func orderedPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}

// lockUsers locks the requested user rows in ascending ID order so multi-user
// collaboration transactions can never deadlock with each other or with footprint writes.
func lockUsers(ctx context.Context, tx *ent.Tx, ids ...string) (map[string]*ent.User, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	sort.Strings(unique)
	out := make(map[string]*ent.User, len(unique))
	for _, id := range unique {
		u, err := tx.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
			}
			return nil, internalError(err)
		}
		out[id] = u
	}
	return out, nil
}

func randomFriendCode() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("fc_")
	for _, v := range raw {
		b.WriteByte(friendCodeCharset[int(v)%len(friendCodeCharset)])
	}
	return b.String(), nil
}

func ensureFriendCode(ctx context.Context, tx *ent.Tx, u *ent.User) (string, error) {
	if u.FriendCode != nil && *u.FriendCode != "" {
		return *u.FriendCode, nil
	}
	for range 8 {
		code, err := randomFriendCode()
		if err != nil {
			return "", internalError(err)
		}
		exists, err := tx.User.Query().Where(user.FriendCodeEQ(code)).Exist(ctx)
		if err != nil {
			return "", internalError(err)
		}
		if exists {
			continue
		}
		if err = tx.User.UpdateOneID(u.ID).SetFriendCode(code).Exec(ctx); err != nil {
			continue
		}
		u.FriendCode = &code
		return code, nil
	}
	return "", internalError(errors.New("exhausted friend code retries"))
}

func toFriendUser(u *ent.User) *tripmapv1.FriendUser {
	if u == nil {
		return &tripmapv1.FriendUser{}
	}
	username := ""
	if u.Username != nil {
		username = *u.Username
	}
	display := u.DisplayName
	if display == "" {
		display = username
	}
	return &tripmapv1.FriendUser{
		Id:          u.ID,
		Username:    username,
		DisplayName: display,
		AvatarUrl:   u.AvatarURL,
	}
}

func toFriendSettings(u *ent.User) *tripmapv1.FriendShareSettings {
	cities := u.FriendShowCities
	return &tripmapv1.FriendShareSettings{
		ShowCities: cities,
		ShowDates:  cities && u.FriendShowDates,
		ShowNotes:  cities && u.FriendShowNotes,
	}
}

func toFriendshipInfo(row *ent.Friendship, callerID string, other *ent.User) *tripmapv1.FriendshipInfo {
	dir := "accepted"
	if row.Status == friendship.StatusPending {
		if row.RequesterID == callerID {
			dir = "outgoing"
		} else {
			dir = "incoming"
		}
	}
	out := &tripmapv1.FriendshipInfo{
		Id:        row.ID,
		Friend:    toFriendUser(other),
		Status:    string(row.Status),
		Direction: dir,
		CreatedAt: timestamppb.New(row.CreatedAt),
	}
	if row.AcceptedAt != nil {
		out.AcceptedAt = timestamppb.New(*row.AcceptedAt)
	}
	return out
}

func areConfirmedFriends(ctx context.Context, tx *ent.Tx, aID, bID string) (bool, error) {
	low, high := orderedPair(aID, bID)
	ok, err := tx.Friendship.Query().Where(
		friendship.UserLowIDEQ(low),
		friendship.UserHighIDEQ(high),
		friendship.StatusEQ(friendship.StatusAccepted),
	).Exist(ctx)
	if err != nil {
		return false, internalError(err)
	}
	return ok, nil
}

func (s *CollaborationService) buildSharedTripInfo(ctx context.Context, tx *ent.Tx, st *ent.SharedTrip, callerID string) (*tripmapv1.SharedTripInfo, error) {
	members, err := tx.TripMember.Query().
		Where(tripmember.SharedTripIDEQ(st.ID)).
		Order(ent.Asc(tripmember.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	userIDs := make([]string, 0, len(members)+1)
	userIDs = append(userIDs, st.OwnerID)
	for _, m := range members {
		userIDs = append(userIDs, m.UserID)
	}
	users, err := tx.User.Query().Where(user.IDIn(userIDs...)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	byID := make(map[string]*ent.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	cityName, provinceCode, _ := s.regions.City(st.RegionCode)
	info := &tripmapv1.SharedTripInfo{
		Id:           st.ID,
		RegionCode:   st.RegionCode,
		RegionName:   cityName,
		ProvinceCode: provinceCode,
		TripId:       st.TripID,
		Date:         st.TravelDate,
		Note:         st.Note,
		Owner:        toFriendUser(byID[st.OwnerID]),
		UpdatedAt:    timestamppb.New(st.UpdatedAt),
	}
	for _, m := range members {
		u := byID[m.UserID]
		if u == nil {
			continue
		}
		if m.UserID == callerID {
			info.MyRole = string(m.Role)
			info.MyStatus = string(m.Status)
			if m.MemberTripID != "" {
				info.TripId = m.MemberTripID
			}
		}
		sm := &tripmapv1.SharedTripMember{
			User:   toFriendUser(u),
			Role:   string(m.Role),
			Status: string(m.Status),
		}
		if m.JoinedAt != nil {
			sm.JoinedAt = timestamppb.New(*m.JoinedAt)
		}
		info.Members = append(info.Members, sm)
	}
	return info, nil
}

func (s *CollaborationService) GetCollaborationOverview(ctx context.Context, _ *connect.Request[tripmapv1.GetCollaborationOverviewRequest]) (*connect.Response[tripmapv1.GetCollaborationOverviewResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:read:"+p.UserID, collabReadLimit, collabReadWindow); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, p.UserID)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil, errUnauthenticated
		}
		return nil, err
	}
	me := locked[p.UserID]
	code, err := ensureFriendCode(ctx, tx, me)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Friendship.Query().Where(
		friendship.Or(friendship.RequesterIDEQ(p.UserID), friendship.AddresseeIDEQ(p.UserID)),
	).Order(ent.Desc(friendship.FieldUpdatedAt)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	otherIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.RequesterID == p.UserID {
			otherIDs = append(otherIDs, r.AddresseeID)
		} else {
			otherIDs = append(otherIDs, r.RequesterID)
		}
	}
	others, err := tx.User.Query().Where(user.IDIn(otherIDs...)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	otherByID := make(map[string]*ent.User, len(others))
	for _, u := range others {
		otherByID[u.ID] = u
	}
	var friends []*tripmapv1.FriendshipInfo
	for _, r := range rows {
		otherID := r.RequesterID
		if otherID == p.UserID {
			otherID = r.AddresseeID
		}
		other := otherByID[otherID]
		if other == nil {
			continue
		}
		friends = append(friends, toFriendshipInfo(r, p.UserID, other))
	}

	myMemberships, err := tx.TripMember.Query().
		Where(tripmember.UserIDEQ(p.UserID)).
		Order(ent.Desc(tripmember.FieldUpdatedAt)).
		All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	var sharedTrips []*tripmapv1.SharedTripInfo
	for _, m := range myMemberships {
		stRow, err := tx.SharedTrip.Get(ctx, m.SharedTripID)
		if ent.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, internalError(err)
		}
		info, err := s.buildSharedTripInfo(ctx, tx, stRow, p.UserID)
		if err != nil {
			return nil, err
		}
		sharedTrips = append(sharedTrips, info)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.GetCollaborationOverviewResponse{
		MyFriendCode:   code,
		FriendSettings: toFriendSettings(me),
		Friends:        friends,
		SharedTrips:    sharedTrips,
	}), nil
}

func (s *CollaborationService) UpdateFriendSettings(ctx context.Context, req *connect.Request[tripmapv1.UpdateFriendSettingsRequest]) (*connect.Response[tripmapv1.UpdateFriendSettingsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	in := req.Msg.GetSettings()
	if in == nil {
		return nil, invalid("settings are required")
	}
	showCities := in.GetShowCities()
	showDates := showCities && in.GetShowDates()
	showNotes := showCities && in.GetShowNotes()

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, p.UserID)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return nil, errUnauthenticated
		}
		return nil, err
	}
	me := locked[p.UserID]
	me, err = tx.User.UpdateOneID(me.ID).
		SetFriendShowCities(showCities).
		SetFriendShowDates(showDates).
		SetFriendShowNotes(showNotes).
		Save(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.UpdateFriendSettingsResponse{
		Settings: toFriendSettings(me),
	}), nil
}

func (s *CollaborationService) SendFriendRequest(ctx context.Context, req *connect.Request[tripmapv1.SendFriendRequestRequest]) (*connect.Response[tripmapv1.SendFriendRequestResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	target := strings.TrimSpace(req.Msg.GetTarget())
	if target == "" || len(target) > 64 {
		return nil, invalid("username or friend code is required")
	}
	lower := strings.ToLower(target)
	// Invite codes are unique; cached Logto usernames are not. Never pick an
	// arbitrary identity when several subjects share a display login.
	targetUser, err := st.DB.User.Query().Where(user.FriendCodeEQ(lower)).Only(ctx)
	if ent.IsNotFound(err) {
		matches, lookupErr := st.DB.User.Query().Where(user.UsernameEqualFold(target)).Limit(2).All(ctx)
		if lookupErr != nil {
			return nil, internalError(lookupErr)
		}
		if len(matches) > 1 {
			return nil, invalid("ambiguous username; use a friend code")
		}
		if len(matches) == 1 {
			targetUser, err = matches[0], nil
		}
	}
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	if targetUser.ID == p.UserID {
		return nil, invalid("cannot add yourself as a friend")
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, p.UserID, targetUser.ID)
	if err != nil {
		return nil, err
	}
	other := locked[targetUser.ID]
	low, high := orderedPair(p.UserID, other.ID)
	existing, err := tx.Friendship.Query().Where(
		friendship.UserLowIDEQ(low),
		friendship.UserHighIDEQ(high),
	).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		existing, err = tx.Friendship.Create().
			SetRequesterID(p.UserID).
			SetAddresseeID(other.ID).
			SetUserLowID(low).
			SetUserHighID(high).
			SetStatus(friendship.StatusPending).
			Save(ctx)
		if err != nil {
			return nil, internalError(err)
		}
	case err != nil:
		return nil, internalError(err)
	default:
		if existing.Status == friendship.StatusPending && existing.AddresseeID == p.UserID {
			now := time.Now().UTC()
			existing, err = existing.Update().
				SetStatus(friendship.StatusAccepted).
				SetAcceptedAt(now).
				Save(ctx)
			if err != nil {
				return nil, internalError(err)
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.SendFriendRequestResponse{
		Friendship: toFriendshipInfo(existing, p.UserID, other),
	}), nil
}

func (s *CollaborationService) RespondFriendRequest(ctx context.Context, req *connect.Request[tripmapv1.RespondFriendRequestRequest]) (*connect.Response[tripmapv1.RespondFriendRequestResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.Msg.GetFriendshipId())
	if id == "" {
		return nil, invalid("friendship_id is required")
	}
	row, err := st.DB.Friendship.Get(ctx, id)
	if ent.IsNotFound(err) || (row.RequesterID != p.UserID && row.AddresseeID != p.UserID) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("friend request not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, row.RequesterID, row.AddresseeID)
	if err != nil {
		return nil, err
	}
	row, err = tx.Friendship.Get(ctx, id)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("friend request not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	otherID := row.RequesterID
	if otherID == p.UserID {
		otherID = row.AddresseeID
	}
	other := locked[otherID]

	if req.Msg.GetAccept() {
		if row.AddresseeID != p.UserID {
			return nil, errCollabForbidden
		}
		if row.Status != friendship.StatusAccepted {
			now := time.Now().UTC()
			row, err = row.Update().
				SetStatus(friendship.StatusAccepted).
				SetAcceptedAt(now).
				Save(ctx)
			if err != nil {
				return nil, internalError(err)
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return connect.NewResponse(&tripmapv1.RespondFriendRequestResponse{
			Friendship: toFriendshipInfo(row, p.UserID, other),
		}), nil
	}

	if row.Status == friendship.StatusAccepted {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("friendship is already accepted; use RemoveFriend"))
	}
	if err = tx.Friendship.DeleteOneID(row.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RespondFriendRequestResponse{}), nil
}

// detachCrossSharedTrips removes any shared trip memberships between aID and bID
// without deleting either user's already-synced personal footprints.
func detachCrossSharedTrips(ctx context.Context, tx *ent.Tx, aID, bID string) error {
	aOwned, err := tx.SharedTrip.Query().Where(sharedtrip.OwnerIDEQ(aID)).Select(sharedtrip.FieldID).Strings(ctx)
	if err != nil {
		return err
	}
	if len(aOwned) > 0 {
		if _, err = tx.TripMember.Delete().Where(tripmember.SharedTripIDIn(aOwned...), tripmember.UserIDEQ(bID)).Exec(ctx); err != nil {
			return err
		}
	}
	bOwned, err := tx.SharedTrip.Query().Where(sharedtrip.OwnerIDEQ(bID)).Select(sharedtrip.FieldID).Strings(ctx)
	if err != nil {
		return err
	}
	if len(bOwned) > 0 {
		if _, err = tx.TripMember.Delete().Where(tripmember.SharedTripIDIn(bOwned...), tripmember.UserIDEQ(aID)).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *CollaborationService) RemoveFriend(ctx context.Context, req *connect.Request[tripmapv1.RemoveFriendRequest]) (*connect.Response[tripmapv1.RemoveFriendResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(req.Msg.GetFriendshipId())
	if id == "" {
		return nil, invalid("friendship_id is required")
	}
	row, err := st.DB.Friendship.Get(ctx, id)
	if ent.IsNotFound(err) || (row.RequesterID != p.UserID && row.AddresseeID != p.UserID) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("friendship not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockUsers(ctx, tx, row.RequesterID, row.AddresseeID); err != nil {
		return nil, err
	}
	if err = detachCrossSharedTrips(ctx, tx, row.RequesterID, row.AddresseeID); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Friendship.DeleteOneID(row.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RemoveFriendResponse{}), nil
}

func exploredProvinceSet(src share.Source) map[string]bool {
	set := make(map[string]bool, len(src.Cities)+len(src.Marks))
	for _, c := range src.Cities {
		prov := regions.CanonicalProvince(c.Code, c.Province)
		if prov != "" {
			set[prov] = true
		}
	}
	for _, m := range src.Marks {
		if m != "" {
			set[m] = true
		}
	}
	return set
}

func (s *CollaborationService) GetFriendFootprints(ctx context.Context, req *connect.Request[tripmapv1.GetFriendFootprintsRequest]) (*connect.Response[tripmapv1.GetFriendFootprintsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:read:"+p.UserID, collabReadLimit, collabReadWindow); err != nil {
		return nil, err
	}
	friendID := strings.TrimSpace(req.Msg.GetFriendUserId())
	if friendID == "" || friendID == p.UserID {
		return nil, invalid("friend_user_id is required")
	}

	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := areConfirmedFriends(ctx, tx, p.UserID, friendID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errCollabForbidden
	}
	me, err := tx.User.Get(ctx, p.UserID)
	if err != nil {
		return nil, errUnauthenticated
	}
	friend, err := tx.User.Get(ctx, friendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("friend not found"))
	}

	mySrc, err := loadShareSource(ctx, tx, me, nil)
	if err != nil {
		return nil, internalError(err)
	}
	friendSrc, err := loadShareSource(ctx, tx, friend, nil)
	if err != nil {
		return nil, internalError(err)
	}

	fSettings := share.Settings{
		Cities: friend.FriendShowCities,
		Dates:  friend.FriendShowCities && friend.FriendShowDates,
		Notes:  friend.FriendShowCities && friend.FriendShowNotes,
		Photos: false,
	}
	fu := toFriendUser(friend)
	payload, err := share.Build(s.regions, friendSrc, fSettings, time.Now().UTC(), share.NewHandle)
	if err != nil {
		return nil, internalError(err)
	}
	contentBytes, err := payload.Content.PublicJSON()
	if err != nil {
		return nil, internalError(err)
	}

	myProvSet := exploredProvinceSet(mySrc)
	friendProvSet := exploredProvinceSet(friendSrc)
	allProvs := make([]string, 0, len(myProvSet)+len(friendProvSet))
	seenProv := make(map[string]bool, len(myProvSet)+len(friendProvSet))
	for code := range myProvSet {
		seenProv[code] = true
		allProvs = append(allProvs, code)
	}
	for code := range friendProvSet {
		if !seenProv[code] {
			seenProv[code] = true
			allProvs = append(allProvs, code)
		}
	}
	sort.Strings(allProvs)
	cmp := &tripmapv1.FriendComparison{}
	for _, code := range allProvs {
		inMe, inFriend := myProvSet[code], friendProvSet[code]
		switch {
		case inMe && inFriend:
			cmp.CommonProvinces = append(cmp.CommonProvinces, code)
		case inMe:
			cmp.OnlyMeProvinces = append(cmp.OnlyMeProvinces, code)
		case inFriend:
			cmp.OnlyFriendProvinces = append(cmp.OnlyFriendProvinces, code)
		}
	}

	if me.FriendShowCities && friend.FriendShowCities {
		cmp.CityComparisonAvailable = true
		myCityByCode := make(map[string]share.SourceCity, len(mySrc.Cities))
		for _, v := range mySrc.Cities {
			myCityByCode[v.Code] = v
		}
		friendCityByCode := make(map[string]share.SourceCity, len(friendSrc.Cities))
		for _, v := range friendSrc.Cities {
			friendCityByCode[v.Code] = v
		}
		for _, fv := range friendSrc.Cities {
			if mv, both := myCityByCode[fv.Code]; both {
				cmp.CommonCities = append(cmp.CommonCities, &tripmapv1.FriendSharedCity{
					Code:        fv.Code,
					Name:        fv.Name,
					Province:    regions.CanonicalProvince(fv.Code, fv.Province),
					MyColor:     mv.Color,
					FriendColor: fv.Color,
				})
			} else {
				cmp.FriendOnlyCityCount++
			}
		}
		for _, mv := range mySrc.Cities {
			if _, both := friendCityByCode[mv.Code]; !both {
				cmp.MyOnlyCityCount++
			}
		}
	}

	return connect.NewResponse(&tripmapv1.GetFriendFootprintsResponse{
		Friend:         fu,
		FriendSettings: toFriendSettings(friend),
		ContentJson:    string(contentBytes),
		Comparison:     cmp,
	}), nil
}

func parseMemberRole(role string) (tripmember.Role, error) {
	switch role {
	case "", "editor":
		return tripmember.RoleEditor, nil
	case "viewer":
		return tripmember.RoleViewer, nil
	default:
		return "", invalid(`role must be "editor" or "viewer"`)
	}
}

func syncMemberTripUpsert(ctx context.Context, tx *ent.Tx, idx *regions.Index, member *ent.User, ownerID, regionCode, memberTripID, date, note string) error {
	if member.SchemaVersion < schemaTrips {
		if err := upgradeAccount(ctx, tx, member); err != nil {
			return err
		}
	}
	cityName, provinceCode, ok := idx.City(regionCode)
	if !ok {
		return errors.New("unknown region code")
	}
	rev := member.Revision
	cityRow, err := tx.CityVisit.Query().Where(cityvisit.UserID(member.ID), cityvisit.RegionCode(regionCode)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		color := defaultCityColor
		if ownerCity, oerr := tx.CityVisit.Query().Where(cityvisit.UserID(ownerID), cityvisit.RegionCode(regionCode)).Only(ctx); oerr == nil && footprintColors[ownerCity.Color] {
			color = ownerCity.Color
		}
		cityRow, err = tx.CityVisit.Create().
			SetUserID(member.ID).
			SetRegionCode(regionCode).
			SetRegionName(cityName).
			SetProvinceCode(provinceCode).
			SetColor(color).
			Save(ctx)
		if err != nil {
			return err
		}
		rev++
		cityMut := &tripmapv1.FootprintMutation{
			Operation: &tripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &tripmapv1.Visit{Code: regionCode, Name: cityName, Province: provinceCode, Color: color},
			},
		}
		if err = recordChange(ctx, tx, member.ID, rev, cityMut, schemaTrips); err != nil {
			return err
		}
	case err != nil:
		return err
	}

	changed, err := upsertTripRow(ctx, tx, member.ID, cityRow, memberTripID, date, note)
	if err != nil {
		return err
	}
	if changed {
		rev++
		tripMut := &tripmapv1.FootprintMutation{
			Operation: &tripmapv1.FootprintMutation_UpsertTrip{
				UpsertTrip: &tripmapv1.Trip{Id: memberTripID, RegionCode: regionCode, Date: date, Note: note},
			},
		}
		if err = recordChange(ctx, tx, member.ID, rev, tripMut, schemaTrips); err != nil {
			return err
		}
	}
	if rev != member.Revision {
		if err = tx.User.UpdateOneID(member.ID).SetRevision(rev).Exec(ctx); err != nil {
			return err
		}
		member.Revision = rev
	}
	return nil
}

func syncMemberTripDelete(ctx context.Context, tx *ent.Tx, member *ent.User, regionCode, memberTripID string) error {
	if memberTripID == "" {
		return nil
	}
	n, err := tx.Trip.Delete().Where(
		trip.UserID(member.ID),
		trip.TripID(memberTripID),
		trip.RegionCode(regionCode),
	).Exec(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		rev := member.Revision + 1
		delMut := &tripmapv1.FootprintMutation{
			Operation: &tripmapv1.FootprintMutation_DeleteTrip{
				DeleteTrip: &tripmapv1.DeleteTrip{RegionCode: regionCode, Id: memberTripID},
			},
		}
		if err = recordChange(ctx, tx, member.ID, rev, delMut, schemaTrips); err != nil {
			return err
		}
		if err = tx.User.UpdateOneID(member.ID).SetRevision(rev).Exec(ctx); err != nil {
			return err
		}
		member.Revision = rev
	}
	return nil
}

func (s *CollaborationService) InviteToSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.InviteToSharedTripRequest]) (*connect.Response[tripmapv1.InviteToSharedTripResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	r := req.Msg
	if _, _, ok := s.regions.City(r.GetRegionCode()); !ok {
		return nil, invalid("unknown region code")
	}
	if !tripIDPattern.MatchString(r.GetTripId()) {
		return nil, invalid("invalid trip_id")
	}
	friendID := strings.TrimSpace(r.GetFriendUserId())
	if friendID == "" || friendID == p.UserID {
		return nil, invalid("friend_user_id is required")
	}
	role, err := parseMemberRole(r.GetRole())
	if err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, p.UserID, friendID)
	if err != nil {
		return nil, err
	}
	ok, err := areConfirmedFriends(ctx, tx, p.UserID, friendID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errCollabForbidden
	}
	me := locked[p.UserID]
	if me.SchemaVersion < schemaTrips {
		if err = upgradeAccount(ctx, tx, me); err != nil {
			return nil, internalError(err)
		}
	}
	ownerTrip, err := tx.Trip.Query().Where(
		trip.UserID(p.UserID),
		trip.TripID(r.GetTripId()),
		trip.RegionCode(r.GetRegionCode()),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("trip not found in this city; sync footprints first"))
	}
	if err != nil {
		return nil, internalError(err)
	}

	// Ensure caller is not trying to re-share a trip where caller is only a guest member.
	guestCount, err := tx.TripMember.Query().Where(
		tripmember.UserIDEQ(p.UserID),
		tripmember.MemberTripIDEQ(r.GetTripId()),
		tripmember.RoleNEQ(tripmember.RoleOwner),
	).Count(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if guestCount > 0 {
		return nil, errCollabForbidden
	}

	sharedRow, err := tx.SharedTrip.Query().Where(
		sharedtrip.OwnerIDEQ(p.UserID),
		sharedtrip.TripIDEQ(r.GetTripId()),
	).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		now := time.Now().UTC()
		sharedRow, err = tx.SharedTrip.Create().
			SetOwnerID(p.UserID).
			SetRegionCode(r.GetRegionCode()).
			SetTripID(r.GetTripId()).
			SetTravelDate(ownerTrip.TravelDate).
			SetNote(ownerTrip.Note).
			Save(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		if err = tx.TripMember.Create().
			SetSharedTripID(sharedRow.ID).
			SetUserID(p.UserID).
			SetMemberTripID(r.GetTripId()).
			SetRole(tripmember.RoleOwner).
			SetStatus(tripmember.StatusActive).
			SetInvitedBy(p.UserID).
			SetJoinedAt(now).
			Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	case err != nil:
		return nil, internalError(err)
	}

	memberRow, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(sharedRow.ID),
		tripmember.UserIDEQ(friendID),
	).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		if err = tx.TripMember.Create().
			SetSharedTripID(sharedRow.ID).
			SetUserID(friendID).
			SetRole(role).
			SetStatus(tripmember.StatusInvited).
			SetInvitedBy(p.UserID).
			Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	case err != nil:
		return nil, internalError(err)
	default:
		if err = memberRow.Update().SetRole(role).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}

	info, err := s.buildSharedTripInfo(ctx, tx, sharedRow, p.UserID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.InviteToSharedTripResponse{SharedTrip: info}), nil
}

func resolveMemberTripID(ctx context.Context, tx *ent.Tx, userID, regionCode, preferredTripID, sharedTripID string) (string, error) {
	existing, err := tx.Trip.Query().Where(
		trip.UserID(userID),
		trip.TripID(preferredTripID),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return preferredTripID, nil
	}
	if err != nil {
		return "", err
	}
	if existing.RegionCode == regionCode {
		return preferredTripID, nil
	}
	suffix := sharedTripID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	prefix := preferredTripID
	if len(prefix) > 54 {
		prefix = prefix[:54]
	}
	return prefix + "-" + suffix, nil
}

func (s *CollaborationService) RespondSharedTripInvite(ctx context.Context, req *connect.Request[tripmapv1.RespondSharedTripInviteRequest]) (*connect.Response[tripmapv1.RespondSharedTripInviteResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	if stID == "" {
		return nil, invalid("shared_trip_id is required")
	}
	sharedRow, err := st.DB.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockUsers(ctx, tx, p.UserID, sharedRow.OwnerID)
	if err != nil {
		return nil, err
	}
	me := locked[p.UserID]
	sharedRow, err = tx.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	tm, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.UserIDEQ(p.UserID),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errCollabForbidden
	}
	if err != nil {
		return nil, internalError(err)
	}

	if !req.Msg.GetAccept() {
		if tm.Status != tripmember.StatusInvited {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("invitation is already active; use LeaveSharedTrip"))
		}
		if err = tx.TripMember.DeleteOneID(tm.ID).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
		if err = tx.Commit(); err != nil {
			return nil, internalError(err)
		}
		return connect.NewResponse(&tripmapv1.RespondSharedTripInviteResponse{
			FootprintRevision: me.Revision,
		}), nil
	}

	friendsOK, err := areConfirmedFriends(ctx, tx, p.UserID, sharedRow.OwnerID)
	if err != nil {
		return nil, err
	}
	if !friendsOK {
		_ = tx.TripMember.DeleteOneID(tm.ID).Exec(ctx)
		_ = tx.Commit()
		return nil, errCollabForbidden
	}
	if tm.Status != tripmember.StatusActive {
		memberTripID, err := resolveMemberTripID(ctx, tx, p.UserID, sharedRow.RegionCode, sharedRow.TripID, sharedRow.ID)
		if err != nil {
			return nil, internalError(err)
		}
		if err = syncMemberTripUpsert(ctx, tx, s.regions, me, sharedRow.OwnerID, sharedRow.RegionCode, memberTripID, sharedRow.TravelDate, sharedRow.Note); err != nil {
			return nil, internalError(err)
		}
		now := time.Now().UTC()
		if err = tm.Update().
			SetStatus(tripmember.StatusActive).
			SetMemberTripID(memberTripID).
			SetJoinedAt(now).
			Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	info, err := s.buildSharedTripInfo(ctx, tx, sharedRow, p.UserID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RespondSharedTripInviteResponse{
		SharedTrip:        info,
		FootprintRevision: me.Revision,
	}), nil
}

func lockSharedTripMembers(ctx context.Context, tx *ent.Tx, stID string, extraIDs ...string) (*ent.SharedTrip, []*ent.TripMember, map[string]*ent.User, error) {
	sharedRow, err := tx.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, nil, nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, nil, nil, internalError(err)
	}
	members, err := tx.TripMember.Query().Where(tripmember.SharedTripIDEQ(stID)).All(ctx)
	if err != nil {
		return nil, nil, nil, internalError(err)
	}
	ids := append([]string{sharedRow.OwnerID}, extraIDs...)
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	locked, err := lockUsers(ctx, tx, ids...)
	if err != nil {
		return nil, nil, nil, err
	}
	sharedRow, err = tx.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, nil, nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, nil, nil, internalError(err)
	}
	members, err = tx.TripMember.Query().Where(tripmember.SharedTripIDEQ(stID)).All(ctx)
	if err != nil {
		return nil, nil, nil, internalError(err)
	}
	return sharedRow, members, locked, nil
}

func (s *CollaborationService) UpdateSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.UpdateSharedTripRequest]) (*connect.Response[tripmapv1.UpdateSharedTripResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	r := req.Msg
	stID := strings.TrimSpace(r.GetSharedTripId())
	if stID == "" {
		return nil, invalid("shared_trip_id is required")
	}
	if err = validateDateNote(r.GetDate(), r.GetNote()); err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	sharedRow, members, locked, err := lockSharedTripMembers(ctx, tx, stID, p.UserID)
	if err != nil {
		return nil, err
	}
	var callerMember *ent.TripMember
	for _, m := range members {
		if m.UserID == p.UserID && m.Status == tripmember.StatusActive {
			callerMember = m
			break
		}
	}
	if callerMember == nil || callerMember.Role == tripmember.RoleViewer {
		return nil, errCollabForbidden
	}

	sharedRow, err = sharedRow.Update().
		SetTravelDate(r.GetDate()).
		SetNote(r.GetNote()).
		Save(ctx)
	if err != nil {
		return nil, wrapWrite(err)
	}
	for _, m := range members {
		if m.Status != tripmember.StatusActive {
			continue
		}
		mu := locked[m.UserID]
		if mu == nil {
			continue
		}
		tripID := m.MemberTripID
		if tripID == "" {
			tripID = sharedRow.TripID
		}
		if err = syncMemberTripUpsert(ctx, tx, s.regions, mu, sharedRow.OwnerID, sharedRow.RegionCode, tripID, r.GetDate(), r.GetNote()); err != nil {
			return nil, internalError(err)
		}
	}
	info, err := s.buildSharedTripInfo(ctx, tx, sharedRow, p.UserID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.UpdateSharedTripResponse{
		SharedTrip:        info,
		FootprintRevision: locked[p.UserID].Revision,
	}), nil
}

func (s *CollaborationService) SetSharedTripMemberRole(ctx context.Context, req *connect.Request[tripmapv1.SetSharedTripMemberRoleRequest]) (*connect.Response[tripmapv1.SetSharedTripMemberRoleResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	r := req.Msg
	stID := strings.TrimSpace(r.GetSharedTripId())
	memberID := strings.TrimSpace(r.GetMemberUserId())
	if stID == "" || memberID == "" || memberID == p.UserID {
		return nil, invalid("shared_trip_id and member_user_id are required")
	}
	role, err := parseMemberRole(r.GetRole())
	if err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	sharedRow, _, _, err := lockSharedTripMembers(ctx, tx, stID, p.UserID, memberID)
	if err != nil {
		return nil, err
	}
	if sharedRow.OwnerID != p.UserID {
		return nil, errCollabForbidden
	}
	tm, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.UserIDEQ(memberID),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("member not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	if err = tm.Update().SetRole(role).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	info, err := s.buildSharedTripInfo(ctx, tx, sharedRow, p.UserID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.SetSharedTripMemberRoleResponse{SharedTrip: info}), nil
}

func (s *CollaborationService) RemoveSharedTripMember(ctx context.Context, req *connect.Request[tripmapv1.RemoveSharedTripMemberRequest]) (*connect.Response[tripmapv1.RemoveSharedTripMemberResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	memberID := strings.TrimSpace(req.Msg.GetMemberUserId())
	if stID == "" || memberID == "" || memberID == p.UserID {
		return nil, invalid("shared_trip_id and member_user_id are required")
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	sharedRow, _, locked, err := lockSharedTripMembers(ctx, tx, stID, p.UserID, memberID)
	if err != nil {
		return nil, err
	}
	if sharedRow.OwnerID != p.UserID {
		return nil, errCollabForbidden
	}
	tm, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.UserIDEQ(memberID),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("member not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	if tm.Status == tripmember.StatusActive {
		if err = syncMemberTripDelete(ctx, tx, locked[memberID], sharedRow.RegionCode, tm.MemberTripID); err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.TripMember.DeleteOneID(tm.ID).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	info, err := s.buildSharedTripInfo(ctx, tx, sharedRow, p.UserID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RemoveSharedTripMemberResponse{SharedTrip: info}), nil
}

func (s *CollaborationService) LeaveSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.LeaveSharedTripRequest]) (*connect.Response[tripmapv1.LeaveSharedTripResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	if stID == "" {
		return nil, invalid("shared_trip_id is required")
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	sharedRow, _, locked, err := lockSharedTripMembers(ctx, tx, stID, p.UserID)
	if err != nil {
		return nil, err
	}
	if sharedRow.OwnerID == p.UserID {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("trip owner cannot leave; dissolve the shared trip instead"))
	}
	tm, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.UserIDEQ(p.UserID),
	).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errCollabForbidden
	}
	if err != nil {
		return nil, internalError(err)
	}
	me := locked[p.UserID]
	if tm.Status == tripmember.StatusActive {
		if err = syncMemberTripDelete(ctx, tx, me, sharedRow.RegionCode, tm.MemberTripID); err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.TripMember.DeleteOneID(tm.ID).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.LeaveSharedTripResponse{FootprintRevision: me.Revision}), nil
}

func (s *CollaborationService) DeleteSharedTrip(ctx context.Context, req *connect.Request[tripmapv1.DeleteSharedTripRequest]) (*connect.Response[tripmapv1.DeleteSharedTripResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:write:"+p.UserID, collabWriteLimit, collabWriteWindow); err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	if stID == "" {
		return nil, invalid("shared_trip_id is required")
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	sharedRow, _, _, err := lockSharedTripMembers(ctx, tx, stID, p.UserID)
	if err != nil {
		return nil, err
	}
	if sharedRow.OwnerID != p.UserID {
		return nil, errCollabForbidden
	}
	if err = tx.SharedTrip.DeleteOneID(sharedRow.ID).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.DeleteSharedTripResponse{}), nil
}

func (s *CollaborationService) ListSharedTripMedia(ctx context.Context, req *connect.Request[tripmapv1.ListSharedTripMediaRequest]) (*connect.Response[tripmapv1.ListSharedTripMediaResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "collab:read:"+p.UserID, collabReadLimit, collabReadWindow); err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	if stID == "" {
		return nil, invalid("shared_trip_id is required")
	}
	sharedRow, err := st.DB.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	members, err := st.DB.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.StatusEQ(tripmember.StatusActive),
	).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	callerActive := false
	userIDs := make([]string, 0, len(members))
	for _, m := range members {
		userIDs = append(userIDs, m.UserID)
		if m.UserID == p.UserID {
			callerActive = true
		}
	}
	if !callerActive {
		return nil, errCollabForbidden
	}
	users, err := st.DB.User.Query().Where(user.IDIn(userIDs...)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	userByID := make(map[string]*ent.User, len(users))
	for _, u := range users {
		userByID[u.ID] = u
	}

	var items []*tripmapv1.SharedTripMediaInfo
	for _, m := range members {
		u := userByID[m.UserID]
		if u == nil {
			continue
		}
		tripID := m.MemberTripID
		if tripID == "" {
			tripID = sharedRow.TripID
		}
		rows, err := st.DB.MediaObject.Query().Where(
			mediaobject.UserIDEQ(m.UserID),
			mediaobject.RegionCodeEQ(sharedRow.RegionCode),
			mediaobject.TripIDEQ(tripID),
			mediaobject.StatusEQ(mediaobject.StatusReady),
		).Order(ent.Desc(mediaobject.FieldCreatedAt)).All(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		for _, mediaRow := range rows {
			items = append(items, &tripmapv1.SharedTripMediaInfo{
				Media:    mediaInfo(mediaRow),
				Uploader: toFriendUser(u),
				Mine:     m.UserID == p.UserID,
			})
		}
	}
	slices.SortFunc(items, func(a, b *tripmapv1.SharedTripMediaInfo) int {
		return b.Media.CreatedAt.AsTime().Compare(a.Media.CreatedAt.AsTime())
	})
	return connect.NewResponse(&tripmapv1.ListSharedTripMediaResponse{Media: items}), nil
}

func (s *CollaborationService) ReadSharedTripMedia(ctx context.Context, req *connect.Request[tripmapv1.ReadSharedTripMediaRequest]) (*connect.Response[tripmapv1.ReadSharedTripMediaResponse], error) {
	st, p, err := mediaStore(ctx, s.src)
	if err != nil {
		return nil, err
	}
	stID := strings.TrimSpace(req.Msg.GetSharedTripId())
	mediaID := strings.TrimSpace(req.Msg.GetMediaId())
	if stID == "" || mediaID == "" {
		return nil, invalid("shared_trip_id and media_id are required")
	}
	sharedRow, err := st.DB.SharedTrip.Get(ctx, stID)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("shared trip not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	members, err := st.DB.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(stID),
		tripmember.StatusEQ(tripmember.StatusActive),
	).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	callerActive := false
	tripByUser := make(map[string]string, len(members))
	for _, m := range members {
		if m.UserID == p.UserID {
			callerActive = true
		}
		tid := m.MemberTripID
		if tid == "" {
			tid = sharedRow.TripID
		}
		tripByUser[m.UserID] = tid
	}
	if !callerActive {
		return nil, errCollabForbidden
	}

	mRow, err := st.DB.MediaObject.Get(ctx, mediaID)
	if ent.IsNotFound(err) || mRow.Status != mediaobject.StatusReady || mRow.RegionCode != sharedRow.RegionCode {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("media not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	expectedTripID, allowedUploader := tripByUser[mRow.UserID]
	if !allowedUploader || mRow.TripID != expectedTripID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("media not found"))
	}
	key, mime := mRow.ObjectKey, mRow.ContentType
	if req.Msg.GetThumbnail() {
		if mRow.Width == 0 {
			return nil, invalid("attachment has no thumbnail")
		}
		key, mime = mRow.ThumbnailKey, "image/jpeg"
	}
	if url := st.Storage.MediaURL(key); url != "" {
		res := connect.NewResponse(&tripmapv1.ReadSharedTripMediaResponse{Url: url, ContentType: mime, Filename: mRow.Filename})
		res.Header().Set("Cache-Control", "no-store")
		return res, nil
	}

	r, err := st.Blobs.Get(ctx, key)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("media unavailable"))
	}
	defer r.Close()
	data, err := boundedMedia(r)
	if err != nil {
		return nil, internalError(err)
	}
	res := connect.NewResponse(&tripmapv1.ReadSharedTripMediaResponse{
		Data:        data,
		ContentType: mime,
		Filename:    mRow.Filename,
	})
	res.Header().Set("Cache-Control", "no-store")
	return res, nil
}

// sharedTripPeerUserIDs returns uid plus any other active members of shared trips
// that uid belongs to, so SyncChanges can lock all affected user rows in ascending order.
func sharedTripPeerUserIDs(ctx context.Context, db *ent.Client, uid string) ([]string, error) {
	myMemberships, err := db.TripMember.Query().Where(
		tripmember.UserIDEQ(uid),
		tripmember.StatusEQ(tripmember.StatusActive),
	).Select(tripmember.FieldSharedTripID).Strings(ctx)
	if err != nil {
		return nil, err
	}
	if len(myMemberships) == 0 {
		return []string{uid}, nil
	}
	peerIDs, err := db.TripMember.Query().Where(
		tripmember.SharedTripIDIn(myMemberships...),
		tripmember.StatusEQ(tripmember.StatusActive),
	).Select(tripmember.FieldUserID).Strings(ctx)
	if err != nil {
		return nil, err
	}
	return append(peerIDs, uid), nil
}

func checkSharedTripWriteAllowed(ctx context.Context, tx *ent.Tx, uid, tripID string) (*ent.TripMember, error) {
	tm, err := tx.TripMember.Query().Where(
		tripmember.UserIDEQ(uid),
		tripmember.MemberTripIDEQ(tripID),
		tripmember.StatusEQ(tripmember.StatusActive),
	).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, internalError(err)
	}
	if tm.Role == tripmember.RoleViewer {
		return nil, errCollabForbidden
	}
	return tm, nil
}

func propagateSharedTripUpsert(ctx context.Context, tx *ent.Tx, idx *regions.Index, tm *ent.TripMember, callerID, date, note string) error {
	if tm == nil {
		return nil
	}
	sharedRow, err := tx.SharedTrip.Get(ctx, tm.SharedTripID)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return internalError(err)
	}
	if err = sharedRow.Update().SetTravelDate(date).SetNote(note).Exec(ctx); err != nil {
		return wrapWrite(err)
	}
	members, err := tx.TripMember.Query().Where(
		tripmember.SharedTripIDEQ(sharedRow.ID),
		tripmember.StatusEQ(tripmember.StatusActive),
	).All(ctx)
	if err != nil {
		return internalError(err)
	}
	for _, m := range members {
		if m.UserID == callerID {
			continue
		}
		peer, err := tx.User.Get(ctx, m.UserID)
		if err != nil {
			continue
		}
		memberTripID := m.MemberTripID
		if memberTripID == "" {
			memberTripID = sharedRow.TripID
		}
		if err = syncMemberTripUpsert(ctx, tx, idx, peer, sharedRow.OwnerID, sharedRow.RegionCode, memberTripID, date, note); err != nil {
			return internalError(err)
		}
	}
	return nil
}

func handleSharedTripDelete(ctx context.Context, tx *ent.Tx, uid, tripID string) error {
	tm, err := checkSharedTripWriteAllowed(ctx, tx, uid, tripID)
	if err != nil || tm == nil {
		return err
	}
	if tm.Role == tripmember.RoleOwner {
		if err = tx.SharedTrip.DeleteOneID(tm.SharedTripID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return internalError(err)
		}
		return nil
	}
	if err = tx.TripMember.DeleteOneID(tm.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return internalError(err)
	}
	return nil
}

func handleSharedCityDelete(ctx context.Context, tx *ent.Tx, uid, regionCode string) error {
	tripIDs, err := tx.Trip.Query().Where(
		trip.UserID(uid),
		trip.RegionCode(regionCode),
	).Select(trip.FieldTripID).Strings(ctx)
	if err != nil {
		return internalError(err)
	}
	for _, tid := range tripIDs {
		if err = handleSharedTripDelete(ctx, tx, uid, tid); err != nil {
			return err
		}
	}
	return nil
}
