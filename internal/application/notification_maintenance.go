package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/notificationdelivery"
	"go.orx.me/apps/trip-map/internal/ent/notificationsetting"
	"go.orx.me/apps/trip-map/internal/ent/trip"
	"go.orx.me/apps/trip-map/internal/notification"
	"go.orx.me/apps/trip-map/internal/regions"
	"go.orx.me/apps/trip-map/internal/report"
)

const (
	// notificationBatch bounds how many settings rows one query reads.
	notificationBatch = 500
	// deliveryAttempts is how many times one notification is tried in its period.
	deliveryAttempts = 3
	// deliveryRetention keeps delivery rows long enough to cover the longest
	// period (the annual recap week) with margin.
	deliveryRetention = 30 * 24 * time.Hour
)

// RunNotificationMaintenance sends the reminders that are due at now. It is
// safe to run on several replicas and to repeat: a notification is claimed in
// notification_deliveries before it is sent, so each kind, channel and period
// (local date, or year for the annual recap) is sent at most once. A crash
// between claim and send loses that one notification rather than repeating it.
//
// Settings are re-read for every account when its turn comes, so switching a
// kind or channel off takes effect for the next reminder immediately.
func RunNotificationMaintenance(ctx context.Context, st *Store, idx *regions.Index, n *Notifier) error {
	if st == nil || st.DB == nil || n == nil || (!n.EmailAvailable() && !n.PushAvailable()) {
		return nil
	}
	now := n.now()
	var after string
	for {
		ids, err := st.DB.NotificationSetting.Query().
			Where(notificationsetting.Enabled(true), notificationsetting.UserIDGT(after)).
			Order(ent.Asc(notificationsetting.FieldUserID)).
			Limit(notificationBatch).
			Select(notificationsetting.FieldUserID).Strings(ctx)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := n.remindUser(ctx, st, idx, id, now); err != nil {
				slog.Error("notification for account failed", "error", err)
			}
		}
		if len(ids) < notificationBatch {
			break
		}
	}
	_, err := st.DB.NotificationDelivery.Delete().Where(notificationdelivery.CreatedAtLT(now.Add(-deliveryRetention))).Exec(ctx)
	return err
}

type dueKind struct {
	kind   notification.Kind
	period string
	// build returns the message for this account, or false when there is nothing to say.
	build func() (notification.Message, bool, error)
}

func (n *Notifier) remindUser(ctx context.Context, st *Store, idx *regions.Index, userID string, now time.Time) error {
	u, err := st.DB.User.Get(ctx, userID)
	if ent.IsNotFound(err) || (err == nil && u.Disabled) {
		return nil
	}
	if err != nil {
		return err
	}
	state, err := loadNotificationState(ctx, st.DB, nil, userID)
	if err != nil {
		return err
	}
	loc, _ := report.ResolveTimezone(state.timezone())
	local := now.In(loc)
	if local.Hour() < state.sendHour() {
		return nil
	}
	due := []dueKind{{
		kind:   notification.KindMemory,
		period: local.Format("2006-01-02"),
		build: func() (notification.Message, bool, error) {
			entries, err := datedEntries(ctx, st, idx, u, fmt.Sprintf("-%02d-%02d", int(local.Month()), local.Day()), "")
			if err != nil {
				return notification.Message{}, false, err
			}
			if local.Month() == time.February && local.Day() == 28 {
				leap, err := datedEntries(ctx, st, idx, u, "-02-29", "")
				if err != nil {
					return notification.Message{}, false, err
				}
				entries = append(entries, leap...)
			}
			msg, ok := notification.MemoryMessage(notification.MemoryMatches(local, entries), state.showPlaces())
			return msg, ok, nil
		},
	}}
	if year, ok := notification.AnnualWindow(local); ok {
		due = append(due, dueKind{
			kind:   notification.KindAnnualReport,
			period: strconv.Itoa(year),
			build: func() (notification.Message, bool, error) {
				entries, err := datedEntries(ctx, st, idx, u, "", fmt.Sprintf("%04d-", year))
				if err != nil {
					return notification.Message{}, false, err
				}
				msg, ok := notification.AnnualMessage(year, entries)
				return msg, ok, nil
			},
		})
	}
	var firstErr error
	for _, d := range due {
		if err := n.deliverDue(ctx, st, u, state, d); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// usableChannels lists the channels that are switched on for kind and could
// actually deliver right now.
func (n *Notifier) usableChannels(state *notificationState, kind notification.Kind) []notification.Channel {
	var out []notification.Channel
	for _, c := range notification.Channels {
		if !state.enabled[settingKey{kind, c}] {
			continue
		}
		switch {
		case c == notification.ChannelEmail && n.EmailAvailable() && state.verifiedEmail() != "":
			out = append(out, c)
		case c == notification.ChannelPush && n.PushAvailable() && state.pushes > 0:
			out = append(out, c)
		}
	}
	return out
}

func (n *Notifier) deliverDue(ctx context.Context, st *Store, u *ent.User, state *notificationState, d dueKind) error {
	channels := n.usableChannels(state, d.kind)
	if len(channels) == 0 {
		return nil
	}
	rows, err := st.DB.NotificationDelivery.Query().Where(
		notificationdelivery.UserID(u.ID), notificationdelivery.KindEQ(notificationdelivery.Kind(d.kind)), notificationdelivery.Period(d.period),
	).All(ctx)
	if err != nil {
		return err
	}
	var todo []notification.Channel
	for _, c := range channels {
		i := slices.IndexFunc(rows, func(r *ent.NotificationDelivery) bool { return r.Channel.String() == string(c) })
		// Sent, skipped and pending (claimed by a run that may still be sending)
		// are final; a failed one is retried a limited number of times.
		if i < 0 || (rows[i].Status == notificationdelivery.StatusFailed && rows[i].Attempts < deliveryAttempts) {
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	msg, ok, err := d.build()
	if err != nil {
		return err
	}
	var firstErr error
	for _, c := range todo {
		id, claimed, err := n.claim(ctx, st, u.ID, d, c)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !claimed {
			continue
		}
		if !ok {
			_ = st.DB.NotificationDelivery.UpdateOneID(id).SetStatus(notificationdelivery.StatusSkipped).Exec(ctx)
			continue
		}
		sendErr := n.sendOne(ctx, st, u.ID, state, d.kind, c, msg)
		status := notificationdelivery.StatusSent
		if sendErr != nil {
			status = notificationdelivery.StatusFailed
			slog.Warn("notification delivery failed", "kind", d.kind, "channel", c, "error", sendErr)
		}
		if err := st.DB.NotificationDelivery.UpdateOneID(id).SetStatus(status).Exec(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// claim reserves (account, kind, channel, period). It reports false when another
// run holds it or it is finished; a failed delivery with attempts left is
// reclaimed.
func (n *Notifier) claim(ctx context.Context, st *Store, userID string, d dueKind, c notification.Channel) (string, bool, error) {
	kind, channel := notificationdelivery.Kind(d.kind), notificationdelivery.Channel(c)
	row, err := st.DB.NotificationDelivery.Create().SetUserID(userID).SetKind(kind).SetChannel(channel).SetPeriod(d.period).SetCreatedAt(n.now()).Save(ctx)
	if err == nil {
		return row.ID, true, nil
	}
	if !ent.IsConstraintError(err) {
		return "", false, err
	}
	rows, err := st.DB.NotificationDelivery.Update().Where(
		notificationdelivery.UserID(userID), notificationdelivery.KindEQ(kind), notificationdelivery.ChannelEQ(channel), notificationdelivery.Period(d.period),
		notificationdelivery.StatusEQ(notificationdelivery.StatusFailed), notificationdelivery.AttemptsLT(deliveryAttempts),
	).SetStatus(notificationdelivery.StatusPending).AddAttempts(1).Save(ctx)
	if err != nil || rows == 0 {
		return "", false, err
	}
	existing, err := st.DB.NotificationDelivery.Query().Where(
		notificationdelivery.UserID(userID), notificationdelivery.KindEQ(kind), notificationdelivery.ChannelEQ(channel), notificationdelivery.Period(d.period),
	).Only(ctx)
	if err != nil {
		return "", false, err
	}
	return existing.ID, true, nil
}

func (n *Notifier) sendOne(ctx context.Context, st *Store, userID string, state *notificationState, kind notification.Kind, c notification.Channel, msg notification.Message) error {
	sendCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	switch c {
	case notification.ChannelEmail:
		return n.sendMail(sendCtx, n.reminderMail(state.verifiedEmail(), userID, kind, msg))
	case notification.ChannelPush:
		// pushTo already removes dead subscriptions; success needs one browser.
		if n.pushTo(sendCtx, st, userID, notification.PushPayload{Title: msg.Title, Body: msg.Body, URL: n.appURL(), Tag: string(kind)}) == 0 {
			return errors.New("no browser accepted the push")
		}
	}
	return nil
}

// datedEntries returns the user's dated trips whose travel date ends with suffix
// or starts with prefix (one of them is set), with canonical city names. Schema 1
// accounts keep one date per city; schema 2 accounts keep trips.
func datedEntries(ctx context.Context, st *Store, idx *regions.Index, u *ent.User, suffix, prefix string) ([]notification.Entry, error) {
	type row struct{ id, code, date string }
	var rows []row
	if u.SchemaVersion >= schemaTrips {
		q := st.DB.Trip.Query().Where(trip.UserID(u.ID))
		if suffix != "" {
			q = q.Where(trip.TravelDateHasSuffix(suffix))
		} else {
			q = q.Where(trip.TravelDateHasPrefix(prefix))
		}
		trips, err := q.All(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range trips {
			rows = append(rows, row{t.TripID, t.RegionCode, t.TravelDate})
		}
	} else {
		q := st.DB.CityVisit.Query().Where(cityvisit.UserID(u.ID))
		if suffix != "" {
			q = q.Where(cityvisit.TravelDateHasSuffix(suffix))
		} else {
			q = q.Where(cityvisit.TravelDateHasPrefix(prefix))
		}
		visits, err := q.All(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range visits {
			rows = append(rows, row{legacyTripID(v.RegionCode), v.RegionCode, v.TravelDate})
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r.code)
	}
	visits, err := st.DB.CityVisit.Query().Where(cityvisit.UserID(u.ID), cityvisit.RegionCodeIn(codes...)).All(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, v := range visits {
		name := v.RegionName
		if idx != nil {
			if ref, err := idx.ResolveCity(v.RegionCode, v.RegionName, v.ProvinceCode); err == nil {
				name = ref.Name
			}
		}
		names[v.RegionCode] = name
	}
	out := make([]notification.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, notification.Entry{TripID: r.id, CityCode: r.code, CityName: names[r.code], Date: r.date})
	}
	return out, nil
}
