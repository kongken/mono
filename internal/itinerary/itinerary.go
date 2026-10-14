package itinerary

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed coords.json
var rawCoords []byte

const (
	ProviderID       = "offline_great_circle"
	ProviderLabel    = "内置城市中心点球面与交通系数估算"
	CostPolicy       = "free_builtin"
	CacheStrategy    = "redis_leg_24h_and_local_deterministic"
	PolicyDisclaimer = "基于随项目分发的城市中心点球面距离与交通方式系数离线估算，不发起第三方地图请求，不产生外部 API 费用；仅供参考，不作为实时导航依据"

	earthRadiusKm = 6371.0
)

// Coord holds [gcjLng, gcjLat, wgsLng, wgsLat] for a city region code.
type Coord [4]float64

var defaultCoords map[string]Coord

func init() {
	var parsed map[string]Coord
	if err := json.Unmarshal(rawCoords, &parsed); err != nil {
		panic(fmt.Sprintf("parse embedded city coords: %v", err))
	}
	defaultCoords = parsed
}

// Coords returns a copy of the embedded city coordinate map.
func Coords() map[string]Coord {
	out := make(map[string]Coord, len(defaultCoords))
	for k, v := range defaultCoords {
		out[k] = v
	}
	return out
}

// CityCoord looks up the coordinate tuple for a canonical city region code.
func CityCoord(regionCode string) (Coord, bool) {
	c, ok := defaultCoords[regionCode]
	return c, ok
}

// EstimationPolicy returns the active F11 route estimation policy metadata.
func EstimationPolicy() *tripmapv1.ItineraryEstimationPolicy {
	return &tripmapv1.ItineraryEstimationPolicy{
		Provider:           ProviderID,
		ProviderLabel:      ProviderLabel,
		ExternalApiEnabled: false,
		CostPolicy:         CostPolicy,
		CacheStrategy:      CacheStrategy,
		Disclaimer:         PolicyDisclaimer,
	}
}

// ValidTransport reports whether mode is a supported itinerary leg transport mode.
func ValidTransport(mode string) bool {
	switch mode {
	case "", "train", "car", "flight", "ship", "other":
		return true
	default:
		return false
	}
}

// NormalizeTransport returns the canonical transport mode name (defaulting to "train").
func NormalizeTransport(mode string) string {
	switch mode {
	case "car", "flight", "ship", "other":
		return mode
	default:
		return "train"
	}
}

type modeParams struct {
	detourPct int64
	speedKmh  int64
	bufferMin int32
}

func paramsForMode(mode string) modeParams {
	switch NormalizeTransport(mode) {
	case "flight":
		return modeParams{detourPct: 105, speedKmh: 700, bufferMin: 50}
	case "ship":
		return modeParams{detourPct: 115, speedKmh: 35, bufferMin: 25}
	case "car":
		return modeParams{detourPct: 130, speedKmh: 80, bufferMin: 15}
	case "other":
		return modeParams{detourPct: 120, speedKmh: 100, bufferMin: 15}
	default:
		return modeParams{detourPct: 120, speedKmh: 220, bufferMin: 20}
	}
}

// LegEstimate holds the computed distance (in integer tenths of km) and duration for one leg.
type LegEstimate struct {
	Transport        string `json:"transport"`
	StraightTenthsKm int64  `json:"straight_tenths_km"`
	EstTenthsKm      int64  `json:"est_tenths_km"`
	DurationMinutes  int32  `json:"duration_minutes"`
	FromCache        bool   `json:"-"`
}

// ComputeLeg deterministically estimates distance and travel time between two cities.
func ComputeLeg(fromCode, toCode, transport string) LegEstimate {
	mode := NormalizeTransport(transport)
	if fromCode == toCode {
		return LegEstimate{Transport: mode}
	}
	c1, ok1 := defaultCoords[fromCode]
	c2, ok2 := defaultCoords[toCode]
	if !ok1 || !ok2 {
		return LegEstimate{Transport: mode}
	}
	lng1, lat1 := c1[2], c1[3]
	lng2, lat2 := c2[2], c2[3]
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLng := (lng2 - lng1) * math.Pi / 180.0
	rLat1 := lat1 * math.Pi / 180.0
	rLat2 := lat2 * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rLat1)*math.Cos(rLat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(math.Max(0, 1-a)))
	rawKm := earthRadiusKm * c
	straightTenths := int64(math.Round(rawKm * 10))
	if straightTenths < 1 {
		straightTenths = 1
	}
	mp := paramsForMode(mode)
	estTenths := (straightTenths*mp.detourPct + 50) / 100
	if estTenths < straightTenths {
		estTenths = straightTenths
	}
	travelMin := int32((estTenths*6 + mp.speedKmh/2) / mp.speedKmh)
	if travelMin < 1 {
		travelMin = 1
	}
	return LegEstimate{
		Transport:        mode,
		StraightTenthsKm: straightTenths,
		EstTenthsKm:      estTenths,
		DurationMinutes:  travelMin + mp.bufferMin,
	}
}

var singleCityProvinces = map[string]bool{
	"110000": true,
	"120000": true,
	"310000": true,
	"500000": true,
	"810000": true,
	"820000": true,
}

func statsCityCode(regionCode, provinceCode string) string {
	if singleCityProvinces[provinceCode] {
		return provinceCode
	}
	return regionCode
}
type StopRecord struct {
	ID         string
	RegionCode string
	Date       string
	Note       string
	Transport  string
	TripID     string
}

// LegResolver can optionally wrap ComputeLeg with a cache (such as Redis).
type LegResolver func(fromCode, toCode, transport string) LegEstimate

// BuildInfo constructs a complete ItineraryInfo with enriched stops, legs and totals.
func BuildInfo(
	idx *regions.Index,
	id, title, note, color string,
	version int64,
	stops []StopRecord,
	createdAt, updatedAt time.Time,
	resolveLeg LegResolver,
) *tripmapv1.ItineraryInfo {
	if resolveLeg == nil {
		resolveLeg = ComputeLeg
	}
	outStops := make([]*tripmapv1.ItineraryStop, 0, len(stops))
	citySet := make(map[string]bool, len(stops))
	provSet := make(map[string]bool, len(stops))

	for i, s := range stops {
		code := s.RegionCode
		if resolved, ok := idx.ResolveCode(code); ok {
			code = resolved
		}
		cityName, provCode, _ := idx.City(code)
		provName, _ := idx.ProvinceName(provCode)
		coord := defaultCoords[code]
		mode := NormalizeTransport(s.Transport)
		if i == 0 {
			mode = NormalizeTransport(s.Transport)
		}
		outStops = append(outStops, &tripmapv1.ItineraryStop{
			Id:           s.ID,
			Order:        int32(i + 1),
			RegionCode:   code,
			RegionName:   cityName,
			ProvinceCode: provCode,
			ProvinceName: provName,
			Date:         s.Date,
			Note:         s.Note,
			Transport:    mode,
			TripId:       s.TripID,
			Longitude:    coord[0],
			Latitude:     coord[1],
			WgsLongitude: coord[2],
			WgsLatitude:  coord[3],
		})
		if statsCity := statsCityCode(code, provCode); statsCity != "" {
			citySet[statsCity] = true
		}
		if provCode != "" {
			provSet[provCode] = true
		}
	}

	var (
		legs                = make([]*tripmapv1.ItineraryLeg, 0, max(0, len(outStops)-1))
		totalStraightTenths int64
		totalEstTenths      int64
		totalDurationMin    int32
		allCached           = len(outStops) > 1
	)
	for i := 1; i < len(outStops); i++ {
		prev := outStops[i-1]
		curr := outStops[i]
		est := resolveLeg(prev.RegionCode, curr.RegionCode, curr.Transport)
		if !est.FromCache {
			allCached = false
		}
		totalStraightTenths += est.StraightTenthsKm
		totalEstTenths += est.EstTenthsKm
		totalDurationMin += est.DurationMinutes
		legs = append(legs, &tripmapv1.ItineraryLeg{
			FromOrder:                prev.Order,
			ToOrder:                  curr.Order,
			FromRegionCode:           prev.RegionCode,
			FromRegionName:           prev.RegionName,
			ToRegionCode:             curr.RegionCode,
			ToRegionName:             curr.RegionName,
			Transport:                est.Transport,
			StraightDistanceKm:       float64(est.StraightTenthsKm) / 10.0,
			EstimatedDistanceKm:      float64(est.EstTenthsKm) / 10.0,
			EstimatedDurationMinutes: est.DurationMinutes,
			FromCache:                est.FromCache,
		})
	}

	info := &tripmapv1.ItineraryInfo{
		Id:                            id,
		Title:                         title,
		Note:                          note,
		Color:                         color,
		Version:                       version,
		Stops:                         outStops,
		Legs:                          legs,
		TotalStraightDistanceKm:       float64(totalStraightTenths) / 10.0,
		TotalEstimatedDistanceKm:      float64(totalEstTenths) / 10.0,
		TotalEstimatedDurationMinutes: totalDurationMin,
		CityCount:                     int32(len(citySet)),
		ProvinceCount:                 int32(len(provSet)),
		FromCache:                     allCached,
	}
	if !createdAt.IsZero() {
		info.CreatedAt = timestamppb.New(createdAt)
	}
	if !updatedAt.IsZero() {
		info.UpdatedAt = timestamppb.New(updatedAt)
	}
	return info
}
