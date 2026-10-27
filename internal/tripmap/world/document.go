// Package world validates and canonicalises the world footprint document (世界视图,
// docs/trip-map/world-spec.md sections 4 and 9) and computes what the replace
// protocol compares: the canonical form, the structural-equality check and the
// request digest. It is pure: no database, no RPC types beyond the protobuf
// document.
//
// The rules are the same as apps/trip-map/src/lib/world.ts and are checked
// against the shared fixture packages/fixtures/world-documents.json.
package world

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// Kind and Version identify the document structure; only version 1 exists.
	Kind    = "world"
	Version = 1

	MaxPlaces = 5000
	MaxTrips  = 10_000
	// MaxStringBytes bounds the sum of the UTF-8 byte lengths of every string field
	// (id, name, country, code, color, date, note). It does not depend on any
	// serialisation, and keeps a document far below the gRPC message limits.
	MaxStringBytes = 2_500_000
	// NameUnits and NoteUnits count UTF-16 code units, like the browser's string length.
	NameUnits = 60
	NoteUnits = 1000
)

// ErrorKind classifies a rejected document; the names are those of the shared fixture.
type ErrorKind string

const (
	ErrFormat     ErrorKind = "format"
	ErrKind       ErrorKind = "kind"
	ErrVersion    ErrorKind = "version"
	ErrCountry    ErrorKind = "country"
	ErrColor      ErrorKind = "color"
	ErrDuplicate  ErrorKind = "duplicate"
	ErrID         ErrorKind = "id"
	ErrName       ErrorKind = "name"
	ErrCoordinate ErrorKind = "coordinate"
	ErrDate       ErrorKind = "date"
	ErrNote       ErrorKind = "note"
	ErrLimit      ErrorKind = "limit"
)

// Error is a rejected document. Path names the record, e.g. `places[2](p123).name`.
type Error struct {
	Kind    ErrorKind
	Path    string
	Message string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

func reject(kind ErrorKind, path, format string, args ...any) *Error {
	return &Error{Kind: kind, Path: path, Message: fmt.Sprintf(format, args...)}
}

// Palette is the five footprint colours, the same as the China footprints.
var palette = []string{"#4b937d", "#e2af5c", "#d88370", "#719fc6", "#a08cbd"}

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ValidID reports whether id is a city, trip or request ID: ^[A-Za-z0-9_-]{1,64}$.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Empty is the document of a user who has never written: no marks, no cities.
func Empty() *tripmapv1.WorldDocument {
	return &tripmapv1.WorldDocument{Kind: Kind, Version: Version}
}

// utf16Units counts UTF-16 code units; invalid UTF-8 (which no protobuf string
// holds, and which stands for a lone surrogate) is reported by the caller first.
func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// isEdgeSpace is the set stripped from both ends of a name: every Unicode White_Space
// code point plus U+FEFF (never a language default trim).
func isEdgeSpace(r rune) bool { return r == 0xFEFF || unicode.Is(unicode.White_Space, r) }

// TrimName strips the edge characters from both ends of a name.
func TrimName(name string) string { return strings.TrimFunc(name, isEdgeSpace) }

// ValidDate reports whether date is a real YYYY-MM-DD calendar date (or empty).
func ValidDate(date string) bool {
	if date == "" {
		return true
	}
	if !datePattern.MatchString(date) {
		return false
	}
	parsed, err := time.Parse("2006-01-02", date)
	return err == nil && parsed.Format("2006-01-02") == date
}

// StringBytes is the size measure of the limit: the UTF-8 byte lengths of all string fields.
func StringBytes(doc *tripmapv1.WorldDocument) int {
	total := 0
	for _, c := range doc.GetCountries() {
		total += len(c.GetCode()) + len(c.GetColor())
	}
	for _, p := range doc.GetPlaces() {
		total += len(p.GetId()) + len(p.GetName()) + len(p.GetCountry())
		for _, t := range p.GetTrips() {
			total += len(t.GetId()) + len(t.GetDate()) + len(t.GetNote())
		}
	}
	return total
}

func countryCode(cat Catalog, code, path string) *Error {
	if !utf8.ValidString(code) {
		return reject(ErrCountry, path, "country code is not valid UTF-8")
	}
	if _, ok := cat.c.codes[code]; !ok {
		return reject(ErrCountry, path, "country code is not in the catalog")
	}
	return nil
}

// Validate checks doc against the world rules and returns its canonical form: a
// new message with countries sorted by code, cities and trips sorted by id and
// names trimmed. It never changes doc, never repairs a field silently and stops
// at the first problem, in the same order as the TypeScript validator.
func Validate(cat Catalog, doc *tripmapv1.WorldDocument) (*tripmapv1.WorldDocument, error) {
	if doc == nil {
		return nil, reject(ErrFormat, "", "document is required")
	}
	if doc.GetKind() != Kind {
		return nil, reject(ErrKind, "kind", "not a world document")
	}
	if doc.GetVersion() != Version {
		return nil, reject(ErrVersion, "version", "unknown world document version %d", doc.GetVersion())
	}
	if len(doc.GetPlaces()) > MaxPlaces {
		return nil, reject(ErrLimit, "places", "at most %d cities, got %d", MaxPlaces, len(doc.GetPlaces()))
	}

	marks := make(map[string]struct{}, len(doc.GetCountries()))
	countries := make([]*tripmapv1.WorldCountry, 0, len(doc.GetCountries()))
	for i, raw := range doc.GetCountries() {
		path := fmt.Sprintf("countries[%d]", i)
		if raw == nil {
			return nil, reject(ErrFormat, path, "mark is required")
		}
		code := raw.GetCode()
		if code == DerivedCountry {
			return nil, reject(ErrCountry, path+".code", "CN is derived from the China footprints and cannot be marked")
		}
		if err := countryCode(cat, code, path+".code"); err != nil {
			return nil, err
		}
		if _, dup := marks[code]; dup {
			return nil, reject(ErrDuplicate, path, "country %s is marked twice", code)
		}
		marks[code] = struct{}{}
		if !slices.Contains(palette, raw.GetColor()) {
			return nil, reject(ErrColor, fmt.Sprintf("%s(%s).color", path, code), "color is not in the footprint palette")
		}
		countries = append(countries, &tripmapv1.WorldCountry{Code: code, Color: raw.GetColor()})
	}

	placeIDs := make(map[string]struct{}, len(doc.GetPlaces()))
	tripIDs := make(map[string]struct{})
	trips := 0
	places := make([]*tripmapv1.WorldPlace, 0, len(doc.GetPlaces()))
	for i, raw := range doc.GetPlaces() {
		at := fmt.Sprintf("places[%d]", i)
		if raw == nil {
			return nil, reject(ErrFormat, at, "city is required")
		}
		if !idPattern.MatchString(raw.GetId()) {
			return nil, reject(ErrID, at+".id", "city id must match [A-Za-z0-9_-]{1,64}")
		}
		path := fmt.Sprintf("%s(%s)", at, raw.GetId())
		if _, dup := placeIDs[raw.GetId()]; dup {
			return nil, reject(ErrDuplicate, path, "city id %s is used twice", raw.GetId())
		}
		placeIDs[raw.GetId()] = struct{}{}

		if !utf8.ValidString(raw.GetName()) {
			return nil, reject(ErrName, path+".name", "name is not valid UTF-8 (a lone surrogate)")
		}
		name := TrimName(raw.GetName())
		switch {
		case name == "":
			return nil, reject(ErrName, path+".name", "name must not be empty")
		case utf16Units(name) > NameUnits:
			return nil, reject(ErrName, path+".name", "name is longer than %d characters", NameUnits)
		case strings.IndexFunc(name, func(r rune) bool { return unicode.Is(unicode.Cc, r) }) >= 0:
			return nil, reject(ErrName, path+".name", "name must not contain control characters")
		}
		if raw.GetCountry() == DerivedCountry {
			return nil, reject(ErrCountry, path+".country", "domestic cities belong to the China view, not the world view")
		}
		if err := countryCode(cat, raw.GetCountry(), path+".country"); err != nil {
			return nil, err
		}
		if lat := raw.GetLatE6(); lat < -90_000_000 || lat > 90_000_000 {
			return nil, reject(ErrCoordinate, path+".lat_e6", "latitude is out of range")
		}
		if lng := raw.GetLngE6(); lng < -180_000_000 || lng > 180_000_000 {
			return nil, reject(ErrCoordinate, path+".lng_e6", "longitude is out of range")
		}
		trips += len(raw.GetTrips())
		if trips > MaxTrips {
			return nil, reject(ErrLimit, path+".trips", "at most %d trips", MaxTrips)
		}
		list := make([]*tripmapv1.WorldTrip, 0, len(raw.GetTrips()))
		for j, t := range raw.GetTrips() {
			tp := fmt.Sprintf("%s.trips[%d]", path, j)
			if t == nil {
				return nil, reject(ErrFormat, tp, "trip is required")
			}
			if !idPattern.MatchString(t.GetId()) {
				return nil, reject(ErrID, tp+".id", "trip id must match [A-Za-z0-9_-]{1,64}")
			}
			if _, dup := tripIDs[t.GetId()]; dup {
				return nil, reject(ErrDuplicate, fmt.Sprintf("%s(%s)", tp, t.GetId()), "trip id %s is used twice", t.GetId())
			}
			tripIDs[t.GetId()] = struct{}{}
			if !ValidDate(t.GetDate()) {
				return nil, reject(ErrDate, fmt.Sprintf("%s(%s).date", tp, t.GetId()), "date must be a real YYYY-MM-DD date or empty")
			}
			if !utf8.ValidString(t.GetNote()) || utf16Units(t.GetNote()) > NoteUnits {
				return nil, reject(ErrNote, fmt.Sprintf("%s(%s).note", tp, t.GetId()), "note must be valid text of at most %d characters", NoteUnits)
			}
			list = append(list, &tripmapv1.WorldTrip{Id: t.GetId(), Date: t.GetDate(), Note: t.GetNote()})
		}
		slices.SortFunc(list, func(a, b *tripmapv1.WorldTrip) int { return strings.Compare(a.GetId(), b.GetId()) })
		places = append(places, &tripmapv1.WorldPlace{
			Id: raw.GetId(), Name: name, Country: raw.GetCountry(),
			LatE6: raw.GetLatE6(), LngE6: raw.GetLngE6(), Trips: list,
		})
	}
	slices.SortFunc(countries, func(a, b *tripmapv1.WorldCountry) int { return strings.Compare(a.GetCode(), b.GetCode()) })
	slices.SortFunc(places, func(a, b *tripmapv1.WorldPlace) int { return strings.Compare(a.GetId(), b.GetId()) })

	out := &tripmapv1.WorldDocument{Kind: Kind, Version: Version, Countries: countries, Places: places}
	if n := StringBytes(out); n > MaxStringBytes {
		return nil, reject(ErrLimit, "", "text content is %d bytes, over the limit of %d", n, MaxStringBytes)
	}
	return out, nil
}

// Equal is structural equality of two canonical documents (not byte equality of
// any serialisation).
func Equal(a, b *tripmapv1.WorldDocument) bool { return proto.Equal(a, b) }

// Counts returns the number of cities and trips.
func Counts(doc *tripmapv1.WorldDocument) (places, trips int) {
	for _, p := range doc.GetPlaces() {
		trips += len(p.GetTrips())
	}
	return len(doc.GetPlaces()), trips
}
