package application

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"

	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/city"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/rollingstocktopic"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CatalogService serves the anonymous, read-only catalog.
type CatalogService struct {
	metrov1.UnimplementedCatalogServiceServer
	db *ent.Client
}

// NewCatalogService returns the catalog reader over db.
func NewCatalogService(db *ent.Client) *CatalogService { return &CatalogService{db: db} }

// lineStatusNames maps the proto enum to the stored status.
var lineStatusNames = map[metrov1.LineStatus]line.Status{
	metrov1.LineStatus_LINE_STATUS_PLANNED:            line.StatusPlanned,
	metrov1.LineStatus_LINE_STATUS_UNDER_CONSTRUCTION: line.StatusUnderConstruction,
	metrov1.LineStatus_LINE_STATUS_OPERATING:          line.StatusOperating,
	metrov1.LineStatus_LINE_STATUS_SUSPENDED:          line.StatusSuspended,
	metrov1.LineStatus_LINE_STATUS_RETIRED:            line.StatusRetired,
}

// statusEnum is the inverse of lineStatusNames.
func statusEnum(s line.Status) metrov1.LineStatus {
	for k, v := range lineStatusNames {
		if v == s {
			return k
		}
	}
	return metrov1.LineStatus_LINE_STATUS_UNSPECIFIED
}

// statusFilter turns a request's statuses into stored values. Nothing (or only
// unspecified entries) means operating lines.
func statusFilter(in []metrov1.LineStatus) ([]line.Status, error) {
	var out []line.Status
	for _, s := range in {
		if s == metrov1.LineStatus_LINE_STATUS_UNSPECIFIED {
			continue
		}
		name, ok := lineStatusNames[s]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "unknown line status")
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		out = []line.Status{line.StatusOperating}
	}
	return out, nil
}

// roundKm keeps summed lengths at the two decimals they are stored with, so the
// float sum of a few numeric(8,2) values does not show binary noise.
func roundKm(v float64) float64 { return math.Round(v*100) / 100 }

// dateLayout is the only date format of the catalog.
const dateLayout = "2006-01-02"

// lineMessage converts a line with its city and rolling stock edges loaded.
func lineMessage(l *ent.Line) *metrov1.Line {
	out := &metrov1.Line{
		Code: l.Code, Name: l.Name, Number: int32(l.Number), Status: statusEnum(l.Status),
		Cars: int32(l.Cars), CarsAddition: int32(l.CarsAddition), Stations: int32(l.Stations),
		LengthKm: l.LengthKm, Color: l.Color, IsLoop: l.IsLoop,
	}
	if c := l.Edges.City; c != nil {
		out.CitySlug, out.CityName = c.Slug, c.Name
	}
	if rs := l.Edges.RollingStock; rs != nil {
		out.RollingStockCode = rs.Code
	}
	if l.CommencedOn != nil {
		out.CommencedOn = l.CommencedOn.Format(dateLayout)
	}
	if l.ExtendedOn != nil {
		out.ExtendedOn = l.ExtendedOn.Format(dateLayout)
	}
	return out
}

// sortLines orders lines of one city by number with unnumbered (0) last, then by code.
func sortLines(lines []*ent.Line) {
	slices.SortStableFunc(lines, func(a, b *ent.Line) int {
		if (a.Number == 0) != (b.Number == 0) {
			if a.Number == 0 {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Number, b.Number), strings.Compare(a.Code, b.Code))
	})
}

func totalLength(lines []*ent.Line) float64 {
	var sum float64
	for _, l := range lines {
		sum += l.LengthKm
	}
	return roundKm(sum)
}

func cityMessage(c *ent.City, lineCount int, km float64) *metrov1.City {
	return &metrov1.City{
		Slug: c.Slug, Name: c.Name, HeaderImages: slices.Clone(c.HeaderImages), MapSvgUrl: c.MapSvgURL,
		SortOrder: int32(c.SortOrder), LineCount: int32(lineCount), TotalLengthKm: roundKm(km),
	}
}

func (s *CatalogService) ListCities(ctx context.Context, req *metrov1.ListCitiesRequest) (*metrov1.ListCitiesResponse, error) {
	out := &metrov1.ListCitiesResponse{}
	err := readSnapshot(ctx, s.db, func(tx *ent.Tx, version int64) error {
		out.Version = version
		if req.GetKnownVersion() != 0 && req.GetKnownVersion() == version {
			out.NotModified = true
			return nil
		}
		cities, err := tx.City.Query().Order(ent.Asc(city.FieldSortOrder), ent.Asc(city.FieldSlug)).All(ctx)
		if err != nil {
			return err
		}
		operating, err := tx.Line.Query().Where(line.StatusEQ(line.StatusOperating)).
			Select(line.FieldCityID, line.FieldLengthKm).All(ctx)
		if err != nil {
			return err
		}
		type tally struct {
			n  int
			km float64
		}
		byCity := map[string]*tally{}
		for _, l := range operating {
			t := byCity[l.CityID]
			if t == nil {
				t = &tally{}
				byCity[l.CityID] = t
			}
			t.n++
			t.km += l.LengthKm
		}
		for _, c := range cities {
			t := byCity[c.ID]
			if t == nil {
				t = &tally{}
			}
			out.Cities = append(out.Cities, cityMessage(c, t.n, t.km))
		}
		return nil
	})
	if err != nil {
		return nil, internalError("list cities", err)
	}
	return out, nil
}

func (s *CatalogService) GetCity(ctx context.Context, req *metrov1.GetCityRequest) (*metrov1.GetCityResponse, error) {
	statuses, err := statusFilter(req.GetStatuses())
	if err != nil {
		return nil, err
	}
	out := &metrov1.GetCityResponse{}
	err = readSnapshot(ctx, s.db, func(tx *ent.Tx, version int64) error {
		out.Version = version
		c, err := tx.City.Query().Where(city.Slug(req.GetSlug())).Only(ctx)
		if err != nil {
			return err
		}
		lines, err := tx.Line.Query().Where(line.CityID(c.ID), line.StatusIn(statuses...)).
			WithCity().WithRollingStock().All(ctx)
		if err != nil {
			return err
		}
		sortLines(lines)
		km := totalLength(lines)
		out.City = cityMessage(c, len(lines), km)
		out.TotalLines, out.TotalLengthKm = int32(len(lines)), km
		for _, l := range lines {
			out.Lines = append(out.Lines, lineMessage(l))
		}
		return nil
	})
	switch {
	case ent.IsNotFound(err):
		return nil, status.Error(codes.NotFound, "city not found")
	case err != nil:
		return nil, internalError("get city", err)
	}
	return out, nil
}

func (s *CatalogService) ListLines(ctx context.Context, req *metrov1.ListLinesRequest) (*metrov1.ListLinesResponse, error) {
	statuses, err := statusFilter(req.GetStatuses())
	if err != nil {
		return nil, err
	}
	out := &metrov1.ListLinesResponse{}
	err = readSnapshot(ctx, s.db, func(tx *ent.Tx, version int64) error {
		out.Version = version
		q := tx.Line.Query().Where(line.StatusIn(statuses...))
		if slug := req.GetCitySlug(); slug != "" {
			c, err := tx.City.Query().Where(city.Slug(slug)).Only(ctx)
			if err != nil {
				return err
			}
			q = q.Where(line.CityID(c.ID))
		}
		lines, err := q.WithCity().WithRollingStock().All(ctx)
		if err != nil {
			return err
		}
		sortLinesByCity(lines)
		for _, l := range lines {
			out.Lines = append(out.Lines, lineMessage(l))
		}
		return nil
	})
	switch {
	case ent.IsNotFound(err):
		return nil, status.Error(codes.NotFound, "city not found")
	case err != nil:
		return nil, internalError("list lines", err)
	}
	return out, nil
}

// sortLinesByCity orders lines by city (sort order, then slug), then by line number.
func sortLinesByCity(lines []*ent.Line) {
	sortLines(lines)
	slices.SortStableFunc(lines, func(a, b *ent.Line) int {
		ca, cb := a.Edges.City, b.Edges.City
		if ca == nil || cb == nil {
			return 0
		}
		return cmp.Or(cmp.Compare(ca.SortOrder, cb.SortOrder), strings.Compare(ca.Slug, cb.Slug))
	})
}

func topicMessage(t *ent.RollingStockTopic) *metrov1.RollingStockTopic {
	out := &metrov1.RollingStockTopic{Slug: t.Slug, Name: t.Name, RollingStockCodes: slices.Clone(t.RollingStockCodes)}
	for _, n := range t.Cars {
		out.Cars = append(out.Cars, int32(n))
	}
	return out
}

func (s *CatalogService) ListRollingStockTopics(ctx context.Context, _ *metrov1.ListRollingStockTopicsRequest) (*metrov1.ListRollingStockTopicsResponse, error) {
	out := &metrov1.ListRollingStockTopicsResponse{}
	err := readSnapshot(ctx, s.db, func(tx *ent.Tx, version int64) error {
		out.Version = version
		topics, err := tx.RollingStockTopic.Query().Order(ent.Asc(rollingstocktopic.FieldSlug)).All(ctx)
		if err != nil {
			return err
		}
		for _, t := range topics {
			out.Topics = append(out.Topics, topicMessage(t))
		}
		return nil
	})
	if err != nil {
		return nil, internalError("list topics", err)
	}
	return out, nil
}

// topicMatches reports whether an operating line is selected by the topic: the
// car count (or the extended car count) must be one of the topic's, and when the
// topic names rolling stock codes the line's code must be one of them. An empty
// list does not filter on that attribute.
func topicMatches(t *ent.RollingStockTopic, l *ent.Line) bool {
	if len(t.Cars) > 0 && !slices.Contains(t.Cars, l.Cars) && !slices.Contains(t.Cars, l.CarsAddition) {
		return false
	}
	if len(t.RollingStockCodes) > 0 {
		rs := l.Edges.RollingStock
		if rs == nil || !slices.Contains(t.RollingStockCodes, rs.Code) {
			return false
		}
	}
	return true
}

func (s *CatalogService) GetRollingStockTopic(ctx context.Context, req *metrov1.GetRollingStockTopicRequest) (*metrov1.GetRollingStockTopicResponse, error) {
	out := &metrov1.GetRollingStockTopicResponse{}
	err := readSnapshot(ctx, s.db, func(tx *ent.Tx, version int64) error {
		out.Version = version
		topic, err := tx.RollingStockTopic.Query().Where(rollingstocktopic.Slug(req.GetSlug())).Only(ctx)
		if err != nil {
			return err
		}
		out.Topic = topicMessage(topic)
		candidates, err := tx.Line.Query().Where(line.StatusEQ(line.StatusOperating)).
			WithCity().WithRollingStock().All(ctx)
		if err != nil {
			return err
		}
		var lines []*ent.Line
		for _, l := range candidates {
			if topicMatches(topic, l) {
				lines = append(lines, l)
			}
		}
		sortLinesByCity(lines)
		var current *metrov1.TopicCity
		var cityKm float64
		flush := func() {
			if current != nil {
				current.TotalLengthKm = roundKm(cityKm)
				out.Cities = append(out.Cities, current)
			}
		}
		for _, l := range lines {
			if current == nil || current.CitySlug != l.Edges.City.Slug {
				flush()
				current, cityKm = &metrov1.TopicCity{CitySlug: l.Edges.City.Slug, CityName: l.Edges.City.Name}, 0
			}
			current.LineCount++
			cityKm += l.LengthKm
			out.Lines = append(out.Lines, lineMessage(l))
		}
		flush()
		out.TotalLines, out.TotalLengthKm = int32(len(lines)), totalLength(lines)
		return nil
	})
	switch {
	case ent.IsNotFound(err):
		return nil, status.Error(codes.NotFound, "topic not found")
	case err != nil:
		return nil, internalError("get topic", err)
	}
	return out, nil
}
