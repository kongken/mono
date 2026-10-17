package application

import (
	"context"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type regionGRPC struct {
	tripmapv1.UnimplementedRegionServiceServer
	*RegionService
}

// RegionGRPC adapts RegionService for the native gRPC server.
func RegionGRPC(s *RegionService) tripmapv1.RegionServiceServer {
	return regionGRPC{RegionService: s}
}

func (r regionGRPC) GetRegionIndex(ctx context.Context, req *tripmapv1.GetRegionIndexRequest) (*tripmapv1.GetRegionIndexResponse, error) {
	return connectUnary(r.RegionService.GetRegionIndex, nil, ctx, req)
}

func (r regionGRPC) PreviewRegionUpgrade(ctx context.Context, req *tripmapv1.PreviewRegionUpgradeRequest) (*tripmapv1.PreviewRegionUpgradeResponse, error) {
	return connectUnary(r.RegionService.PreviewRegionUpgrade, nil, ctx, req)
}

func (r regionGRPC) ApplyRegionUpgrade(ctx context.Context, req *tripmapv1.ApplyRegionUpgradeRequest) (*tripmapv1.ApplyRegionUpgradeResponse, error) {
	return connectUnary(r.RegionService.ApplyRegionUpgrade, nil, ctx, req)
}
