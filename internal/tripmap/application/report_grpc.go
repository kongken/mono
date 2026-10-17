package application

import (
	"context"

	"connectrpc.com/connect"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type reportGRPC struct {
	tripmapv1.UnimplementedReportServiceServer
	*ReportService
}

// ReportGRPC adapts ReportService for the native gRPC server.
func ReportGRPC(s *ReportService) tripmapv1.ReportServiceServer {
	return reportGRPC{ReportService: s}
}

func (r reportGRPC) GetReport(ctx context.Context, req *tripmapv1.GetReportRequest) (*tripmapv1.GetReportResponse, error) {
	res, err := r.ReportService.GetReport(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, grpcError(err)
	}
	return res.Msg, nil
}
