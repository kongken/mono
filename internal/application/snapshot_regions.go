package application

import (
	"errors"

	"connectrpc.com/connect"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

// normalizeSnapshot is a read-time projection. Historical payload bytes, dates,
// notes, colors and trip IDs are never rewritten. Unmapped or colliding records
// reject the whole preview/restore instead of silently discarding one record.
func (s *SnapshotService) normalizeSnapshot(input *tripmapv1.SnapshotData) (*tripmapv1.SnapshotData, error) {
	blocked := func() (*tripmapv1.SnapshotData, error) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot contains unsupported region version or colliding/unmapped regions"))
	}
	if !s.fp.regions.SupportsVersion(input.RegionVersion) {
		return blocked()
	}
	data := proto.Clone(input).(*tripmapv1.SnapshotData)
	codes, occupied := map[string]string{}, map[string]bool{}
	for _, v := range data.Visits {
		ref, err := s.fp.regions.ResolveCity(v.Code, v.Name, v.Province)
		if err != nil || occupied[ref.Code] {
			return blocked()
		}
		codes[v.Code], occupied[ref.Code] = ref.Code, true
		v.Code, v.Name, v.Province = ref.Code, ref.Name, ref.Province
	}
	for _, t := range data.Trips {
		code, ok := codes[t.RegionCode]
		if !ok {
			return blocked()
		}
		t.RegionCode = code
	}
	for _, m := range data.ProvinceMarks {
		if !s.fp.regions.HasProvince(m.Province) {
			return blocked()
		}
	}
	data.RegionVersion = s.fp.regions.Version()
	return data, nil
}
