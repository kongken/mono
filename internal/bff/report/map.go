package report

import (
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var reportCodec = protojson.MarshalOptions{EmitUnpopulated: false}

func mapReportResponse(in *tripmapv1.GetReportResponse) (*bffv1.GetReportResponse, error) {
	if in == nil {
		return &bffv1.GetReportResponse{}, nil
	}
	b, err := reportCodec.Marshal(in)
	if err != nil {
		return nil, err
	}
	out := &bffv1.GetReportResponse{}
	if err := protojson.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}
