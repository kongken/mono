package tripmap

import (
	"context"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/grpc"
)

// Cleaner removes a user's tripmap data through the internal AccountCleanupService.
type Cleaner struct {
	client tripmapv1.AccountCleanupServiceClient
}

// NewCleaner returns the tripmap user-data cleaner for the account deletion flow.
func NewCleaner(conn grpc.ClientConnInterface) *Cleaner {
	return &Cleaner{client: tripmapv1.NewAccountCleanupServiceClient(conn)}
}

func (*Cleaner) Name() string { return Service }

func (c *Cleaner) CleanupUserData(ctx context.Context, userID string) error {
	_, err := c.client.CleanupUserData(ctx, &tripmapv1.CleanupUserDataRequest{UserId: userID})
	return err
}
