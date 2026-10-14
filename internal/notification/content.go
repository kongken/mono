// Package notification holds the pure parts of F12 reminders: which trips make a
// memory, the wording of each message, unsubscribe links and the outgoing
// channels. Nothing here reads the database, so the rules are testable alone.
package notification

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is one category of notification the user can switch on or off.
type Kind string

const (
	// KindMemory is "N years ago today you were in …".
	KindMemory Kind = "memory"
	// KindAnnualReport tells that last year's travel recap is ready.
	KindAnnualReport Kind = "annual_report"
)

// Kinds lists every kind in display order.
var Kinds = []Kind{KindMemory, KindAnnualReport}

// Channel is where a notification is delivered.
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelPush  Channel = "push"
)

// Channels lists every channel in display order.
var Channels = []Channel{ChannelEmail, ChannelPush}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// Valid reports whether c is a known channel.
func (c Channel) Valid() bool { return slices.Contains(Channels, c) }

// Label is the user-facing name of the kind.
func (k Kind) Label() string {
	if k == KindAnnualReport {
		return "年度旅行回顾"
	}
	return "回忆提醒"
}

// Entry is one dated trip as the reminder rules see it. Notes and photos are
// deliberately absent: they can never end up in a notification.
type Entry struct {
	TripID   string
	CityCode string
	CityName string
	// Date is the travel date, YYYY-MM-DD.
	Date string
}

// Message is channel-independent notification content.
type Message struct {
	Title string
	Body  string
}

// MemoryMatches returns the entries whose travel date falls on today's month and
// day in an earlier year, most recent year first. A trip on 29 February is
// remembered on 28 February in years without one. today must already be in the
// user's time zone.
func MemoryMatches(today time.Time, entries []Entry) []MemoryMatch {
	var out []MemoryMatch
	for _, e := range entries {
		d, err := time.Parse("2006-01-02", e.Date)
		if err != nil || d.Year() >= today.Year() {
			continue
		}
		same := d.Month() == today.Month() && d.Day() == today.Day()
		leapDay := d.Month() == time.February && d.Day() == 29 && today.Month() == time.February && today.Day() == 28 && !isLeap(today.Year())
		if !same && !leapDay {
			continue
		}
		out = append(out, MemoryMatch{Entry: e, YearsAgo: today.Year() - d.Year()})
	}
	slices.SortStableFunc(out, func(a, b MemoryMatch) int {
		if a.YearsAgo != b.YearsAgo {
			return a.YearsAgo - b.YearsAgo
		}
		return strings.Compare(a.TripID+a.CityCode, b.TripID+b.CityCode)
	})
	return out
}

// MemoryMatch is an Entry that happened YearsAgo years before today.
type MemoryMatch struct {
	Entry
	YearsAgo int
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// MemoryMessage words the reminder for matches (as returned by MemoryMatches).
// It reports false when there is nothing to remember. Place names appear only
// when showPlaces is set; notes and photos never do.
func MemoryMessage(matches []MemoryMatch, showPlaces bool) (Message, bool) {
	if len(matches) == 0 {
		return Message{}, false
	}
	years := matches[0].YearsAgo
	var group []MemoryMatch
	for _, m := range matches {
		if m.YearsAgo == years {
			group = append(group, m)
		}
	}
	msg := Message{Title: fmt.Sprintf("山海足迹 · %s", ago(years))}
	if showPlaces {
		var names []string
		for _, m := range group {
			if m.CityName != "" && !slices.Contains(names, m.CityName) {
				names = append(names, m.CityName)
			}
		}
		slices.Sort(names)
		switch {
		case len(names) == 0:
			msg.Body = fmt.Sprintf("%s，你有 %d 段旅行记忆。", ago(years), len(group))
		case len(names) <= 3:
			msg.Body = fmt.Sprintf("%s，你在%s留下了足迹。", ago(years), strings.Join(names, "、"))
		default:
			msg.Body = fmt.Sprintf("%s，你在%s等 %d 座城市留下了足迹。", ago(years), strings.Join(names[:3], "、"), len(names))
		}
	} else {
		msg.Body = fmt.Sprintf("%s，你有 %d 段旅行记忆。", ago(years), len(group))
	}
	if rest := len(matches) - len(group); rest > 0 {
		msg.Body += fmt.Sprintf("另有 %d 段更早的旅行记忆。", rest)
	}
	msg.Body += "打开山海足迹回顾。"
	return msg, true
}

func ago(years int) string {
	if years == 1 {
		return "一年前的今天"
	}
	return fmt.Sprintf("%d 年前的今天", years)
}

// AnnualMessage words the yearly recap notice. It reports false for a year
// without dated trips. Only counts are included, never place names or notes.
func AnnualMessage(year int, entries []Entry) (Message, bool) {
	trips := map[string]bool{}
	cities := map[string]bool{}
	prefix := fmt.Sprintf("%04d-", year)
	for _, e := range entries {
		if !strings.HasPrefix(e.Date, prefix) {
			continue
		}
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			continue
		}
		trips[e.TripID] = true
		cities[e.CityCode] = true
	}
	if len(trips) == 0 {
		return Message{}, false
	}
	return Message{
		Title: fmt.Sprintf("山海足迹 · %d 年旅行回顾", year),
		Body:  fmt.Sprintf("%d 年你记录了 %d 次旅行，走过 %d 座城市。打开山海足迹查看年度报告。", year, len(trips), len(cities)),
	}, true
}

// AnnualWindow reports whether local falls in the first week of January, when
// the previous year's recap is sent, and returns that previous year.
func AnnualWindow(local time.Time) (int, bool) {
	if local.Month() == time.January && local.Day() <= 7 {
		return local.Year() - 1, true
	}
	return 0, false
}
