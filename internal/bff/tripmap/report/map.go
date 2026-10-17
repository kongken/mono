package report

import (
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var reportCodec = protojson.MarshalOptions{EmitUnpopulated: false}

func mapReportResponse(in *tripmapv1.GetReportResponse) (*bfftripmapv1.GetReportResponse, error) {
	if in == nil {
		return &bfftripmapv1.GetReportResponse{}, nil
	}
	b, err := reportCodec.Marshal(in)
	if err != nil {
		return nil, err
	}
	out := &bfftripmapv1.GetReportResponse{}
	if err := protojson.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}
