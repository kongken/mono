package snapshot

import (
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapInfo(in *tripmapv1.SnapshotInfo) *bfftripmapv1.SnapshotInfo {
	if in == nil {
		return nil
	}
	return &bfftripmapv1.SnapshotInfo{
		Id: in.GetId(), Reason: in.GetReason(), CreatedAt: in.GetCreatedAt(),
		VisitCount: in.GetVisitCount(), ProvinceMarkCount: in.GetProvinceMarkCount(),
		Revision: in.GetRevision(), TripCount: in.GetTripCount(), SchemaVersion: in.GetSchemaVersion(),
	}
}

func mapData(in *tripmapv1.SnapshotData) *bfftripmapv1.SnapshotData {
	if in == nil {
		return nil
	}
	out := &bfftripmapv1.SnapshotData{
		SchemaVersion: in.GetSchemaVersion(), RegionVersion: in.GetRegionVersion(),
	}
	for _, v := range in.GetVisits() {
		out.Visits = append(out.Visits, mapVisit(v))
	}
	for _, m := range in.GetProvinceMarks() {
		out.ProvinceMarks = append(out.ProvinceMarks, mapMark(m))
	}
	for _, t := range in.GetTrips() {
		out.Trips = append(out.Trips, mapTrip(t))
	}
	return out
}

func mapVisit(v *tripmapv1.Visit) *bfftripmapv1.Visit {
	if v == nil {
		return nil
	}
	return &bfftripmapv1.Visit{
		Code: v.GetCode(), Name: v.GetName(), Province: v.GetProvince(), Color: v.GetColor(),
		Date: v.GetDate(), Note: v.GetNote(), UpdatedAt: v.GetUpdatedAt(),
	}
}

func mapMark(m *tripmapv1.ProvinceMark) *bfftripmapv1.ProvinceMark {
	if m == nil {
		return nil
	}
	return &bfftripmapv1.ProvinceMark{Province: m.GetProvince(), Color: m.GetColor(), UpdatedAt: m.GetUpdatedAt()}
}

func mapTrip(t *tripmapv1.Trip) *bfftripmapv1.Trip {
	if t == nil {
		return nil
	}
	return &bfftripmapv1.Trip{
		Id: t.GetId(), City: t.GetRegionCode(), Date: t.GetDate(),
		Note: t.GetNote(), UpdatedAt: t.GetUpdatedAt(),
	}
}
