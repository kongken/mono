package footprint

import (
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapFootprints(in *tripmapv1.GetFootprintsResponse) *bfftripmapv1.GetFootprintsResponse {
	out := &bfftripmapv1.GetFootprintsResponse{
		Revision: in.GetRevision(), RegionVersion: in.GetRegionVersion(), SchemaVersion: in.GetSchemaVersion(),
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

func toVisit(v *bfftripmapv1.Visit) *tripmapv1.Visit {
	if v == nil {
		return nil
	}
	return &tripmapv1.Visit{
		Code: v.GetCode(), Name: v.GetName(), Province: v.GetProvince(), Color: v.GetColor(),
		Date: v.GetDate(), Note: v.GetNote(), UpdatedAt: v.GetUpdatedAt(),
	}
}

func toMark(m *bfftripmapv1.ProvinceMark) *tripmapv1.ProvinceMark {
	if m == nil {
		return nil
	}
	return &tripmapv1.ProvinceMark{Province: m.GetProvince(), Color: m.GetColor(), UpdatedAt: m.GetUpdatedAt()}
}

func toTrip(t *bfftripmapv1.Trip) *tripmapv1.Trip {
	if t == nil {
		return nil
	}
	return &tripmapv1.Trip{
		Id: t.GetId(), RegionCode: t.GetCity(), Date: t.GetDate(),
		Note: t.GetNote(), UpdatedAt: t.GetUpdatedAt(),
	}
}

func mapChanges(in *tripmapv1.GetChangesResponse) *bfftripmapv1.GetChangesResponse {
	out := &bfftripmapv1.GetChangesResponse{
		NextRevision: in.GetNextRevision(), CurrentRevision: in.GetCurrentRevision(),
		HasMore: in.GetHasMore(), ResyncRequired: in.GetResyncRequired(),
		SchemaVersion: in.GetSchemaVersion(), RegionVersion: in.GetRegionVersion(),
	}
	for _, c := range in.GetChanges() {
		out.Changes = append(out.Changes, &bfftripmapv1.FootprintChange{Revision: c.GetRevision(), Mutation: mapMutation(c.GetMutation())})
	}
	return out
}

func mapMutation(m *tripmapv1.FootprintMutation) *bfftripmapv1.FootprintMutation {
	if m == nil {
		return nil
	}
	out := &bfftripmapv1.FootprintMutation{}
	switch op := m.Operation.(type) {
	case *tripmapv1.FootprintMutation_UpsertVisit:
		out.Operation = &bfftripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: mapVisit(op.UpsertVisit)}
	case *tripmapv1.FootprintMutation_DeleteVisit:
		out.Operation = &bfftripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: op.DeleteVisit}
	case *tripmapv1.FootprintMutation_UpsertMark:
		out.Operation = &bfftripmapv1.FootprintMutation_UpsertMark{UpsertMark: mapMark(op.UpsertMark)}
	case *tripmapv1.FootprintMutation_DeleteMark:
		out.Operation = &bfftripmapv1.FootprintMutation_DeleteMark{DeleteMark: op.DeleteMark}
	case *tripmapv1.FootprintMutation_UpsertCity:
		out.Operation = &bfftripmapv1.FootprintMutation_UpsertCity{UpsertCity: mapVisit(op.UpsertCity)}
	case *tripmapv1.FootprintMutation_UpsertTrip:
		out.Operation = &bfftripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: mapTrip(op.UpsertTrip)}
	case *tripmapv1.FootprintMutation_DeleteTrip:
		out.Operation = &bfftripmapv1.FootprintMutation_DeleteTrip{DeleteTrip: &bfftripmapv1.DeleteTrip{City: op.DeleteTrip.GetRegionCode(), Id: op.DeleteTrip.GetId()}}
	}
	return out
}

func toMutations(in []*bfftripmapv1.FootprintMutation) []*tripmapv1.FootprintMutation {
	out := make([]*tripmapv1.FootprintMutation, len(in))
	for i, m := range in {
		out[i] = toMutation(m)
	}
	return out
}

func toMutation(m *bfftripmapv1.FootprintMutation) *tripmapv1.FootprintMutation {
	if m == nil {
		return nil
	}
	out := &tripmapv1.FootprintMutation{}
	switch op := m.Operation.(type) {
	case *bfftripmapv1.FootprintMutation_UpsertVisit:
		out.Operation = &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: toVisit(op.UpsertVisit)}
	case *bfftripmapv1.FootprintMutation_DeleteVisit:
		out.Operation = &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: op.DeleteVisit}
	case *bfftripmapv1.FootprintMutation_UpsertMark:
		out.Operation = &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: toMark(op.UpsertMark)}
	case *bfftripmapv1.FootprintMutation_DeleteMark:
		out.Operation = &tripmapv1.FootprintMutation_DeleteMark{DeleteMark: op.DeleteMark}
	case *bfftripmapv1.FootprintMutation_UpsertCity:
		out.Operation = &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: toVisit(op.UpsertCity)}
	case *bfftripmapv1.FootprintMutation_UpsertTrip:
		out.Operation = &tripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: toTrip(op.UpsertTrip)}
	case *bfftripmapv1.FootprintMutation_DeleteTrip:
		out.Operation = &tripmapv1.FootprintMutation_DeleteTrip{DeleteTrip: &tripmapv1.DeleteTrip{RegionCode: op.DeleteTrip.GetCity(), Id: op.DeleteTrip.GetId()}}
	}
	return out
}
