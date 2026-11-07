package timeline

import (
	"context"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc"
)

const Service = "timeline"

type Cleaner struct {
	client timelinev1.AccountCleanupServiceClient
}

func NewCleaner(conn grpc.ClientConnInterface) *Cleaner {
	return &Cleaner{client: timelinev1.NewAccountCleanupServiceClient(conn)}
}
func (*Cleaner) Name() string { return Service }
func (c *Cleaner) CleanupUserData(ctx context.Context, userID string) error {
	_, err := c.client.CleanupUserData(ctx, &timelinev1.CleanupUserDataRequest{UserId: userID})
	return err
}
