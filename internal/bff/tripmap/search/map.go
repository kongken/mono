package search

import (
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapSearchResponse(in *tripmapv1.SearchNotesResponse) *bfftripmapv1.SearchNotesResponse {
	if in == nil {
		return &bfftripmapv1.SearchNotesResponse{}
	}
	out := &bfftripmapv1.SearchNotesResponse{
		Revision: in.GetRevision(), NextOffset: in.GetNextOffset(), HasMore: in.GetHasMore(),
	}
	for _, m := range in.GetMatches() {
		out.Matches = append(out.Matches, &bfftripmapv1.NoteSearchMatch{
			RegionCode: m.GetRegionCode(), RegionName: m.GetRegionName(), ProvinceCode: m.GetProvinceCode(),
			TripId: m.GetTripId(), Date: m.GetDate(), Note: m.GetNote(),
		})
	}
	return out
}
