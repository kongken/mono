package media

import (
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

func mapMedia(in *tripmapv1.MediaInfo) *bffv1.MediaInfo {
	if in == nil {
		return nil
	}
	return &bffv1.MediaInfo{
		Id: in.GetId(), RegionCode: in.GetRegionCode(), Filename: in.GetFilename(),
		ContentType: in.GetContentType(), SizeBytes: in.GetSizeBytes(),
		Width: in.GetWidth(), Height: in.GetHeight(), Status: in.GetStatus(),
		CreatedAt: in.GetCreatedAt(), TripId: in.GetTripId(),
	}
}

func mapMediaList(in []*tripmapv1.MediaInfo) []*bffv1.MediaInfo {
	out := make([]*bffv1.MediaInfo, 0, len(in))
	for _, m := range in {
		out = append(out, mapMedia(m))
	}
	return out
}
