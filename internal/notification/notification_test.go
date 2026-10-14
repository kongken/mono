package notification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestMemoryMatchesUseMonthAndDayOfEarlierYears(t *testing.T) {
	entries := []Entry{
		{TripID: "a", CityCode: "1", Date: "2025-10-15"},
		{TripID: "b", CityCode: "2", Date: "2023-10-15"},
		{TripID: "c", CityCode: "3", Date: "2025-10-16"},  // other day
		{TripID: "d", CityCode: "4", Date: "2026-10-15"},  // this year is not a memory
		{TripID: "e", CityCode: "5", Date: "2027-10-15"},  // the future neither
		{TripID: "f", CityCode: "6", Date: ""},            // undated
		{TripID: "g", CityCode: "7", Date: "not-a-date"},  // corrupt
		{TripID: "h", CityCode: "8", Date: "2025-10-15x"}, // corrupt
	}
	got := MemoryMatches(day("2026-10-15"), entries)
	if len(got) != 2 || got[0].TripID != "a" || got[0].YearsAgo != 1 || got[1].TripID != "b" || got[1].YearsAgo != 3 {
		t.Fatalf("matches: %+v", got)
	}
	if MemoryMatches(day("2026-10-14"), entries) != nil {
		t.Fatal("matched on a different day")
	}
}

func TestMemoryMatchesLeapDay(t *testing.T) {
	entries := []Entry{{TripID: "leap", Date: "2024-02-29"}}
	if got := MemoryMatches(day("2028-02-29"), entries); len(got) != 1 || got[0].YearsAgo != 4 {
		t.Fatalf("leap year: %+v", got)
	}
	if got := MemoryMatches(day("2027-02-28"), entries); len(got) != 1 || got[0].YearsAgo != 3 {
		t.Fatalf("non-leap year Feb 28: %+v", got)
	}
	if got := MemoryMatches(day("2028-02-28"), entries); len(got) != 0 {
		t.Fatalf("Feb 28 of a leap year must not fire: %+v", got)
	}
	if got := MemoryMatches(day("2027-03-01"), entries); len(got) != 0 {
		t.Fatalf("March 1: %+v", got)
	}
}

func TestMemoryMessageWordingAndPrivacy(t *testing.T) {
	matches := MemoryMatches(day("2026-10-15"), []Entry{
		{TripID: "1", CityCode: "330100", CityName: "杭州市", Date: "2025-10-15"},
		{TripID: "2", CityCode: "320100", CityName: "南京市", Date: "2025-10-15"},
		{TripID: "3", CityCode: "110101", CityName: "东城区", Date: "2023-10-15"},
	})
	hidden, ok := MemoryMessage(matches, false)
	if !ok || hidden.Title != "山海足迹 · 一年前的今天" {
		t.Fatalf("title: %+v", hidden)
	}
	for _, name := range []string{"杭州", "南京", "东城"} {
		if strings.Contains(hidden.Title+hidden.Body, name) {
			t.Fatalf("place %q shown without permission: %s", name, hidden.Body)
		}
	}
	if !strings.Contains(hidden.Body, "你有 2 段旅行记忆") || !strings.Contains(hidden.Body, "另有 1 段更早的旅行记忆") {
		t.Fatalf("body: %s", hidden.Body)
	}
	shown, _ := MemoryMessage(matches, true)
	if !strings.Contains(shown.Body, "你在南京市、杭州市留下了足迹") || strings.Contains(shown.Body, "东城区") {
		t.Fatalf("body: %s", shown.Body)
	}
	many := MemoryMatches(day("2026-10-15"), []Entry{
		{TripID: "1", CityCode: "1", CityName: "甲", Date: "2025-10-15"}, {TripID: "2", CityCode: "2", CityName: "乙", Date: "2025-10-15"},
		{TripID: "3", CityCode: "3", CityName: "丙", Date: "2025-10-15"}, {TripID: "4", CityCode: "4", CityName: "丁", Date: "2025-10-15"},
	})
	if m, _ := MemoryMessage(many, true); !strings.Contains(m.Body, "等 4 座城市") {
		t.Fatalf("many places: %s", m.Body)
	}
	if m, _ := MemoryMessage(MemoryMatches(day("2026-10-15"), []Entry{{TripID: "x", Date: "2022-10-15"}}), false); !strings.Contains(m.Title, "4 年前的今天") {
		t.Fatalf("years: %+v", m)
	}
	if _, ok := MemoryMessage(nil, true); ok {
		t.Fatal("empty matches produced a message")
	}
}

func TestAnnualMessageAndWindow(t *testing.T) {
	entries := []Entry{
		{TripID: "1", CityCode: "a", CityName: "秘密城市", Date: "2026-03-01"},
		{TripID: "2", CityCode: "a", Date: "2026-04-01"},
		{TripID: "3", CityCode: "b", Date: "2026-05-01"},
		{TripID: "4", CityCode: "c", Date: "2025-05-01"},
		{TripID: "5", CityCode: "d", Date: "2026-13-40"},
	}
	m, ok := AnnualMessage(2026, entries)
	if !ok || !strings.Contains(m.Body, "3 次旅行") || !strings.Contains(m.Body, "2 座城市") || strings.Contains(m.Title+m.Body, "秘密") {
		t.Fatalf("annual: %+v", m)
	}
	if _, ok := AnnualMessage(2024, entries); ok {
		t.Fatal("year without trips produced a message")
	}
	for date, want := range map[string]bool{"2027-01-01": true, "2027-01-07": true, "2027-01-08": false, "2026-12-31": false, "2027-02-01": false} {
		year, ok := AnnualWindow(day(date))
		if ok != want || (ok && year != 2026) {
			t.Errorf("%s: %d %v", date, year, ok)
		}
	}
}

func TestUnsubscribeTokens(t *testing.T) {
	secret := strings.Repeat("k", 32)
	for _, u := range []Unsubscribe{{"user-1", string(KindMemory)}, {"user-1", AllKinds}, {"3f0c-uuid", string(KindAnnualReport)}} {
		got, err := VerifyUnsubscribe(secret, SignUnsubscribe(secret, u))
		if err != nil || got != u {
			t.Fatalf("round trip %+v: %+v %v", u, got, err)
		}
	}
	token := SignUnsubscribe(secret, Unsubscribe{"user-1", string(KindMemory)})
	other := SignUnsubscribe(secret, Unsubscribe{"user-2", string(KindMemory)})
	tampered := strings.Split(other, ".")[0] + "." + strings.Split(token, ".")[1]
	for name, bad := range map[string]string{
		"empty": "", "no dot": "abcdef", "wrong secret": SignUnsubscribe(strings.Repeat("z", 32), Unsubscribe{"user-1", AllKinds}),
		"other user's payload": tampered, "huge": strings.Repeat("a", 600), "bad base64": "!!!.???",
		"unknown kind": SignUnsubscribe(secret, Unsubscribe{"user-1", "weekly"}),
		"no user":      SignUnsubscribe(secret, Unsubscribe{"", AllKinds}),
	} {
		if _, err := VerifyUnsubscribe(secret, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := VerifyUnsubscribe("", token); err == nil {
		t.Error("accepted without a secret")
	}
}

func TestEmailValidationAndMasking(t *testing.T) {
	if got, err := ValidEmail("  Alice.Smith+trip@Example.COM "); err != nil || got != "alice.smith+trip@example.com" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "alice", "alice@", "@example.com", "alice@example", "a@b.c\r\nBcc: x@y.z", "Alice <a@example.com>", "a,b@example.com", "a b@example.com", "\"a\"@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if _, err := ValidEmail(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if MaskEmail("alice@example.com") != "a***@example.com" || MaskEmail("nonsense") != "" {
		t.Fatal("mask")
	}
}

func TestMessageHeadersAreSafeAndEncoded(t *testing.T) {
	from, _ := mail.ParseAddress("山海足迹 <noreply@example.com>")
	raw, err := buildMessage(from, "alice@example.com", Mail{Subject: "山海足迹 · 一年前的今天", Text: "第一行\n第二行", UnsubscribeURL: "https://trip.example.com/api/notifications/unsubscribe?t=abc"}, time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	h := msg.Header
	if h.Get("To") != "alice@example.com" || h.Get("List-Unsubscribe") != "<https://trip.example.com/api/notifications/unsubscribe?t=abc>" || h.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers: %v", h)
	}
	if !strings.HasPrefix(h.Get("Subject"), "=?utf-8?q?") || !strings.Contains(h.Get("Content-Type"), "utf-8") {
		t.Fatalf("subject/content type: %v", h)
	}
	if strings.Count(string(raw), "\nTo:") != 1 {
		t.Fatal("extra recipient header")
	}
	// A Send call refuses subjects that could inject headers.
	s := &SMTP{Host: "127.0.0.1", From: "noreply@example.com", Security: "none"}
	if err := s.Send(context.Background(), Mail{To: "alice@example.com", Subject: "x\r\nBcc: evil@example.com", Text: "t"}); err == nil || !strings.Contains(err.Error(), "invalid header") {
		t.Fatalf("header injection: %v", err)
	}
}

func TestPushEndpointAndKeyValidation(t *testing.T) {
	for _, ok := range []string{"https://fcm.googleapis.com/fcm/send/abc", "https://updates.push.services.mozilla.com:443/wpush/v2/abc"} {
		if err := ValidPushEndpoint(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "http://push.example.com/x", "https://127.0.0.1/x", "https://[::1]/x", "https://localhost/x", "https://push.example.com:8443/x", "https://u@push.example.com/x", "https://push.example.com/x#frag", "ftp://push.example.com/x", "https://" + strings.Repeat("a", 2100)} {
		if ValidPushEndpoint(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if ValidPushKeys("short", "short") == nil {
		t.Error("accepted short keys")
	}
}

func TestPublicIPClassification(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "142.250.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.5": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "::1": false, "fe80::1": false, "fc00::1": false, "::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := PublicIP(netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s: %v", ip, got)
		}
	}
}

func TestGuardedClientRefusesInternalAddressesAndRedirects(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer server.Close()
	client := guardedClient(2 * time.Second)
	if resp, err := client.Get(server.URL); err == nil {
		resp.Body.Close()
		t.Fatal("connected to a loopback address")
	}
	if hit {
		t.Fatal("request reached the loopback server")
	}
	redirect := false
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		redirect = true
	}
	if redirect {
		t.Fatal("redirects are followed")
	}
}
