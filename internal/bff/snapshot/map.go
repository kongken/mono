package snapshot

import (
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapInfo(in *tripmapv1.SnapshotInfo) *bffv1.SnapshotInfo {
	if in == nil {
		return nil
	}
	return &bffv1.SnapshotInfo{
		Id: in.GetId(), Reason: in.GetReason(), CreatedAt: in.GetCreatedAt(),
		VisitCount: in.GetVisitCount(), ProvinceMarkCount: in.GetProvinceMarkCount(),
		Revision: in.GetRevision(), TripCount: in.GetTripCount(), SchemaVersion: in.GetSchemaVersion(),
	}
}

func mapData(in *tripmapv1.SnapshotData) *bffv1.SnapshotData {
	if in == nil {
		return nil
	}
	out := &bffv1.SnapshotData{
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

func mapVisit(v *tripmapv1.Visit) *bffv1.Visit {
	if v == nil {
		return nil
	}
	return &bffv1.Visit{
		Code: v.GetCode(), Name: v.GetName(), Province: v.GetProvince(), Color: v.GetColor(),
		Date: v.GetDate(), Note: v.GetNote(), UpdatedAt: v.GetUpdatedAt(),
	}
}

func mapMark(m *tripmapv1.ProvinceMark) *bffv1.ProvinceMark {
	if m == nil {
		return nil
	}
	return &bffv1.ProvinceMark{Province: m.GetProvince(), Color: m.GetColor(), UpdatedAt: m.GetUpdatedAt()}
}

func mapTrip(t *tripmapv1.Trip) *bffv1.Trip {
	if t == nil {
		return nil
	}
	return &bffv1.Trip{
		Id: t.GetId(), City: t.GetRegionCode(), Date: t.GetDate(),
		Note: t.GetNote(), UpdatedAt: t.GetUpdatedAt(),
	}
}
