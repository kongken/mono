package report

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/share"
)

type reportFixture struct {
	Cases []struct {
		Name     string   `json:"name"`
		Now      string   `json:"now"`
		Timezone string   `json:"timezone"`
		Marks    []string `json:"marks"`
		Cities   []struct {
			Code     string `json:"code"`
			Province string `json:"province"`
			Trips    []struct {
				ID   string `json:"id"`
				Date string `json:"date"`
				Note string `json:"note"`
			} `json:"trips"`
		} `json:"cities"`
		Expect struct {
			Timezone       string  `json:"timezone"`
			GeneratedOn    string  `json:"generatedOn"`
			CurrentYear    int32   `json:"currentYear"`
			SelectedYear   int32   `json:"selectedYear"`
			AvailableYears []int32 `json:"availableYears"`
			Overview       struct {
				ExploredProvinces int32 `json:"exploredProvinces"`
				TotalProvinces    int32 `json:"totalProvinces"`
				VisitedCities     int32 `json:"visitedCities"`
				TotalCities       int32 `json:"totalCities"`
				VisitedRegions    int32 `json:"visitedRegions"`
				TotalRegions      int32 `json:"totalRegions"`
				TotalTrips        int32 `json:"totalTrips"`
				DatedTrips        int32 `json:"datedTrips"`
				UndatedTrips      int32 `json:"undatedTrips"`
			} `json:"overview"`
			TopProvinceCode string `json:"topProvinceCode"`
			Year2026        *struct {
				TripCount           int32  `json:"tripCount"`
				CityCount           int32  `json:"cityCount"`
				RegionCount         int32  `json:"regionCount"`
				NewCityCount        int32  `json:"newCityCount"`
				NewRegionCount      int32  `json:"newRegionCount"`
				ProvinceCount       int32  `json:"provinceCount"`
				NewProvinceCount    int32  `json:"newProvinceCount"`
				SpanDays            int32  `json:"spanDays"`
				BusiestMonth        int32  `json:"busiestMonth"`
				BusiestMonthTrips   int32  `json:"busiestMonthTrips"`
				EarliestTripID      string `json:"earliestTripId"`
				LatestTripID        string `json:"latestTripId"`
				LongestNoteTripID   string `json:"longestNoteTripId"`
				MostVisitedCityCode string `json:"mostVisitedCityCode"`
			} `json:"year2026"`
			UnlockedAchievements []struct {
				ID                  string `json:"id"`
				Current             int32  `json:"current"`
				Target              int32  `json:"target"`
				UnlockedAt          string `json:"unlockedAt"`
				FirstEvidenceCode   string `json:"firstEvidenceCode"`
				FirstEvidenceTripID string `json:"firstEvidenceTripId"`
			} `json:"unlockedAchievements"`
		} `json:"expect"`
	} `json:"cases"`
}

func TestBuildMatchesSharedFrontendFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../packages/fixtures/report-stats.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx reportFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range fx.Cases {
		now, err := time.Parse(time.RFC3339, tc.Now)
		if err != nil {
			t.Fatalf("%s: parse now: %v", tc.Name, err)
		}
		src := share.Source{Marks: tc.Marks}
		for _, c := range tc.Cities {
			sc := share.SourceCity{Code: c.Code, Province: c.Province}
			for _, tr := range c.Trips {
				sc.Trips = append(sc.Trips, share.SourceTrip{ID: tr.ID, Date: tr.Date, Note: tr.Note})
			}
			src.Cities = append(src.Cities, sc)
		}
		got := Build(idx, src, Options{
			Timezone: tc.Timezone,
			Now:      now,
		})

		if got.Timezone != tc.Expect.Timezone || got.GeneratedOn != tc.Expect.GeneratedOn || got.CurrentYear != tc.Expect.CurrentYear || got.SelectedYear != tc.Expect.SelectedYear {
			t.Fatalf("%s: header got tz=%s date=%s cur=%d sel=%d, want tz=%s date=%s cur=%d sel=%d",
				tc.Name, got.Timezone, got.GeneratedOn, got.CurrentYear, got.SelectedYear,
				tc.Expect.Timezone, tc.Expect.GeneratedOn, tc.Expect.CurrentYear, tc.Expect.SelectedYear)
		}
		if !slices.Equal(got.AvailableYears, tc.Expect.AvailableYears) {
			t.Fatalf("%s: available years %v, want %v", tc.Name, got.AvailableYears, tc.Expect.AvailableYears)
		}
		ov := got.Overview
		if ov.ExploredProvinces != tc.Expect.Overview.ExploredProvinces ||
			ov.TotalProvinces != tc.Expect.Overview.TotalProvinces ||
			ov.VisitedCities != tc.Expect.Overview.VisitedCities ||
			ov.TotalCities != tc.Expect.Overview.TotalCities ||
			ov.VisitedRegions != tc.Expect.Overview.VisitedRegions ||
			ov.TotalRegions != tc.Expect.Overview.TotalRegions ||
			ov.TotalTrips != tc.Expect.Overview.TotalTrips ||
			ov.DatedTrips != tc.Expect.Overview.DatedTrips ||
			ov.UndatedTrips != tc.Expect.Overview.UndatedTrips {
			t.Fatalf("%s: overview %+v, want %+v", tc.Name, ov, tc.Expect.Overview)
		}
		if tc.Expect.TopProvinceCode != "" {
			if len(ov.ProvinceProgress) == 0 || ov.ProvinceProgress[0].ProvinceCode != tc.Expect.TopProvinceCode {
				t.Fatalf("%s: top province %+v, want %s", tc.Name, ov.ProvinceProgress, tc.Expect.TopProvinceCode)
			}
		}

		if tc.Expect.Year2026 != nil {
			var y2026 *AnnualReport
			for _, ar := range got.AnnualReports {
				if ar.Year == 2026 {
					y2026 = ar
					break
				}
			}
			if y2026 == nil {
				t.Fatalf("%s: missing 2026 annual report", tc.Name)
			}
			want := tc.Expect.Year2026
			if y2026.TripCount != want.TripCount ||
				y2026.CityCount != want.CityCount ||
				y2026.RegionCount != want.RegionCount ||
				y2026.NewCityCount != want.NewCityCount ||
				y2026.NewRegionCount != want.NewRegionCount ||
				y2026.ProvinceCount != want.ProvinceCount ||
				y2026.NewProvinceCount != want.NewProvinceCount ||
				y2026.SpanDays != want.SpanDays ||
				y2026.BusiestMonth != want.BusiestMonth ||
				y2026.BusiestMonthTrips != want.BusiestMonthTrips {
				t.Fatalf("%s: 2026 report %+v, want %+v", tc.Name, y2026, want)
			}
			if y2026.EarliestTrip.GetTripId() != want.EarliestTripID ||
				y2026.LatestTrip.GetTripId() != want.LatestTripID ||
				y2026.LongestNoteTrip.GetTripId() != want.LongestNoteTripID ||
				y2026.MostVisitedCity.GetRegionCode() != want.MostVisitedCityCode {
				t.Fatalf("%s: 2026 highlights earliest=%s latest=%s longest=%s most=%s",
					tc.Name, y2026.EarliestTrip.GetTripId(), y2026.LatestTrip.GetTripId(),
					y2026.LongestNoteTrip.GetTripId(), y2026.MostVisitedCity.GetRegionCode())
			}
		}

		var unlockedCount int
		for _, ach := range got.Achievements {
			if !ach.Unlocked {
				continue
			}
			unlockedCount++
		}
		if unlockedCount != len(tc.Expect.UnlockedAchievements) {
			t.Fatalf("%s: unlocked count %d, want %d", tc.Name, unlockedCount, len(tc.Expect.UnlockedAchievements))
		}
		for _, exp := range tc.Expect.UnlockedAchievements {
			var found bool
			for _, ach := range got.Achievements {
				if ach.Id != exp.ID {
					continue
				}
				found = true
				if !ach.Unlocked || ach.Current != exp.Current || ach.Target != exp.Target || ach.UnlockedAt != exp.UnlockedAt {
					t.Fatalf("%s/%s: got %+v, want %+v", tc.Name, exp.ID, ach, exp)
				}
				if len(ach.Evidence) == 0 {
					t.Fatalf("%s/%s: expected traceable evidence", tc.Name, exp.ID)
				}
				if exp.FirstEvidenceCode != "" && ach.Evidence[0].ProvinceCode != exp.FirstEvidenceCode {
					t.Fatalf("%s/%s: first evidence province %s, want %s", tc.Name, exp.ID, ach.Evidence[0].ProvinceCode, exp.FirstEvidenceCode)
				}
				if exp.FirstEvidenceTripID != "" && ach.Evidence[0].TripId != exp.FirstEvidenceTripID {
					t.Fatalf("%s/%s: first evidence trip %s, want %s", tc.Name, exp.ID, ach.Evidence[0].TripId, exp.FirstEvidenceTripID)
				}
			}
			if !found {
				t.Fatalf("%s: achievement %s not found", tc.Name, exp.ID)
			}
		}
	}
}

func TestVisitingEveryRegionUnlocksAllAchievements(t *testing.T) {
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	var rawProvinces []struct {
		Code   string `json:"code"`
		Cities []struct {
			Code string `json:"code"`
		} `json:"cities"`
	}
	raw, err := os.ReadFile("../../../packages/geo-data/data/regions.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rawProvinces); err != nil {
		t.Fatal(err)
	}
	var src share.Source
	for pi, p := range rawProvinces {
		for ci, c := range p.Cities {
			trips := []share.SourceTrip{{
				ID:   "t-" + c.Code + "-1",
				Date: time.Date(2026, time.Month((pi%12)+1), (ci%28)+1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
				Note: "走遍山海的完整旅行手账记录",
			}}
			if c.Code == "330100" {
				trips = append(trips,
					share.SourceTrip{ID: "t-330100-2", Date: "2026-06-01", Note: "第二次来到杭州西湖"},
					share.SourceTrip{ID: "t-330100-3", Date: "2026-09-01", Note: "第三次来到杭州西湖"},
				)
			}
			src.Cities = append(src.Cities, share.SourceCity{Code: c.Code, Province: p.Code, Trips: trips})
		}
	}
	res := Build(idx, src, Options{Timezone: "Asia/Shanghai", Now: time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)})
	if res.Overview.ExploredProvinces != 34 || res.Overview.VisitedCities != 391 || res.Overview.VisitedRegions != 497 {
		t.Fatalf("unexpected overview totals: %+v", res.Overview)
	}
	for _, ach := range res.Achievements {
		if !ach.Unlocked || len(ach.Evidence) == 0 || ach.UnlockedAt == "" {
			t.Fatalf("expected achievement %s to be unlocked with evidence, got %+v", ach.Id, ach)
		}
	}
}
