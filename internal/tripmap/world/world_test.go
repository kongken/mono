package world_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/kongken/mono/internal/tripmap/world"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

const (
	fixtureFile  = "../../../packages/fixtures/world-documents.json"
	datesFile    = "../../../packages/fixtures/trip-dates.json"
	frontCatalog = "../../../packages/geo-data/data/world-catalog.json"
)

type fixtureCase struct {
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Scope     string          `json:"scope"`
	Document  json.RawMessage `json:"document"`
	Canonical json.RawMessage `json:"canonical"`
}

type fixtures struct {
	Limits     map[string]int `json:"limits"`
	Whitespace []string       `json:"whitespace"`
	Valid      []fixtureCase  `json:"valid"`
	Invalid    []fixtureCase  `json:"invalid"`
	Coords     struct {
		Valid []struct {
			Value float64 `json:"value"`
			E6    int32   `json:"e6"`
		} `json:"valid"`
		Normalize []struct {
			Input float64 `json:"input"`
			E6    int32   `json:"e6"`
		} `json:"normalize"`
	} `json:"coordinates"`
}

func loadFixtures(t *testing.T) fixtures {
	t.Helper()
	raw, err := os.ReadFile(fixtureFile)
	if err != nil {
		t.Fatal(err)
	}
	var f fixtures
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// toE6 is what the interface writes: round(|x| * 1e6) in float64, sign restored.
func toE6(x float64) int32 {
	e6 := int32(math.Round(math.Abs(x) * 1e6))
	if x < 0 {
		return -e6
	}
	return e6
}

type jsonDoc struct {
	Kind      string `json:"kind"`
	Version   int32  `json:"version"`
	Countries []struct {
		Code  string `json:"code"`
		Color string `json:"color"`
	} `json:"countries"`
	Places []struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Country string  `json:"country"`
		Lat     float64 `json:"lat"`
		Lng     float64 `json:"lng"`
		Trips   []struct {
			ID   string `json:"id"`
			Date string `json:"date"`
			Note string `json:"note"`
		} `json:"trips"`
	} `json:"places"`
}

// fromJSON turns a fixture document (a typed one: scope-less entries only) into the
// cloud message: the same fields, coordinates in whole micro-degrees.
func fromJSON(t *testing.T, raw json.RawMessage) *tripmapv1.WorldDocument {
	t.Helper()
	var j jsonDoc
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatalf("fixture document is not typed: %v: %s", err, raw)
	}
	doc := &tripmapv1.WorldDocument{Kind: j.Kind, Version: j.Version}
	for _, c := range j.Countries {
		doc.Countries = append(doc.Countries, &tripmapv1.WorldCountry{Code: c.Code, Color: c.Color})
	}
	for _, p := range j.Places {
		place := &tripmapv1.WorldPlace{Id: p.ID, Name: p.Name, Country: p.Country, LatE6: toE6(p.Lat), LngE6: toE6(p.Lng)}
		for _, tr := range p.Trips {
			place.Trips = append(place.Trips, &tripmapv1.WorldTrip{Id: tr.ID, Date: tr.Date, Note: tr.Note})
		}
		doc.Places = append(doc.Places, place)
	}
	return doc
}

func catalog(t *testing.T) world.Catalog {
	t.Helper()
	cat, err := world.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func kindOf(err error) world.ErrorKind {
	if we, ok := errors.AsType[*world.Error](err); ok {
		return we.Kind
	}
	return ""
}

// Every fixture entry without a scope must give the same answer here as in
// apps/trip-map/scripts/world.test.mjs. Scope json-text (JSON shapes and types, a China
// backup, float decimals) needs a JSON decoder and the cloud API has none: typed
// messages carry whole micro-degrees. Scope utf16 (lone surrogates) cannot come out of
// a JSON reader or a protobuf string; the same rule is exercised below with the invalid
// UTF-8 a lone surrogate stands for.
func TestSharedFixtures(t *testing.T) {
	f := loadFixtures(t)
	cat := catalog(t)
	if len(f.Valid) != 12 || len(f.Invalid) != 64 {
		t.Fatalf("fixture changed shape (valid %d, invalid %d): extend this test", len(f.Valid), len(f.Invalid))
	}
	ran, skipped := 0, map[string]int{}
	for _, c := range f.Valid {
		if c.Scope != "" {
			skipped[c.Scope]++
			continue
		}
		t.Run("valid/"+c.Name, func(t *testing.T) {
			got, err := world.Validate(cat, fromJSON(t, c.Document))
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			want := fromJSON(t, c.Canonical)
			if !proto.Equal(got, want) {
				t.Fatalf("canonical form\n got %v\nwant %v", got, want)
			}
			again, err := world.Validate(cat, got)
			if err != nil || !proto.Equal(again, got) {
				t.Fatalf("canonical form is not stable: %v", err)
			}
		})
		ran++
	}
	for _, c := range f.Invalid {
		if c.Scope != "" {
			skipped[c.Scope]++
			continue
		}
		t.Run("invalid/"+c.Name, func(t *testing.T) {
			_, err := world.Validate(cat, fromJSON(t, c.Document))
			if err == nil {
				t.Fatal("accepted")
			}
			if got := string(kindOf(err)); got != c.Kind {
				t.Fatalf("rejected as %q (%v), want %q", got, err, c.Kind)
			}
		})
		ran++
	}
	t.Logf("ran %d fixture entries; skipped by scope: %v", ran, skipped)
	if skipped["json-text"] != 20 || skipped["utf16"] != 2 || ran != 54 {
		t.Fatalf("scope accounting changed: ran %d, skipped %v", ran, skipped)
	}
}

func TestFixtureCoordinatesMapToMicroDegrees(t *testing.T) {
	f := loadFixtures(t)
	for _, c := range f.Coords.Valid {
		if got := toE6(c.Value); got != c.E6 {
			t.Errorf("%v -> %d, want %d", c.Value, got, c.E6)
		}
	}
	for _, c := range f.Coords.Normalize {
		if got := toE6(c.Input); got != c.E6 {
			t.Errorf("normalise %v -> %d, want %d", c.Input, got, c.E6)
		}
	}
}

func place(id, name string) *tripmapv1.WorldPlace {
	return &tripmapv1.WorldPlace{Id: id, Name: name, Country: "JP", LatE6: 35_676_200, LngE6: 139_650_300}
}

func doc(places ...*tripmapv1.WorldPlace) *tripmapv1.WorldDocument {
	return &tripmapv1.WorldDocument{Kind: "world", Version: 1, Places: places}
}

// A lone surrogate cannot be written in a JSON fixture the Go reader keeps, nor in a
// protobuf string; invalid UTF-8 is what it becomes, and it is refused the same way.
func TestLoneSurrogateStandInIsRejected(t *testing.T) {
	cat := catalog(t)
	for _, name := range []string{"a\xed\xa0\x80b", "a\xed\xb0\x80", "\xff"} {
		if _, err := world.Validate(cat, doc(place("p1", name))); kindOf(err) != world.ErrName {
			t.Errorf("name %q: %v", name, err)
		}
	}
	d := doc(place("p1", "x"))
	d.Places[0].Trips = []*tripmapv1.WorldTrip{{Id: "t1", Note: "a\xed\xa0\x80"}}
	if _, err := world.Validate(cat, d); kindOf(err) != world.ErrNote {
		t.Errorf("note: %v", err)
	}
	if _, err := world.Validate(cat, &tripmapv1.WorldDocument{Kind: "world", Version: 1, Countries: []*tripmapv1.WorldCountry{{Code: "J\xffP", Color: "#4b937d"}}}); kindOf(err) != world.ErrCountry {
		t.Errorf("country code: %v", err)
	}
}

func TestNilAndEmptyShapes(t *testing.T) {
	cat := catalog(t)
	if _, err := world.Validate(cat, nil); kindOf(err) != world.ErrFormat {
		t.Errorf("nil document: %v", err)
	}
	for name, d := range map[string]*tripmapv1.WorldDocument{
		"nil mark":  {Kind: "world", Version: 1, Countries: []*tripmapv1.WorldCountry{nil}},
		"nil place": {Kind: "world", Version: 1, Places: []*tripmapv1.WorldPlace{nil}},
		"nil trip":  {Kind: "world", Version: 1, Places: []*tripmapv1.WorldPlace{{Id: "p", Name: "x", Country: "JP", Trips: []*tripmapv1.WorldTrip{nil}}}},
	} {
		if _, err := world.Validate(cat, d); kindOf(err) != world.ErrFormat {
			t.Errorf("%s: %v", name, err)
		}
	}
	got, err := world.Validate(cat, world.Empty())
	if err != nil || !proto.Equal(got, world.Empty()) {
		t.Fatalf("the empty document: %v %v", got, err)
	}
}

// The edge characters are the fixture's list, code point by code point: every Unicode
// White_Space character plus U+FEFF, nothing the language's default trim adds or lacks.
func TestNameTrimUsesTheFixtureWhitespaceSet(t *testing.T) {
	f := loadFixtures(t)
	if len(f.Whitespace) != 26 {
		t.Fatalf("whitespace list has %d entries", len(f.Whitespace))
	}
	listed := map[rune]bool{}
	for _, entry := range f.Whitespace {
		var cp rune
		if _, err := fmt.Sscanf(entry, "U+%X", &cp); err != nil {
			t.Fatal(err)
		}
		listed[cp] = true
		if got := world.TrimName(string(cp) + "a" + string(cp)); got != "a" {
			t.Errorf("%s is not trimmed: %q", entry, got)
		}
		if got := world.TrimName("a" + string(cp) + "b"); got != "a"+string(cp)+"b" {
			t.Errorf("%s inside a name was touched: %q", entry, got)
		}
	}
	for cp := rune(0); cp <= unicode.MaxRune; cp++ {
		if got := world.TrimName("a" + string(cp)); (got == "a") != listed[cp] {
			t.Fatalf("U+%04X: trimmed=%v, listed=%v", cp, got == "a", listed[cp])
		}
	}
	if world.TrimName("\u200bx\u180e") != "\u200bx\u180e" {
		t.Error("zero-width characters are not White_Space")
	}
}

func TestDatesMatchTheSharedTripDateFixtures(t *testing.T) {
	raw, err := os.ReadFile(datesFile)
	if err != nil {
		t.Fatal(err)
	}
	var dates struct {
		Valid   []string `json:"valid"`
		Invalid []string `json:"invalid"`
	}
	if err = json.Unmarshal(raw, &dates); err != nil {
		t.Fatal(err)
	}
	for _, d := range dates.Valid {
		if !world.ValidDate(d) {
			t.Errorf("valid date %q was rejected", d)
		}
	}
	for _, d := range dates.Invalid {
		if world.ValidDate(d) {
			t.Errorf("invalid date %q was accepted", d)
		}
	}
}

func TestLimits(t *testing.T) {
	cat := catalog(t)
	f := loadFixtures(t)
	if f.Limits["maxPlaces"] != world.MaxPlaces || f.Limits["maxTrips"] != world.MaxTrips ||
		f.Limits["maxStringBytes"] != world.MaxStringBytes || f.Limits["nameUnits"] != world.NameUnits || f.Limits["noteUnits"] != world.NoteUnits {
		t.Fatalf("limits differ from the shared fixture: %v", f.Limits)
	}
	many := func(n int) []*tripmapv1.WorldPlace {
		out := make([]*tripmapv1.WorldPlace, n)
		for i := range out {
			out[i] = place(fmt.Sprintf("p%05d", i), "c")
		}
		return out
	}
	if _, err := world.Validate(cat, doc(many(world.MaxPlaces)...)); err != nil {
		t.Fatalf("5000 cities: %v", err)
	}
	if _, err := world.Validate(cat, doc(many(world.MaxPlaces+1)...)); kindOf(err) != world.ErrLimit {
		t.Fatalf("5001 cities: %v", err)
	}

	tripsOf := func(n int) []*tripmapv1.WorldPlace {
		places := many(2)
		for i := 0; i < n; i++ {
			p := places[i%2]
			p.Trips = append(p.Trips, &tripmapv1.WorldTrip{Id: fmt.Sprintf("t%05d", i)})
		}
		return places
	}
	if _, err := world.Validate(cat, doc(tripsOf(world.MaxTrips)...)); err != nil {
		t.Fatalf("10000 trips: %v", err)
	}
	if _, err := world.Validate(cat, doc(tripsOf(world.MaxTrips+1)...)); kindOf(err) != world.ErrLimit {
		t.Fatalf("10001 trips: %v", err)
	}

	// Text size: 1000-unit notes of three-byte characters, then trimmed to the exact limit.
	build := func(extra int) *tripmapv1.WorldDocument {
		p := place("p1", "x") // id 2 + name 1 + country 2
		const fixed = 2 + 1 + 2
		budget := world.MaxStringBytes - fixed + extra
		for i := 0; budget > 0; i++ {
			id := fmt.Sprintf("t%04d", i) // 5 bytes, empty date
			budget -= len(id)
			n := min(budget/3, 1000)
			note := strings.Repeat("山", n)
			budget -= len(note)
			p.Trips = append(p.Trips, &tripmapv1.WorldTrip{Id: id, Note: note})
		}
		return doc(p)
	}
	exact := build(0)
	if n := world.StringBytes(exact); n != world.MaxStringBytes {
		t.Fatalf("test document has %d bytes", n)
	}
	if _, err := world.Validate(cat, exact); err != nil {
		t.Fatalf("exactly %d bytes: %v", world.MaxStringBytes, err)
	}
	over := build(0)
	over.Places[0].Trips = append(over.Places[0].Trips, &tripmapv1.WorldTrip{Id: "z"})
	if _, err := world.Validate(cat, over); kindOf(err) != world.ErrLimit {
		t.Fatalf("one byte over: %v", err)
	}
	if world.MaxStringBytes >= 16<<20 {
		t.Fatal("the text limit no longer fits under the public message limit")
	}
}

func TestStoredDocumentRoundTripAndDigest(t *testing.T) {
	cat := catalog(t)
	d := doc(place("pb", "  B  "), place("pa", "A"))
	d.Countries = []*tripmapv1.WorldCountry{{Code: "US", Color: "#e2af5c"}, {Code: "JP", Color: "#719fc6"}}
	canon, err := world.Validate(cat, d)
	if err != nil {
		t.Fatal(err)
	}
	if canon.Places[0].Id != "pa" || canon.Places[1].Name != "B" || canon.Countries[0].Code != "JP" {
		t.Fatalf("not canonical: %v", canon)
	}
	if d.Places[0].Id != "pb" || d.Places[0].Name != "  B  " {
		t.Fatal("Validate changed its input")
	}
	payload, err := world.Encode(canon)
	if err != nil {
		t.Fatal(err)
	}
	back, err := world.Decode(payload)
	if err != nil || !world.Equal(back, canon) {
		t.Fatalf("round trip: %v %v", back, err)
	}
	if _, err = world.Decode([]byte("not gzip")); err == nil {
		t.Fatal("garbage decoded")
	}

	// The digest sees the base revision and every field, in canonical order.
	reordered := doc(place("pa", "A"), place("pb", "B"))
	reordered.Countries = []*tripmapv1.WorldCountry{{Code: "JP", Color: "#719fc6"}, {Code: "US", Color: "#e2af5c"}}
	other, err := world.Validate(cat, reordered)
	if err != nil {
		t.Fatal(err)
	}
	base := world.Digest(3, canon)
	if len(base) != 64 || base != world.Digest(3, other) {
		t.Fatalf("digest of equal canonical documents differs: %s", base)
	}
	if base == world.Digest(4, canon) {
		t.Fatal("digest ignores base_revision")
	}
	changed := proto.Clone(canon).(*tripmapv1.WorldDocument)
	changed.Places[0].LatE6++
	if base == world.Digest(3, changed) {
		t.Fatal("digest ignores coordinates")
	}
	// Field boundaries are part of the encoding: moving a character between two strings changes it.
	x := proto.Clone(canon).(*tripmapv1.WorldDocument)
	y := proto.Clone(canon).(*tripmapv1.WorldDocument)
	x.Places[0].Name, x.Places[0].Country = "ab", "c"
	y.Places[0].Name, y.Places[0].Country = "a", "bc"
	if world.Digest(1, x) == world.Digest(1, y) {
		t.Fatal("digest is ambiguous between neighbouring strings")
	}
}

// The embedded catalog is a copy of the frontend's file, byte for byte.
func TestCatalogIsTheFrontendCatalog(t *testing.T) {
	front, err := os.ReadFile(frontCatalog)
	if err != nil {
		t.Skip("packages/geo-data is not part of this build context")
	}
	own, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(front, own) {
		t.Fatal("internal/tripmap/world/catalog.json differs from packages/geo-data/data/world-catalog.json; run npm run prepare:world")
	}
	cat := catalog(t)
	if cat.Len() != 247 || !strings.HasPrefix(cat.Version(), "sha256:") {
		t.Fatalf("catalog has %d entries, version %q", cat.Len(), cat.Version())
	}
	for _, code := range []string{"JP", "US", "XK", "EH", "PS", "CY", "SO", "CN"} {
		if !cat.Has(code) {
			t.Errorf("%s is missing", code)
		}
	}
	for _, code := range []string{"HK", "MO", "TW", "ZZ", "jp", ""} {
		if cat.Has(code) {
			t.Errorf("%s must not be in the catalog", code)
		}
	}
}
