// Package report computes travel statistics, annual reports and achievements
// (F8) with the exact same definitions as src/lib/report.ts.
package report

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/kongken/mono/internal/tripmap/regions"
	"github.com/kongken/mono/internal/tripmap/share"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

const DefaultTimezone = "Asia/Shanghai"

type (
	ReportRecordRef        = tripmapv1.ReportRecordRef
	ReportProvinceProgress = tripmapv1.ReportProvinceProgress
	ReportOverview         = tripmapv1.ReportOverview
	AnnualReport           = tripmapv1.AnnualReport
	AchievementStatus      = tripmapv1.AchievementStatus
	Result                 = tripmapv1.GetReportResponse
)

type Options struct {
	Timezone string
	Year     int32
	Now      time.Time
	Revision int64
}

var districtDestinations = map[string]bool{
	"110000": true,
	"120000": true,
	"310000": true,
	"500000": true,
	"810000": true,
	"820000": true,
}

func isDistrictDestination(province string) bool {
	return districtDestinations[province]
}

// ResolveTimezone returns a valid IANA location and its canonical name,
// falling back to Asia/Shanghai when tz is empty or invalid.
func ResolveTimezone(tz string) (*time.Location, string) {
	trimmed := strings.TrimSpace(tz)
	if trimmed != "" {
		if loc, err := time.LoadLocation(trimmed); err == nil {
			return loc, trimmed
		}
	}
	if loc, err := time.LoadLocation(DefaultTimezone); err == nil {
		return loc, DefaultTimezone
	}
	return time.FixedZone(DefaultTimezone, 8*3600), DefaultTimezone
}

type normTrip struct {
	id           string
	date         string
	year         int32
	month        int32
	validDate    bool
	note         string
	noteLength   int32
	notePreview  string
	regionCode   string
	regionName   string
	provinceCode string
	provinceName string
}

type normCity struct {
	code         string
	name         string
	provinceCode string
	provinceName string
	destKey      string
	earliestDate string
	earliestTrip string
	trips        []normTrip
}

func parseValidDate(raw string) (string, int32, int32, bool) {
	d := strings.TrimSpace(raw)
	if len(d) != 10 || d[4] != '-' || d[7] != '-' {
		return "", 0, 0, false
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return "", 0, 0, false
	}
	y := t.Year()
	if y < 1900 || y > 9999 {
		return "", 0, 0, false
	}
	return d, int32(y), int32(t.Month()), true
}

func utf16Len(s string) int32 {
	return int32(len(utf16.Encode([]rune(s))))
}

func makeNotePreview(s string) string {
	clean := strings.Join(strings.Fields(s), " ")
	u16 := utf16.Encode([]rune(clean))
	if len(u16) <= 40 {
		return clean
	}
	return string(utf16.Decode(u16[:40])) + "…"
}

func compareDatesEmptyLast(a, b string) int {
	if (a == "") != (b == "") {
		if a == "" {
			return 1
		}
		return -1
	}
	return cmp.Compare(a, b)
}

func daysBetweenInclusive(startDate, endDate string) int32 {
	t1, err1 := time.Parse("2006-01-02", startDate)
	t2, err2 := time.Parse("2006-01-02", endDate)
	if err1 != nil || err2 != nil || t2.Before(t1) {
		return 1
	}
	return int32(t2.Sub(t1)/(24*time.Hour)) + 1
}

// Build computes the complete travel report, annual summaries and achievements.
func Build(idx *regions.Index, src share.Source, opts Options) *Result {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	loc, tzName := ResolveTimezone(opts.Timezone)
	localNow := now.In(loc)
	generatedOn := localNow.Format("2006-01-02")
	currentYear := int32(localNow.Year())

	totalProvinces := int32(idx.Provinces())
	var totalCities, totalRegions int32
	for _, pCode := range idx.ProvinceCodes() {
		rc := int32(idx.ProvinceCities(pCode))
		totalRegions += rc
		if isDistrictDestination(pCode) {
			totalCities++
		} else {
			totalCities += rc
		}
	}

	exploredSet := map[string]bool{}
	markedSet := map[string]bool{}
	for _, m := range src.Marks {
		if idx.HasProvince(m) {
			exploredSet[m] = true
			markedSet[m] = true
		}
	}

	cityByCode := map[string]*normCity{}
	for _, c := range src.Cities {
		cName, pCode, ok := idx.City(c.Code)
		if !ok {
			continue
		}
		pName, _ := idx.ProvinceName(pCode)
		exploredSet[pCode] = true
		nc, exists := cityByCode[c.Code]
		if !exists {
			destKey := c.Code
			if isDistrictDestination(pCode) {
				destKey = pCode
			}
			nc = &normCity{
				code:         c.Code,
				name:         cName,
				provinceCode: pCode,
				provinceName: pName,
				destKey:      destKey,
			}
			cityByCode[c.Code] = nc
		}
		for i, tr := range c.Trips {
			tid := strings.TrimSpace(tr.ID)
			if tid == "" {
				tid = c.Code + "-" + strconv.Itoa(i+1)
			}
			d, y, m, valid := parseValidDate(tr.Date)
			note := strings.TrimSpace(tr.Note)
			nt := normTrip{
				id:           tid,
				date:         d,
				year:         y,
				month:        m,
				validDate:    valid,
				note:         note,
				noteLength:   utf16Len(note),
				notePreview:  makeNotePreview(note),
				regionCode:   c.Code,
				regionName:   cName,
				provinceCode: pCode,
				provinceName: pName,
			}
			nc.trips = append(nc.trips, nt)
		}
	}

	cities := make([]*normCity, 0, len(cityByCode))
	for _, nc := range cityByCode {
		slices.SortFunc(nc.trips, func(a, b normTrip) int {
			if c := compareDatesEmptyLast(a.date, b.date); c != 0 {
				return c
			}
			return cmp.Compare(a.id, b.id)
		})
		for _, tr := range nc.trips {
			if tr.validDate {
				nc.earliestDate = tr.date
				nc.earliestTrip = tr.id
				break
			}
		}
		cities = append(cities, nc)
	}
	slices.SortFunc(cities, func(a, b *normCity) int {
		return cmp.Compare(a.code, b.code)
	})

	destSet := map[string]bool{}
	var totalTrips, datedTrips int32
	citiesByProvince := map[string][]*normCity{}
	firstRegionYear := map[string]int32{}
	firstDestDate := map[string]string{}
	firstDestYear := map[string]int32{}
	firstProvinceDate := map[string]string{}
	firstProvinceYear := map[string]int32{}
	yearSet := map[int32]bool{currentYear: true}
	var allDatedTrips []normTrip

	for _, nc := range cities {
		destSet[nc.destKey] = true
		citiesByProvince[nc.provinceCode] = append(citiesByProvince[nc.provinceCode], nc)
		totalTrips += int32(len(nc.trips))
		for _, tr := range nc.trips {
			if !tr.validDate {
				continue
			}
			datedTrips++
			yearSet[tr.year] = true
			allDatedTrips = append(allDatedTrips, tr)
			if prev, ok := firstRegionYear[nc.code]; !ok || tr.year < prev {
				firstRegionYear[nc.code] = tr.year
			}
			if prev, ok := firstDestDate[nc.destKey]; !ok || tr.date < prev {
				firstDestDate[nc.destKey] = tr.date
				firstDestYear[nc.destKey] = tr.year
			}
			if prev, ok := firstProvinceDate[nc.provinceCode]; !ok || tr.date < prev {
				firstProvinceDate[nc.provinceCode] = tr.date
				firstProvinceYear[nc.provinceCode] = tr.year
			}
		}
	}

	var provinceProgress []*ReportProvinceProgress
	for pCode := range exploredSet {
		pName, _ := idx.ProvinceName(pCode)
		pCities := citiesByProvince[pCode]
		refs := make([]*ReportRecordRef, 0, len(pCities))
		for _, nc := range pCities {
			refs = append(refs, &ReportRecordRef{
				Kind:         "city",
				TripId:       nc.earliestTrip,
				RegionCode:   nc.code,
				RegionName:   nc.name,
				ProvinceCode: nc.provinceCode,
				ProvinceName: nc.provinceName,
				Date:         nc.earliestDate,
				TripCount:    int32(len(nc.trips)),
			})
		}
		provinceProgress = append(provinceProgress, &ReportProvinceProgress{
			ProvinceCode:   pCode,
			ProvinceName:   pName,
			VisitedCities:  int32(len(pCities)),
			TotalCities:    int32(idx.ProvinceCities(pCode)),
			MarkedOnly:     len(pCities) == 0 && markedSet[pCode],
			VisitedRegions: refs,
		})
	}
	slices.SortFunc(provinceProgress, func(a, b *ReportProvinceProgress) int {
		lhs := int64(a.VisitedCities) * int64(b.TotalCities)
		rhs := int64(b.VisitedCities) * int64(a.TotalCities)
		if lhs != rhs {
			return cmp.Compare(rhs, lhs)
		}
		if a.VisitedCities != b.VisitedCities {
			return cmp.Compare(b.VisitedCities, a.VisitedCities)
		}
		return cmp.Compare(a.ProvinceCode, b.ProvinceCode)
	})

	overview := &ReportOverview{
		ExploredProvinces: int32(len(exploredSet)),
		TotalProvinces:    totalProvinces,
		VisitedCities:     int32(len(destSet)),
		TotalCities:       totalCities,
		VisitedRegions:    int32(len(cities)),
		TotalRegions:      totalRegions,
		TotalTrips:        totalTrips,
		DatedTrips:        datedTrips,
		UndatedTrips:      totalTrips - datedTrips,
		ProvinceProgress:  provinceProgress,
	}

	availableYears := make([]int32, 0, len(yearSet))
	for y := range yearSet {
		availableYears = append(availableYears, y)
	}
	slices.SortFunc(availableYears, func(a, b int32) int {
		return cmp.Compare(b, a)
	})

	annualReports := make([]*AnnualReport, 0, len(availableYears))
	for _, y := range availableYears {
		annualReports = append(annualReports, buildAnnualReport(y, allDatedTrips, cityByCode, firstRegionYear, firstDestYear, firstProvinceYear))
	}

	selectedYear := currentYear
	if opts.Year > 0 && slices.Contains(availableYears, opts.Year) {
		selectedYear = opts.Year
	} else {
		for _, ar := range annualReports {
			if ar.TripCount > 0 {
				selectedYear = ar.Year
				break
			}
		}
	}

	achievements := buildAchievements(idx, overview, cities, exploredSet)

	return &Result{
		Revision:       opts.Revision,
		Timezone:       tzName,
		GeneratedOn:    generatedOn,
		CurrentYear:    currentYear,
		SelectedYear:   selectedYear,
		AvailableYears: availableYears,
		Overview:       overview,
		AnnualReports:  annualReports,
		Achievements:   achievements,
	}
}

func tripToRef(tr normTrip) *ReportRecordRef {
	return &ReportRecordRef{
		Kind:         "trip",
		TripId:       tr.id,
		RegionCode:   tr.regionCode,
		RegionName:   tr.regionName,
		ProvinceCode: tr.provinceCode,
		ProvinceName: tr.provinceName,
		Date:         tr.date,
		NoteLength:   tr.noteLength,
		NotePreview:  tr.notePreview,
	}
}

func buildAnnualReport(
	year int32,
	allDatedTrips []normTrip,
	cityByCode map[string]*normCity,
	firstRegionYear map[string]int32,
	firstDestYear map[string]int32,
	firstProvinceYear map[string]int32,
) *AnnualReport {
	var yearTrips []normTrip
	for _, tr := range allDatedTrips {
		if tr.year == year {
			yearTrips = append(yearTrips, tr)
		}
	}
	slices.SortFunc(yearTrips, func(a, b normTrip) int {
		if c := cmp.Compare(a.date, b.date); c != 0 {
			return c
		}
		if c := cmp.Compare(a.regionCode, b.regionCode); c != 0 {
			return c
		}
		return cmp.Compare(a.id, b.id)
	})

	ar := &AnnualReport{Year: year}
	if len(yearTrips) == 0 {
		return ar
	}

	destInYear := map[string]bool{}
	newDestInYear := map[string]bool{}
	regionCountInYear := map[string]int32{}
	regionFirstTripInYear := map[string]normTrip{}
	provinceCountInYear := map[string]int32{}
	provinceFirstTripInYear := map[string]normTrip{}
	newProvinceInYear := map[string]bool{}
	monthCounts := map[int32]int32{}

	tripRefs := make([]*ReportRecordRef, 0, len(yearTrips))
	for _, tr := range yearTrips {
		tripRefs = append(tripRefs, tripToRef(tr))
		nc := cityByCode[tr.regionCode]
		destInYear[nc.destKey] = true
		if firstDestYear[nc.destKey] == year {
			newDestInYear[nc.destKey] = true
		}
		regionCountInYear[tr.regionCode]++
		if _, ok := regionFirstTripInYear[tr.regionCode]; !ok {
			regionFirstTripInYear[tr.regionCode] = tr
		}
		provinceCountInYear[tr.provinceCode]++
		if _, ok := provinceFirstTripInYear[tr.provinceCode]; !ok {
			provinceFirstTripInYear[tr.provinceCode] = tr
		}
		if firstProvinceYear[tr.provinceCode] == year {
			newProvinceInYear[tr.provinceCode] = true
		}
		monthCounts[tr.month]++
	}

	var newRegions []*ReportRecordRef
	var mostVisited *ReportRecordRef
	for code, count := range regionCountInYear {
		firstTr := regionFirstTripInYear[code]
		nc := cityByCode[code]
		if firstRegionYear[code] == year {
			newRegions = append(newRegions, &ReportRecordRef{
				Kind:         "city",
				TripId:        nc.earliestTrip,
				RegionCode:   code,
				RegionName:   nc.name,
				ProvinceCode: nc.provinceCode,
				ProvinceName: nc.provinceName,
				Date:         nc.earliestDate,
				TripCount:    count,
			})
		}
		candidate := &ReportRecordRef{
			Kind:         "city",
			TripId:       firstTr.id,
			RegionCode:   code,
			RegionName:   nc.name,
			ProvinceCode: nc.provinceCode,
			ProvinceName: nc.provinceName,
			Date:         firstTr.date,
			TripCount:    count,
		}
		if mostVisited == nil ||
			candidate.TripCount > mostVisited.TripCount ||
			(candidate.TripCount == mostVisited.TripCount && candidate.Date < mostVisited.Date) ||
			(candidate.TripCount == mostVisited.TripCount && candidate.Date == mostVisited.Date && candidate.RegionCode < mostVisited.RegionCode) {
			mostVisited = candidate
		}
	}
	slices.SortFunc(newRegions, func(a, b *ReportRecordRef) int {
		if c := cmp.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.RegionCode, b.RegionCode)
	})

	var provinces []*ReportRecordRef
	for pCode, count := range provinceCountInYear {
		firstTr := provinceFirstTripInYear[pCode]
		provinces = append(provinces, &ReportRecordRef{
			Kind:         "province",
			TripId:       firstTr.id,
			RegionCode:   firstTr.regionCode,
			RegionName:   firstTr.regionName,
			ProvinceCode: pCode,
			ProvinceName: firstTr.provinceName,
			Date:         firstTr.date,
			TripCount:    count,
		})
	}
	slices.SortFunc(provinces, func(a, b *ReportRecordRef) int {
		if a.TripCount != b.TripCount {
			return cmp.Compare(b.TripCount, a.TripCount)
		}
		return cmp.Compare(a.ProvinceCode, b.ProvinceCode)
	})

	bestNoteTrip := yearTrips[0]
	for _, tr := range yearTrips[1:] {
		if tr.noteLength > bestNoteTrip.noteLength ||
			(tr.noteLength == bestNoteTrip.noteLength && tr.date > bestNoteTrip.date) ||
			(tr.noteLength == bestNoteTrip.noteLength && tr.date == bestNoteTrip.date && tr.regionCode < bestNoteTrip.regionCode) ||
			(tr.noteLength == bestNoteTrip.noteLength && tr.date == bestNoteTrip.date && tr.regionCode == bestNoteTrip.regionCode && tr.id < bestNoteTrip.id) {
			bestNoteTrip = tr
		}
	}

	var busiestMonth, busiestMonthTrips int32
	for m := int32(1); m <= 12; m++ {
		if monthCounts[m] > busiestMonthTrips {
			busiestMonth = m
			busiestMonthTrips = monthCounts[m]
		}
	}

	earliest := tripRefs[0]
	latest := tripRefs[len(tripRefs)-1]

	ar.TripCount = int32(len(yearTrips))
	ar.CityCount = int32(len(destInYear))
	ar.RegionCount = int32(len(regionCountInYear))
	ar.NewCityCount = int32(len(newDestInYear))
	ar.NewRegionCount = int32(len(newRegions))
	ar.ProvinceCount = int32(len(provinces))
	ar.NewProvinceCount = int32(len(newProvinceInYear))
	ar.SpanDays = daysBetweenInclusive(earliest.Date, latest.Date)
	ar.BusiestMonth = busiestMonth
	ar.BusiestMonthTrips = busiestMonthTrips
	ar.EarliestTrip = earliest
	ar.LatestTrip = latest
	ar.LongestNoteTrip = tripToRef(bestNoteTrip)
	ar.MostVisitedCity = mostVisited
	ar.NewRegions = newRegions
	ar.Provinces = provinces
	ar.Trips = tripRefs
	return ar
}

func buildMilestoneAchievement(id, title, description string, target int32, items []*ReportRecordRef) *AchievementStatus {
	current := int32(len(items))
	unlocked := current >= target
	var unlockedAt string
	var evidence []*ReportRecordRef
	if unlocked {
		unlockedAt = items[target-1].Date
		if target <= 5 {
			evidence = slices.Clone(items[:target])
		} else {
			evidence = []*ReportRecordRef{items[0], items[1], items[2], items[3], items[target-1]}
		}
	} else if len(items) > 0 {
		limit := min(len(items), 5)
		evidence = slices.Clone(items[:limit])
	}
	return &AchievementStatus{
		Id:          id,
		Title:       title,
		Description: description,
		Unlocked:    unlocked,
		Current:     current,
		Target:      target,
		UnlockedAt:  unlockedAt,
		Evidence:    evidence,
	}
}

func buildAchievements(idx *regions.Index, overview *ReportOverview, cities []*normCity, exploredSet map[string]bool) []*AchievementStatus {
	citiesByProvince := map[string][]*normCity{}
	destBest := map[string]*ReportRecordRef{}
	var richNoteTrips []*ReportRecordRef

	for _, nc := range cities {
		citiesByProvince[nc.provinceCode] = append(citiesByProvince[nc.provinceCode], nc)
		cityRef := &ReportRecordRef{
			Kind:         "city",
			TripId:       nc.earliestTrip,
			RegionCode:   nc.code,
			RegionName:   nc.name,
			ProvinceCode: nc.provinceCode,
			ProvinceName: nc.provinceName,
			Date:         nc.earliestDate,
			TripCount:    int32(len(nc.trips)),
		}
		if prev, ok := destBest[nc.destKey]; !ok {
			destBest[nc.destKey] = cityRef
		} else {
			prev.TripCount += cityRef.TripCount
			if compareDatesEmptyLast(cityRef.Date, prev.Date) < 0 ||
				(cityRef.Date == prev.Date && cityRef.RegionCode < prev.RegionCode) {
				prev.TripId = cityRef.TripId
				prev.RegionCode = cityRef.RegionCode
				prev.RegionName = cityRef.RegionName
				prev.Date = cityRef.Date
			}
		}
		for _, tr := range nc.trips {
			if tr.noteLength >= 10 {
				richNoteTrips = append(richNoteTrips, tripToRef(tr))
			}
		}
	}

	sortedProvinces := make([]*ReportRecordRef, 0, len(exploredSet))
	var completedProvinces []*ReportRecordRef
	for pCode := range exploredSet {
		pName, _ := idx.ProvinceName(pCode)
		pCities := citiesByProvince[pCode]
		var earliestDate, earliestTrip, earliestRegionCode, earliestRegionName string
		var latestCityFirstDate string
		var totalProvinceTrips int32
		for _, nc := range pCities {
			totalProvinceTrips += int32(len(nc.trips))
			if nc.earliestDate != "" {
				if earliestDate == "" || nc.earliestDate < earliestDate || (nc.earliestDate == earliestDate && nc.code < earliestRegionCode) {
					earliestDate = nc.earliestDate
					earliestTrip = nc.earliestTrip
					earliestRegionCode = nc.code
					earliestRegionName = nc.name
				}
				if nc.earliestDate > latestCityFirstDate {
					latestCityFirstDate = nc.earliestDate
				}
			}
		}
		sortedProvinces = append(sortedProvinces, &ReportRecordRef{
			Kind:         "province",
			TripId:       earliestTrip,
			RegionCode:   earliestRegionCode,
			RegionName:   earliestRegionName,
			ProvinceCode: pCode,
			ProvinceName: pName,
			Date:         earliestDate,
			TripCount:    totalProvinceTrips,
		})
		totalInProv := idx.ProvinceCities(pCode)
		if totalInProv > 0 && len(pCities) == totalInProv {
			completedProvinces = append(completedProvinces, &ReportRecordRef{
				Kind:         "province",
				TripId:       earliestTrip,
				RegionCode:   earliestRegionCode,
				RegionName:   earliestRegionName,
				ProvinceCode: pCode,
				ProvinceName: pName,
				Date:         latestCityFirstDate,
				TripCount:    int32(len(pCities)),
			})
		}
	}
	slices.SortFunc(sortedProvinces, func(a, b *ReportRecordRef) int {
		if c := compareDatesEmptyLast(a.Date, b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.ProvinceCode, b.ProvinceCode)
	})
	slices.SortFunc(completedProvinces, func(a, b *ReportRecordRef) int {
		if c := compareDatesEmptyLast(a.Date, b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.ProvinceCode, b.ProvinceCode)
	})

	sortedDestinations := make([]*ReportRecordRef, 0, len(destBest))
	for _, ref := range destBest {
		sortedDestinations = append(sortedDestinations, ref)
	}
	slices.SortFunc(sortedDestinations, func(a, b *ReportRecordRef) int {
		if c := compareDatesEmptyLast(a.Date, b.Date); c != 0 {
			return c
		}
		return cmp.Compare(a.RegionCode, b.RegionCode)
	})

	slices.SortFunc(richNoteTrips, func(a, b *ReportRecordRef) int {
		if c := compareDatesEmptyLast(a.Date, b.Date); c != 0 {
			return c
		}
		if c := cmp.Compare(a.RegionCode, b.RegionCode); c != 0 {
			return c
		}
		return cmp.Compare(a.TripId, b.TripId)
	})

	var bestRepeatCity *normCity
	for _, nc := range cities {
		if len(nc.trips) == 0 {
			continue
		}
		if bestRepeatCity == nil || len(nc.trips) > len(bestRepeatCity.trips) {
			bestRepeatCity = nc
			continue
		}
		if len(nc.trips) == len(bestRepeatCity.trips) {
			idxCmp := min(len(nc.trips), 3) - 1
			if c := compareDatesEmptyLast(nc.trips[idxCmp].date, bestRepeatCity.trips[idxCmp].date); c < 0 ||
				(c == 0 && nc.code < bestRepeatCity.code) {
				bestRepeatCity = nc
			}
		}
	}
	multiTripAch := &AchievementStatus{
		Id:          "multi_trip_city",
		Title:       "故地重游",
		Description: "在同一座城市或区县记录至少 3 次旅行",
		Target:      3,
	}
	if bestRepeatCity != nil {
		multiTripAch.Current = int32(len(bestRepeatCity.trips))
		multiTripAch.Unlocked = multiTripAch.Current >= 3
		if multiTripAch.Unlocked {
			multiTripAch.UnlockedAt = bestRepeatCity.trips[2].date
		}
		limit := min(len(bestRepeatCity.trips), 5)
		for _, tr := range bestRepeatCity.trips[:limit] {
			multiTripAch.Evidence = append(multiTripAch.Evidence, tripToRef(tr))
		}
	}

	return []*AchievementStatus{
		buildMilestoneAchievement("first_footprint", "初踏山海", "点亮第 1 个省级区域（含城市打卡或省份独立标记）", 1, sortedProvinces),
		buildMilestoneAchievement("ten_cities", "十城足迹", "按全国城市口径累计打卡达到 10 座城市", 10, sortedDestinations),
		buildMilestoneAchievement("fifty_cities", "半百风物", "按全国城市口径累计打卡达到 50 座城市", 50, sortedDestinations),
		buildMilestoneAchievement("half_china", "半壁山河", "已探索省级区域达到 17 个（全国 34 个省级区域的一半）", 17, sortedProvinces),
		buildMilestoneAchievement("all_china", "九州同游", "点亮全国全部 34 个省级区域", 34, sortedProvinces),
		buildMilestoneAchievement("province_master", "全省制霸", "在任意一个省级区域打卡其全部可记录城市或区县（不含仅省份标记）", 1, completedProvinces),
		multiTripAch,
		buildMilestoneAchievement("chronicler", "山海执笔人", "累计撰写 5 条不少于 10 字的旅行记忆", 5, richNoteTrips),
	}
}
