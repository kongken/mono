package share

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.orx.me/apps/trip-map/internal/tripmap/regions"
)

func index(t *testing.T) *regions.Index {
	t.Helper()
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

type statsFixture struct {
	TotalProvinces int `json:"totalProvinces"`
	TotalCities    int `json:"totalCities"`
	Cases          []struct {
		Name   string   `json:"name"`
		Marks  []string `json:"marks"`
		Cities []struct {
			Code     string `json:"code"`
			Province string `json:"province"`
			Trips    int    `json:"trips"`
		} `json:"cities"`
		Expect struct {
			ExploredProvinces int            `json:"exploredProvinces"`
			Cities            int            `json:"cities"`
			Trips             int            `json:"trips"`
			ByProvince        map[string]int `json:"byProvince"`
		} `json:"expect"`
	} `json:"cases"`
}

// The same fixture drives scripts/share.test.mjs against src/lib/statistics.ts.
func TestStatsMatchTheSharedFrontendFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../scripts/fixtures/share-stats.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx statsFixture
	if err = json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	idx := index(t)
	for _, tc := range fx.Cases {
		var src Source
		src.Marks = tc.Marks
		for _, c := range tc.Cities {
			city := SourceCity{Code: c.Code, Province: c.Province}
			for i := 0; i < c.Trips; i++ {
				city.Trips = append(city.Trips, SourceTrip{ID: "t" + string(rune('a'+i))})
			}
			src.Cities = append(src.Cities, city)
		}
		st, _ := ComputeStats(idx, src)
		if st.TotalProvinces != fx.TotalProvinces || st.TotalCities != fx.TotalCities {
			t.Fatalf("%s: totals %d/%d, want %d/%d", tc.Name, st.TotalProvinces, st.TotalCities, fx.TotalProvinces, fx.TotalCities)
		}
		if st.ExploredProvinces != tc.Expect.ExploredProvinces || st.Cities != tc.Expect.Cities || st.Trips != tc.Expect.Trips {
			t.Fatalf("%s: got %+v, want %+v", tc.Name, st, tc.Expect)
		}
		got := map[string]int{}
		for _, p := range st.ByProvince {
			got[p.Code] = p.Cities
		}
		if len(got) != len(tc.Expect.ByProvince) {
			t.Fatalf("%s: by province %v, want %v", tc.Name, got, tc.Expect.ByProvince)
		}
		for code, n := range tc.Expect.ByProvince {
			if got[code] != n {
				t.Fatalf("%s: province %s has %d cities, want %d", tc.Name, code, got[code], n)
			}
		}
	}
}

func TestLegacyTaiwanAssignmentOfTheTwoIslandsCountsForFujian(t *testing.T) {
	st, provinces := ComputeStats(index(t), Source{Cities: []SourceCity{{Code: "tw-09020", Province: "710000"}}})
	if st.ExploredProvinces != 1 || len(provinces) != 1 || provinces[0] != "350000" {
		t.Fatalf("stats %+v provinces %v", st, provinces)
	}
}

func source() Source {
	return Source{
		Cities: []SourceCity{
			{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d", Trips: []SourceTrip{
				{ID: "trip-b", Date: "2025-05-01", Note: "龙井茶田"}, {ID: "trip-a", Date: "2024-03-02", Note: "苏堤春晓"}, {ID: "trip-c"}}},
			{Code: "350200", Name: "厦门市", Province: "350000", Color: "#e2af5c", Trips: []SourceTrip{{ID: "trip-d", Date: "2023-10-01", Note: "鼓浪屿"}}},
		},
		Marks:  []string{"530000"},
		Photos: map[string]SourcePhoto{"media-1": {Region: "330100", TripID: "trip-a", Width: 400, Height: 300}, "media-2": {Region: "350200", Width: 10, Height: 10}},
	}
}

var now = time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)

func TestDefaultSettingsPublishOnlyProvincesAndAggregates(t *testing.T) {
	p, err := Build(index(t), source(), Settings{}, now, NewHandle)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := p.Content.PublicJSON()
	for _, secret := range []string{"杭州", "厦门", "龙井", "苏堤", "鼓浪屿", "2025-05-01", "2024-03-02", "330100", "350200", "trip-a", "media-1", "#4b937d"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("default share leaks %q: %s", secret, raw)
		}
	}
	if p.Content.Fields != (Fields{}) || len(p.Content.Cities) != 0 || len(p.Photos) != 0 {
		t.Fatalf("fields %+v cities %d photos %d", p.Content.Fields, len(p.Content.Cities), len(p.Photos))
	}
	if strings.Join(p.Content.Provinces, ",") != "330000,350000,530000" || p.Content.Stats.Cities != 2 || p.Content.Stats.Trips != 4 {
		t.Fatalf("content %+v", p.Content)
	}
}

func TestSettingsDependOnCityNames(t *testing.T) {
	for _, s := range []Settings{{Dates: true}, {Notes: true}, {Photos: true}, {PhotoIDs: []string{"a"}, Cities: true}} {
		if _, err := Build(index(t), source(), s, now, NewHandle); err == nil {
			t.Errorf("%+v accepted", s)
		}
	}
	tooMany := Settings{Cities: true, Photos: true}
	for i := 0; i < MaxPhotos+1; i++ {
		tooMany.PhotoIDs = append(tooMany.PhotoIDs, "m"+string(rune('a'+i)))
	}
	if tooMany.Validate() == nil {
		t.Fatal("more than 12 photos accepted")
	}
	if (Settings{Cities: true, Photos: true, PhotoIDs: []string{"a", "a"}}).Validate() == nil {
		t.Fatal("duplicate photo accepted")
	}
}

func TestFullSettingsKeepTripOrderAndAttachPhotos(t *testing.T) {
	set := Settings{Cities: true, Dates: true, Notes: true, Photos: true, PhotoIDs: []string{"media-1", "media-2"}}
	p, err := Build(index(t), source(), set, now, NewHandle)
	if err != nil {
		t.Fatal(err)
	}
	hz := p.Content.Cities[0]
	if hz.Name != "杭州市" || len(hz.Trips) != 3 || hz.Trips[0].Date != "2024-03-02" || hz.Trips[1].Date != "2025-05-01" || hz.Trips[2].Date != "" {
		t.Fatalf("trips %+v", hz.Trips)
	}
	if len(hz.Trips[0].Photos) != 1 || len(p.Content.Cities[1].Photos) != 1 {
		t.Fatalf("photos were not attached to their trip and city: %+v", p.Content.Cities)
	}
	raw, _ := p.Content.PublicJSON()
	for _, hidden := range []string{"media-1", "media-2", "trip-a", "trip-b", "330000\"", "uid"} {
		if hidden != "330000\"" && strings.Contains(string(raw), hidden) {
			t.Errorf("public document contains %q", hidden)
		}
	}
	for handle, id := range p.Photos {
		if !strings.HasPrefix(handle, "p") || len(handle) != 9 || strings.Contains(string(raw), id) {
			t.Fatalf("handle %q -> %q", handle, id)
		}
	}
}

func TestWithoutDatesTheTripOrderCarriesNoChronology(t *testing.T) {
	p, err := Build(index(t), source(), Settings{Cities: true, Notes: true}, now, NewHandle)
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, tr := range p.Content.Cities[0].Trips {
		if tr.Date != "" {
			t.Fatal("date leaked")
		}
		notes = append(notes, tr.Note)
	}
	// By trip ID (a, b, c), not by date (2024, 2025, undated).
	if strings.Join(notes, "|") != "苏堤春晓|龙井茶田|" {
		t.Fatalf("notes %q", notes)
	}
}

func TestPhotoOfAnUnpublishedCityIsRejected(t *testing.T) {
	src := source()
	src.Photos["media-9"] = SourcePhoto{Region: "110101"}
	if _, err := Build(index(t), src, Settings{Cities: true, Photos: true, PhotoIDs: []string{"media-9"}}, now, NewHandle); err != ErrPhotoUnavailable {
		t.Fatalf("err = %v", err)
	}
	if _, err := Build(index(t), src, Settings{Cities: true, Photos: true, PhotoIDs: []string{"gone"}}, now, NewHandle); err != ErrPhotoUnavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestNarrowRemovesWithoutAddingAndAddsIsDetected(t *testing.T) {
	full := Settings{Cities: true, Dates: true, Notes: true, Photos: true, PhotoIDs: []string{"media-1", "media-2"}}
	p, err := Build(index(t), source(), full, now, NewHandle)
	if err != nil {
		t.Fatal(err)
	}
	if full.Adds(Settings{Cities: true, Notes: true}) {
		t.Fatal("removing fields reported as an addition")
	}
	if !(Settings{}).Adds(Settings{Cities: true}) || !(Settings{Cities: true}).Adds(Settings{Cities: true, Notes: true}) ||
		!(Settings{Cities: true, Photos: true, PhotoIDs: []string{"a"}}).Adds(Settings{Cities: true, Photos: true, PhotoIDs: []string{"a", "b"}}) {
		t.Fatal("additions not detected")
	}
	narrow, err := Narrow(p, Settings{Cities: true, Photos: true, PhotoIDs: []string{"media-2"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := narrow.Content.PublicJSON()
	for _, gone := range []string{"龙井", "苏堤", "2025-05-01", "2024-03-02"} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("narrowed content still has %q", gone)
		}
	}
	if len(narrow.Photos) != 1 || len(narrow.Content.Cities[0].Trips[0].Photos) != 0 || len(narrow.Content.Cities[1].Photos) != 1 || !strings.Contains(string(raw), "杭州市") {
		t.Fatalf("narrowed %+v photos %v", narrow.Content.Cities, narrow.Photos)
	}
	none, err := Narrow(p, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = none.Content.PublicJSON()
	if strings.Contains(string(raw), "杭州") || len(none.Photos) != 0 || none.Content.Fields != (Fields{}) {
		t.Fatalf("hiding cities left content: %s", raw)
	}
	if _, err = Narrow(p, Settings{Dates: true}); err == nil {
		t.Fatal("invalid narrowed settings accepted")
	}
	same, _ := Narrow(p, full)
	if !SameContent(p, same) {
		t.Fatal("narrowing to identical settings changed the content")
	}
}

func TestEncodeDecodeAndSizeLimit(t *testing.T) {
	p, _ := Build(index(t), source(), Settings{Cities: true, Notes: true}, now, NewHandle)
	data, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(data)
	if err != nil || !SameContent(p, back) {
		t.Fatalf("round trip: %v", err)
	}
	huge := source()
	// Incompressible notes exceed the 4 MiB stored limit.
	var b bytes.Buffer
	for i := 0; b.Len() < 11<<20; i++ {
		b.WriteString(NewHandle())
		b.WriteString(NewHandle())
	}
	for i := range 40 {
		huge.Cities[0].Trips = append(huge.Cities[0].Trips, SourceTrip{ID: "big" + string(rune('a'+i)), Note: b.String()[i*250000 : (i+1)*250000]})
	}
	hp, err := Build(index(t), huge, Settings{Cities: true, Notes: true}, now, NewHandle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Encode(hp); err != ErrTooLarge {
		t.Fatalf("err = %v", err)
	}
	if _, err = Decode([]byte("not gzip")); err == nil {
		t.Fatal("garbage decoded")
	}
}

func TestTokensAreLongRandomAndValidated(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := NewToken()
		if err != nil || len(tok) != 43 || !ValidToken(tok) || seen[tok] {
			t.Fatalf("token %q: %v", tok, err)
		}
		seen[tok] = true
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 42), strings.Repeat("a", 44), strings.Repeat("a", 42) + "=", strings.Repeat("a", 42) + "/", strings.Repeat("é", 43), "../" + strings.Repeat("a", 40)} {
		if ValidToken(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
