package application

import (
	"context"

	"github.com/kongken/mono/internal/tripmap/ent"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

type snapshotGRPC struct {
	tripmapv1.UnimplementedSnapshotServiceServer
	*SnapshotService
}

// SnapshotGRPC adapts SnapshotService for the native gRPC server.
func SnapshotGRPC(s *SnapshotService) tripmapv1.SnapshotServiceServer {
	return snapshotGRPC{SnapshotService: s}
}

func (s snapshotGRPC) withAccountSchema(ctx context.Context) (context.Context, error) {
	p, err := principal(ctx)
	if err != nil {
		return ctx, grpcError(err)
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return ctx, grpcError(err)
	}
	u, err := st.DB.User.Get(ctx, p.UserID)
	if err != nil {
		if ent.IsNotFound(err) {
			return ctx, grpcError(errUnauthenticated)
		}
		return ctx, grpcError(internalError(err))
	}
	return withSchemasFromRPC(ctx, u.SchemaVersion), nil
}

func (s snapshotGRPC) ListSnapshots(ctx context.Context, req *tripmapv1.ListSnapshotsRequest) (*tripmapv1.ListSnapshotsResponse, error) {
	return connectUnary(s.SnapshotService.ListSnapshots, s.withAccountSchema, ctx, req)
}

func (s snapshotGRPC) CreateSnapshot(ctx context.Context, req *tripmapv1.CreateSnapshotRequest) (*tripmapv1.CreateSnapshotResponse, error) {
	return connectUnary(s.SnapshotService.CreateSnapshot, s.withAccountSchema, ctx, req)
}

func (s snapshotGRPC) GetSnapshot(ctx context.Context, req *tripmapv1.GetSnapshotRequest) (*tripmapv1.GetSnapshotResponse, error) {
	return connectUnary(s.SnapshotService.GetSnapshot, s.withAccountSchema, ctx, req)
}

func (s snapshotGRPC) RestoreSnapshot(ctx context.Context, req *tripmapv1.RestoreSnapshotRequest) (*tripmapv1.RestoreSnapshotResponse, error) {
	return connectUnary(s.SnapshotService.RestoreSnapshot, s.withAccountSchema, ctx, req)
}
