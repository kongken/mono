package application

import (
	"testing"
	"time"

	"go.orx.me/apps/trip-map/internal/ent"
)

func TestLegacyIslandSnapshot(t *testing.T) {
	for code, name := range map[string]string{"tw-09020": "金门县", "tw-09007": "连江县"} {
		row := &ent.CityVisit{RegionCode: code, RegionName: name, ProvinceCode: "710000", Color: "#719fc6", TravelDate: "2026-10-12", Note: "旧旅行记忆", UpdatedAt: time.Now()}
		visit := toVisit(row)
		if visit.Province != "350000" || visit.Code != code || visit.Name != name || visit.Color != row.Color || visit.Date != row.TravelDate || visit.Note != row.Note || !visit.UpdatedAt.AsTime().Equal(row.UpdatedAt) {
			t.Fatalf("snapshot lost legacy data: %v", visit)
		}
		if row.ProvinceCode != "710000" {
			t.Fatal("reading a snapshot must not mutate the stored row")
		}
	}
}
