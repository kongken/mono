package itinerary

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kongken/mono/internal/tripmap/regions"
)

type routeFixture struct {
	Policy struct {
		Provider           string `json:"provider"`
		ExternalAPIEnabled bool   `json:"externalApiEnabled"`
		CostPolicy         string `json:"costPolicy"`
		CacheStrategy      string `json:"cacheStrategy"`
	} `json:"policy"`
	Cases []struct {
		Name    string `json:"name"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		Note    string `json:"note"`
		Color   string `json:"color"`
		Version int64  `json:"version"`
		Stops   []struct {
			ID         string `json:"id"`
			RegionCode string `json:"regionCode"`
			Date       string `json:"date"`
			Note       string `json:"note"`
			Transport  string `json:"transport"`
			TripID     string `json:"tripId"`
		} `json:"stops"`
		Expect struct {
			TotalStraightDistanceKm       float64 `json:"totalStraightDistanceKm"`
			TotalEstimatedDistanceKm      float64 `json:"totalEstimatedDistanceKm"`
			TotalEstimatedDurationMinutes int32   `json:"totalEstimatedDurationMinutes"`
			CityCount                     int32   `json:"cityCount"`
			ProvinceCount                 int32   `json:"provinceCount"`
			Legs                          []struct {
				FromOrder                int32   `json:"fromOrder"`
				ToOrder                  int32   `json:"toOrder"`
				FromRegionCode           string  `json:"fromRegionCode"`
				FromRegionName           string  `json:"fromRegionName"`
				ToRegionCode             string  `json:"toRegionCode"`
				ToRegionName             string  `json:"toRegionName"`
				Transport                string  `json:"transport"`
				StraightDistanceKm       float64 `json:"straightDistanceKm"`
				EstimatedDistanceKm      float64 `json:"estimatedDistanceKm"`
				EstimatedDurationMinutes int32   `json:"estimatedDurationMinutes"`
			} `json:"legs"`
		} `json:"expect"`
	} `json:"cases"`
}

func TestCoordsMatchFrontendAndCoverAllCities(t *testing.T) {
	frontRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "src", "city-coords.json"))
	if err != nil {
		t.Fatalf("read src/city-coords.json: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(frontRaw), bytes.TrimSpace(rawCoords)) {
		t.Fatalf("internal/tripmap/itinerary/coords.json is out of sync with src/city-coords.json; run node scripts/prepare-city-coords.mjs")
	}

	idx, err := regions.Default()
	if err != nil {
		t.Fatalf("regions.Default: %v", err)
	}
	for _, code := range idx.CityCodes("") {
		cityName, _, _ := idx.City(code)
		coord, ok := CityCoord(code)
		if !ok {
			t.Fatalf("missing coordinates for city %s (%s)", code, cityName)
		}
		if coord[0] < 70 || coord[0] > 140 || coord[1] < 3 || coord[1] > 55 {
			t.Fatalf("out-of-range coordinates for %s (%s): %v", code, cityName, coord)
		}
	}
}

func TestRouteFixturesMatchFrontend(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "fixtures", "itinerary-routes.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx routeFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	pol := EstimationPolicy()
	if pol.Provider != fx.Policy.Provider ||
		pol.ExternalApiEnabled != fx.Policy.ExternalAPIEnabled ||
		pol.CostPolicy != fx.Policy.CostPolicy ||
		pol.CacheStrategy != fx.Policy.CacheStrategy {
		t.Fatalf("policy mismatch: got %+v, want %+v", pol, fx.Policy)
	}

	idx, err := regions.Default()
	if err != nil {
		t.Fatalf("regions.Default: %v", err)
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	for _, tc := range fx.Cases {
		stops := make([]StopRecord, 0, len(tc.Stops))
		for _, s := range tc.Stops {
			stops = append(stops, StopRecord{
				ID:         s.ID,
				RegionCode: s.RegionCode,
				Date:       s.Date,
				Note:       s.Note,
				Transport:  s.Transport,
				TripID:     s.TripID,
			})
		}
		info := BuildInfo(idx, tc.ID, tc.Title, tc.Note, tc.Color, tc.Version, stops, now, now, nil)
		if math.Abs(info.TotalStraightDistanceKm-tc.Expect.TotalStraightDistanceKm) > 1e-6 {
			t.Fatalf("%s: totalStraightDistanceKm = %v, want %v", tc.Name, info.TotalStraightDistanceKm, tc.Expect.TotalStraightDistanceKm)
		}
		if math.Abs(info.TotalEstimatedDistanceKm-tc.Expect.TotalEstimatedDistanceKm) > 1e-6 {
			t.Fatalf("%s: totalEstimatedDistanceKm = %v, want %v", tc.Name, info.TotalEstimatedDistanceKm, tc.Expect.TotalEstimatedDistanceKm)
		}
		if info.TotalEstimatedDurationMinutes != tc.Expect.TotalEstimatedDurationMinutes {
			t.Fatalf("%s: totalEstimatedDurationMinutes = %v, want %v", tc.Name, info.TotalEstimatedDurationMinutes, tc.Expect.TotalEstimatedDurationMinutes)
		}
		if info.CityCount != tc.Expect.CityCount || info.ProvinceCount != tc.Expect.ProvinceCount {
			t.Fatalf("%s: counts = (%d cities, %d provinces), want (%d, %d)",
				tc.Name, info.CityCount, info.ProvinceCount, tc.Expect.CityCount, tc.Expect.ProvinceCount)
		}
		if len(info.Legs) != len(tc.Expect.Legs) {
			t.Fatalf("%s: legs len = %d, want %d", tc.Name, len(info.Legs), len(tc.Expect.Legs))
		}
		for i, exp := range tc.Expect.Legs {
			got := info.Legs[i]
			if got.FromOrder != exp.FromOrder || got.ToOrder != exp.ToOrder ||
				got.FromRegionCode != exp.FromRegionCode || got.FromRegionName != exp.FromRegionName ||
				got.ToRegionCode != exp.ToRegionCode || got.ToRegionName != exp.ToRegionName ||
				got.Transport != exp.Transport ||
				math.Abs(got.StraightDistanceKm-exp.StraightDistanceKm) > 1e-6 ||
				math.Abs(got.EstimatedDistanceKm-exp.EstimatedDistanceKm) > 1e-6 ||
				got.EstimatedDurationMinutes != exp.EstimatedDurationMinutes {
				t.Fatalf("%s leg[%d]: got %+v, want %+v", tc.Name, i, got, exp)
			}
		}
	}
}
