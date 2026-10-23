package metro

import (
	"context"

	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/grpc"
)

// Cleaner removes a user's metro data (rider profile and ride records) through
// the internal AccountCleanupService when an account is deleted.
type Cleaner struct {
	client metrov1.AccountCleanupServiceClient
}

// NewCleaner returns the metro user-data cleaner for the account deletion flow.
func NewCleaner(conn grpc.ClientConnInterface) *Cleaner {
	return &Cleaner{client: metrov1.NewAccountCleanupServiceClient(conn)}
}

func (*Cleaner) Name() string { return Service }

func (c *Cleaner) CleanupUserData(ctx context.Context, userID string) error {
	_, err := c.client.CleanupUserData(ctx, &metrov1.CleanupUserDataRequest{UserId: userID})
	return err
}
